// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/security"
	"github.com/damonto/ims-go/ims/voice"
)

func TestRCSConfigAndProfileSelection(t *testing.T) {
	x := `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="15"/><parm name="validity" value="3600"/></characteristic><characteristic type="APPLICATION"><parm name="APPID" value="ap2002"/><characteristic type="IMS"><parm name="To-AppRef" value="rcs"/></characteristic><characteristic type="SERVICES"><parm name="ChatAuth" value="1"/></characteristic></characteristic><characteristic type="APPLICATION"><parm name="APPID" value="ap2001"/><parm name="AppRef" value="rcs"/><parm name="Home_network_domain_name" value="example.invalid"/><parm name="Private_User_Identity" value="user@example.invalid"/><characteristic type="Public_User_Identity_List"><parm name="public_user_identity1" value="sip:+821000000001@example.invalid"/></characteristic><characteristic type="LBO_P-CSCF_Address"><characteristic type="Node"><parm name="address" value="proxy.example.invalid:5061"/></characteristic></characteristic><characteristic type="APPAUTH"><parm name="AuthType" value="Digest"/><parm name="Realm" value="example.invalid"/><parm name="UserName" value="user"/><parm name="UserPwd" value="private-test-password"/></characteristic></characteristic></wap-provisioningdoc>`
	doc, err := parseRCSConfig([]byte(x + "\n "))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseRCSProfile(doc, "", "")
	if err != nil || !p.chat || p.transport != "SIPoTLS" || len(p.proxies) != 1 {
		t.Fatal("profile selection failed", err)
	}
	for _, bad := range []string{strings.Replace(x, `value="rcs"/>`, `value="missing"/>`, 1), strings.Replace(x, `value="example.invalid"/>`, `value="example.invalid&#10;bad"/>`, 1), strings.Replace(x, `name="version" value="15"/>`, `name="version" value="15"/><parm name="VERSION" value="16"/>`, 1), x + "<other/>"} {
		doc, e := parseRCSConfig([]byte(bad))
		if e == nil {
			_, e = parseRCSProfile(doc, "", "")
		}
		if e == nil {
			t.Fatal("ambiguous or injected configuration accepted")
		}
	}
	if got := rcsExpiry(doc, 100); !got.Equal(time.Unix(3700, 0)) {
		t.Fatal("configuration validity lost")
	}
}

func TestRCSDigestRFC2069AndServerProof(t *testing.T) {
	parsed, err := parseRCSChallenge(`Digest realm="testrealm@host.com",nonce="challenge",algorithm=MD5,qop="auth,auth-int",opaque="a\"b"`)
	if err != nil || parsed.Algorithm != "MD5" || parsed.QOP != "auth,auth-int" || parsed.Opaque != "a\"b" {
		t.Fatal("ordinary Digest challenge rejected", err)
	}
	for _, bad := range []string{`Digest realm="r",nonce="n",REALM="other"`, `Digest realm="r",nonce="unterminated`, `Digest realm="r",nonce="n" junk`, `Digest realm="r",nonce="n",`} {
		if _, err := parseRCSChallenge(bad); err == nil {
			t.Fatal("ambiguous digest accepted")
		}
	}
	challenge := security.Challenge{Realm: "testrealm@host.com", Nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093", Algorithm: "MD5"}
	p, err := rcsDigest(challenge, "Mufasa", "Circle Of Life", "GET", "/dir/index.html", "", 1)
	if err != nil || p.auth.Response != "670fd8c2df070c60b045671b8b24ff02" {
		t.Fatal("RFC digest vector mismatch")
	}
	if p.verify(security.Info{RSPAuth: "00000000000000000000000000000000"}, "") == nil {
		t.Fatal("forged server proof accepted")
	}
	challenge.QOP = "auth-int"
	challenge.Algorithm = "SHA-256"
	a, _ := rcsDigest(challenge, "user", "password", "REGISTER", "sip:example.invalid", "", 1)
	b, _ := rcsDigest(challenge, "user", "password", "REGISTER", "sip:example.invalid", "changed", 1)
	if len(a.auth.Response) != 64 || a.auth.Response == b.auth.Response || a.auth.NonceCount != "00000001" {
		t.Fatal("digest did not bind request body/count")
	}
	challenge.Algorithm = "unknown"
	if _, e := rcsDigest(challenge, "u", "p", "REGISTER", "sip:x", "", 1); e == nil {
		t.Fatal("unknown algorithm accepted")
	}
}

func TestRCSSRVDiscoveryKeepsProvisionedTLSIdentity(t *testing.T) {
	p := &rcsProfile{transport: "SIPoTLS", proxies: []string{"service.example.invalid:5061", "direct.example.invalid:5062"}, discover: map[string]bool{"service.example.invalid:5061": true}}
	queries := 0
	endpoints := rcsEndpoints(context.Background(), p, func(_ context.Context, service, proto, host string) (string, []*net.SRV, error) {
		queries++
		if service != "sips" || proto != "tcp" || host != "service.example.invalid" {
			t.Fatal("wrong SIP service query")
		}
		return "", []*net.SRV{{Target: "loadbalancer.example.invalid.", Port: 9673}}, nil
	})
	if queries != 1 || len(endpoints) != 2 || endpoints[0].address != "loadbalancer.example.invalid:9673" || endpoints[0].identity != "service.example.invalid" || endpoints[1].address != p.proxies[1] {
		t.Fatal("SRV discovery changed authentication identity or explicit port")
	}
	endpoints = rcsEndpoints(context.Background(), p, func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", nil, errors.New("no SRV")
	})
	if endpoints[0].address != p.proxies[0] {
		t.Fatal("A/AAAA fallback lost")
	}
}

