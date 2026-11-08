// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const controlSocket = "/var/run/modemconfig-ko.sock"

var runtimeServices struct {
	sync.RWMutex
	voice *voiceService
	rcs   *rcsService
}

func controlCommand(method string, reader io.Reader) int {
	request, err := readMessageRequest(reader)
	if err != nil {
		emit(map[string]any{"error": "invalid-request"})
		return 0
	}
	data, _ := json.Marshal(request)
	result, err := callServices(context.Background(), method, data)
	if err != nil {
		result = map[string]any{"error": "service-unavailable"}
	}
	emit(result)
	return 0
}

func callServices(ctx context.Context, method string, data []byte) (map[string]any, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", controlSocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/"+method, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 65537)).Decode(&result) != nil {
		result = map[string]any{"error": "service-unavailable"}
	}
	return result, nil
}

func serveServices(ctx context.Context, v *voiceService, r *rcsService, subscription, session string) (func(), error) {
	if info, err := os.Lstat(controlSocket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("invalid control socket")
		}
		if err = os.Remove(controlSocket); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("unix", controlSocket)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(controlSocket, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	runtimeServices.Lock()
	runtimeServices.voice, runtimeServices.rcs = v, r
	runtimeServices.Unlock()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fail := func(code string) { _ = json.NewEncoder(w).Encode(map[string]any{"error": code}) }
		if req.Method != http.MethodPost {
			fail("invalid-request")
			return
		}
		input, err := readMessageRequest(req.Body)
		if err != nil {
			fail("invalid-request")
			return
		}
		method := strings.TrimPrefix(req.URL.Path, "/")
		if method != "voice_status" && method != "rcs_status" && (input.Subscription != subscription || input.Session != session) {
			fail("sim-unavailable")
			return
		}
		if method == "rcs_status" {
			_ = json.NewEncoder(w).Encode(r.snapshot())
			return
		}
		if method == "rcs_capability" {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": r.capability(req.Context(), input.To)})
			return
		}
		if method == "rcs_retry" {
			select {
			case r.retry <- struct{}{}:
			default:
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "scheduled"})
			return
		}
		if !strings.HasPrefix(method, "voice_") {
			fail("invalid-request")
			return
		}
		callCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		defer stop()
		result, err := v.action(callCtx, method, input)
		if err != nil {
			fail(err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 4096}
	go func() { _ = server.Serve(listener) }()
	return func() {
		_ = server.Close()
		_ = os.Remove(controlSocket)
		runtimeServices.Lock()
		runtimeServices.voice, runtimeServices.rcs = nil, nil
		runtimeServices.Unlock()
	}, nil
}
