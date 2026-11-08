// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damonto/ims-go/ims/security"
	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

const rcsChatFeatures = `+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.oma.cpm.session";+g.3gpp.iari-ref="urn%3Aurn-7%3A3gpp-application.ims.iari.rcse.im"`

type rcsStream struct {
	conn            net.Conn
	pending         []byte
	readMu, writeMu sync.Mutex
}

func (t *rcsStream) LocalAddr() net.Addr  { return t.conn.LocalAddr() }
func (t *rcsStream) RemoteAddr() net.Addr { return t.conn.RemoteAddr() }
func (t *rcsStream) Close() error         { return t.conn.Close() }
func (t *rcsStream) WritePacket(ctx context.Context, data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = t.conn.SetWriteDeadline(deadline)
	defer t.conn.SetWriteDeadline(time.Time{})
	for len(data) > 0 {
		n, err := t.conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func (t *rcsStream) ReadPacket(ctx context.Context) ([]byte, net.Addr, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()
	for {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if bytes.HasPrefix(t.pending, []byte("\r\n\r\n")) {
			t.pending = t.pending[4:]
			return []byte("\r\n\r\n"), t.RemoteAddr(), nil
		}
		if bytes.HasPrefix(t.pending, []byte("\r\n")) {
			t.pending = t.pending[2:]
			return []byte("\r\n"), t.RemoteAddr(), nil
		}
		if index := bytes.Index(t.pending, []byte("\r\n\r\n")); index >= 0 {
			if index > 32768 {
				return nil, nil, errors.New("rcs-sip-header")
			}
			length := -1
			for _, line := range strings.Split(string(t.pending[:index]), "\r\n")[1:] {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "l") {
					if length >= 0 {
						return nil, nil, errors.New("rcs-sip-length")
					}
					n, e := strconv.Atoi(strings.TrimSpace(value))
					if e != nil || n < 0 || n > maxRCSConfig {
						return nil, nil, errors.New("rcs-sip-length")
					}
					length = n
				}
			}
			if length < 0 {
				return nil, nil, errors.New("rcs-sip-length")
			}
			end := index + 4 + length
			if len(t.pending) >= end {
				packet := bytes.Clone(t.pending[:end])
				t.pending = t.pending[end:]
				return packet, t.RemoteAddr(), nil
			}
		} else if len(t.pending) > 32768 {
			return nil, nil, errors.New("rcs-sip-header")
		}
		_ = t.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buffer := make([]byte, 8192)
		n, err := t.conn.Read(buffer)
		if n > 0 {
			t.pending = append(t.pending, buffer[:n]...)
		}
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return nil, nil, err
		}
	}
}

type rcsSIP struct {
	mu                                    sync.Mutex
	stack                                 *session.Stack
	profile                               *rcsProfile
	provider                              security.AKAProvider
	agent, contact, callID, tag, instance string
	seq                                   int
	routes                                []string
	db                                    *messageDB
	verify                                func(context.Context) error
	ctx                                   context.Context
	dialogs                               map[string]*rcsDialog
	done                                  chan struct{}
	transport                             session.Transport
}

type rcsEndpoint struct{ address, identity string }

// RFC 3263: a provisioned FQDN without a port can name a SIP service,
// rather than an A/AAAA host. Keep the original name for TLS authentication.
func rcsEndpoints(ctx context.Context, profile *rcsProfile, lookup func(context.Context, string, string, string) (string, []*net.SRV, error)) []rcsEndpoint {
	var endpoints []rcsEndpoint
	for _, proxy := range profile.proxies {
		host, _, _ := net.SplitHostPort(proxy)
		if profile.discover[proxy] {
			service := "sip"
			if profile.transport == "SIPoTLS" {
				service = "sips"
			}
			query, stop := context.WithTimeout(ctx, 6*time.Second)
			_, records, err := lookup(query, service, "tcp", host)
			stop()
			if err == nil && len(records) > 0 {
				for _, record := range records {
					target := strings.TrimSuffix(record.Target, ".")
					if rcsHost(target) && record.Port != 0 && len(endpoints) < 32 {
						endpoints = append(endpoints, rcsEndpoint{net.JoinHostPort(target, strconv.Itoa(int(record.Port))), host})
					}
				}
				continue
			}
		}
		endpoints = append(endpoints, rcsEndpoint{proxy, host})
	}
	return endpoints
}

