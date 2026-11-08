// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

type rcsDialog struct {
	mu                                                           sync.Mutex
	owner                                                        *rcsSIP
	id, peer, localTag, remoteTag, target, localPath, remotePath string
	remoteTo                                                     string
	routes                                                       []string
	seq                                                          int
	media                                                        *msrpSession
	listener                                                     net.Listener
	identity                                                     *rcsMediaIdentity
	ready                                                        chan struct{}
	stop                                                         context.CancelFunc
}

func (d *rcsDialog) close() {
	d.mu.Lock()
	if d.stop != nil {
		d.stop()
	}
	if d.media != nil {
		_ = d.media.conn.Close()
	}
	if d.listener != nil {
		_ = d.listener.Close()
	}
	d.mu.Unlock()
	d.owner.mu.Lock()
	if d.owner.dialogs[d.id] == d {
		delete(d.owner.dialogs, d.id)
	}
	d.owner.mu.Unlock()
}

func rcsURI(value string) string {
	if left := strings.IndexByte(value, '<'); left >= 0 {
		if right := strings.IndexByte(value[left+1:], '>'); right >= 0 {
			return value[left+1 : left+1+right]
		}
	}
	return strings.TrimSpace(strings.SplitN(value, ";tag=", 2)[0])
}

func (d *rcsDialog) sdp(setup, profile string) []byte {
	host, port, _ := net.SplitHostPort(strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(d.localPath, "msrps://"), "msrp://"), "/", 2)[0])
	family := "IP4"
	if strings.Contains(host, ":") {
		family = "IP6"
	}
	text := fmt.Sprintf("v=0\r\no=- %d 1 IN %s %s\r\ns=-\r\nc=IN %s %s\r\nt=0 0\r\nm=message %s %s *\r\na=path:%s\r\na=setup:%s\r\na=accept-types:message/cpim application/im-iscomposing+xml\r\na=accept-wrapped-types:text/plain message/imdn+xml\r\na=sendrecv\r\n", time.Now().Unix(), family, host, family, host, port, profile, d.localPath, setup)
	if profile == "TCP/TLS/MSRP" {
		text += "a=fingerprint:" + d.identity.fingerprint + "\r\n"
	}
	return []byte(text)
}

func (r *rcsSIP) newDialog(peer string) (*rcsDialog, error) {
	identity, err := newRCSMediaIdentity()
	if err != nil {
		return nil, errors.New("rcs-media-identity")
	}
	host, _, err := net.SplitHostPort(r.stack.LocalAddr().String())
	if err != nil {
		return nil, errors.New("rcs-media-address")
	}
	id, _, tag, _ := sip.NewTransaction()
	d := &rcsDialog{owner: r, id: id, peer: peer, localTag: tag, target: peer, seq: 1, ready: make(chan struct{}), identity: identity}
	d.localPath = "msrps://" + net.JoinHostPort(host, "9") + "/" + sip.RandomHex(32) + ";tcp"
	return d, nil
}

func (d *rcsDialog) connect(ctx context.Context, offer rcsMediaOffer) error {
	remote, err := msrpURL(strings.Fields(offer.path)[0])
	if err != nil {
		return err
	}
	var conn net.Conn
	if d.listener != nil {
		type accepted struct {
			c net.Conn
			e error
		}
		ch := make(chan accepted, 1)
		go func() { c, e := d.listener.Accept(); ch <- accepted{c, e} }()
		select {
		case <-ctx.Done():
			d.listener.Close()
			result := <-ch
			if result.c != nil {
				result.c.Close()
			}
			return ctx.Err()
		case result := <-ch:
			conn, err = result.c, result.e
		}
	} else {
		conn, err = rcsDialContext(ctx, "tcp", remote.Host)
	}
	if err != nil {
		return errors.New("rcs-media-network")
	}
	if remote.Scheme == "msrps" {
		config, e := msrpTLS(d.identity, remote.Hostname(), offer.fingerprint, d.listener != nil)
		if e != nil {
			conn.Close()
			return e
		}
		var secure *tls.Conn
		if d.listener != nil {
			secure = tls.Server(conn, config)
		} else {
			secure = tls.Client(conn, config)
		}
		if err = secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			return errors.New("rcs-media-tls")
		}
		conn = secure
	}
	d.mu.Lock()
	d.remotePath = offer.path
	d.media = newMSRPSession(conn, d.localPath, d.remotePath, func(id, kind string, body []byte) error { return d.receive(id, kind, body) }, func(id, status string) {
		if strings.HasPrefix(status, "000 200") {
			_ = d.owner.db.rcsTransportEvent(id, "accepted_by_service")
		}
	})
	media := d.media
	d.mu.Unlock()
	if d.listener == nil {
		if err = media.send(ctx, sip.RandomHex(32), "", nil); err != nil {
			return err
		}
	}
	return nil
}

