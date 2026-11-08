// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	ims "github.com/damonto/ims-go"
	"golang.org/x/sys/unix"
)

// This diagnostic owns RMNET1 only. Additions use "add", never "replace" or
// "flush", so cleanup can remove exactly what this process successfully added.
type imsNetwork struct {
	pcscf         []net.IP
	pcscfRecords  [][]byte
	undo          [][]string
	icmp          net.PacketConn
	firewallMu    sync.Mutex
	firewallPath  string
	allowedPorts  map[int]bool
	localIPv6     net.IP
	sipServerPort int
	closed        bool
}

func (n *imsNetwork) Replace(ctx context.Context, info ims.PDNNetworkInfo) (string, error) {
	n.firewallMu.Lock()
	defer n.firewallMu.Unlock()
	if len(n.undo) != 0 {
		return "", errors.New("IMS network is already configured")
	}
	n.closed = false
	n.allowedPorts = nil
	n.localIPv6 = append(net.IP(nil), info.LocalIPv6...)
	iface, err := net.InterfaceByName("wwan1")
	if err != nil {
		return "", err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !ip.IsLinkLocalUnicast() {
			return "", errors.New("IMS interface is already in use")
		}
	}
	if iface.Flags&net.FlagUp == 0 {
		if err := n.add(ctx, []string{"link", "set", "dev", "wwan1", "up"}, []string{"link", "set", "dev", "wwan1", "down"}); err != nil {
			return "", err
		}
	}
	for _, ip := range []net.IP{info.LocalIPv4, info.LocalIPv6} {
		if len(ip) == 0 {
			continue
		}
		family, prefix := ipFamily(ip)
		address := ip.String() + prefix
		add := []string{family, "addr", "add", address, "dev", "wwan1"}
		if family == "-6" {
			add = append(add, "nodad")
		}
		if err := n.add(ctx, add, []string{family, "addr", "del", address, "dev", "wwan1"}); err != nil {
			return "", err
		}
		if err := n.addSourceRoute(ctx, ip); err != nil {
			return "", err
		}
	}
	n.pcscf = append([]net.IP(nil), info.PCSCFIPs...)
	routed := map[string]bool{}
	source := "pco"
	if len(n.pcscf) == 0 {
		n.pcscf = resolvePCSCF(ctx, n.pcscfRecords, nil)
		source = "sim-literal"
		for _, server := range info.DNS {
			family, prefix := ipFamily(server)
			local := info.LocalIPv6
			if family == "-4" {
				local = info.LocalIPv4
			}
			if len(local) == 0 || !server.IsGlobalUnicast() {
				continue
			}
			address := server.String() + prefix
			if !routed[address] {
				if err := n.add(ctx,
					[]string{family, "route", "add", address, "dev", "wwan1", "src", local.String()},
					[]string{family, "route", "del", address, "dev", "wwan1", "src", local.String()}); err != nil {
					return "", err
				}
				routed[address] = true
			}
			if resolved := resolvePCSCF(ctx, n.pcscfRecords, imsDNSResolver(server, local).LookupIPAddr); len(resolved) != 0 {
				n.pcscf = resolved
				source = "sim-ims-dns"
				break
			}
		}
		if len(info.DNS) == 0 && len(n.pcscf) == 0 {
			n.pcscf = resolvePCSCF(ctx, n.pcscfRecords, net.DefaultResolver.LookupIPAddr)
			source = "sim-system-dns"
		}
	}
	routes := 0
	for _, server := range n.pcscf {
		family, prefix := ipFamily(server)
		local := info.LocalIPv6
		if family == "-4" {
			local = info.LocalIPv4
		}
		if len(local) == 0 || !server.IsGlobalUnicast() {
			continue
		}
		address := server.String() + prefix
		if !routed[address] {
			if err := n.add(ctx,
				[]string{family, "route", "add", address, "dev", "wwan1", "src", local.String()},
				[]string{family, "route", "del", address, "dev", "wwan1", "src", local.String()}); err != nil {
				return "", err
			}
			routed[address] = true
		}
		routes++
	}
	ipv4, ipv6 := 0, 0
	for _, ip := range n.pcscf {
		if ip.To4() != nil {
			ipv4++
		} else {
			ipv6++
		}
	}
	emit(map[string]any{"stage": "ims-host-network", "state": "configured", "interface": "wwan1", "pcscf_routes": routes,
		"pcscf_ipv4": ipv4, "pcscf_ipv6": ipv6, "pcscf_source": source, "ims_dns_count": len(info.DNS),
		"local_ipv4": len(info.LocalIPv4) != 0, "local_ipv6": len(info.LocalIPv6) != 0})
	if len(info.LocalIPv6) != 0 {
		config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
			var bindErr error
			err := raw.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), "wwan1") })
			return errors.Join(err, bindErr)
		}}
		if conn, err := config.ListenPacket(ctx, "ip6:ipv6-icmp", info.LocalIPv6.String()); err == nil {
			n.icmp = conn
			go func() {
				buffer := make([]byte, 2048)
				for {
					size, _, err := conn.ReadFrom(buffer)
					if err != nil {
						return
					}
					if event := icmpDiagnostic(buffer[:size]); event != nil {
						emit(event)
					}
				}
			}()
		}
	}
	return "wwan1", nil
}