func TestMSRPWireReassemblyAndDurableFailure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		a, b := net.Pipe()
		leftPath, rightPath := "msrps://one.invalid:9/a;tcp", "msrps://two.invalid:9/b;tcp"
		payload := []byte(strings.Repeat("한글\r\n", 1000) + "last")
		received := make(chan []byte, 1)
		left := newMSRPSession(a, leftPath, rightPath, func(string, string, []byte) error { return nil }, nil)
		right := newMSRPSession(b, rightPath, leftPath, func(_, _ string, data []byte) error {
			if reject {
				return errors.New("disk unavailable")
			}
			received <- bytes.Clone(data)
			return nil
		}, nil)
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		err := left.send(ctx, "unique-message", "message/cpim", payload)
		if reject {
			if err == nil {
				t.Fatal("failed durable storage received success")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			select {
			case data := <-received:
				if !bytes.Equal(data, payload) {
					t.Fatal("fragmented payload changed")
				}
			case <-ctx.Done():
				t.Fatal("message was not reassembled")
			}
		}
		stop()
		a.Close()
		b.Close()
		<-left.done
		<-right.done
	}
	for _, bad := range []string{"1-2/2 junk", "1-2/3/4", "0-1/1", "1-2/999999999", "1--2/2"} {
		if _, _, _, e := msrpRange(bad, 2, '$'); e == nil {
			t.Fatal("malformed range accepted")
		}
	}
	wire := msrpFrame{transaction: "abc123", method: "SEND", headers: map[string]string{"to-path": "msrp://a:9/x;tcp", "from-path": "msrp://b:9/y;tcp", "message-id": "id", "byte-range": "1-0/0"}, flag: '$'}.wire()
	if _, err := readMSRP(bufio.NewReader(bytes.NewReader(wire))); err != nil {
		t.Fatal("empty path-binding SEND rejected", err)
	}
}

func TestMSRPTLSRequiresNegotiatedFingerprint(t *testing.T) {
	one, _ := newRCSMediaIdentity()
	two, _ := newRCSMediaIdentity()
	client, e := msrpTLS(one, "invalid", two.fingerprint, false)
	if e != nil {
		t.Fatal(e)
	}
	server, e := msrpTLS(two, "", one.fingerprint, true)
	if e != nil {
		t.Fatal(e)
	}
	a, b := net.Pipe()
	c, s := tls.Client(a, client), tls.Server(b, server)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- s.HandshakeContext(ctx) }()
	if e = c.HandshakeContext(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	a.Close()
	b.Close()
	if e = client.VerifyConnection(tls.ConnectionState{}); e == nil {
		t.Fatal("missing peer certificate accepted")
	}
}

