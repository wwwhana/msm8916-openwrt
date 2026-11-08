// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

const rcsPIDFNamespace = "urn:ietf:params:xml:ns:pidf"
const rcsOMAPresence = "urn:oma:xml:prs:pidf:oma-pres"
const rcsChatService = "org.openmobilealliance:ChatSession"
const rcsDiscoveryService = "org.3gpp.urn:urn-7:3gpp-application.ims.iari.rcse.dp"

func rcsPresenceBody(profile *rcsProfile) []byte {
	entity := xmlEscape(profile.public)
	body := `<?xml version="1.0" encoding="UTF-8"?><presence xmlns="` + rcsPIDFNamespace + `" xmlns:op="` + rcsOMAPresence + `" entity="` + entity + `">`
	for index, service := range []struct{ id, version string }{{rcsDiscoveryService, "1.0"}, {rcsChatService, "2.0"}} {
		if service.id == rcsChatService && !profile.chat {
			continue
		}
		body += `<tuple id="service-` + strconv.Itoa(index) + `"><status><basic>open</basic></status><op:service-description><op:service-id>` + service.id + `</op:service-id><op:version>` + service.version + `</op:version></op:service-description><contact>` + entity + `</contact></tuple>`
	}
	return []byte(body + `</presence>`)
}

// Inspect tokens before decoding: foreign namespaces, extra roots, deep trees
// and declarations must not turn a network document into a false capability.
func parseRCSPresence(data []byte, peer string) (bool, error) {
	bad := errors.New("rcs-presence-invalid")
	if len(data) == 0 || len(data) > 512*1024 {
		return false, bad
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots, nodes := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, bad
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			nodes++
			if depth > 16 || roots > 1 || nodes > 4096 {
				return false, bad
			}
			seen := map[xml.Name]bool{}
			for _, attr := range token.Attr {
				if seen[attr.Name] {
					return false, bad
				}
				seen[attr.Name] = true
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return false, bad
		case xml.ProcInst:
			if token.Target != "xml" || roots != 0 {
				return false, bad
			}
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return false, bad
			}
		}
	}
	var document struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:pidf presence"`
		Entity  string   `xml:"entity,attr"`
		Tuples  []struct {
			ID     string `xml:"id,attr"`
			Status struct {
				Basic string `xml:"urn:ietf:params:xml:ns:pidf basic"`
			} `xml:"urn:ietf:params:xml:ns:pidf status"`
			Services []struct {
				ID      string `xml:"urn:oma:xml:prs:pidf:oma-pres service-id"`
				Version string `xml:"urn:oma:xml:prs:pidf:oma-pres version"`
			} `xml:"urn:oma:xml:prs:pidf:oma-pres service-description"`
		} `xml:"urn:ietf:params:xml:ns:pidf tuple"`
	}
	if roots != 1 || depth != 0 || xml.Unmarshal(data, &document) != nil || !validPhone(canonicalPeer(peer)) || !rcsField(document.Entity) || canonicalPeer(document.Entity) != canonicalPeer(peer) {
		return false, bad
	}
	chat := false
	ids := map[string]bool{}
	for _, tuple := range document.Tuples {
		if !rcsField(tuple.ID) || ids[tuple.ID] {
			return false, bad
		}
		ids[tuple.ID] = true
		for _, service := range tuple.Services {
			// Closed describes availability, not loss of RCS support; the
			// network can still accept a store-and-forward chat for this peer.
			if service.ID == rcsChatService && service.Version == "2.0" && (tuple.Status.Basic == "open" || tuple.Status.Basic == "closed") {
				chat = true
			}
		}
	}
	return chat, nil
}

type rcsPresenceQuery struct {
	peer, tag, remoteTag string
	result               chan error
	sequence             int
}

