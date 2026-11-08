// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

func testRCSProfile() *rcsProfile {
	return &rcsProfile{domain: "example.invalid", public: "sip:+821000000002@example.invalid", auth: "Digest", realm: "example.invalid", username: "test-user", password: "test-only-password", transport: "SIPoTLS", chat: true, presence: true, capExpiry: time.Hour, nonRCSExpiry: time.Minute, publishThrottle: 25 * time.Second}
}

func testRCSSIP(t *testing.T) (*rcsSIP, *rcsStream, context.Context) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	stack, err := session.Open(session.Config{Transport: "tcp", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, &rcsStream{conn: client})
	if err != nil {
		client.Close()
		server.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	r := &rcsSIP{stack: stack, profile: testRCSProfile(), contact: "<sip:fixture@" + client.LocalAddr().String() + ">;" + rcsChatFeatures + ";" + rcsPresenceFeature, ctx: ctx, done: make(chan struct{}), verify: func(context.Context) error { return nil }}
	if _, err := stack.Attach(r); err != nil {
		t.Fatal(err)
	}
	go func() { defer close(r.done); _ = stack.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		r.close()
		server.Close()
		<-r.done
	})
	return r, &rcsStream{conn: server}, ctx
}

func readRCSTestSIP(t *testing.T, wire *rcsStream, ctx context.Context) sip.Message {
	t.Helper()
	_ = wire.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	data, _, err := wire.ReadPacket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := sip.ParseMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func writeRCSTestSIP(t *testing.T, wire *rcsStream, ctx context.Context, message sip.Message) {
	t.Helper()
	data, err := message.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err = wire.WritePacket(ctx, data); err != nil {
		t.Fatal(err)
	}
}

func rcsTestReply(request sip.Message, status int, fields ...string) sip.Message {
	response := sip.NewResponse(status, "Fixture")
	response.Headers = sip.ResponseHeaders(request, status)
	for index := 0; index < len(fields); index += 2 {
		response.Headers.Set(fields[index], fields[index+1])
	}
	return response
}

func rcsTestNotify(request sip.Message, body []byte) sip.Message {
	notification := sip.NewRequest("NOTIFY", "sip:fixture@127.0.0.1")
	notification.Headers.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK"+sip.RandomHex(20))
	notification.Headers.Add("From", request.Header("To")+";tag=presence-fixture")
	notification.Headers.Add("To", request.Header("From"))
	notification.Headers.Add("Call-ID", request.Header("Call-ID"))
	notification.Headers.Add("CSeq", "2 NOTIFY")
	notification.Headers.Add("Event", "presence")
	notification.Headers.Add("Subscription-State", "terminated;reason=timeout")
	notification.Headers.Add("Content-Type", "application/pidf+xml")
	notification.Body = body
	return notification
}

const rcsPresenceConfigFixture = `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="1"/></characteristic><characteristic type="APPLICATION"><parm name="APPID" value="ap2001"/><parm name="AppRef" value="RCS"/><parm name="Home_network_domain_name" value="example.invalid"/><parm name="Private_User_Identity" value="test@example.invalid"/><characteristic type="Public_User_Identity_List"><parm name="public_user_identity" value="sip:+821000000002@example.invalid"/></characteristic><parm name="P-CSCF_Address" value="proxy.example.invalid"/><characteristic type="Ext"><characteristic type="GSMA"><parm name="AuthType" value="Digest"/><parm name="Realm" value="example.invalid"/><parm name="UserName" value="test"/><parm name="UserPwd" value="test-only-password"/><characteristic type="transportProto"><parm name="psSignalling" value="SIPoTLS"/><parm name="psMedia" value="MSRPoTLS"/></characteristic></characteristic></characteristic></characteristic><characteristic type="APPLICATION"><parm name="APPID" value="ap2002"/><parm name="To-AppRef" value="RCS"/><characteristic type="SERVICES"><parm name="ChatAuth" value="1"/></characteristic><characteristic type="CAPDISCOVERY"><parm name="defaultDisc" value="1"/><parm name="capInfoExpiry" value="123"/><parm name="nonRCScapInfoExpiry" value="45"/></characteristic><characteristic type="PRESENCE"><parm name="source-throttlepublish" value="20"/></characteristic></characteristic></wap-provisioningdoc>`

func TestRCSPresenceProfileParameters(t *testing.T) {
	x := rcsPresenceConfigFixture
	doc, err := parseRCSConfig([]byte(x))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := parseRCSProfile(doc, "", "")
	if err != nil || !profile.presence || profile.capExpiry != 123*time.Second || profile.nonRCSExpiry != 45*time.Second || profile.publishThrottle != 20*time.Second {
		t.Fatal("operator discovery parameters lost", err)
	}
	for _, bad := range []string{strings.Replace(x, `name="defaultDisc" value="1"`, `name="defaultDisc" value="2"`, 1), strings.Replace(x, `value="123"`, `value="-1"`, 1), strings.Replace(x, `value="20"`, `value="invalid"`, 1)} {
		doc, err := parseRCSConfig([]byte(bad))
		if err == nil {
			_, err = parseRCSProfile(doc, "", "")
		}
		if err == nil {
			t.Fatal("invalid operator discovery setting accepted")
		}
	}
}

func TestRCSPresenceDocumentsAndContactFeatures(t *testing.T) {
	profile := testRCSProfile()
	body := rcsPresenceBody(profile)
	for _, value := range []string{string(body), strings.Replace(string(body), "<basic>open</basic>", "<basic>closed</basic>", -1)} {
		chat, err := parseRCSPresence([]byte(value), "+821000000002")
		if err != nil || !chat {
			t.Fatal("CPM chat capability was not found", err)
		}
	}
	for _, bad := range []string{strings.Replace(string(body), rcsOMAPresence, "urn:untrusted", 1), strings.Replace(string(body), "2.0", "1.0", 1)} {
		if chat, err := parseRCSPresence([]byte(bad), "+821000000002"); err != nil || chat {
			t.Fatal("foreign namespace or wrong chat version became support")
		}
	}
	for _, bad := range []string{string(body) + "<extra/>", strings.Replace(string(body), "+821000000002", "+821000000009", -1), strings.Replace(string(body), "<status>", strings.Repeat("<nested>", 30)+"<status>", 1), `<!DOCTYPE presence>` + string(body), strings.Replace(string(body), `entity=`, `entity="sip:+821000000009@example.invalid" entity=`, 1)} {
		if _, err := parseRCSPresence([]byte(bad), "+821000000002"); err == nil {
			t.Fatal("unrelated or malformed presence accepted")
		}
	}
	if strings.Contains(string(body), "File-Transfer") || strings.Contains(string(body), "geopush") {
		t.Fatal("unimplemented services advertised")
	}
	for _, feature := range []string{rcsChatFeatures, `+g.3gpp.iari-ref="urn:urn-7:3gpp-application.ims.iari.rcse.im"`, `+g.3gpp.iari-ref="urn%3aurn-7%3a3gpp-service.ims.icsi.oma.cpm.session"`} {
		if !rcsContactChat([]string{"<sip:test@example.invalid>;" + feature}) {
			t.Fatal("complete chat feature rejected")
		}
	}
	for _, contact := range []string{`<sip:rcse.im@example.invalid>;description="rcse.im"`, `<sip:test@example.invalid>;+g.3gpp.iari-ref="urn:urn-7:3gpp-application.ims.iari.rcse.im.fake"`, `<sip:test@example.invalid>;+g.3gpp.iari-ref="unterminated`} {
		if rcsContactChat([]string{contact}) {
			t.Fatal("substring became a chat feature")
		}
	}
	if !rcsContactChat([]string{`<sip:first@example.invalid>;` + rcsChatFeatures + `, <sip:second@example.invalid>`}) {
		t.Fatal("comma-separated contacts lost the first contact's capability")
	}
}

func TestRCSPresenceEarlyNotifyAndSIMScopedCache(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	peer := "+821000000001"
	result := make(chan error, 1)
	go func() { result <- r.capability(ctx, peer) }()
	request := readRCSTestSIP(t, wire, ctx)
	for range 4 {
		go func() { result <- r.capability(ctx, peer) }()
	}
	if request.Method() != "SUBSCRIBE" || request.Header("Expires") != "0" || request.Header("Event") != "presence" || request.Header("Privacy") != "id" || !strings.Contains(request.Header("From"), "anonymous@anonymous.invalid") {
		t.Fatal("anonymous fetch request is incomplete")
	}
	profile := testRCSProfile()
	profile.public = "sip:" + peer + "@example.invalid"
	notification := rcsTestNotify(request, rcsPresenceBody(profile))
	wrong := notification
	wrong.Headers = notification.Headers.Clone()
	wrong.Headers.Set("To", `"Anonymous" <sip:anonymous@anonymous.invalid>;tag=wrong`)
	writeRCSTestSIP(t, wire, ctx, wrong)
	if response := readRCSTestSIP(t, wire, ctx); response.StatusCode() != 481 {
		t.Fatal("unrelated dialog poisoned the capability")
	}
	wrong = notification
	wrong.Body = []byte(strings.ReplaceAll(string(notification.Body), peer, "+821000000009"))
	writeRCSTestSIP(t, wire, ctx, wrong)
	if response := readRCSTestSIP(t, wire, ctx); response.StatusCode() != 400 {
		t.Fatal("another peer's PIDF was accepted")
	}
	writeRCSTestSIP(t, wire, ctx, notification)
	if response := readRCSTestSIP(t, wire, ctx); response.StatusCode() != 200 {
		t.Fatal("early NOTIFY was rejected")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 200))
	for range 5 {
		if err := <-result; err != nil {
			t.Fatal("early NOTIFY or shared query result was lost", err)
		}
	}
	if err := r.capability(ctx, peer); err != nil {
		t.Fatal("operator cache was not reused", err)
	}
	if len(r.presenceQueries) != 0 {
		t.Fatal("completed subscription leaked")
	}
	other, otherWire, otherCtx := testRCSSIP(t)
	go func() { result <- other.capability(otherCtx, peer) }()
	request = readRCSTestSIP(t, otherWire, otherCtx)
	writeRCSTestSIP(t, otherWire, otherCtx, rcsTestReply(request, 404))
	if err := <-result; err == nil || err.Error() != "rcs-peer-unavailable" {
		t.Fatal("a new registration inherited another SIM's cached support")
	}
}

