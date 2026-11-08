// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/damonto/ims-go/ims/security"
	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

// Library SIP logs include authentication data and message bodies. Emit only
// a fixed request method or numeric response status, never the log itself.
type protocolLog struct{ allowSIP func(int) error }

func (protocolLog) Enabled(context.Context, slog.Level) bool { return true }
func (p protocolLog) WithAttrs([]slog.Attr) slog.Handler     { return p }
func (p protocolLog) WithGroup(string) slog.Handler          { return p }
func (p protocolLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "sip transport policy" {
		trace := map[string]any{"stage": "transport"}
		record.Attrs(func(attr slog.Attr) bool {
			switch attr.Key {
			case "method":
				switch method := attr.Value.String(); method {
				case "INVITE", "ACK", "CANCEL", "BYE", "PRACK", "UPDATE":
					trace["method"] = method
				}
			case "datagram_only":
				trace["udp_only"] = attr.Value.Bool()
			case "bytes":
				trace["bytes"] = attr.Value.Int64()
			}
			return true
		})
		if trace["method"] != nil {
			addVoiceTrace(trace)
		}
		return nil
	}
	if event := libraryDiagnostic(record); event != nil {
		emit(event)
		return nil
	}
	if record.Message == "retrying initial IMS registration after invalid Security-Server" {
		emit(map[string]any{"stage": "ims-security-agreement", "state": "invalid-server-proposal"})
		return nil
	}
	if record.Message != "sip message" {
		return nil
	}
	var direction, startLine string
	messageBytes := 0
	var message string
	var target net.Addr
	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "direction":
			direction = attr.Value.String()
		case "start_line":
			startLine = attr.Value.String()
		case "sip_message":
			message = attr.Value.String()
			messageBytes = len(message)
		case "target_addr":
			target, _ = attr.Value.Any().(net.Addr)
		}
		return true
	})
	if event := sipDiagnostic(direction, startLine); event != nil {
		if parsed, err := sip.ParseMessage([]byte(message)); err == nil {
			method := parsed.Method()
			if parsed.StatusCode() != 0 {
				fields := strings.Fields(parsed.Header("CSeq"))
				if len(fields) == 2 {
					method = fields[1]
				}
			}
			switch method {
			case "INVITE", "ACK", "CANCEL", "BYE", "PRACK", "UPDATE":
				trace := map[string]any{"direction": direction, "method": method}
				if method == "INVITE" {
					trace["record_routes"] = len(parsed.HeadersByName("Record-Route"))
					trace["requires_100rel"] = parsed.HeaderHasToken("Require", "100rel")
					trace["requires_precondition"] = parsed.HeaderHasToken("Require", "precondition")
				}
				if parsed.StatusCode() != 0 {
					trace["status"] = parsed.StatusCode()
				}
				addVoiceTrace(trace)
			}
		}
		if direction == "send" && event["method"] == "REGISTER" && p.allowSIP != nil {
			if port := sipViaPort(message); port != 0 {
				if err := p.allowSIP(port); err != nil {
					return failed("ims-firewall", err)
				}
			}
		}
		event["bytes"] = messageBytes
		if family := addressFamily(target); family != 0 {
			event["target_family"] = family
		}
		emit(event)
		if direction == "recv" && event["status"] == 401 {
			if details := sipSecurityDiagnostic(message); details != nil {
				emit(details)
			}
		}
		if direction == "recv" && event["method"] == "MESSAGE" {
			if parsed, err := sip.ParseMessage([]byte(message)); err == nil {
				detail := map[string]any{"stage": "sms-wire", "body_bytes": len(parsed.Body)}
				content, _, _ := strings.Cut(parsed.Header("Content-Type"), ";")
				switch strings.ToLower(strings.TrimSpace(content)) {
				case "application/vnd.3gpp.sms":
					detail["content"] = "3gpp-sms"
					if len(parsed.Body) != 0 {
						detail["rp_type"] = int(parsed.Body[0])
					}
				case "multipart/mixed":
					detail["content"] = "multipart"
				}
				emit(detail)
			}
		}
	}
	return nil
}

