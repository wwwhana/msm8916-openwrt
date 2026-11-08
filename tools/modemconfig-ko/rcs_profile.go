// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type rcsProfile struct {
	domain, private, public, auth, realm, username, password, transport string
	proxies                                                             []string
	discover                                                            map[string]bool
	chat                                                                bool
	mediaTLS                                                            bool
	presence                                                            bool
	capExpiry, nonRCSExpiry, publishThrottle                            time.Duration
}

func rcsField(value string) bool {
	return value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n\x00\t \"<>\\")
}
func rcsHost(value string) bool {
	if len(value) < 1 || len(value) > 253 {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return !strings.HasPrefix(value, ".") && !strings.HasSuffix(value, ".")
}

func rcsValues(node rcsNode, key string) []string {
	var params []rcsParam
	var walk func(rcsNode)
	walk = func(n rcsNode) {
		for _, p := range n.Params {
			k := strings.ToLower(p.Name)
			if k == key {
				params = append(params, p)
			} else if strings.HasPrefix(k, key) {
				suffix := strings.TrimPrefix(k, key)
				if i, e := strconv.Atoi(suffix); e == nil && i > 0 && i <= 32 {
					params = append(params, p)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)
	sort.SliceStable(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	values := []string{}
	for _, p := range params {
		values = append(values, p.Value)
	}
	return values
}

func parseRCSProfile(d *rcsDocument, fallbackPrivate, fallbackPublic string) (*rcsProfile, error) {
	invalid := errors.New("rcs-profile-invalid")
	if d == nil {
		return nil, invalid
	}
	profiles := map[string]rcsNode{}
	selected := ""
	rcsCount := 0
	var rcs *rcsNode
	for _, app := range d.Nodes {
		if !strings.EqualFold(app.Type, "APPLICATION") {
			continue
		}
		switch strings.ToLower(app.value("APPID")) {
		case "ap2002":
			rcsCount++
			a := app
			rcs = &a
			selected = app.value("To-AppRef")
			if node := app.child("IMS"); node != nil {
				if other := node.value("To-AppRef"); other != "" {
					if selected != "" && selected != other {
						return nil, invalid
					}
					selected = other
				}
			}
		case "ap2001", "urn:oma:mo:ext-3gpp-ims:1.0":
			candidates := []rcsNode{app}
			if strings.EqualFold(app.value("APPID"), "urn:oma:mo:ext-3gpp-ims:1.0") {
				candidates = nil
				for _, n := range app.Children {
					if strings.EqualFold(n.Type, "3GPP_IMS") {
						candidates = append(candidates, n)
					}
				}
			}
			for _, n := range candidates {
				ref := n.value("AppRef")
				if ext := n.child("Ext"); ext != nil {
					if gsma := ext.child("GSMA"); gsma != nil {
						if ref2 := gsma.value("AppRef"); ref2 != "" {
							if ref != "" && ref != ref2 {
								return nil, invalid
							}
							ref = ref2
						}
					}
				}
				if _, found := profiles[ref]; found {
					return nil, invalid
				}
				profiles[ref] = n
			}
		}
	}
	profile, found := profiles[selected]
	if rcsCount != 1 || !found || selected == "" {
		return nil, invalid
	}
	p := &rcsProfile{domain: profile.value("Home_network_domain_name"), private: profile.value("Private_User_Identity"), transport: "SIPoTLS", mediaTLS: true, discover: map[string]bool{}}
	if !rcsHost(p.domain) {
		return nil, invalid
	}
	var gsma *rcsNode
	if ext := profile.child("Ext"); ext != nil {
		gsma = ext.child("GSMA")
	}
	auth := profile.child("APPAUTH")
	if auth == nil {
		auth = gsma
	}
	if auth == nil {
		return nil, invalid
	}
	p.auth, p.realm, p.username, p.password = auth.value("AuthType"), auth.value("Realm"), auth.value("UserName"), auth.value("UserPwd")
	if p.auth != "Digest" && p.auth != "AKA" && p.auth != "EarlyIMS" {
		return nil, invalid
	}
	if p.private == "" && p.auth == "AKA" {
		p.private = fallbackPrivate
	}
	identities := rcsValues(profile, "public_user_identity")
	for _, identity := range identities {
		if strings.HasPrefix(identity, "sip:") || strings.HasPrefix(identity, "sips:") {
			p.public = identity
			break
		}
	}
	if p.public == "" && p.auth == "AKA" {
		p.public = fallbackPublic
	}
	if !rcsField(p.public) || !rcsField(p.private) {
		return nil, invalid
	}
	if p.auth == "Digest" && (!rcsField(p.realm) || !rcsField(p.username) || p.password == "" || len(p.password) > 1024 || strings.ContainsAny(p.password, "\r\n\x00")) {
		return nil, invalid
	}
	var transport *rcsNode
	if gsma != nil {
		transport = gsma.child("transportProto")
	}
	if transport == nil {
		if ext := profile.child("Ext"); ext != nil {
			transport = ext.child("transportProto")
		}
	}
	if transport != nil {
		if value := transport.value("psSignalling"); value != "" {
			p.transport = value
		}
		if media := transport.value("psMedia"); media != "" {
			if media != "MSRPoTLS" && media != "MSRP" && media != "MSRPoTCP" {
				return nil, invalid
			}
			p.mediaTLS = media == "MSRPoTLS"
		}
	}
	if p.transport != "SIPoTLS" && p.transport != "SIPoTCP" && p.transport != "SIPoUDP" {
		return nil, invalid
	}
	if p.auth == "Digest" && p.transport != "SIPoTLS" {
		return nil, invalid
	}
	addresses := []string{}
	if lbo := profile.child("LBO_P-CSCF_Address"); lbo != nil {
		addresses = rcsValues(*lbo, "address")
	}
	if len(addresses) == 0 {
		if address := profile.value("P-CSCF_Address"); address != "" {
			addresses = []string{address}
		}
	}
	for _, address := range addresses {
		if net.ParseIP(address) != nil {
			address = net.JoinHostPort(address, "")
		}
		u, e := url.Parse("//" + address)
		if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !rcsHost(u.Hostname()) {
			return nil, invalid
		}
		port := 5061
		if p.transport != "SIPoTLS" {
			port = 5060
		}
		if u.Port() != "" {
			port, e = strconv.Atoi(u.Port())
			if e != nil || port < 1 || port > 65535 {
				return nil, invalid
			}
		}
		endpoint := net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
		p.proxies = append(p.proxies, endpoint)
		p.discover[endpoint] = u.Port() == "" && net.ParseIP(u.Hostname()) == nil
	}
	if len(p.proxies) < 1 || len(p.proxies) > 8 {
		return nil, invalid
	}
	// Advertise only one-to-one chat; groups, file transfer and MaaP require
	// their own complete implementations before adding their feature tags.
	for _, n := range rcsNodes(rcs.Children, "SERVICES") {
		p.chat = n.value("ChatAuth") == "1"
	}
	if !p.chat {
		for _, n := range rcsNodes(rcs.Children, "CHAT") {
			p.chat = n.value("ChatAuth") == "1"
		}
	}
	// TTA R9 §6.5 selects Presence with CAPDISCOVERY/defaultDisc=1.
	p.capExpiry, p.nonRCSExpiry, p.publishThrottle = 24*time.Hour, 10*time.Minute, 25*time.Second
	if discovery := rcs.child("CAPDISCOVERY"); discovery != nil {
		switch discovery.value("defaultDisc") {
		case "", "0":
		case "1":
			p.presence = true
		default:
			return nil, invalid
		}
		for key, destination := range map[string]*time.Duration{"capInfoExpiry": &p.capExpiry, "nonRCScapInfoExpiry": &p.nonRCSExpiry} {
			if value := discovery.value(key); value != "" {
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds < 0 || seconds > 30*24*3600 {
					return nil, invalid
				}
				*destination = time.Duration(seconds) * time.Second
			}
		}
	}
	if presence := rcs.child("PRESENCE"); presence != nil {
		if value := presence.value("source-throttlepublish"); value != "" {
			seconds, err := strconv.Atoi(value)
			if err != nil || seconds < 0 || seconds > 24*3600 {
				return nil, invalid
			}
			p.publishThrottle = time.Duration(seconds) * time.Second
		}
	}
	return p, nil
}
