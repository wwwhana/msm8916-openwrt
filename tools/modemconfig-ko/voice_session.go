// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/damonto/ims-go/ims/voice"
	"os"
	"path/filepath"
	"time"
)

const voiceLeaseDuration = 12 * time.Second
const voiceDisplayPath = "/var/run/a8-call.json"

// A fresh random token belongs to one page, never to localStorage or a URL.
type voiceOwner struct {
	token     string
	expires   time.Time
	releasing bool
}

func (o *voiceOwner) matches(token string) bool { return hexID(token, 32) && o.token == token }
func (o *voiceOwner) alive(now time.Time) bool  { return o.token != "" && now.Before(o.expires) }
func (o *voiceOwner) claim(token string, now time.Time) error {
	if !hexID(token, 32) {
		return errors.New("invalid-request")
	}
	// An expired active owner is released only after its call has been ended.
	if o.releasing || o.token != "" && !o.matches(token) {
		return errors.New("call-in-use")
	}
	o.token, o.expires = token, now.Add(voiceLeaseDuration)
	return nil
}

func (v *voiceService) trackState(state string, now time.Time) {
	if v.owner.releasing && state != "ended" && state != "failed" {
		state = "ending"
	}
	previous := v.state
	v.state = state
	if (state == "confirmed" || state == "active") && v.connectedAt.IsZero() {
		v.connectedAt = now
	}
	if state == "ended" || state == "failed" {
		if v.endedAt.IsZero() {
			v.endedAt = now
		}
		if previous != "ended" && previous != "failed" {
			v.owner = voiceOwner{}
		}
	}
}

func (v *voiceService) duration(now time.Time) int64 {
	if v.connectedAt.IsZero() {
		return 0
	}
	if !v.endedAt.IsZero() {
		now = v.endedAt
	}
	return max(0, int64(now.Sub(v.connectedAt)/time.Second))
}

func (v *voiceService) expireOwner(now time.Time) {
	v.mu.Lock()
	expired, token := v.owner.token != "" && !v.owner.releasing && !v.owner.alive(now), v.owner.token
	if expired {
		v.owner.expires = now.Add(voiceLeaseDuration)
	}
	v.mu.Unlock()
	if expired {
		go func() { _, _ = v.releaseOwner(token, "page-expired") }()
	}
}

func (v *voiceService) releaseOwner(token, reason string) (any, error) {
	v.mu.Lock()
	if !v.owner.matches(token) {
		v.mu.Unlock()
		return nil, errors.New("call-in-use")
	}
	if v.owner.releasing {
		v.mu.Unlock()
		return map[string]any{"state": "ending"}, nil
	}
	call, id := v.call, v.id
	active := call != nil && !callFinished(call.State())
	if !active && !v.busy {
		v.owner = voiceOwner{}
		v.mu.Unlock()
		return map[string]any{"state": "released"}, nil
	}
	v.owner.releasing = true
	v.endReason = reason
	// The first outgoing event supplies the Call. It must receive Hangup before
	// its context is canceled, so SIP CANCEL and the final ACK can be sent.
	v.state = "ending"
	v.mu.Unlock()
	if active {
		v.releaseCall(call, id)
	}
	v.publishDisplay()
	return map[string]any{"state": "released"}, nil
}

// SIP retains its canceled INVITE transaction for late responses. Once the
// cancellation intent has been committed, that cleanup must not own the UI,
// microphone or the next call. Its late events cannot replace a newer call.
func (v *voiceService) releaseCall(call *voice.Call, id string) {
	ctx, stop := context.WithTimeout(v.ctx, 2*time.Second)
	err := call.Hangup(ctx)
	stop()
	v.mu.Lock()
	if v.id == id && v.call == call {
		if err != nil && v.detail == "" {
			v.detail = voiceFailure(err)
		}
		if v.dialCancel != nil {
			v.dialCancel()
			v.dialCancel = nil
		}
		v.busy = false
		v.call = nil
		state := "ended"
		if v.failure != "" {
			state = "failed"
		}
		v.trackState(state, time.Now())
	}
	v.mu.Unlock()
	v.publishDisplay()
}

// Root-only volatile display state; neither audio nor the ownership token is saved.
func (v *voiceService) publishDisplay() {
	_ = v.persistHistory()
	v.mu.Lock()
	state := v.state
	if v.call != nil {
		state = v.callState()
		v.trackState(state, time.Now())
	}
	data := map[string]any{"pid": os.Getpid(), "updated_at": time.Now().Unix(), "state": state,
		"id": v.id, "peer": v.peer, "direction": v.direction, "audio": v.audio, "duration": v.duration(time.Now())}
	v.mu.Unlock()
	file, err := os.CreateTemp(filepath.Dir(voiceDisplayPath), ".a8-call-")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(data)
	closed := file.Close()
	if err == nil && closed == nil {
		_ = os.Rename(file.Name(), voiceDisplayPath)
	}
}

// Keep cancellation visible until the SIP transaction has actually finished.
func (v *voiceService) callState() string {
	state := string(v.call.State())
	if v.owner.releasing && state != "ended" && state != "failed" {
		return "ending"
	}
	return state
}
