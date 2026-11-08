// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
)

func testMessageDB(t *testing.T) *messageDB {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := openMessageDB(filepath.Join(directory, "messages.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.bind("test-sim-one", 1, "45006"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rows, err := db.run("PRAGMA foreign_key_check;", true)
		if err != nil || len(rows) > 0 {
			t.Error("database contains invalid references")
		}
	})
	return db
}

func TestMessageMigrationPreservesTextAndSIMOwnership(t *testing.T) {
	db := testMessageDB(t)
	directory := t.TempDir()
	segments := t.TempDir()
	body := "한국어 '단어' \"문장\"\n둘째 줄; DELETE FROM messages; -- 😀"
	message := ims.SMS{CallID: "migration", From: "010-0000-0001", To: "+821000000002", Text: body, ReceivedAt: time.Now()}
	if err := storeSMS(directory, message); err != nil {
		t.Fatal(err)
	}
	other := message
	other.CallID = "different-sim"
	other.To = "+821000000003"
	if err := storeSMS(directory, other); err != nil {
		t.Fatal(err)
	}
	segment := imssms.Incoming{CallID: message.CallID, From: message.From, To: message.To, RawTPDU: []byte{0, 1, 2, 0xff}, Text: body}
	if err := storeIncomingSegment(segments, segment); err != nil {
		t.Fatal(err)
	}
	if err := db.importSegments(segments); err != nil {
		t.Fatal(err)
	}
	if err := db.importInbox(directory, []string{"sip:+821000000002@example.invalid"}, false); err != nil {
		t.Fatal(err)
	}
	rows, err := db.run("SELECT m.body,s.tpdu IS NOT NULL AS raw FROM messages m JOIN sms_segments s ON s.message_id=m.id;", true)
	if err != nil || len(rows) != 1 || rows[0]["body"] != body || rows[0]["raw"] != float64(1) {
		t.Fatal("migration lost original text or segment link")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 1 {
		t.Fatal("unmatched SIM source was removed")
	}
	if err := db.incoming(message); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT COUNT(*) AS n FROM messages;", true)
	if rows[0]["n"] != float64(1) {
		t.Fatal("retry duplicated message")
	}
	info, _ := os.Stat(db.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("message data is not private")
	}
}

func TestContactsMultipleNumbersAndAtomicConflict(t *testing.T) {
	db := testMessageDB(t)
	original := savedContact{Name: "이름 '한글'", Addresses: []contactAddress{{Address: "010-0000-0001", Label: "휴대전화"}, {Address: "02-123-4567", Label: "집"}}}
	id, err := db.saveContact(original)
	if err != nil {
		t.Fatal(err)
	}
	original.ID = id
	original.Name = "수정한 이름"
	if _, err := db.saveContact(original); err != nil {
		t.Fatal(err)
	}
	second, err := db.saveContact(savedContact{Name: "다른 연락처", Addresses: []contactAddress{{Address: "01000000002"}}})
	if err != nil {
		t.Fatal(err)
	}
	original.Addresses = append(original.Addresses, contactAddress{Address: "01000000002"})
	if _, err := db.saveContact(original); err == nil {
		t.Fatal("duplicate address accepted")
	}
	contacts, err := db.contacts()
	if err != nil || len(contacts) != 2 {
		t.Fatal("contact update removed data")
	}
	for _, contact := range contacts {
		if contact.ID == id && (contact.Name != "수정한 이름" || len(contact.Addresses) != 2) {
			t.Fatal("failed update was not atomic")
		}
	}
	if _, err := db.messageMethod("delete_contact", messageRequest{ID: second}, messagingRuntime{}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT COUNT(*) AS n FROM contact_addresses;", true)
	if rows[0]["n"] != float64(2) {
		t.Fatal("contact delete left addresses")
	}
}

func TestQueuedMessagesAreIdempotentAndBoundToSIMSession(t *testing.T) {
	db := testMessageDB(t)
	firstSIM := db.subscription
	session := opaqueID()
	request := opaqueID()
	id, err := db.queueSMS(session, request, "01000000001", "테스트")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := db.queueSMS(session, request, "+821000000001", "테스트"); err != nil || again != id {
		t.Fatal("request retry was not idempotent")
	}
	if _, err := db.queueSMS(session, request, "+821000000001", "변경"); err == nil {
		t.Fatal("request ID reused for another message")
	}
	if err := db.bind("test-sim-two", 2, "45005"); err != nil {
		t.Fatal(err)
	}
	if message, err := db.claimSMS(session); err != nil || message != nil {
		t.Fatal("another SIM consumed queue")
	}
	db.subscription = firstSIM
	if message, err := db.claimSMS(opaqueID()); err != nil || message != nil {
		t.Fatal("another IMS session consumed queue")
	}
	message, err := db.claimSMS(session)
	if err != nil || message == nil {
		t.Fatal("active SIM failed to claim message")
	}
	if err := db.recoverPending(opaqueID()); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT state FROM transport_attempts;", true)
	if len(rows) != 1 || rows[0]["state"] != "submit_unknown" {
		t.Fatal("restart lost uncertain send state")
	}
	if message, err := db.claimSMS(session); err != nil || message != nil {
		t.Fatal("uncertain message was resent")
	}
	if _, err := db.messageMethod("send", messageRequest{Subscription: firstSIM, Session: session, RequestID: opaqueID(), To: "01000000001", Text: "테스트"}, messagingRuntime{Registration: "registered", Subscription: db.subscription, Session: opaqueID()}); err == nil {
		t.Fatal("stale UI session sent SMS")
	}
}

func TestMultipartAcceptanceIsNotDelivery(t *testing.T) {
	db := testMessageDB(t)
	session := opaqueID()
	_, err := db.queueSMS(session, opaqueID(), "01000000001", strings.Repeat("한", 71))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.claimSMS(session)
	if err != nil {
		t.Fatal(err)
	}
	attempt := int64(claimed["attempt"].(float64))
	submission := ims.SMSSubmission{SubmittedAt: time.Now(), SegmentCount: 2, Segments: []ims.SMSSubmissionSegment{{Index: 1}, {Index: 2}}}
	if err := db.submission(attempt, submission); err != nil {
		t.Fatal(err)
	}
	update := ims.SMSSubmissionUpdate{SegmentIndex: 1, SegmentCount: 2, State: ims.SMSDelivered, ReceivedAt: time.Now()}
	if err := db.submissionUpdate(attempt, update); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] == "delivered" {
		t.Fatal("one delivered segment marked whole SMS delivered")
	}
	update.SegmentIndex = 2
	update.State = ims.SMSAcceptedBySMSC
	if err := db.submissionUpdate(attempt, update); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] != "accepted_by_smsc" {
		t.Fatal("SMSC acceptance state missing")
	}
	update.State = ims.SMSDelivered
	if err := db.submissionUpdate(attempt, update); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] != "delivered" {
		t.Fatal("all delivered parts not combined")
	}
}

