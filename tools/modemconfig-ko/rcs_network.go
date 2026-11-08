// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"
)

func rcsEnabled() bool {
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	value, _ := exec.CommandContext(ctx, "uci", "-q", "get", "sms_manager.@sms_manager[0].rcs_enabled").Output()
	return strings.TrimSpace(string(value)) != "0"
}

// TTA R9 §6.4.3 uses the active internet route, including Wi-Fi. Binding to
// wwan0 would strand RCS on LTE while the router has switched its WAN route.
func rcsDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 15 * time.Second}
	return dialer.DialContext(ctx, network, address)
}