func (n *imsNetwork) PCSCFIPs() []net.IP {
	result := make([]net.IP, len(n.pcscf))
	for i, ip := range n.pcscf {
		result[i] = append(net.IP(nil), ip...)
	}
	return result
}

// Resolve on the IMS connection without changing the system resolver or WAN.
func imsDNSResolver(server, local net.IP) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialer := net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
			var bindErr error
			err := raw.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), "wwan1") })
			return errors.Join(err, bindErr)
		}}
		if strings.HasPrefix(network, "tcp") {
			dialer.LocalAddr = &net.TCPAddr{IP: local}
		} else {
			dialer.LocalAddr = &net.UDPAddr{IP: local}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(server.String(), "53"))
	}}
}

func ipFamily(ip net.IP) (string, string) {
	if ip.To4() != nil {
		return "-4", "/32"
	}
	return "-6", "/128"
}

// IMS replies may target another carrier proxy named in Via. Route only
// traffic from our IMS address through RMNET1; leave the main table alone.
func (n *imsNetwork) addSourceRoute(ctx context.Context, local net.IP) error {
	if !local.IsGlobalUnicast() {
		return errors.New("invalid IMS route source")
	}
	family, prefix := ipFamily(local)
	table := strconv.Itoa(100000 + os.Getpid())
	for _, query := range [][]string{{family, "-N", "-j", "rule", "show"}, {family, "-N", "-j", "route", "show", "table", "all"}} {
		data, err := exec.CommandContext(ctx, "ip", query...).Output()
		if err != nil {
			return err
		}
		var entries []struct{ Table json.RawMessage }
		if err := json.Unmarshal(data, &entries); err != nil {
			return errors.New("invalid IMS route table inventory")
		}
		for _, entry := range entries {
			if strings.Trim(string(entry.Table), "\"") == table {
				return errors.New("IMS route table is already in use")
			}
		}
	}
	if err := n.add(ctx,
		[]string{family, "route", "add", "default", "dev", "wwan1", "src", local.String(), "table", table},
		[]string{family, "route", "del", "default", "dev", "wwan1", "src", local.String(), "table", table}); err != nil {
		return err
	}
	return n.add(ctx,
		[]string{family, "rule", "add", "priority", "10000", "from", local.String() + prefix, "lookup", table},
		[]string{family, "rule", "del", "priority", "10000", "from", local.String() + prefix, "lookup", table})
}

func (n *imsNetwork) add(ctx context.Context, add, undo []string) error {
	if err := exec.CommandContext(ctx, "ip", add...).Run(); err != nil {
		return err
	}
	n.undo = append(n.undo, undo)
	return nil
}