func (r *rcsSIP) presenceCapability(ctx context.Context, peer string) error {
	callID, _, tag, _ := sip.NewTransaction()
	query := &rcsPresenceQuery{peer: peer, tag: tag, result: make(chan error, 1)}
	r.mu.Lock()
	if r.presenceQueries == nil {
		r.presenceQueries = map[string]*rcsPresenceQuery{}
	}
	r.presenceQueries[callID] = query
	routes := append([]string(nil), r.routes...)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.presenceQueries, callID)
		r.mu.Unlock()
	}()
	target := "tel:" + peer
	request := r.request("SUBSCRIBE", target, "<"+target+">", `"Anonymous" <sip:anonymous@anonymous.invalid>;tag=`+tag, callID, 1)
	request.Headers.Add("Event", "presence")
	request.Headers.Add("Expires", "0")
	request.Headers.Add("Accept", "application/pidf+xml")
	request.Headers.Add("P-Preferred-Identity", "<"+r.profile.public+">")
	request.Headers.Add("Privacy", "id")
	request.Headers.Add("Accept-Contact", "*;+g.oma.sip-im;explicit;require")
	sip.ApplyRouteSet(&request, routes)
	response, err := r.exchange(ctx, request)
	if err != nil {
		return err
	}
	if response.StatusCode() == 404 || response.StatusCode() == 604 {
		return errors.New("rcs-peer-unavailable")
	}
	if response.StatusCode() < 200 || response.StatusCode() >= 300 {
		return errors.New("rcs-capability-unavailable")
	}
	// RFC 6665 allows NOTIFY before the SUBSCRIBE response. The query is
	// installed before sending and its result channel retains that early reply.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return errors.New("rcs-sip-network")
	case err = <-query.result:
		return err
	}
}

func (r *rcsSIP) handlePresence(ctx context.Context, req session.Request) error {
	message := req.Message
	event := strings.Split(message.Header("Event"), ";")
	if !r.profile.presence || !strings.EqualFold(strings.TrimSpace(event[0]), "presence") {
		return r.stack.WriteStatus(ctx, req, 489, "Bad Event")
	}
	for _, parameter := range event[1:] {
		if key, _, _ := strings.Cut(strings.TrimSpace(parameter), "="); strings.EqualFold(key, "id") {
			return r.stack.WriteStatus(ctx, req, 481, "Subscription Does Not Exist")
		}
	}
	r.mu.Lock()
	query := r.presenceQueries[message.Header("Call-ID")]
	remoteTag := sip.HeaderTag(message.Header("From"))
	matched := query != nil && remoteTag != "" && sip.HeaderTag(message.Header("To")) == query.tag && canonicalPeer(rcsURI(message.Header("From"))) == query.peer &&
		(query.remoteTag == "" || query.remoteTag == remoteTag)
	if matched {
		query.remoteTag = remoteTag
	}
	r.mu.Unlock()
	if !matched {
		return r.stack.WriteStatus(ctx, req, 481, "Subscription Does Not Exist")
	}
	subscription := strings.Split(message.Header("Subscription-State"), ";")
	state := strings.ToLower(strings.TrimSpace(subscription[0]))
	reason := ""
	for _, parameter := range subscription[1:] {
		key, value, _ := strings.Cut(strings.TrimSpace(parameter), "=")
		if strings.EqualFold(key, "reason") {
			if reason != "" || value == "" {
				return r.stack.WriteStatus(ctx, req, 400, "Bad Request")
			}
			reason = strings.ToLower(strings.TrimSpace(value))
		}
	}
	sequence := strings.Fields(message.Header("CSeq"))
	if len(sequence) != 2 || sequence[1] != "NOTIFY" || len(message.Headers.Values("Subscription-State")) != 1 || len(message.Headers.Values("Event")) != 1 || (state != "terminated" && state != "active" && state != "pending") {
		return r.stack.WriteStatus(ctx, req, 400, "Bad Request")
	}
	number, err := strconv.Atoi(sequence[0])
	if err != nil || number < 1 || number >= 1<<31 {
		return r.stack.WriteStatus(ctx, req, 400, "Bad Request")
	}
	r.mu.Lock()
	older := number < query.sequence
	if !older {
		query.sequence = number
	}
	r.mu.Unlock()
	if older {
		return r.stack.WriteStatus(ctx, req, 500, "CSeq Out of Order")
	}
	var result error
	if state == "terminated" && reason != "timeout" {
		// A refused or deactivated subscription is not a capability answer.
		result = errors.New("rcs-capability-unavailable")
	} else if len(message.Body) == 0 {
		if state != "terminated" {
			return r.stack.WriteStatus(ctx, req, 200, "OK")
		}
		result = errors.New("rcs-capability-unavailable")
	} else {
		kind, _, err := mime.ParseMediaType(message.Header("Content-Type"))
		if err != nil || kind != "application/pidf+xml" || message.Header("Content-Encoding") != "" {
			return r.stack.WriteStatus(ctx, req, 415, "Unsupported Media Type")
		}
		chat, err := parseRCSPresence(message.Body, query.peer)
		if err != nil {
			return r.stack.WriteStatus(ctx, req, 400, "Bad Request")
		}
		if !chat {
			result = errors.New("rcs-peer-unavailable")
		}
	}
	if err := r.stack.WriteStatus(ctx, req, 200, "OK"); err != nil {
		return err
	}
	select {
	case query.result <- result:
	default:
	}
	return nil
}

