// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damonto/ims-go/ims/sip"
)

const maxRCSMessage = 65536

type msrpFrame struct {
	transaction, method string
	status              int
	headers             map[string]string
	body                []byte
	flag                byte
}

func msrpLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		part, err := reader.ReadSlice('\n')
		out = append(out, part...)
		if len(out) > limit {
			return nil, errors.New("msrp-line-limit")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !bytes.HasSuffix(out, []byte("\r\n")) {
			return nil, errors.New("msrp-line")
		}
		return out[:len(out)-2], nil
	}
}
func readMSRP(reader *bufio.Reader) (msrpFrame, error) {
	bad := errors.New("msrp-frame")
	f := msrpFrame{headers: map[string]string{}}
	line, err := msrpLine(reader, 4096)
	if err != nil {
		return f, err
	}
	fields := strings.SplitN(string(line), " ", 4)
	if len(fields) < 3 || fields[0] != "MSRP" || len(fields[1]) < 3 || len(fields[1]) > 64 {
		return f, bad
	}
	for _, c := range fields[1] {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-+%=", c) {
			return f, bad
		}
	}
	f.transaction = fields[1]
	f.method = fields[2]
	if status, e := strconv.Atoi(f.method); e == nil {
		if status < 200 || status > 599 {
			return f, bad
		}
		f.status = status
		f.method = ""
	}
	delimiter := "-------" + f.transaction
	for size := 0; size < 32768; {
		line, err = msrpLine(reader, 4096)
		if err != nil {
			return f, err
		}
		size += len(line) + 2
		if strings.HasPrefix(string(line), delimiter) {
			if len(line) != len(delimiter)+1 {
				return f, bad
			}
			f.flag = line[len(line)-1]
			if f.flag != '$' && f.flag != '+' && f.flag != '#' {
				return f, bad
			}
			return f, nil
		}
		if len(line) == 0 {
			break
		}
		key, value, ok := strings.Cut(string(line), ":")
		key = strings.ToLower(key)
		if !ok || strings.TrimSpace(key) != key || f.headers[key] != "" || len(f.headers) >= 64 {
			return f, bad
		}
		f.headers[key] = strings.TrimSpace(value)
		if size >= 32768 {
			return f, bad
		}
	}
	for {
		line, err = msrpLine(reader, maxRCSMessage+256)
		if err != nil {
			return f, err
		}
		if strings.HasPrefix(string(line), delimiter) {
			if len(line) != len(delimiter)+1 {
				return f, bad
			}
			f.flag = line[len(line)-1]
			if f.flag != '$' && f.flag != '+' && f.flag != '#' {
				return f, bad
			}
			if !bytes.HasSuffix(f.body, []byte("\r\n")) {
				return f, bad
			}
			f.body = f.body[:len(f.body)-2]
			return f, nil
		}
		f.body = append(f.body, line...)
		f.body = append(f.body, '\r', '\n')
		if len(f.body) > maxRCSMessage+2 {
			return f, bad
		}
	}
}

func (f msrpFrame) wire() []byte {
	start := f.method
	if f.status > 0 {
		start = strconv.Itoa(f.status) + " " + http.StatusText(f.status)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "MSRP %s %s\r\n", f.transaction, start)
	for _, key := range []string{"To-Path", "From-Path", "Message-ID", "Byte-Range", "Success-Report", "Failure-Report", "Status", "Content-Type"} {
		if value := f.headers[strings.ToLower(key)]; value != "" {
			fmt.Fprintf(&out, "%s: %s\r\n", key, value)
		}
	}
	if len(f.body) > 0 {
		out.WriteString("\r\n")
		out.Write(f.body)
		out.WriteString("\r\n")
	}
	flag := f.flag
	if flag == 0 {
		flag = '$'
	}
	fmt.Fprintf(&out, "-------%s%c\r\n", f.transaction, flag)
	return []byte(out.String())
}