func (r *rcsSIP) HandleRequest(ctx context.Context, req session.Request) (bool, error) {
	method := req.Message.Method()
	if method == "OPTIONS" {
		response := sip.NewResponse(200, "OK")
		response.Headers = sip.ResponseHeaders(req.Message, 200)
		response.Headers.Add("Contact", r.contact)
		response.Headers.Add("Allow", "INVITE,ACK,CANCEL,BYE,MESSAGE,OPTIONS,NOTIFY")
		return true, r.stack.WriteResponse(ctx, req, response)
	}
	if method == "NOTIFY" {
		return true, r.stack.WriteStatus(ctx, req, 489, "Bad Event")
	}
	if method == "INVITE" {
		if !r.profile.chat {
			return true, r.stack.WriteStatus(ctx, req, 488, "Not Acceptable Here")
		}
		if req.Message.Header("Require") != "" {
			return true, r.stack.WriteStatus(ctx, req, 420, "Bad Extension")
		}
		if !strings.HasPrefix(strings.ToLower(req.Message.Header("Content-Type")), "application/sdp") {
			return true, r.stack.WriteStatus(ctx, req, 415, "Unsupported Media Type")
		}
		offer, err := parseRCSMedia(req.Message.Body)
		if err != nil || r.profile.mediaTLS && offer.profile != "TCP/TLS/MSRP" {
			return true, r.stack.WriteStatus(ctx, req, 488, "Not Acceptable Here")
		}
		peer := rcsURI(req.Message.Header("P-Asserted-Identity"))
		if peer == "" {
			peer = rcsURI(req.Message.Header("From"))
		}
		if !rcsField(peer) {
			return true, r.stack.WriteStatus(ctx, req, 400, "Bad Request")
		}
		d, err := r.newDialog(peer)
		if err != nil {
			return true, r.stack.WriteStatus(ctx, req, 500, "Server Internal Error")
		}
		d.id = req.Message.Header("Call-ID")
		d.remoteTag = sip.HeaderTag(req.Message.Header("From"))
		d.remoteTo = req.Message.Header("From")
		d.target = rcsURI(req.Message.Header("Contact"))
		d.routes = req.Message.HeaderList("Record-Route")
		if !rcsField(d.target) {
			return true, r.stack.WriteStatus(ctx, req, 400, "Bad Request")
		}
		setup := "active"
		if offer.setup == "active" {
			setup = "passive"
			host, _, _ := net.SplitHostPort(r.stack.LocalAddr().String())
			listener, e := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if e != nil {
				return true, r.stack.WriteStatus(ctx, req, 500, "Server Internal Error")
			}
			d.listener = listener
			scheme := "msrps"
			if offer.profile == "TCP/MSRP" {
				scheme = "msrp"
			}
			d.localPath = scheme + "://" + listener.Addr().String() + "/" + sip.RandomHex(32) + ";tcp"
		} else if offer.profile == "TCP/MSRP" {
			d.localPath = strings.Replace(d.localPath, "msrps:", "msrp:", 1)
		}
		r.mu.Lock()
		if len(r.dialogs) >= 8 || r.dialogs[d.id] != nil {
			r.mu.Unlock()
			d.close()
			return true, r.stack.WriteStatus(ctx, req, 486, "Busy Here")
		}
		r.dialogs[d.id] = d
		r.mu.Unlock()
		response := sip.NewResponse(200, "OK")
		response.Headers = sip.ResponseHeaders(req.Message, 200)
		response.Headers.Set("To", req.Message.Header("To")+";tag="+d.localTag)
		response.Headers.Add("Contact", r.contact)
		response.Headers.Add("Content-Type", "application/sdp")
		response.Body = d.sdp(setup, offer.profile)
		if err = r.stack.WriteResponse(ctx, req, response); err != nil {
			d.close()
			return true, err
		}
		go func() {
			connect, stop := context.WithTimeout(r.ctx, 20*time.Second)
			defer stop()
			if err := d.connect(connect, offer); err != nil {
				d.close()
			}
			close(d.ready)
		}()
		return true, nil
	}
	r.mu.Lock()
	d := r.dialogs[req.Message.Header("Call-ID")]
	r.mu.Unlock()
	if method == "CANCEL" {
		// Incoming INVITEs are answered immediately; CANCEL cannot terminate
		// an established dialog. Only an authenticated in-dialog BYE can.
		return true, r.stack.WriteStatus(ctx, req, 481, "Call/Transaction Does Not Exist")
	}
	if d != nil && (method == "ACK" || method == "BYE") {
		if method == "ACK" {
			return true, nil
		}
		if method == "BYE" && (sip.HeaderTag(req.Message.Header("To")) != d.localTag || sip.HeaderTag(req.Message.Header("From")) != d.remoteTag) {
			return true, r.stack.WriteStatus(ctx, req, 481, "Call/Transaction Does Not Exist")
		}
		d.close()
		return true, r.stack.WriteStatus(ctx, req, 200, "OK")
	}
	if method == "MESSAGE" && strings.HasPrefix(strings.ToLower(req.Message.Header("Content-Type")), "message/cpim") {
		peer := rcsURI(req.Message.Header("P-Asserted-Identity"))
		if peer == "" {
			peer = rcsURI(req.Message.Header("From"))
		}
		if _, err := r.db.acceptRCS(peer, r.profile.public, req.Message.Header("Call-ID"), req.Message.Body); err != nil {
			return true, r.stack.WriteStatus(ctx, req, 400, "Bad Request")
		}
		return true, r.stack.WriteStatus(ctx, req, 200, "OK")
	}
	return false, nil
}

