// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

// A bounded in-memory trace contains only fixed labels and response codes.
// Neither SIP headers, numbers, tokens nor microphone data enter diagnostics.
var voiceTrace struct {
	sync.Mutex
	events []map[string]any
}

func addVoiceTrace(event map[string]any) {
	voiceTrace.Lock()
	defer voiceTrace.Unlock()
	event["at"] = time.Now().UnixMilli()
	voiceTrace.events = append(voiceTrace.events, event)
	if len(voiceTrace.events) > 40 {
		voiceTrace.events = voiceTrace.events[len(voiceTrace.events)-40:]
	}
}
func snapshotVoiceTrace() []map[string]any {
	voiceTrace.Lock()
	defer voiceTrace.Unlock()
	return append([]map[string]any(nil), voiceTrace.events...)
}
func traceVoiceState(state, cause string) {
	addVoiceTrace(map[string]any{"state": state, "detail": voiceFailure(errors.New(cause))})
}

var voiceSIPCode = regexp.MustCompile(`(?:SIP/2\.0\s+|status(?: code)?[=: ]+)([1-6][0-9]{2})\b`)

func voiceFailure(err error) string {
	if err == nil || err.Error() == "" {
		return ""
	}
	var response *sip.ResponseError
	if errors.As(err, &response) {
		return "sip-" + strconv.Itoa(response.StatusCode)
	}
	if match := voiceSIPCode.FindStringSubmatch(err.Error()); len(match) == 2 {
		return "sip-" + match[1]
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var timeout *session.TransactionTimeoutError
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) {
		return "timeout"
	}
	var op *net.OpError
	if errors.As(err, &op) {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			return "network-" + strconv.Itoa(int(errno))
		}
		return "network-error"
	}
	text := strings.ToLower(err.Error())
	for _, label := range []string{"cancelled", "canceled", "codec", "precondition", "ringing", "sdp", "rtcp", "media address family", "local ims pdn", "media resources", "rtp", "history-unavailable", "timeout"} {
		if strings.Contains(text, label) {
			return strings.ReplaceAll(label, " ", "-")
		}
	}
	return "call-error"
}