type rcsMediaIdentity struct {
	certificate tls.Certificate
	fingerprint string
}

func newRCSMediaIdentity() (*rcsMediaIdentity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ModemConfig-KO"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(der)
	parts := []string{}
	for _, b := range digest {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}
	return &rcsMediaIdentity{certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, fingerprint: "sha-256 " + strings.Join(parts, ":")}, nil
}
func msrpTLS(identity *rcsMediaIdentity, host, fingerprint string, server bool) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, Certificates: []tls.Certificate{identity.certificate}}
	if server {
		config.ClientAuth = tls.RequestClientCert
	}
	if fingerprint != "" {
		algorithm, value, ok := strings.Cut(fingerprint, " ")
		if !ok {
			return nil, errors.New("msrp-fingerprint")
		}
		want, err := hex.DecodeString(strings.ReplaceAll(value, ":", ""))
		if err != nil {
			return nil, errors.New("msrp-fingerprint")
		}
		if !strings.EqualFold(algorithm, "sha-256") && !strings.EqualFold(algorithm, "sha-1") {
			return nil, errors.New("msrp-fingerprint")
		}
		// RFC 4975 §14.4: the authenticated SIP SDP fingerprint replaces CA
		// validation for this one media certificate; no unpinned TLS is accepted.
		config.InsecureSkipVerify = true
		if server {
			config.ClientAuth = tls.RequireAnyClientCert
		}
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("msrp-fingerprint")
			}
			var got []byte
			if strings.EqualFold(algorithm, "sha-256") {
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				got = sum[:]
			} else {
				sum := sha1.Sum(state.PeerCertificates[0].Raw)
				got = sum[:]
			}
			if subtle.ConstantTimeCompare(got, want) != 1 {
				return errors.New("msrp-fingerprint")
			}
			return nil
		}
	}
	return config, nil
}

type rcsMediaOffer struct{ path, setup, fingerprint, profile string }

func parseRCSMedia(data []byte) (rcsMediaOffer, error) {
	offer := rcsMediaOffer{}
	bad := errors.New("rcs-media-invalid")
	if len(data) > 32768 || !bytes.Contains(data, []byte("\r\n")) {
		return offer, bad
	}
	message := false
	mediaCount := 0
	acceptCPIM := false
	for _, line := range strings.Split(string(data), "\r\n") {
		if strings.HasPrefix(line, "m=") {
			mediaCount++
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[0] != "m=message" || fields[1] == "0" || (fields[2] != "TCP/MSRP" && fields[2] != "TCP/TLS/MSRP") {
				return offer, bad
			}
			message = true
			offer.profile = fields[2]
		}
		// setup and fingerprint may be session-level SDP attributes.
		if !message && !strings.HasPrefix(line, "a=setup:") && !strings.HasPrefix(line, "a=fingerprint:") {
			continue
		}
		if strings.HasPrefix(line, "a=path:") {
			if offer.path != "" {
				return offer, bad
			}
			offer.path = strings.TrimPrefix(line, "a=path:")
		}
		if strings.HasPrefix(line, "a=setup:") {
			offer.setup = strings.TrimPrefix(line, "a=setup:")
		}
		if strings.HasPrefix(line, "a=fingerprint:") {
			offer.fingerprint = strings.TrimPrefix(line, "a=fingerprint:")
		}
		if strings.HasPrefix(line, "a=accept-types:") {
			for _, kind := range strings.Fields(strings.TrimPrefix(line, "a=accept-types:")) {
				acceptCPIM = acceptCPIM || kind == "message/cpim" || kind == "*"
			}
		}
	}
	if mediaCount != 1 || !acceptCPIM || offer.path == "" {
		return offer, bad
	}
	if offer.setup == "" {
		offer.setup = "passive"
	}
	if offer.setup != "active" && offer.setup != "passive" && offer.setup != "actpass" {
		return offer, bad
	}
	paths := strings.Fields(offer.path)
	if len(paths) > 8 {
		return offer, bad
	}
	for _, path := range paths {
		if _, err := msrpURL(path); err != nil {
			return offer, bad
		}
	}
	if strings.HasPrefix(paths[0], "msrps:") != (offer.profile == "TCP/TLS/MSRP") {
		return offer, bad
	}
	return offer, nil
}
func msrpURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || !rcsField(value) || (u.Scheme != "msrp" && u.Scheme != "msrps") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !rcsHost(u.Hostname()) || !strings.HasSuffix(u.Path, ";tcp") {
		return nil, errors.New("msrp-path")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("msrp-path")
	}
	return u, nil
}

