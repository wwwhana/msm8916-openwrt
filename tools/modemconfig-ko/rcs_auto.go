// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type rcsPeerCapability struct {
	state   string
	expires time.Time
}

type rcsCapabilityQuery struct {
	done chan struct{}
	err  error
}

// Compare complete decoded feature URNs, never an arbitrary Contact substring.
func rcsContactChat(contacts []string) bool {
	for _, contact := range contacts {
		var parts []string
		start, angle := 0, 0
		quoted, escaped := false, false
		for i, c := range contact {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quoted {
				escaped = true
			} else if c == '"' {
				quoted = !quoted
			} else if !quoted {
				switch c {
				case '<':
					angle++
				case '>':
					angle--
				case ';', ',':
					if angle == 0 {
						parts = append(parts, contact[start:i])
						start = i + 1
					}
				}
			}
		}
		if quoted || angle != 0 {
			continue
		}
		parts = append(parts, contact[start:])
		for _, part := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || !strings.EqualFold(key, "+g.3gpp.icsi-ref") && !strings.EqualFold(key, "+g.3gpp.iari-ref") {
				continue
			}
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, `"`) {
				decoded, err := strconv.Unquote(value)
				if err != nil {
					continue
				}
				value = decoded
			}
			value, err := url.PathUnescape(value)
			if err != nil {
				continue
			}
			for _, feature := range strings.Split(value, ",") {
				if feature == "urn:urn-7:3gpp-service.ims.icsi.oma.cpm.session" || strings.EqualFold(key, "+g.3gpp.iari-ref") && feature == "urn:urn-7:3gpp-application.ims.iari.rcse.im" {
					return true
				}
			}
		}
	}
	return false
}

func (r *rcsSIP) capability(ctx context.Context, peer string) error {
	peer = canonicalPeer(peer)
	if !r.profile.chat || !validPhone(peer) {
		return errors.New("rcs-peer-unavailable")
	}
	r.mu.Lock()
	if value, ok := r.capabilities[peer]; ok && time.Now().Before(value.expires) {
		r.mu.Unlock()
		if value.state == "supported" {
			return nil
		}
		if value.state == "unsupported" {
			return errors.New("rcs-peer-unavailable")
		}
		return errors.New("rcs-capability-unavailable")
	}
	if query := r.capabilityQueries[peer]; query != nil {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-query.done:
			return query.err
		}
	}
	if len(r.capabilityQueries) >= 32 {
		r.mu.Unlock()
		return errors.New("rcs-busy")
	}
	query := &rcsCapabilityQuery{done: make(chan struct{})}
	if r.capabilityQueries == nil {
		r.capabilityQueries = map[string]*rcsCapabilityQuery{}
	}
	r.capabilityQueries[peer] = query
	r.mu.Unlock()
	if r.profile.presence {
		query.err = r.presenceCapability(ctx, peer)
	} else {
		query.err = r.optionsCapability(ctx, peer)
	}
	state, expiry := "supported", r.profile.capExpiry
	if query.err != nil {
		state, expiry = "unknown", 30*time.Second
		if query.err.Error() == "rcs-peer-unavailable" {
			state, expiry = "unsupported", r.profile.nonRCSExpiry
		}
	}
	r.mu.Lock()
	if r.capabilities == nil {
		r.capabilities = map[string]rcsPeerCapability{}
	}
	if len(r.capabilities) >= 256 {
		oldest := peer
		for key, value := range r.capabilities {
			if old, ok := r.capabilities[oldest]; !ok || value.expires.Before(old.expires) {
				oldest = key
			}
		}
		delete(r.capabilities, oldest)
	}
	r.capabilities[peer] = rcsPeerCapability{state, time.Now().Add(expiry)}
	delete(r.capabilityQueries, peer)
	close(query.done)
	r.mu.Unlock()
	return query.err
}

func (r *rcsService) capability(ctx context.Context, to string) string {
	peer := canonicalPeer(to)
	if !validPhone(peer) {
		return "unknown"
	}
	if !rcsEnabled() {
		return "disabled"
	}
	r.mu.Lock()
	client := r.client
	ready := r.readyLocked() && r.chat
	r.mu.Unlock()
	if !ready || client == nil {
		return "unknown"
	}
	query, stop := context.WithTimeout(ctx, 4*time.Second)
	defer stop()
	if client.verify(query) != nil {
		return "unknown"
	}
	err := client.capability(query, peer)
	state := "supported"
	if err != nil {
		state = "unknown"
		if err.Error() == "rcs-peer-unavailable" {
			state = "unsupported"
		}
	}
	r.mu.Lock()
	current := r.client == client && r.readyLocked()
	r.mu.Unlock()
	if !current {
		return "unknown"
	}
	return state
}

func autoRCSPeer(state messagingRuntime, request messageRequest) bool {
	if !state.rcsReady() || !rcsEnabled() || !validPhone(canonicalPeer(request.To)) {
		return false
	}
	data, _ := json.Marshal(messageRequest{Subscription: request.Subscription, Session: request.Session, To: request.To})
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	result, err := callServices(ctx, "rcs_capability", data)
	return err == nil && result["state"] == "supported"
}