func TestRCSStorageSIMScopeIdempotencyAndReceipts(t *testing.T) {
	db := testMessageDB(t)
	session, requestID := opaqueID(), opaqueID()
	id, e := db.queueRCS(session, requestID, "01000000001", "대화 '한글'")
	if e != nil {
		t.Fatal(e)
	}
	if again, e := db.queueRCS(session, requestID, "+821000000001", "대화 '한글'"); e != nil || again != id {
		t.Fatal("retry duplicated message")
	}
	if _, e = db.queueRCS(session, requestID, "01000000001", "changed"); e == nil {
		t.Fatal("request conflict accepted")
	}
	rows, _ := db.run("SELECT cpim_message_id FROM messages WHERE id="+sqlText(id)+";", true)
	cpimID := rows[0]["cpim_message_id"].(string)
	if e = db.rcsEvent(cpimID, "+821000000009", "delivered", "wrong-peer"); e != nil {
		t.Fatal(e)
	}
	rows, _ = db.run("SELECT state FROM transport_attempts WHERE message_id="+sqlText(id)+";", true)
	if rows[0]["state"] != "queued" {
		t.Fatal("receipt matched wrong recipient")
	}
	for _, kind := range []string{"displayed", "accepted_by_service", "delivered"} {
		if e = db.rcsEvent(cpimID, "+821000000001", kind, kind); e != nil {
			t.Fatal(e)
		}
	}
	rows, _ = db.run("SELECT state FROM transport_attempts WHERE message_id="+sqlText(id)+";", true)
	if rows[0]["state"] != "displayed" {
		t.Fatal("late service acceptance downgraded read receipt")
	}
	data := makeCPIM("tel:+821000000001", "sip:+821000000002@example.invalid", "remote-message", "text/plain", []byte("incoming"), true)
	for range 2 {
		if _, e = db.acceptRCS("tel:+821000000001", "tel:+821000000002", "network", data); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.acceptRCS("tel:+821000000009", "tel:+821000000002", "network", data); e == nil {
		t.Fatal("spoofed CPIM sender accepted")
	}
	rows, _ = db.run("SELECT COUNT(*) AS n FROM messages WHERE direction='incoming';", true)
	if rows[0]["n"] != float64(1) {
		t.Fatal("incoming retry duplicated message")
	}
	db.subscription = "another-sim"
	if e = db.rcsEvent(cpimID, "+821000000001", "failed", "wrong-sim"); e != nil {
		t.Fatal(e)
	}
	rows, _ = db.run("SELECT state FROM transport_attempts WHERE message_id="+sqlText(id)+";", true)
	if rows[0]["state"] != "displayed" {
		t.Fatal("receipt crossed SIM ownership")
	}
}

func TestVoiceAMRBothNegotiatedFormats(t *testing.T) {
	for _, name := range []voice.AudioCodec{voice.CodecAMR, voice.CodecAMRWB} {
		codec, e := newVoicePCM(voice.NegotiatedMedia{AudioFormat: voice.AudioFormat{Codec: name, ModeSet: "0,1,2"}})
		if e != nil {
			t.Fatal(e)
		}
		pcm := make([]int16, 320)
		for i := range pcm {
			pcm[i] = int16(i*31 - 4000)
		}
		frame, e := codec.encode(pcm)
		if e != nil {
			t.Fatal(e)
		}
		decoded, e := codec.decode(frame)
		if e != nil || len(decoded) != 320 {
			t.Fatal("AMR PCM frame roundtrip failed", e)
		}
	}
}

func TestBluetoothRequiresDuplexHeadsetPCM(t *testing.T) {
	path := "/org/bluealsa/hci0/dev_01_23_45_67_89_AB/hfpag/source"
	info := "Transport: HFP-AG\nMode: source\nFormat: S16_LE\nChannels: 1\nSampling: 16000 Hz\n"
	pcm, mode, ok := parseBluetoothPCM(path, info)
	if !ok || mode != "source" || pcm.rate != 16000 || pcm.Label != "01:23:45:67:89:AB" {
		t.Fatal("headset PCM not recognized")
	}
	for _, bad := range []string{strings.Replace(info, "HFP-AG", "A2DP-source", 1), strings.Replace(info, "16000", "48000", 1), strings.Replace(info, "S16_LE", "S32_LE", 1)} {
		if _, _, ok := parseBluetoothPCM(path, bad); ok {
			t.Fatal("incompatible PCM accepted")
		}
	}
	if _, _, ok := parseBluetoothPCM(path+";command", info); ok {
		t.Fatal("invalid object path accepted")
	}
	b := &bluetoothAudio{devices: []bluetoothPCM{{ID: "one"}, {ID: "two"}}}
	if _, e := b.selectDevice(""); e == nil {
		t.Fatal("ambiguous headset selected")
	}
	if p, e := b.selectDevice("two"); e != nil || p.ID != "two" {
		t.Fatal("selected headset lost")
	}
}