func TestR9FallbackKeepsMMSPartsAndPerRecipientReceipts(t *testing.T) {
	db := testMessageDB(t)
	conversation, err := db.conversation("+821000000001")
	if err != nil {
		t.Fatal(err)
	}
	// A single semantic message falls back from RCS to MMS without losing IDs.
	query := "INSERT INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,created_at,cpim_message_id,reference_id,reference_type) VALUES('message'," + sqlText(conversation) + ",'r9-fixture','rcs','outgoing','self','본문','2026-11-07','cpim-original','reply-target',1);"
	query += "INSERT INTO transport_attempts(id,message_id,protocol,state,attempt_number,created_at,updated_at) VALUES(1,'message','rcs','failed',1,'now','now');"
	query += "INSERT INTO transport_attempts(id,message_id,protocol,state,attempt_number,fallback_from,created_at,updated_at) VALUES(2,'message','mms','accepted',2,1,'now','now');"
	query += "INSERT INTO mms_metadata(message_id,transaction_id,content_location,expires_at) VALUES('message','mms-transaction','https://example.invalid/mms','2026-11-08');"
	query += "INSERT INTO message_parts(id,message_id,position,mime_type,text_content,content_id) VALUES('smil','message',0,'application/smil','<smil/>','presentation');"
	query += "INSERT INTO message_parts(id,message_id,position,mime_type,filename,remote_url,remote_expires_at) VALUES('image','message',1,'image/jpeg','사진.jpg','https://example.invalid/file','2026-11-08');"
	query += "INSERT INTO delivery_events(message_id,attempt_id,recipient,kind,occurred_at) VALUES('message',2,'recipient-one','delivered','now'),('message',2,'recipient-two','displayed','now');"
	if _, err := db.run(query, false); err != nil {
		t.Fatal(err)
	}
	rows, err := db.run("SELECT cpim_message_id,reference_id,(SELECT COUNT(*) FROM transport_attempts) AS attempts,(SELECT COUNT(*) FROM message_parts) AS parts,(SELECT COUNT(DISTINCT recipient) FROM delivery_events) AS recipients FROM messages;", true)
	if err != nil || len(rows) != 1 || rows[0]["cpim_message_id"] != "cpim-original" || rows[0]["reference_id"] != "reply-target" || rows[0]["attempts"] != float64(2) || rows[0]["parts"] != float64(2) || rows[0]["recipients"] != float64(2) {
		t.Fatal("R9 fallback overwrote message or receipt data")
	}
	if _, err := db.run("DELETE FROM conversations;", false); err != nil {
		t.Fatal("conversation delete failed", err)
	}
	rows, _ = db.run("SELECT COUNT(*) AS n FROM message_parts;", true)
	if rows[0]["n"] != float64(0) {
		t.Fatal("deleted thread retained private parts")
	}
}