type rcsPublication struct {
	etag             string
	until, next      time.Time
	seconds          int
	status, failures int
}

func rcsSIPToken(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-.!%*_+`'~", c)) {
			return false
		}
	}
	return true
}

func (p *rcsPublication) request(ctx context.Context, r *rcsSIP, remove bool) (sip.Message, error) {
	callID, _, tag, _ := sip.NewTransaction()
	request := r.request("PUBLISH", r.profile.public, "<"+r.profile.public+">", "<"+r.profile.public+">;tag="+tag, callID, 1)
	request.Headers.Add("Event", "presence")
	request.Headers.Add("P-Preferred-Identity", "<"+r.profile.public+">")
	request.Headers.Add("Accept", "application/pidf+xml")
	request.Headers.Add("Accept-Contact", "*;+g.oma.sip-im;explicit;require")
	if p.etag != "" {
		request.Headers.Add("SIP-If-Match", p.etag)
	} else if !remove {
		request.Headers.Add("Content-Type", "application/pidf+xml")
		request.Body = rcsPresenceBody(r.profile)
	}
	seconds := p.seconds
	if seconds == 0 {
		seconds = 3600
	}
	if remove {
		seconds = 0
	}
	request.Headers.Add("Expires", strconv.Itoa(seconds))
	r.mu.Lock()
	routes := append([]string(nil), r.routes...)
	r.mu.Unlock()
	sip.ApplyRouteSet(&request, routes)
	return r.exchange(ctx, request)
}

func (p *rcsPublication) update(ctx context.Context, r *rcsSIP, now time.Time) {
	if now.Before(p.next) {
		return
	}
	response, err := p.request(ctx, r, false)
	status := response.StatusCode()
	if err != nil {
		status = 0
	}
	if status == 200 {
		seconds, e := strconv.Atoi(response.Header("Expires"))
		etag := response.Header("SIP-ETag")
		if e == nil && seconds > 0 && seconds <= 30*24*3600 && rcsSIPToken(etag) {
			p.etag, p.status, p.failures = etag, status, 0
			p.until = now.Add(time.Duration(seconds) * time.Second)
			p.next = now.Add(max(time.Duration(seconds)*time.Second*4/5, r.profile.publishThrottle))
			return
		}
		status = 0
	}
	p.failures++
	p.status = status
	delay := 20 * time.Minute
	switch status {
	case 400, 403, 412, 423, 489:
		if p.failures == 1 {
			delay = 0
		}
		if status == 412 && p.etag != "" {
			p.etag, p.until = "", time.Time{}
			if p.failures <= 2 {
				delay = 0
			}
		}
		if status == 423 {
			seconds, err := strconv.Atoi(response.Header("Min-Expires"))
			requested := p.seconds
			if requested == 0 {
				requested = 3600
			}
			if err == nil && seconds > requested && seconds <= 30*24*3600 {
				p.seconds = seconds
				if p.failures <= 2 {
					delay = 0
				}
			} else {
				delay = 20 * time.Minute
			}
		}
	case 0, 408, 500, 503:
		backoff := [...]time.Duration{120, 240, 480, 960, 3600}
		delay = backoff[min(max(p.failures-1, 0), len(backoff)-1)] * time.Second
		if parts := strings.Fields(strings.Split(response.Header("Retry-After"), ";")[0]); len(parts) > 0 {
			if seconds, err := strconv.Atoi(parts[0]); err == nil && seconds >= 0 && seconds <= 30*24*3600 {
				delay = time.Duration(seconds) * time.Second
			}
		}
	}
	if delay > 0 {
		delay = max(delay, r.profile.publishThrottle)
	}
	p.next = now.Add(delay)
}

func (p *rcsPublication) delay(now time.Time) time.Duration {
	next := p.next
	if now.Before(p.until) && p.until.Before(next) {
		next = p.until
	}
	return max(time.Millisecond, next.Sub(now))
}