func openRCSSIP(ctx context.Context, profile *rcsProfile, provider security.AKAProvider, agent, scope string, db *messageDB, verify func(context.Context) error) (*rcsSIP, error) {
	if profile.transport == "SIPoUDP" {
		return nil, errors.New("rcs-udp-unsupported")
	}
	var conn net.Conn
	lastError := errors.New("rcs-sip-network")
	for _, proxy := range rcsEndpoints(ctx, profile, net.DefaultResolver.LookupSRV) {
		connect, stop := context.WithTimeout(ctx, 15*time.Second)
		var err error
		conn, err = rcsDialContext(connect, "tcp", proxy.address)
		if err != nil {
			var dns *net.DNSError
			var ne net.Error
			if errors.As(err, &dns) {
				lastError = errors.New("rcs-sip-dns")
			} else if errors.As(err, &ne) && ne.Timeout() {
				lastError = errors.New("rcs-sip-timeout")
			} else {
				lastError = errors.New("rcs-sip-connect")
			}
		}
		if err == nil && profile.transport == "SIPoTLS" {
			secure := tls.Client(conn, &tls.Config{ServerName: proxy.identity, MinVersion: tls.VersionTLS12})
			if err = secure.HandshakeContext(connect); err != nil {
				lastError = errors.New("rcs-sip-tls-rejected")
				var cert *tls.CertificateVerificationError
				var host x509.HostnameError
				if errors.As(err, &cert) {
					lastError = errors.New("rcs-sip-certificate-invalid")
					if errors.As(err, &host) {
						lastError = errors.New("rcs-sip-certificate-host")
					}
				}
				conn.Close()
				conn = nil
			} else {
				conn = secure
			}
		}
		stop()
		if err == nil {
			break
		}
		conn = nil
	}
	if conn == nil {
		return nil, lastError
	}
	stream := &rcsStream{conn: conn}
	stack, err := session.Open(session.Config{Transport: "tcp", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, stream)
	if err != nil {
		conn.Close()
		return nil, errors.New("rcs-sip-network")
	}
	callID, _, tag, _ := sip.NewTransaction()
	instance := scope[:8] + "-" + scope[8:12] + "-" + scope[12:16] + "-" + scope[16:20] + "-" + scope[20:32]
	protocol := "tcp"
	if profile.transport == "SIPoTLS" {
		protocol = "tls"
	}
	contact := "<sip:" + sip.RandomHex(12) + "@" + conn.LocalAddr().String() + ";transport=" + protocol + ">;+sip.instance=\"<urn:uuid:" + instance + ">\""
	if profile.chat {
		contact += ";" + rcsChatFeatures
	}
	r := &rcsSIP{stack: stack, profile: profile, provider: provider, agent: agent, contact: contact, callID: callID, tag: tag, instance: instance, db: db, verify: verify, ctx: ctx, dialogs: map[string]*rcsDialog{}, done: make(chan struct{}), transport: stream}
	if _, err = stack.Attach(r); err != nil {
		stack.Close()
		return nil, err
	}
	go func() { defer close(r.done); _ = stack.Run(ctx) }()
	return r, nil
}

func (r *rcsSIP) keepalive(ctx context.Context) error {
	return r.transport.WritePacket(ctx, []byte("\r\n\r\n"))
}

type rcsSIPFailure struct{ status int }

func (e *rcsSIPFailure) Error() string { return "rcs-sip-rejected" }

func (r *rcsSIP) request(method, target, to, from, callID string, sequence int) sip.Message {
	request := sip.NewRequest(method, target)
	protocol := "TCP"
	if r.profile.transport == "SIPoTLS" {
		protocol = "TLS"
	}
	request.Headers.Add("Via", "SIP/2.0/"+protocol+" "+r.stack.LocalAddr().String()+";branch=z9hG4bK"+sip.RandomHex(20)+";rport")
	request.Headers.Add("To", to)
	request.Headers.Add("From", from)
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", strconv.Itoa(sequence)+" "+method)
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("User-Agent", r.agent)
	request.Headers.Add("Contact", r.contact)
	request.Headers.Add("Allow", "INVITE,ACK,CANCEL,BYE,MESSAGE,OPTIONS,NOTIFY")
	return request
}

func (r *rcsSIP) register(ctx context.Context, expires int) (time.Duration, error) {
	if err := r.verify(ctx); err != nil {
		return 0, errors.New("sim-unavailable")
	}
	target := "sip:" + r.profile.domain
	var auth *security.Authorization
	// RCC.07 requires the provisioned private identity even before a
	// challenge, so the registrar can select the correct subscriber record.
	if r.profile.auth == "Digest" {
		auth = &security.Authorization{Username: r.profile.username, Realm: r.profile.realm, URI: target, Algorithm: "MD5"}
	} else if r.profile.auth == "AKA" {
		auth = &security.Authorization{Username: r.profile.private, Realm: r.profile.domain, URI: target, Algorithm: "AKAv1-MD5", IntegrityProtected: "no"}
	}
	header := "Authorization"
	var proof *rcsDigestProof
	for attempt := 0; attempt < 3; attempt++ {
		r.mu.Lock()
		r.seq++
		sequence := r.seq
		r.mu.Unlock()
		request := r.request("REGISTER", target, "<"+r.profile.public+">", "<"+r.profile.public+">;tag="+r.tag, r.callID, sequence)
		request.Headers.Add("Expires", strconv.Itoa(expires))
		request.Headers.Add("Supported", "path,outbound,gruu")
		if auth != nil {
			request.Headers.Add(header, auth.String())
		}
		response, err := r.stack.Exchange(ctx, request)
		if err != nil {
			return 0, errors.New("rcs-sip-network")
		}
		status := response.StatusCode()
		emit(map[string]any{"stage": "rcs-registration", "sip_status": status, "authenticated": auth != nil && auth.Response != ""})
		if status == 200 {
			if proof != nil && response.Header("Authentication-Info") != "" {
				var info security.Info
				if info.UnmarshalText([]byte(response.Header("Authentication-Info"))) != nil || proof.verify(info, string(response.Body)) != nil {
					return 0, errors.New("rcs-server-proof")
				}
			}
			if response.Headers.Has("Security-Server") || response.HeaderHasToken("Require", "sec-agree") {
				return 0, errors.New("rcs-security-unsupported")
			}
			r.mu.Lock()
			r.routes = response.HeaderList("Service-Route")
			r.mu.Unlock()
			seconds := expires
			if value, e := strconv.Atoi(response.Header("Expires")); e == nil && value >= 0 {
				seconds = value
			}
			for _, value := range response.HeaderList("Contact") {
				if index := strings.LastIndex(strings.ToLower(value), ";expires="); index >= 0 {
					v := strings.SplitN(value[index+9:], ";", 2)[0]
					if n, e := strconv.Atoi(strings.Trim(v, "\" ")); e == nil && n < seconds {
						seconds = n
					}
				}
			}
			if expires > 0 && seconds < 1 {
				return 0, errors.New("rcs-registration-expired")
			}
			return time.Duration(seconds) * time.Second, nil
		}
		if status != 401 && status != 407 {
			return 0, &rcsSIPFailure{status}
		}
		challengeHeader := "WWW-Authenticate"
		header = "Authorization"
		if status == 407 {
			challengeHeader, header = "Proxy-Authenticate", "Proxy-Authorization"
		}
		var challenge security.Challenge
		var parseError error
		if r.profile.auth == "Digest" {
			challenge, parseError = parseRCSChallenge(response.Header(challengeHeader))
		} else {
			parseError = challenge.UnmarshalText([]byte(response.Header(challengeHeader)))
		}
		if parseError != nil || challenge.Nonce == "" {
			return 0, errors.New("rcs-auth-invalid")
		}
		if response.Headers.Has("Security-Server") || response.HeaderHasToken("Require", "sec-agree") {
			return 0, errors.New("rcs-security-unsupported")
		}
		if r.profile.auth == "Digest" {
			if challenge.Realm != r.profile.realm {
				return 0, errors.New("rcs-auth-realm")
			}
			p, e := rcsDigest(challenge, r.profile.username, r.profile.password, "REGISTER", target, "", 1)
			if e != nil {
				return 0, e
			}
			proof = &p
			auth = &p.auth
		} else if r.profile.auth == "AKA" {
			result, e := security.BuildAKAAuthorization(ctx, r.provider, security.Identity{PrivateIdentity: r.profile.private, RegisterURI: target, Realm: r.profile.domain}, challenge, 1)
			if e != nil {
				return 0, errors.New("rcs-aka-failed")
			}
			a := result.Authorization
			a.IntegrityProtected = "no"
			auth = &a
		} else {
			return 0, errors.New("rcs-auth-unsupported")
		}
	}
	return 0, errors.New("rcs-auth-failed")
}

// The IMS library's challenge decoder intentionally accepts AKA algorithms
// only. Ordinary RCS Digest needs its own RFC 7616 directive decoder.
func parseRCSChallenge(value string) (security.Challenge, error) {
	var c security.Challenge
	bad := errors.New("rcs-auth-invalid")
	if len(value) > 8192 || strings.ContainsAny(value, "\r\n\x00") {
		return c, bad
	}
	scheme, rest, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Digest") {
		return c, bad
	}
	seen := map[string]string{}
	for strings.TrimSpace(rest) != "" {
		rest = strings.TrimSpace(rest)
		key, remaining, ok := strings.Cut(rest, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || key == "" || strings.ContainsAny(key, " ,\t\"") {
			return c, bad
		}
		if _, duplicate := seen[key]; duplicate {
			return c, bad
		}
		rest = strings.TrimSpace(remaining)
		var decoded strings.Builder
		if strings.HasPrefix(rest, "\"") {
			rest = rest[1:]
			closed := false
			for len(rest) > 0 {
				b := rest[0]
				rest = rest[1:]
				if b == '"' {
					closed = true
					break
				}
				if b == '\\' {
					if len(rest) == 0 {
						return c, bad
					}
					b = rest[0]
					rest = rest[1:]
				}
				if b < 32 || b == 127 {
					return c, bad
				}
				decoded.WriteByte(b)
			}
			if !closed {
				return c, bad
			}
			rest = strings.TrimSpace(rest)
			if rest != "" {
				if rest[0] != ',' {
					return c, bad
				}
				rest = rest[1:]
				if strings.TrimSpace(rest) == "" {
					return c, bad
				}
			}
		} else {
			part, remaining, comma := strings.Cut(rest, ",")
			part = strings.TrimSpace(part)
			if part == "" || strings.ContainsAny(part, " \t\"") {
				return c, bad
			}
			decoded.WriteString(part)
			rest = remaining
			if comma && strings.TrimSpace(rest) == "" {
				return c, bad
			}
		}
		seen[key] = decoded.String()
		if len(seen) > 32 {
			return c, bad
		}
	}
	c = security.Challenge{Realm: seen["realm"], Nonce: seen["nonce"], Algorithm: seen["algorithm"], QOP: seen["qop"], Opaque: seen["opaque"]}
	if c.Realm == "" || c.Nonce == "" {
		return c, bad
	}
	return c, nil
}

func (r *rcsSIP) close() {
	r.mu.Lock()
	dialogs := make([]*rcsDialog, 0, len(r.dialogs))
	for _, d := range r.dialogs {
		dialogs = append(dialogs, d)
	}
	r.mu.Unlock()
	for _, d := range dialogs {
		d.close()
	}
	_ = r.stack.Close()
}

// RFC 7616 digest is bound to this provisioned realm and request. AKA uses
// the same SIM-backed implementation as IMS; a digest password is never AKA.
type rcsDigestProof struct {
	auth     security.Authorization
	password string
	newHash  func() hash.Hash
}

func rcsDigest(challenge security.Challenge, username, password, method, uri, body string, count uint32) (rcsDigestProof, error) {
	algorithm := strings.ToUpper(challenge.Algorithm)
	if algorithm == "" {
		algorithm = "MD5"
	}
	var h func() hash.Hash
	switch strings.TrimSuffix(algorithm, "-SESS") {
	case "MD5":
		h = md5.New
	case "SHA-256":
		h = sha256.New
	default:
		return rcsDigestProof{}, errors.New("rcs-digest-algorithm")
	}
	hashText := func(value string) string {
		hasher := h()
		hasher.Write([]byte(value))
		return hex.EncodeToString(hasher.Sum(nil))
	}
	qop := ""
	for _, v := range strings.Split(challenge.QOP, ",") {
		if strings.TrimSpace(v) == "auth" {
			qop = "auth"
			break
		}
		if strings.TrimSpace(v) == "auth-int" {
			qop = "auth-int"
		}
	}
	if challenge.QOP != "" && qop == "" {
		return rcsDigestProof{}, errors.New("rcs-digest-qop")
	}
	a := security.Authorization{Username: username, Realm: challenge.Realm, Nonce: challenge.Nonce, URI: uri, Algorithm: challenge.Algorithm, QOP: qop, Opaque: challenge.Opaque}
	if a.Algorithm == "" {
		a.Algorithm = "MD5"
	}
	if qop != "" || strings.HasSuffix(algorithm, "-SESS") {
		a.CNonce = sip.RandomHex(32)
	}
	ha1 := hashText(username + ":" + a.Realm + ":" + password)
	if strings.HasSuffix(algorithm, "-SESS") {
		ha1 = hashText(ha1 + ":" + a.Nonce + ":" + a.CNonce)
	}
	a2 := method + ":" + uri
	if qop == "auth-int" {
		a2 += ":" + hashText(body)
	}
	data := ha1 + ":" + a.Nonce + ":"
	if qop != "" {
		a.NonceCount = fmt.Sprintf("%08x", count)
		data += a.NonceCount + ":" + a.CNonce + ":" + qop + ":"
	}
	a.Response = hashText(data + hashText(a2))
	return rcsDigestProof{auth: a, password: password, newHash: h}, nil
}

func (p rcsDigestProof) verify(info security.Info, body string) error {
	if info.RSPAuth == "" {
		return nil
	}
	a := p.auth
	h := func(v string) string {
		digest := p.newHash()
		digest.Write([]byte(v))
		return hex.EncodeToString(digest.Sum(nil))
	}
	ha1 := h(a.Username + ":" + a.Realm + ":" + p.password)
	if strings.HasSuffix(strings.ToUpper(a.Algorithm), "-SESS") {
		ha1 = h(ha1 + ":" + a.Nonce + ":" + a.CNonce)
	}
	a2 := ":" + a.URI
	if a.QOP == "auth-int" {
		a2 += ":" + h(body)
	}
	data := ha1 + ":" + a.Nonce + ":"
	if a.QOP != "" {
		if info.CNonce != a.CNonce || info.NonceCount != a.NonceCount || info.QOP != a.QOP {
			return errors.New("rcs-server-proof")
		}
		data += a.NonceCount + ":" + a.CNonce + ":" + a.QOP + ":"
	}
	if subtle.ConstantTimeCompare([]byte(h(data+h(a2))), []byte(strings.ToLower(info.RSPAuth))) != 1 {
		return errors.New("rcs-server-proof")
	}
	return nil
}
