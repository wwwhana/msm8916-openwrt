// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPhoto(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	img.Set(3, 3, color.NRGBA{R: 90, G: 180, B: 240, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func testUpload(t *testing.T, db *messageDB, session string, data []byte) string {
	t.Helper()
	policy := koreanMessagePolicy("45006")
	request := messageRequest{Session: session, RequestID: opaqueID(), Filename: "사진.png", MimeType: "image/png", Size: int64(len(data))}
	result, err := db.upload("begin_upload", request, policy)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["id"].(string)
	request.ID, request.Data = id, base64.StdEncoding.EncodeToString(data)
	result, err = db.upload("write_upload", request, policy)
	if err != nil || !result.(map[string]any)["complete"].(bool) {
		t.Fatal("upload failed", err)
	}
	// A lost response must not append the same chunk twice.
	if _, err := db.upload("write_upload", request, policy); err != nil {
		t.Fatal("chunk retry failed", err)
	}
	return id
}

func TestKoreanMessagePlansAndMMSComposition(t *testing.T) {
	for _, plmn := range []string{"45005", "45006", "45008"} {
		policy := koreanMessagePolicy(plmn)
		for _, test := range []struct {
			text  string
			files int
			kind  string
			valid bool
		}{
			{strings.Repeat("한", 70), 0, "SMS", true}, {strings.Repeat("한", 71), 0, "LMS", true},
			{strings.Repeat("한", 1000), 0, "LMS", true}, {strings.Repeat("한", 1001), 0, "LMS", false},
			{strings.Repeat("😀", 35), 0, "SMS", true}, {strings.Repeat("😀", 36), 0, "LMS", true},
			{"", 1, "MMS", true}, {"사진 설명", 3, "MMS", true}, {"", 4, "SMS", false}, {"", 0, "SMS", false},
		} {
			plan := planMessage(policy, test.text, test.files)
			if plan.Kind != test.kind || plan.Valid != test.valid {
				t.Fatalf("%s plan mismatch: kind=%s valid=%v", plmn, plan.Kind, plan.Valid)
			}
		}
	}
	if planMessage(koreanMessagePolicy("45006"), strings.Repeat("a", 81), 0).Kind != "LMS" {
		t.Fatal("LG U+ Android threshold ignored")
	}
	if planMessage(koreanMessagePolicy("45005"), strings.Repeat("a", 160), 0).Kind != "SMS" {
		t.Fatal("GSM packing ignored")
	}
	if planMessage(koreanMessagePolicy("45005"), strings.Repeat("^", 81), 0).Kind != "LMS" {
		t.Fatal("GSM extension overflow ignored")
	}
	if planMessage(koreanMessagePolicy("99999"), "hello", 0).Valid {
		t.Fatal("unknown carrier accepted")
	}
	for _, text := range []string{"긴 문자\n둘째 줄", "이모지 😀"} {
		wire, _ := composeMMS("test-transaction", "+821000000001", text, []mmsPart{{media: "image/png", name: "사진.png", data: testPhoto(t)}})
		pdu, err := decodeMMS(wire, time.Now())
		if err != nil || pdu.kind != 0x80 || pdu.transaction != "test-transaction" || len(pdu.parts) != 3 || pdu.parts[1].text != text || pdu.parts[2].name != "사진.png" || !bytes.Equal(pdu.parts[2].data, testPhoto(t)) {
			t.Fatal("M-Send.req lost content", err)
		}
		if pdu.reportAllowed != 0 {
			t.Fatal("unexpected report allowed")
		}
	}
}