func (n *imsNetwork) Close() error {
	n.firewallMu.Lock()
	defer n.firewallMu.Unlock()
	n.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result error
	if len(n.allowedPorts) != 0 {
		if err := os.Remove(n.firewallPath); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
		result = errors.Join(result, n.removeSIPRules(ctx, n.allowedPorts))
	}
	n.allowedPorts = nil
	if n.icmp != nil {
		result = errors.Join(result, n.icmp.Close())
		n.icmp = nil
	}
	for i := len(n.undo) - 1; i >= 0; i-- {
		result = errors.Join(result, exec.CommandContext(ctx, "ip", n.undo[i]...).Run())
	}
	n.undo = nil
	return result
}

func (n *imsNetwork) sipRule(port int) []string {
	serverPort := n.sipServerPort
	if serverPort == 0 {
		serverPort = 5060
	}
	marker := "modemconfig-ko-probe-" + strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(port)
	return []string{"iifname", "wwan1", "ip6", "daddr", n.localIPv6.String(), "udp", "sport", strconv.Itoa(serverPort), "udp", "dport", strconv.Itoa(port), "counter", "accept", "comment", strconv.Quote(marker)}
}

// Reloading fw4 changes rule handles. Resolve our exact comments again before
// deleting, so a recycled handle can never remove another service's rule.
func (n *imsNetwork) removeSIPRules(ctx context.Context, ports map[int]bool) error {
	data, err := exec.CommandContext(ctx, "nft", "-j", "list", "chain", "inet", "fw4", "input").Output()
	if err != nil {
		return err
	}
	var chain struct {
		Nftables []struct {
			Rule *struct {
				Handle  uint64
				Comment string
			}
		}
	}
	if err := json.Unmarshal(data, &chain); err != nil {
		return err
	}
	owned := map[string]bool{}
	for port := range ports {
		owned["modemconfig-ko-probe-"+strconv.Itoa(os.Getpid())+"-"+strconv.Itoa(port)] = true
	}
	var result error
	for _, entry := range chain.Nftables {
		if entry.Rule != nil && entry.Rule.Handle != 0 && owned[entry.Rule.Comment] {
			result = errors.Join(result, exec.CommandContext(ctx, "nft", "delete", "rule", "inet", "fw4", "input", "handle", strconv.FormatUint(entry.Rule.Handle, 10)).Run())
		}
	}
	return result
}

func (n *imsNetwork) AllowSIP(port int) error {
	n.firewallMu.Lock()
	defer n.firewallMu.Unlock()
	if n.closed || port <= 0 || port > 65535 || n.localIPv6.To4() != nil || !n.localIPv6.IsGlobalUnicast() {
		return errors.New("invalid IMS firewall endpoint")
	}
	if n.allowedPorts[port] {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverPort := n.sipServerPort
	if serverPort == 0 {
		serverPort = 5060
	}
	if serverPort <= 0 || serverPort > 65535 {
		return errors.New("invalid IMS server port")
	}
	args := append([]string{"insert", "rule", "inet", "fw4", "input"}, n.sipRule(port)...)
	if err := exec.CommandContext(ctx, "nft", args...).Run(); err != nil {
		return err
	}
	if n.allowedPorts == nil {
		n.allowedPorts = map[int]bool{}
	}
	n.allowedPorts[port] = true
	if n.firewallPath == "" {
		n.firewallPath = "/var/run/modemconfig-ko-firewall.nft"
	}
	ports := make([]int, 0, len(n.allowedPorts))
	for allowed := range n.allowedPorts {
		ports = append(ports, allowed)
	}
	sort.Ints(ports)
	var rules strings.Builder
	for _, allowed := range ports {
		rules.WriteString(strings.Join(n.sipRule(allowed), " ") + "\n")
	}
	if err := writePrivate(n.firewallPath, []byte(rules.String())); err != nil {
		delete(n.allowedPorts, port)
		return errors.Join(err, n.removeSIPRules(ctx, map[int]bool{port: true}))
	}
	emit(map[string]any{"stage": "ims-firewall", "state": "temporary-response-rule", "interface": "wwan1"})
	return nil
}

func icmpDiagnostic(data []byte) map[string]any {
	if len(data) < 8 || data[0] < 1 || data[0] > 4 {
		return nil
	}
	return map[string]any{"stage": "ims-icmp-error", "type": data[0], "code": data[1]}
}