func (r *rcsSIP) openChat(ctx context.Context, peer string) (*rcsDialog, error) {
	r.mu.Lock()
	var existing []*rcsDialog
	for _, d := range r.dialogs {
		if canonicalPeer(d.peer) == canonicalPeer(peer) {
			existing = append(existing, d)
		}
	}
	r.mu.Unlock()
	for _, d := range existing {
		select {
		case <-d.ready:
			d.mu.Lock()
			media := d.media
			d.mu.Unlock()
			if media != nil {
				select {
				case <-media.done:
					d.close()
				default:
					return d, nil
				}
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d, err := r.newDialog(peer)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if len(r.dialogs) >= 8 {
		r.mu.Unlock()
		return nil, errors.New("rcs-busy")
	}
	r.dialogs[d.id] = d
	routes := append([]string(nil), r.routes...)
	r.mu.Unlock()
	success := false
	defer func() {
		close(d.ready)
		if !success {
			d.close()
		}
	}()
	request := r.request("INVITE", peer, "<"+peer+">", "<"+r.profile.public+">;tag="+d.localTag, d.id, 1)
	request.Headers.Add("P-Preferred-Identity", "<"+r.profile.public+">")
	request.Headers.Add("P-Preferred-Service", "urn:urn-7:3gpp-service.ims.icsi.oma.cpm.session")
	request.Headers.Add("Accept-Contact", "*;"+rcsChatFeatures+";require;explicit")
	request.Headers.Add("Conversation-ID", sip.RandomHex(32))
	request.Headers.Add("Contribution-ID", sip.RandomHex(32))
	request.Headers.Add("Content-Type", "application/sdp")
	sip.ApplyRouteSet(&request, routes)
	mediaProfile := "TCP/TLS/MSRP"
	if !r.profile.mediaTLS {
		mediaProfile = "TCP/MSRP"
		d.localPath = strings.Replace(d.localPath, "msrps:", "msrp:", 1)
	}
	request.Body = d.sdp("active", mediaProfile)
	response, err := r.stack.Exchange(ctx, request)
	if err != nil {
		return nil, errors.New("rcs-sip-network")
	}
	if response.StatusCode() < 200 || response.StatusCode() >= 300 {
		ack := request
		ack.StartLine = "ACK " + sip.RequestURI(request) + " SIP/2.0"
		ack.Headers.Set("CSeq", "1 ACK")
		ack.Headers.Set("To", response.Header("To"))
		ack.Body = nil
		ack.Headers.Del("Content-Type")
		_ = r.stack.Send(ctx, ack)
		return nil, &rcsSIPFailure{response.StatusCode()}
	}
	d.target = rcsURI(response.Header("Contact"))
	d.remoteTo = response.Header("To")
	d.remoteTag = sip.HeaderTag(d.remoteTo)
	d.routes = response.HeaderList("Record-Route")
	for i, j := 0, len(d.routes)-1; i < j; i, j = i+1, j-1 {
		d.routes[i], d.routes[j] = d.routes[j], d.routes[i]
	}
	if !rcsField(d.target) {
		return nil, errors.New("rcs-dialog-invalid")
	}
	ack := r.request("ACK", d.target, d.remoteTo, "<"+r.profile.public+">;tag="+d.localTag, d.id, 1)
	sip.ApplyRouteSet(&ack, d.routes)
	if err = r.stack.Send(ctx, ack); err != nil {
		return nil, err
	}
	offer, err := parseRCSMedia(response.Body)
	if err != nil || offer.setup != "passive" || r.profile.mediaTLS && offer.profile != "TCP/TLS/MSRP" {
		return nil, errors.New("rcs-media-invalid")
	}
	if err = d.connect(ctx, offer); err != nil {
		return nil, err
	}
	success = true
	return d, nil
}

type cpimMessage struct {
	from, to, id, kind string
	body               []byte
	delivery, display  bool
}

func cpimHeaders(data []byte) (textproto.MIMEHeader, []byte, error) {
	index := bytes.Index(data, []byte("\r\n\r\n"))
	if index < 0 || index > 8192 {
		return nil, nil, errors.New("rcs-cpim-invalid")
	}
	reader := textproto.NewReader(bufio.NewReader(bytes.NewReader(data[:index+4])))
	headers, err := reader.ReadMIMEHeader()
	if err != nil {
		return nil, nil, errors.New("rcs-cpim-invalid")
	}
	return headers, data[index+4:], nil
}
func parseCPIM(data []byte) (cpimMessage, error) {
	m := cpimMessage{}
	bad := errors.New("rcs-cpim-invalid")
	if len(data) > maxRCSMessage {
		return m, bad
	}
	head, content, err := cpimHeaders(data)
	if err != nil {
		return m, err
	}
	bodyHeaders, body, err := cpimHeaders(content)
	if err != nil {
		return m, err
	}
	m.from, m.to = rcsURI(head.Get("From")), rcsURI(head.Get("To"))
	m.id = head.Get("imdn.Message-ID")
	if len(head.Values("From")) != 1 || len(head.Values("To")) != 1 || !rcsField(m.from) || !rcsField(m.to) {
		return m, bad
	}
	for _, ns := range head.Values("NS") {
		parts := strings.Fields(ns)
		if len(parts) == 2 && parts[1] == "<urn:ietf:params:imdn>" {
			if id := head.Get(parts[0] + ".Message-ID"); id != "" {
				m.id = id
			}
		}
	}
	if !rcsField(m.id) || len(m.id) > 256 {
		return m, bad
	}
	m.kind = strings.ToLower(strings.TrimSpace(strings.Split(bodyHeaders.Get("Content-Type"), ";")[0]))
	m.body = body
	if length := bodyHeaders.Get("Content-Length"); length != "" {
		n, e := strconv.Atoi(length)
		if e != nil || n != len(body) {
			return m, bad
		}
	}
	if m.kind != "text/plain" && m.kind != "message/imdn+xml" {
		return m, bad
	}
	if !utf8.Valid(body) {
		return m, bad
	}
	notifications := head.Get("imdn.Disposition-Notification")
	m.delivery = strings.Contains(notifications, "positive-delivery")
	m.display = strings.Contains(notifications, "display")
	return m, nil
}
func makeCPIM(from, to, id, kind string, body []byte, receipt bool) []byte {
	head := fmt.Sprintf("From: <%s>\r\nTo: <%s>\r\nDateTime: %s\r\nNS: imdn <urn:ietf:params:imdn>\r\nimdn.Message-ID: %s\r\n", from, to, time.Now().UTC().Format(time.RFC3339), id)
	if receipt {
		head += "imdn.Disposition-Notification: positive-delivery, display\r\n"
	}
	return append([]byte(head+fmt.Sprintf("\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", kind, len(body))), body...)
}

func (d *rcsDialog) receive(id, kind string, body []byte) error {
	if kind == "application/im-iscomposing+xml" {
		return nil
	}
	if strings.ToLower(strings.Split(kind, ";")[0]) != "message/cpim" {
		return errors.New("rcs-content-unsupported")
	}
	_, err := d.owner.db.acceptRCS(d.peer, d.owner.profile.public, id, body)
	if err != nil {
		return err
	}
	return nil
}
func rcsReceiptXML(id, kind string) []byte {
	notification := "delivery-notification"
	if kind == "displayed" {
		notification = "display-notification"
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><imdn xmlns="urn:ietf:params:xml:ns:imdn"><message-id>` + xmlEscape(id) + `</message-id><datetime>` + time.Now().UTC().Format(time.RFC3339) + `</datetime><` + notification + `><status><` + kind + `/></status></` + notification + `></imdn>`)
}
func (d *rcsDialog) receipt(ctx context.Context, id, eventID, kind string) error {
	body := rcsReceiptXML(id, kind)
	d.mu.Lock()
	media := d.media
	d.mu.Unlock()
	if media == nil {
		return errors.New("rcs-media-network")
	}
	return media.send(ctx, eventID, "message/cpim", makeCPIM(d.owner.profile.public, d.peer, eventID, "message/imdn+xml", body, false))
}
func xmlEscape(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func (r *rcsSIP) capability(ctx context.Context, peer string) error {
	if !r.profile.chat {
		return errors.New("rcs-chat-disabled")
	}
	if !strings.HasPrefix(peer, "tel:") {
		peer = "tel:" + peer
	}
	// A capability query never submits a user message.
	callID, _, tag, _ := sip.NewTransaction()
	request := r.request("OPTIONS", peer, "<"+peer+">", "<"+r.profile.public+">;tag="+tag, callID, 1)
	r.mu.Lock()
	routes := append([]string(nil), r.routes...)
	r.mu.Unlock()
	sip.ApplyRouteSet(&request, routes)
	response, err := r.stack.Exchange(ctx, request)
	if err != nil {
		return errors.New("rcs-capability-unavailable")
	}
	if response.StatusCode() != 200 || !strings.Contains(strings.Join(response.HeaderList("Contact"), ","), "rcse.im") {
		return errors.New("rcs-peer-unavailable")
	}
	return nil
}

func (r *rcsSIP) send(ctx context.Context, peer, id, text string) error {
	if !strings.HasPrefix(peer, "tel:") {
		peer = "tel:" + peer
	}
	if err := r.capability(ctx, peer); err != nil {
		return err
	}
	d, err := r.openChat(ctx, peer)
	if err != nil {
		return err
	}
	d.mu.Lock()
	media := d.media
	d.mu.Unlock()
	if media == nil {
		return errors.New("rcs-media-network")
	}
	return media.send(ctx, id, "message/cpim", makeCPIM(r.profile.public, peer, id, "text/plain; charset=UTF-8", []byte(text), true))
}