func libraryDiagnostic(record slog.Record) map[string]any {
	labels := map[string]string{
		"rejecting sms message payload":                   "payload",
		"rejecting unsupported sms rp message type":       "rp-type",
		"ignoring malformed sms message":                  "rp-format",
		"rejecting inbound sms deliver":                   "deliver",
		"rejecting inbound sms":                           "storage",
		"discarding msisdn-less sms without feature-caps": "missing-feature",
		"discarding msisdn-less sms without xml From":     "missing-sender",
		"rejecting sms-pp data download":                  "sim-download",
		"sms-pp data download delivered":                  "sim-download-accepted",
		"sending inbound sms delivery report":             "delivery-report",
		"reconnecting client":                             "reconnecting",
		"reconnect attempt failed":                        "reconnect-failed",
	}
	label, ok := labels[record.Message]
	if !ok {
		return nil
	}
	event := map[string]any{"stage": "ims-library", "event": label}
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != "err" && attr.Key != "cause" {
			return true
		}
		err, ok := attr.Value.Any().(error)
		if !ok {
			return true
		}
		var response *sip.ResponseError
		if errors.As(err, &response) {
			event["sip_status"] = response.StatusCode
		}
		var timeout *session.TransactionTimeoutError
		if errors.As(err, &timeout) {
			event["timeout"] = true
		}
		if errors.Is(err, context.Canceled) {
			event["canceled"] = true
		}
		if errors.Is(err, io.EOF) {
			event["eof"] = true
		}
		var networkError *net.OpError
		if errors.As(err, &networkError) {
			switch networkError.Op {
			case "read", "write", "dial", "set":
				event["network_op"] = networkError.Op
			}
			event["local_family"] = addressFamily(networkError.Source)
			event["target_family"] = addressFamily(networkError.Addr)
		}
		var errno syscall.Errno
		if errors.As(err, &errno) {
			event["errno"] = uint64(errno)
		}
		for _, kind := range []string{"writing udp packet", "reading udp packet", "unsupported dcs", "truncated", "trailing data", "timestamp", "originator", "keepalive", "invalid", "maintaining IMS registration", "running IMS session"} {
			if strings.Contains(err.Error(), kind) {
				event["reason"] = kind
				break
			}
		}
		return true
	})
	return event
}

func addressFamily(address net.Addr) int {
	var ip net.IP
	switch address := address.(type) {
	case *net.UDPAddr:
		if address != nil {
			ip = address.IP
		}
	case *net.TCPAddr:
		if address != nil {
			ip = address.IP
		}
	}
	if ip.To4() != nil {
		return 4
	}
	if ip.To16() != nil {
		return 6
	}
	return 0
}

func sipSecurityDiagnostic(raw string) map[string]any {
	message, err := sip.ParseMessage([]byte(raw))
	if err != nil {
		return nil
	}
	event := map[string]any{
		"stage":              "ims-security-challenge",
		"security_server":    strings.TrimSpace(message.Headers.Get("Security-Server")) != "",
		"auth_challenge":     message.Headers.Has("WWW-Authenticate"),
		"operator_ipsec_yes": strings.Contains(strings.ToLower(message.Headers.Get("P-LGUplus-Specific-Control")), "ipsec=yes"),
	}
	var challenge security.Challenge
	if challenge.UnmarshalText([]byte(message.Headers.Get("WWW-Authenticate"))) == nil {
		switch challenge.Algorithm {
		case "AKAv1-MD5", "AKAv2-MD5", "MD5":
			event["auth_algorithm"] = challenge.Algorithm
		}
	}
	var algorithms []string
	for _, value := range message.Headers.Values("Security-Server") {
		var parameters security.Params
		if parameters.UnmarshalText([]byte(value)) != nil {
			event["invalid_security_server"] = true
			continue
		}
		for _, param := range parameters {
			switch param.Alg {
			case "hmac-sha-1-96", "hmac-sha2-256-128", "hmac-md5-96":
				algorithms = append(algorithms, param.Alg)
			}
		}
	}
	if len(algorithms) != 0 {
		event["integrity_algorithms"] = algorithms
	}
	return event
}

func sipViaPort(message string) int {
	for line := range strings.SplitSeq(message, "\r\n") {
		if !strings.HasPrefix(line, "Via: SIP/2.0/UDP ") {
			continue
		}
		hostPort, _, _ := strings.Cut(strings.TrimPrefix(line, "Via: SIP/2.0/UDP "), ";")
		_, portText, err := net.SplitHostPort(hostPort)
		port, parseErr := strconv.Atoi(portText)
		if err == nil && parseErr == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	return 0
}

func sipDiagnostic(direction, startLine string) map[string]any {
	if direction != "send" && direction != "recv" {
		return nil
	}
	fields := strings.Fields(startLine)
	if len(fields) < 2 {
		return nil
	}
	event := map[string]any{"stage": "sip", "direction": direction}
	if fields[0] == "SIP/2.0" {
		code, err := strconv.Atoi(fields[1])
		if err != nil || code < 100 || code > 699 {
			return nil
		}
		event["status"] = code
		return event
	}
	switch fields[0] {
	case "REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE", "OPTIONS", "INVITE", "ACK", "CANCEL", "BYE", "PRACK", "UPDATE":
		event["method"] = fields[0]
		return event
	}
	return nil
}