func TestRCSOptionsTransientFailureIsNotUnsupported(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	r.profile.presence = false
	result := make(chan error, 1)
	go func() { result <- r.capability(ctx, "+821000000001") }()
	request := readRCSTestSIP(t, wire, ctx)
	if request.Method() != "OPTIONS" {
		t.Fatal("OPTIONS profile used Presence")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 503))
	if err := <-result; err == nil || err.Error() == "rcs-peer-unavailable" {
		t.Fatal("temporary server failure marked the peer unsupported")
	}
}

func TestRCSPublicationAuthenticationRefreshRecoveryAndRemoval(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	p := &rcsPublication{}
	now := time.Now()
	done := make(chan struct{}, 1)
	update := func(at time.Time) { go func() { p.update(ctx, r, at); done <- struct{}{} }() }
	update(now)
	request := readRCSTestSIP(t, wire, ctx)
	if request.Method() != "PUBLISH" || len(request.Body) == 0 || request.Header("SIP-If-Match") != "" {
		t.Fatal("initial capability publication is incomplete")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 401, "WWW-Authenticate", `Digest realm="example.invalid",nonce="publish-challenge",algorithm=MD5`))
	authorized := readRCSTestSIP(t, wire, ctx)
	ha1 := fmt.Sprintf("%x", md5.Sum([]byte("test-user:example.invalid:test-only-password")))
	ha2 := fmt.Sprintf("%x", md5.Sum([]byte("PUBLISH:"+r.profile.public)))
	want := fmt.Sprintf("%x", md5.Sum([]byte(ha1+":publish-challenge:"+ha2)))
	if !strings.Contains(authorized.Header("Authorization"), `response="`+want+`"`) || authorized.Header("CSeq") != "2 PUBLISH" || authorized.Header("Via") == request.Header("Via") {
		t.Fatal("challenge was not bound to PUBLISH with a new transaction")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(authorized, 200, "SIP-ETag", "first-tag", "Expires", "60"))
	<-done
	if p.etag != "first-tag" || !p.until.Equal(now.Add(time.Minute)) || !p.next.Equal(now.Add(48*time.Second)) {
		t.Fatal("server lifetime or tag was lost")
	}
	refreshAt := now.Add(48 * time.Second)
	update(refreshAt)
	request = readRCSTestSIP(t, wire, ctx)
	if len(request.Body) != 0 || request.Header("SIP-If-Match") != "first-tag" {
		t.Fatal("refresh replaced the publication instead of extending it")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 412))
	<-done
	update(refreshAt)
	request = readRCSTestSIP(t, wire, ctx)
	if len(request.Body) == 0 || request.Header("SIP-If-Match") != "" {
		t.Fatal("expired server tag was reused")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 423, "Min-Expires", "7200"))
	<-done
	update(refreshAt)
	request = readRCSTestSIP(t, wire, ctx)
	if request.Header("Expires") != "7200" {
		t.Fatal("server minimum publication lifetime ignored")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 200, "SIP-ETag", "second-tag", "Expires", "120"))
	<-done
	go func() { _, _ = p.request(ctx, r, true); done <- struct{}{} }()
	request = readRCSTestSIP(t, wire, ctx)
	if request.Header("Expires") != "0" || request.Header("SIP-If-Match") != "second-tag" || len(request.Body) != 0 {
		t.Fatal("shutdown did not remove the advertised capability")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 200))
	<-done
}