func TestMMSQueueUploadsSIMIsolationAndIdempotency(t *testing.T) {
	db := testMessageDB(t)
	first := db.subscription
	session, requestID := opaqueID(), opaqueID()
	upload := testUpload(t, db, session, testPhoto(t))
	if err := db.bind("different-sim", 2, "45005"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.queueMessage(session, requestID, "01000000001", "사진", []string{upload}, koreanMessagePolicy("45005")); err == nil {
		t.Fatal("another SIM consumed attachment")
	}
	db.subscription = first
	id, err := db.queueMessage(session, requestID, "01000000001", "사진", []string{upload}, koreanMessagePolicy("45006"))
	if err != nil {
		t.Fatal(err)
	}
	if again, err := db.queueMessage(session, requestID, "01000000001", "사진", []string{upload}, koreanMessagePolicy("45006")); err != nil || again != id {
		t.Fatal("request retry duplicated or failed", err)
	}
	if _, err := db.queueMessage(session, requestID, "01000000001", "사진", nil, koreanMessagePolicy("45006")); err == nil {
		t.Fatal("changed attachment reused request")
	}
	if claimed, err := db.claimSMS(session); err != nil || claimed != nil {
		t.Fatal("MMS claimed as SMS")
	}
	rows, _ := db.run("SELECT COUNT(*) AS n FROM message_parts WHERE mime_type='image/png';", true)
	if rows[0]["n"] != float64(1) {
		t.Fatal("photo not stored")
	}
	rows, _ = db.run("SELECT COUNT(*) AS n FROM message_uploads;", true)
	if rows[0]["n"] != float64(0) {
		t.Fatal("consumed upload retained")
	}
	if _, err := os.Stat(filepath.Join(db.uploadDirectory(), upload)); !os.IsNotExist(err) {
		t.Fatal("consumed upload file retained")
	}
	thread := stableID(db.subscription, "direct", "+821000000001")
	if _, err := db.messageMethod("delete_thread", messageRequest{ID: thread}, messagingRuntime{}); err == nil {
		t.Fatal("queued MMS deleted")
	}
}

func TestMMSSubmissionPersistsBeforePostAndNeverResendsUnknown(t *testing.T) {
	for _, mode := range []string{"accepted", "rejected", "wrong-transaction", "network"} {
		t.Run(mode, func(t *testing.T) {
			db := testMessageDB(t)
			session := opaqueID()
			text := strings.Repeat("장문", 45)
			id, err := db.queueMessage(session, opaqueID(), "01000000001", text, nil, koreanMessagePolicy("45006"))
			if err != nil {
				t.Fatal(err)
			}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				data, _ := io.ReadAll(r.Body)
				pdu, err := decodeMMS(data, time.Now())
				if err != nil || r.Method != "POST" || r.Header.Get("Content-Type") != "application/vnd.wap.mms-message" || pdu.kind != 0x80 || pdu.parts[1].text != text {
					t.Error("invalid submission", err)
				}
				rows, _ := db.run("SELECT COUNT(*) AS n FROM mms_pdus WHERE message_type='m-send-req';", true)
				if rows[0]["n"] != float64(1) {
					t.Error("original was not durable before POST")
				}
				thread := stableID(db.subscription, "direct", "+821000000001")
				if _, err := db.messageMethod("delete_thread", messageRequest{ID: thread}, messagingRuntime{}); err == nil {
					t.Error("active MMS deleted")
				}
				if mode == "network" {
					w.WriteHeader(503)
					return
				}
				transaction := pdu.transaction
				if mode == "wrong-transaction" {
					transaction = "different"
				}
				response := append([]byte{0x8c, 0x81, 0x98}, wspText(transaction)...)
				status := byte(0x80)
				if mode == "rejected" {
					status = 0xe0
				}
				response = append(response, 0x8d, 0x93, 0x92, status, 0x8b)
				_, _ = w.Write(append(response, wspText("server-id")...))
			}))
			defer server.Close()
			carrier := mmsCarrier{mmsc: server.URL}
			verify := func(context.Context) error { return nil }
			for i := 0; i < 2; i++ {
				if err := db.sendMMS(t.Context(), session, carrier, server.Client(), verify); err != nil {
					t.Fatal(err)
				}
			}
			if posts != 1 {
				t.Fatal("submission sent more than once")
			}
			rows, _ := db.run("SELECT state FROM transport_attempts WHERE message_id="+sqlText(id)+";", true)
			want := map[string]string{"accepted": "accepted_by_mmsc", "rejected": "delivery_failed", "wrong-transaction": "submit_unknown", "network": "submit_unknown"}[mode]
			if rows[0]["state"] != want {
				t.Fatalf("want %s, got %s", want, rows[0]["state"])
			}
		})
	}
}

func TestMMSInterruptedSubmissionAndOldSessionAreNotResent(t *testing.T) {
	db := testMessageDB(t)
	old := opaqueID()
	for _, body := range []string{strings.Repeat("가", 71), strings.Repeat("나", 71)} {
		if _, err := db.queueMessage(old, opaqueID(), "01000000001", body, nil, koreanMessagePolicy("45006")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.run("UPDATE transport_attempts SET state='mms_sending' WHERE id=(SELECT MIN(id) FROM transport_attempts);", false); err != nil {
		t.Fatal(err)
	}
	if err := db.recoverPending(opaqueID()); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT state FROM transport_attempts ORDER BY id;", true)
	if rows[0]["state"] != "submit_unknown" || rows[1]["state"] != "not-sent" {
		t.Fatal("interrupted MMS could be resent")
	}
}

func TestMessageSchemaUpgradePreservesExistingRecords(t *testing.T) {
	db := testMessageDB(t)
	if _, err := db.queueSMS(opaqueID(), opaqueID(), "01000000001", "보존 검사"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.run("DROP TABLE message_uploads; ALTER TABLE transport_attempts DROP COLUMN request_digest; PRAGMA user_version=1;", false); err != nil {
		t.Fatal(err)
	}
	reopened, err := openMessageDB(db.path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reopened.run("SELECT COUNT(*) AS n FROM messages;", true)
	if err != nil || rows[0]["n"] != float64(1) {
		t.Fatal("schema upgrade lost messages")
	}
	rows, err = reopened.run("PRAGMA user_version;", true)
	if err != nil || rows[0]["user_version"] != float64(5) {
		t.Fatal("schema upgrade incomplete")
	}
}

func TestUploadRPCValidatesSessionMediaAndExpiredFiles(t *testing.T) {
	db := testMessageDB(t)
	session := opaqueID()
	state := messagingRuntime{Registration: "registered", Subscription: db.subscription, Session: session, PLMN: "45006"}
	request := messageRequest{Subscription: db.subscription, Session: session, RequestID: opaqueID(), Filename: "bad.png", MimeType: "image/png", Size: 4}
	result, err := db.messageMethod("begin_upload", request, state)
	if err != nil {
		t.Fatal(err)
	}
	request.ID = result.(map[string]any)["id"].(string)
	request.Data = base64.StdEncoding.EncodeToString([]byte("nope"))
	if _, err := db.messageMethod("write_upload", request, state); err == nil {
		t.Fatal("invalid media accepted")
	}
	request.Session = opaqueID()
	if _, err := db.messageMethod("write_upload", request, state); err == nil {
		t.Fatal("stale session wrote upload")
	}
	request.Session = session
	if _, err := db.messageMethod("delete_upload", request, state); err != nil {
		t.Fatal(err)
	}
	id := testUpload(t, db, session, testPhoto(t))
	if _, err := db.run("UPDATE message_uploads SET created_at='2000-01-01T00:00:00.000000000Z';", false); err != nil {
		t.Fatal(err)
	}
	if err := db.withUploadLock(db.cleanUploads); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(db.uploadDirectory(), id)); !os.IsNotExist(err) {
		t.Fatal("expired attachment retained")
	}
}
