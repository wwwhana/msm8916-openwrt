// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/voice"
)

func TestVoiceFailureClearsCallAndPendingDial(t *testing.T) {
	call := &voice.Call{}
	canceled := false
	v := &voiceService{ctx: context.Background(), call: call, id: opaqueID(), busy: true, state: "answering",
		owner: voiceOwner{token: opaqueID(), expires: time.Now().Add(time.Minute)}, dialCancel: func() { canceled = true }}
	v.audioFailed(call, v.id, "call-failed")
	if v.state != "failed" || v.call != nil || v.busy || v.owner.token != "" || !canceled || v.failure != "call-failed" {
		t.Fatal("failure retained its call, owner or pending dial")
	}
}

func TestVoicePageExclusiveOwnerAndRelease(t *testing.T) {
	now := time.Unix(100, 0)
	first, second := opaqueID(), opaqueID()
	var owner voiceOwner
	if owner.claim("", now) == nil || owner.claim(first, now) != nil || owner.claim(second, now) == nil {
		t.Fatal("missing/second page acquired microphone ownership")
	}
	if !owner.matches(first) || owner.matches(second) || !owner.alive(now.Add(11*time.Second)) || owner.alive(now.Add(12*time.Second)) {
		t.Fatal("owner identity or lease boundary changed")
	}
	if owner.claim(second, now.Add(20*time.Second)) == nil {
		t.Fatal("expired active call was stolen before hangup")
	}
	v := &voiceService{ctx: context.Background(), owner: owner, state: "idle"}
	if _, err := v.releaseOwner(second, "hangup"); err == nil || !v.owner.matches(first) {
		t.Fatal("other page released owner")
	}
	if _, err := v.releaseOwner(first, "hangup"); err != nil || v.owner.token != "" {
		t.Fatal("page release left an idle owner")
	}
	if v.owner.claim(second, now) != nil {
		t.Fatal("released page was not reusable")
	}
}

func TestVoiceDurationAndTerminalPolling(t *testing.T) {
	now := time.Unix(100, 0)
	v := &voiceService{state: "dialing"}
	v.trackState("early_media", now)
	if v.duration(now.Add(time.Minute)) != 0 {
		t.Fatal("ringing was counted as talk time")
	}
	v.trackState("confirmed", now.Add(10*time.Second))
	v.trackState("active", now.Add(11*time.Second))
	if v.duration(now.Add(25*time.Second)) != 15 {
		t.Fatal("answer timestamp was reset")
	}
	v.trackState("ended", now.Add(30*time.Second))
	if v.duration(now.Add(time.Hour)) != 20 {
		t.Fatal("ended duration kept increasing")
	}
	token := opaqueID()
	_ = v.owner.claim(token, now)
	v.trackState("ended", now.Add(31*time.Second))
	if !v.owner.matches(token) {
		t.Fatal("polling previous terminal call cleared a new microphone claim")
	}
}

func TestVoiceExpiredPageWaitsForProtocolCancellation(t *testing.T) {
	token := opaqueID()
	canceled := false
	v := &voiceService{ctx: context.Background(), state: "dialing", busy: true, owner: voiceOwner{token: token, expires: time.Unix(100, 0)}, dialCancel: func() { canceled = true }}
	v.expireOwner(time.Unix(101, 0))
	deadline := time.Now().Add(time.Second)
	for {
		v.mu.Lock()
		releasing := v.owner.releasing
		if releasing {
			if canceled || v.state != "ending" || v.endReason != "page-expired" {
				t.Fatal("INVITE was abandoned before protocol cancellation")
			}
			if v.owner.claim(opaqueID(), time.Unix(102, 0)) == nil {
				t.Fatal("second page entered while cancellation was pending")
			}
		}
		v.mu.Unlock()
		if releasing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lost page did not start cancellation")
		}
		time.Sleep(time.Millisecond)
	}
}