func TestRCSPresenceExpiryAndRetryAfter(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	now := time.Now()
	p := &rcsPublication{etag: "old-tag", until: now.Add(10 * time.Second)}
	done := make(chan struct{}, 1)
	go func() { p.update(ctx, r, now); done <- struct{}{} }()
	request := readRCSTestSIP(t, wire, ctx)
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 503, "Retry-After", "300 (maintenance)"))
	<-done
	if p.status != 503 || !p.next.Equal(now.Add(5*time.Minute)) || p.delay(now) != 10*time.Second {
		t.Fatal("retry delay or old publication expiration was lost")
	}
	service := &rcsService{state: "registered", discovery: "presence", publishUntil: time.Now().Add(-time.Second)}
	if state := service.snapshot(); state["ready"] != false || state["state"] != "publishing" {
		t.Fatal("expired presence was reported ready")
	}
	service.publishUntil = time.Now().Add(time.Minute)
	if state := service.snapshot(); state["ready"] != true || state["state"] != "registered" {
		t.Fatal("current publication was not usable")
	}
	p.update(ctx, r, now.Add(10*time.Second))
	if !p.next.Equal(now.Add(5*time.Minute)) || p.delay(now.Add(10*time.Second)) != 290*time.Second {
		t.Fatal("expiration bypassed the server's retry interval")
	}
}