type msrpSession struct {
	conn          net.Conn
	reader        *bufio.Reader
	local, remote string
	writeMu, mu   sync.Mutex
	pending       map[string]chan int
	done          chan struct{}
	onMessage     func(string, string, []byte) error
	parts         map[string]*msrpParts
	onReport      func(string, string)
}
type msrpParts struct {
	kind    string
	total   int
	data    []byte
	seen    []bool
	final   bool
	updated time.Time
}

func newMSRPSession(conn net.Conn, local, remote string, onMessage func(string, string, []byte) error, onReport func(string, string)) *msrpSession {
	s := &msrpSession{conn: conn, reader: bufio.NewReader(conn), local: local, remote: remote, pending: map[string]chan int{}, done: make(chan struct{}), onMessage: onMessage, onReport: onReport, parts: map[string]*msrpParts{}}
	go s.readLoop()
	return s
}
func (s *msrpSession) write(frame msrpFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer s.conn.SetWriteDeadline(time.Time{})
	data := frame.wire()
	for len(data) > 0 {
		n, e := s.conn.Write(data)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func (s *msrpSession) exchange(ctx context.Context, frame msrpFrame) error {
	response := make(chan int, 1)
	s.mu.Lock()
	s.pending[frame.transaction] = response
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, frame.transaction); s.mu.Unlock() }()
	if err := s.write(frame); err != nil {
		return errors.New("rcs-media-network")
	}
	select {
	case <-ctx.Done():
		return errors.New("rcs-media-timeout")
	case <-s.done:
		return errors.New("rcs-media-network")
	case code := <-response:
		if code != 200 {
			return errors.New("rcs-media-rejected")
		}
		return nil
	}
}
func (s *msrpSession) send(ctx context.Context, id, kind string, body []byte) error {
	if len(body) > maxRCSMessage {
		return errors.New("rcs-message-too-large")
	}
	if len(body) == 0 {
		return s.exchange(ctx, msrpFrame{transaction: sip.RandomHex(16), method: "SEND", headers: map[string]string{"to-path": s.remote, "from-path": s.local, "message-id": id, "byte-range": "1-0/0"}, flag: '$'})
	}
	for start := 0; start < len(body); start += 2048 {
		end := min(start+2048, len(body))
		flag := byte('+')
		if end == len(body) {
			flag = '$'
		}
		frame := msrpFrame{transaction: sip.RandomHex(16), method: "SEND", headers: map[string]string{"to-path": s.remote, "from-path": s.local, "message-id": id, "byte-range": fmt.Sprintf("%d-%d/%d", start+1, end, len(body)), "content-type": kind, "success-report": "yes", "failure-report": "yes"}, body: body[start:end], flag: flag}
		if err := s.exchange(ctx, frame); err != nil {
			return err
		}
	}
	return nil
}
func (s *msrpSession) receive(f msrpFrame) (bool, error) {
	id := f.headers["message-id"]
	if !rcsField(id) || len(id) > 256 {
		return false, errors.New("msrp-message-id")
	}
	if len(f.body) == 0 {
		return true, nil
	}
	start, end, total, err := msrpRange(f.headers["byte-range"], len(f.body), f.flag)
	if err != nil {
		return false, err
	}
	if start < 1 || end < start || end-start+1 != len(f.body) || total < end || total > maxRCSMessage {
		return false, errors.New("msrp-range")
	}
	for key, p := range s.parts {
		if time.Since(p.updated) > time.Minute {
			delete(s.parts, key)
		}
	}
	p := s.parts[id]
	if f.flag == '#' {
		delete(s.parts, id)
		return true, nil
	}
	if p == nil {
		if len(s.parts) >= 8 {
			return false, errors.New("msrp-parts-limit")
		}
		p = &msrpParts{kind: f.headers["content-type"], total: total, data: make([]byte, total), seen: make([]bool, total)}
		s.parts[id] = p
	}
	if p.total != total || p.kind != f.headers["content-type"] {
		return false, errors.New("msrp-range")
	}
	for i, b := range f.body {
		index := start - 1 + i
		if p.seen[index] && p.data[index] != b {
			return false, errors.New("msrp-overlap")
		}
		p.data[index], p.seen[index] = b, true
	}
	p.updated = time.Now()
	p.final = p.final || f.flag == '$'
	if !p.final {
		return false, nil
	}
	for _, seen := range p.seen {
		if !seen {
			return false, nil
		}
	}
	if err := s.onMessage(id, p.kind, p.data); err != nil {
		return false, err
	}
	delete(s.parts, id)
	return true, nil
}

