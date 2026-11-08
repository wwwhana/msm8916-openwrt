// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/json"
	"time"
)

type rcsPeerCapability struct {
	state   string
	expires time.Time
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
	ready := r.state == "registered" && r.chat
	if value, ok := r.capabilities[peer]; ok && ready && time.Now().Before(value.expires) {
		r.mu.Unlock()
		return value.state
	}
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
	if r.capabilities == nil {
		r.capabilities = map[string]rcsPeerCapability{}
	}
	if len(r.capabilities) >= 256 {
		r.capabilities = map[string]rcsPeerCapability{}
	}
	duration := 5 * time.Minute
	if state == "unknown" {
		duration = 30 * time.Second
	}
	r.capabilities[peer] = rcsPeerCapability{state, time.Now().Add(duration)}
	r.mu.Unlock()
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