func TestRCSPresenceTimeoutRemovesSubscription(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	query, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- r.capability(query, "+821000000001") }()
	request := readRCSTestSIP(t, wire, ctx)
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 200))
	if err := <-result; err == nil {
		t.Fatal("missing NOTIFY became a supported peer")
	}
	if len(r.presenceQueries) != 0 {
		t.Fatal("timed-out subscription leaked")
	}
	profile := testRCSProfile()
	profile.public = "sip:+821000000001@example.invalid"
	writeRCSTestSIP(t, wire, ctx, rcsTestNotify(request, rcsPresenceBody(profile)))
	if response := readRCSTestSIP(t, wire, ctx); response.StatusCode() != 481 {
		t.Fatal("late notification revived an expired query")
	}
}

func TestRCSPresenceRejectedSubscriptionIsUnknown(t *testing.T) {
	r, wire, ctx := testRCSSIP(t)
	result := make(chan error, 1)
	go func() { result <- r.capability(ctx, "+821000000001") }()
	request := readRCSTestSIP(t, wire, ctx)
	profile := testRCSProfile()
	profile.public = "sip:+821000000001@example.invalid"
	notification := rcsTestNotify(request, rcsPresenceBody(profile))
	notification.Headers.Set("Subscription-State", "terminated;reason=rejected")
	writeRCSTestSIP(t, wire, ctx, notification)
	if response := readRCSTestSIP(t, wire, ctx); response.StatusCode() != 200 {
		t.Fatal("terminated notification was not acknowledged")
	}
	writeRCSTestSIP(t, wire, ctx, rcsTestReply(request, 200))
	if err := <-result; err == nil || err.Error() != "rcs-capability-unavailable" {
		t.Fatal("a rejected subscription became a capability result", err)
	}
}

