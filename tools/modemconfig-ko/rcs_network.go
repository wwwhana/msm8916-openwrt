// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func rcsEnabled() bool {
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	value, _ := exec.CommandContext(ctx, "uci", "-q", "get", "sms_manager.@sms_manager[0].rcs_enabled").Output()
	return strings.TrimSpace(string(value)) != "0"
}

// Wi-Fi failover deliberately tears down the ordinary LTE data bearer.
// RCS uses the active data network; IMS calls keep their dedicated IMS bearer.
func rcsDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 15 * time.Second}
	if device, err := net.InterfaceByName("wwan0"); err == nil && device.Flags&net.FlagUp != 0 {
		if addresses, err := device.Addrs(); err == nil {
			for _, address := range addresses {
				ip, _, _ := net.ParseCIDR(address.String())
				if ip.IsGlobalUnicast() {
					dialer.Control = func(_, _ string, raw syscall.RawConn) error {
						var bind error
						err := raw.Control(func(fd uintptr) { bind = unix.BindToDevice(int(fd), "wwan0") })
						if err != nil {
							return err
						}
						return bind
					}
					break
				}
			}
		}
	}
	return dialer.DialContext(ctx, network, address)
}