func TestQueueChecksSIMBeforeUsingTransport(t *testing.T) {
	db := testMessageDB(t)
	session := opaqueID()
	_, err := db.queueSMS(session, opaqueID(), "01000000001", "검사")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = runSMSQueue(ctx, db, session, func(context.Context) error { return context.Canceled }, func(context.Context, string, string) (ims.SMSSubmission, error) {
		called = true
		return ims.SMSSubmission{}, nil
	})
	if err == nil || called {
		t.Fatal("SIM change reached SMS transport")
	}
	rows, _ := db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] != "not-sent" {
		t.Fatal("blocked send not retained")
	}
}

func TestReadStateAndPagingStaySeparateFromRemoteReceipts(t *testing.T) {
	db := testMessageDB(t)
	message := ims.SMS{CallID: "one", From: "01000000001", To: "01000000002", Text: "내용", ReceivedAt: time.Now()}
	if err := db.incoming(message); err != nil {
		t.Fatal(err)
	}
	id := stableID(db.subscription, "direct", canonicalPeer(message.From))
	result, err := db.messageMethod("thread", messageRequest{ID: id}, messagingRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), "내용") {
		t.Fatal("thread did not return stored text")
	}
	if _, err := db.messageMethod("mark_read", messageRequest{ID: id, Through: 1}, messagingRuntime{}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT local_read_at,(SELECT COUNT(*) FROM delivery_events) AS receipts FROM messages;", true)
	if rows[0]["local_read_at"] == nil || rows[0]["receipts"] != float64(0) {
		t.Fatal("reading locally manufactured a remote receipt")
	}
}