func TestRCSConfigRefreshAndPersistentOperatorDisable(t *testing.T) {
	previous, err := parseRCSConfig([]byte(rcsPresenceConfigFixture))
	if err != nil {
		t.Fatal(err)
	}
	partial := func(version string) *rcsDocument {
		doc, err := parseRCSConfig([]byte(`<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="` + version + `"/><parm name="validity" value="120"/></characteristic></wap-provisioningdoc>`))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	refreshed, err := rcsConfigUpdate(previous, partial("1"), "", "")
	if err != nil || len(rcsNodes(refreshed.Nodes, "APPLICATION")) != 2 || !rcsExpiry(refreshed, 100).Equal(time.Unix(220, 0)) {
		t.Fatal("same-version lifetime refresh lost the IMS profile", err)
	}
	if _, err := rcsConfigUpdate(previous, partial("2"), "", ""); err == nil {
		t.Fatal("a different version without a full profile replaced valid settings")
	}
	for _, version := range []string{"0", "-1", "-2"} {
		fresh := partial(version)
		fresh.Nodes = append(fresh.Nodes, previous.Nodes[1:]...)
		disabled, err := rcsConfigUpdate(previous, fresh, "", "")
		if err != nil || len(disabled.Nodes) != 1 || len(rcsNodes(disabled.Nodes, "APPLICATION")) != 0 {
			t.Fatal("operator disable retained an active IMS profile", err)
		}
		xmlData, err := xml.Marshal(disabled)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(rcsCache{PLMN: "45006", Fetched: 100, XML: xmlData})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "rcs.json")
		if err := writePrivate(path, data); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := os.ReadFile(path)
		var cache rcsCache
		if err != nil || info.Mode().Perm() != 0600 || json.Unmarshal(stored, &cache) != nil {
			t.Fatal("private disable marker did not survive storage")
		}
		doc, err := parseRCSConfig(cache.XML)
		if err != nil || rcsConfigVersion(doc) != rcsConfigVersion(fresh) || strings.Contains(string(cache.XML), "UserPwd") {
			t.Fatal("restart restored credentials instead of the operator disable marker")
		}
	}
}

func TestRCSCPIMNamespacesAndAnonymousReceipts(t *testing.T) {
	data := makeCPIM("tel:+821000000001", "tel:+821000000002", "fixture-id", "text/plain; charset=UTF-8", []byte("한글"), true)
	aliased := strings.ReplaceAll(string(data), "imdn", "status")
	// Only the prefix changes; the namespace URI stays registered to IMDN.
	aliased = strings.ReplaceAll(aliased, "urn:ietf:params:status", "urn:ietf:params:imdn")
	m, err := parseCPIM([]byte(aliased))
	if err != nil || !m.delivery || !m.display || m.id != "fixture-id" || string(m.body) != "한글" {
		t.Fatal("a different IMDN prefix lost delivery/read requests", err)
	}
	for _, bad := range []string{strings.Replace(aliased, "status.Message-ID: fixture-id", "status.Message-ID: fixture-id\r\nstatus.Message-ID: other", 1), strings.Replace(aliased, "charset=UTF-8", "charset=EUC-KR", 1)} {
		if _, err := parseCPIM([]byte(bad)); err == nil {
			t.Fatal("ambiguous ID or unsupported text encoding accepted")
		}
	}
	notRequested := strings.Replace(aliased, "positive-delivery, display", "not-positive-delivery, undisplayed", 1)
	if m, err := parseCPIM([]byte(notRequested)); err != nil || m.delivery || m.display {
		t.Fatal("substring became a delivery/read request")
	}
	receipt := makeCPIM("tel:+821000000001", "tel:+821000000002", "receipt-id", "message/imdn+xml", rcsReceiptXML("fixture-id", "delivered"), false)
	m, err = parseCPIM(receipt)
	if err != nil || m.from != "sip:anonymous@anonymous.invalid" || m.to != "sip:anonymous@anonymous.invalid" || !strings.Contains(string(receipt), "Content-Disposition: notification\r\n") || m.delivery || m.display {
		t.Fatal("TTA delivery report exposed identities or requested another receipt", err)
	}
}