func msrpRange(value string, length int, flag byte) (int, int, int, error) {
	bad := errors.New("msrp-range")
	rangeText, totalText, ok := strings.Cut(value, "/")
	if !ok {
		return 0, 0, 0, bad
	}
	startText, endText, ok := strings.Cut(rangeText, "-")
	if !ok {
		return 0, 0, 0, bad
	}
	number := func(s string) (int, error) {
		if s == "" {
			return 0, bad
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, bad
			}
		}
		return strconv.Atoi(s)
	}
	start, err := number(startText)
	if err != nil {
		return 0, 0, 0, bad
	}
	end := start + length - 1
	if endText != "*" {
		end, err = number(endText)
		if err != nil {
			return 0, 0, 0, bad
		}
	}
	total := end
	if totalText == "*" {
		if flag != '$' {
			return 0, 0, 0, bad
		}
	} else {
		total, err = number(totalText)
		if err != nil {
			return 0, 0, 0, bad
		}
	}
	if start < 1 || end < start || end-start+1 != length || total < end || total > maxRCSMessage {
		return 0, 0, 0, bad
	}
	return start, end, total, nil
}
func (s *msrpSession) readLoop() {
	defer close(s.done)
	defer s.conn.Close()
	for {
		f, err := readMSRP(s.reader)
		if err != nil {
			return
		}
		if f.status != 0 {
			s.mu.Lock()
			ch := s.pending[f.transaction]
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- f.status:
				default:
				}
			}
			continue
		}
		if f.headers["to-path"] != s.local || f.headers["from-path"] != s.remote {
			return
		}
		if f.method == "REPORT" {
			if s.onReport != nil {
				s.onReport(f.headers["message-id"], f.headers["status"])
			}
			continue
		}
		code := 200
		complete := false
		if f.method != "SEND" {
			code = 501
		} else {
			complete, err = s.receive(f)
			if err != nil {
				code = 400
			}
		}
		if f.headers["failure-report"] != "no" {
			if err = s.write(msrpFrame{transaction: f.transaction, status: code, headers: map[string]string{"to-path": f.headers["from-path"], "from-path": f.headers["to-path"]}, flag: '$'}); err != nil {
				return
			}
		}
		if code == 200 && complete && len(f.body) > 0 && f.headers["success-report"] == "yes" {
			_ = s.write(msrpFrame{transaction: sip.RandomHex(16), method: "REPORT", headers: map[string]string{"to-path": s.remote, "from-path": s.local, "message-id": f.headers["message-id"], "byte-range": f.headers["byte-range"], "status": "000 200 OK"}, flag: '$'})
		}
	}
}