func TestDurableSegmentsReassembleAcrossRestartBeforeAcknowledgement(t *testing.T) {
	db := testMessageDB(t)
	second := imssms.Incoming{CallID: "part-two", From: "01000000001", To: "01000000002", Text: "둘째", RawTPDU: []byte{2}, UserDataHeader: []byte{5, 0, 3, 42, 2, 2}}
	if err := db.acceptSegment(second); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT COUNT(*) AS n FROM messages;", true)
	if rows[0]["n"] != float64(0) {
		t.Fatal("incomplete SMS displayed")
	}
	reopened, err := openMessageDB(db.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.subscription = db.subscription
	first := second
	first.CallID = "part-one"
	first.Text = "첫째 "
	first.RawTPDU = []byte{1}
	first.UserDataHeader = []byte{5, 0, 3, 42, 2, 1}
	if err := reopened.acceptSegment(first); err != nil {
		t.Fatal(err)
	}
	if err := reopened.recoverSegments(); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT body,(SELECT COUNT(*) FROM sms_segments WHERE message_id IS NOT NULL) AS parts FROM messages;", true)
	if len(rows) != 1 || rows[0]["body"] != "첫째 둘째" || rows[0]["parts"] != float64(2) {
		t.Fatal("restart lost, reordered or duplicated SMS parts")
	}
	binarySMS := first
	binarySMS.CallID = "wap"
	binarySMS.RawTPDU = []byte{3}
	binarySMS.UserDataHeader = []byte{6, 5, 4, 0x0b, 0x84, 0x23, 0xf0}
	if err := reopened.acceptSegment(binarySMS); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT COUNT(*) AS n FROM messages;", true)
	if rows[0]["n"] != float64(1) {
		t.Fatal("WAP data exposed as a text SMS")
	}
	header, err := smsHeader(binarySMS.UserDataHeader)
	if err != nil || header.destination != 2948 || header.source != 9200 {
		t.Fatal("MMS/WAP port metadata lost")
	}
}

func TestMessagingRPCQueueReachesTransportOnceAndStoresResult(t *testing.T) {
	db := testMessageDB(t)
	session := opaqueID()
	request := messageRequest{Subscription: db.subscription, Session: session, RequestID: opaqueID(), To: "01000000001", Text: "한글\n'본문'"}
	state := messagingRuntime{Registration: "registered", Subscription: db.subscription, Session: session, PLMN: "45006"}
	for i := 0; i < 2; i++ {
		if _, err := db.messageMethod("send", request, state); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var sends atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- runSMSQueue(ctx, db, session, func(context.Context) error { return nil }, func(_ context.Context, to, body string) (ims.SMSSubmission, error) {
			sends.Add(1)
			if to != "+821000000001" || body != request.Text {
				return ims.SMSSubmission{}, context.Canceled
			}
			updates := make(chan ims.SMSSubmissionUpdate, 2)
			updates <- ims.SMSSubmissionUpdate{SegmentIndex: 1, SegmentCount: 1, Recipient: to, State: ims.SMSAcceptedBySMSC, ReceivedAt: time.Now()}
			updates <- ims.SMSSubmissionUpdate{SegmentIndex: 1, SegmentCount: 1, Recipient: to, State: ims.SMSDelivered, ReceivedAt: time.Now()}
			close(updates)
			return ims.SMSSubmission{SubmittedAt: time.Now(), SegmentCount: 1, Segments: []ims.SMSSubmissionSegment{{Index: 1}}, Updates: updates}, nil
		})
	}()
	delivered := false
	for ctx.Err() == nil {
		rows, err := db.run("SELECT state FROM transport_attempts;", true)
		if err != nil {
			t.Error(err)
			break
		}
		if len(rows) == 1 && rows[0]["state"] == "delivered" {
			delivered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !delivered || sends.Load() != 1 {
		t.Fatal("queue lost result or duplicated a retried request")
	}
}

func TestMessagingInputAndRuntimeRejectStaleOrOversizedRequests(t *testing.T) {
	for _, input := range []string{`{} {}`, `{"unexpected":true}`, strings.Repeat("x", 16385)} {
		if _, err := readMessageRequest(strings.NewReader(input)); err == nil {
			t.Fatal("invalid messaging input accepted")
		}
	}
	if _, err := readMessageRequest(strings.NewReader(`{"ubus_rpc_session":"test","id":"test"}`)); err != nil {
		t.Fatal("rpcd session metadata rejected")
	}
	path := filepath.Join(t.TempDir(), "runtime.json")
	subscription, session := opaqueID(), opaqueID()
	if err := writeRuntimeStatus(path, 1, true, false, subscription, session); err != nil {
		t.Fatal(err)
	}
	if currentMessagingRuntime(path).Subscription != subscription {
		t.Fatal("fresh runtime rejected")
	}
	data, _ := os.ReadFile(path)
	var state map[string]any
	_ = json.Unmarshal(data, &state)
	state["updated_at"] = time.Now().Unix() - 9
	data, _ = json.Marshal(state)
	_ = os.WriteFile(path, data, 0600)
	if currentMessagingRuntime(path).Subscription != "" {
		t.Fatal("stale IMS runtime permitted sending")
	}
}
