// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	imssms "github.com/damonto/ims-go/ims/sms"
	"golang.org/x/text/encoding/korean"
)

func testUintvar(n int) []byte {
	value := []byte{byte(n & 127)}
	for n >>= 7; n > 0; n >>= 7 {
		value = append([]byte{byte(n&127) | 128}, value...)
	}
	return value
}
func testLength(data []byte) []byte {
	if len(data) < 31 {
		return append([]byte{byte(len(data))}, data...)
	}
	return append(append([]byte{31}, testUintvar(len(data))...), data...)
}
func testMMSNotification(location string) []byte {
	pdu := []byte{0x8c, 0x82, 0x98}
	pdu = append(pdu, []byte("test-transaction\x00")...)
	pdu = append(pdu, 0x8d, 0x91, 0x89)
	pdu = append(pdu, testLength(append([]byte{128}, []byte("01000000001/TYPE=PLMN\x00")...))...)
	pdu = append(pdu, 0x8e, 2, 1, 0, 0x88, 3, 129, 1, 120, 0x83)
	return append(pdu, []byte(location+"\x00")...)
}
func testMMSPush(pdu []byte, textual bool) []byte {
	content := []byte{0xbe}
	if textual {
		content = append([]byte("application/vnd.wap.mms-message"), 0)
	}
	return append(append(append([]byte{1, 6}, testUintvar(len(content))...), content...), pdu...)
}
func testMMSRetrieval(t *testing.T) ([]byte, []byte) {
	t.Helper()
	koreanText, err := korean.EUCKR.NewEncoder().Bytes([]byte("한글 본문\n둘째 줄"))
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{137, 'P', 'N', 'G', 13, 10, 26, 10}
	pdu := []byte{0x8c, 0x84, 0x8d, 0x91, 0x8b}
	pdu = append(pdu, []byte("test-message\x00")...)
	pdu = append(pdu, 0x96)
	pdu = append(pdu, []byte("subject\x00")...)
	pdu = append(pdu, 0x84, 0xb3, 3)
	parts := []struct{ header, data []byte }{
		{append([]byte("application/smil"), 0), []byte("<smil><body/></smil>")},
		{[]byte{3, 0x83, 0x81, 0xa6}, koreanText},
		{[]byte{0x9e, 0x8e, '.', '.', '/', 'p', 'h', 'o', 't', 'o', '.', 'p', 'n', 'g', 0}, image},
	}
	for _, part := range parts {
		pdu = append(pdu, testUintvar(len(part.header))...)
		pdu = append(pdu, testUintvar(len(part.data))...)
		pdu = append(pdu, part.header...)
		pdu = append(pdu, part.data...)
	}
	return pdu, image
}

func TestMMSCodecKoreanMultipartAndMalformedInputs(t *testing.T) {
	for _, textual := range []bool{false, true} {
		data, err := decodeMMSPush(testMMSPush(testMMSNotification("http://omammsc.uplus.co.kr:9095/example"), textual))
		if err != nil {
			t.Fatal(err)
		}
		pdu, err := decodeMMS(data, time.Now())
		if err != nil || pdu.kind != 0x82 || pdu.transaction != "test-transaction" || mmsAddress(pdu.from) != "+821000000001" || pdu.expires.IsZero() {
			t.Fatal("MMS notification fields lost")
		}
	}
	data, image := testMMSRetrieval(t)
	pdu, err := decodeMMS(data, time.Now())
	if err != nil || len(pdu.parts) != 3 || pdu.parts[1].text != "한글 본문\n둘째 줄" || !bytes.Equal(pdu.parts[2].data, image) {
		t.Fatal("Korean MMS multipart decode failed", err)
	}
	for index := range data {
		_, _ = decodeMMS(data[:index], time.Now())
	}
	for _, invalid := range [][]byte{{255, 255, 255, 255, 255, 255}, {1, 6, 127}, {1, 6, 1, 0x83}, {1, 6, 1, 0xbe, 0x8c}} {
		_, _ = decodeMMSPush(invalid)
		_, _ = decodeMMS(invalid, time.Now())
	}
	carrier, _ := koreanMMSCarrier("45006")
	for _, bad := range []string{"http://127.0.0.1/", "http://omammsc.uplus.co.kr.example.com/", "file:///etc/passwd", "http://user@omammsc.uplus.co.kr:9095/", "http://omammsc.uplus.co.kr:22/", "http://omammsc.uplus.co.kr/#fragment"} {
		if carrier.validateLocation(bad) == nil {
			t.Fatal("untrusted MMS location accepted")
		}
	}
}

func TestMMSReassemblySurvivesRestartAndSeparatesSIMs(t *testing.T) {
	db := testMessageDB(t)
	push := testMMSPush(testMMSNotification("http://omammsc.uplus.co.kr:9095/example"), true)
	for index, data := range [][]byte{push[:70], push[70:]} {
		header := []byte{11, 5, 4, 0x0b, 0x84, 0x23, 0xf0, 0, 3, 23, 2, byte(index + 1)}
		message := imssms.Incoming{CallID: "mms-part-" + strconv.Itoa(index), From: "01000000001", To: "+821000000002", DataCodingScheme: 4,
			UserDataHeader: header, UserData: append(append([]byte{}, header...), data...), RawTPDU: append([]byte{byte(index)}, data...)}
		if err := db.acceptSegment(message); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			rows, _ := db.run("SELECT COUNT(*) AS n FROM messages;", true)
			if rows[0]["n"] != float64(0) {
				t.Fatal("partial MMS exposed as a text message")
			}
			var err error
			db, err = openMessageDB(db.path)
			if err != nil || db.bind("test-sim-one", 1, "45006") != nil {
				t.Fatal("restart failed")
			}
		}
	}
	if err := db.recoverSegments(); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT protocol,(SELECT COUNT(*) FROM sms_segments WHERE message_id=messages.id) AS parts FROM messages;", true)
	if len(rows) != 1 || rows[0]["protocol"] != "mms" || rows[0]["parts"] != float64(2) {
		t.Fatal("reassembled MMS duplicated or lost its segments")
	}
	if err := db.bind("test-sim-two", 2, "45008"); err != nil {
		t.Fatal(err)
	}
	verify := func(context.Context) error { t.Fatal("another SIM processed the MMS"); return nil }
	if err := db.processMMS(context.Background(), mmsCarrier{}, &http.Client{}, verify); err != nil {
		t.Fatal(err)
	}
}

func TestMMSDurableBeforeAcknowledgementAndNoRedownload(t *testing.T) {
	db := testMessageDB(t)
	data, image := testMMSRetrieval(t)
	gets, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
			w.Header().Set("Content-Type", "application/vnd.wap.mms-message")
			_, _ = w.Write(data)
			return
		}
		posts++
		rows, _ := db.run("SELECT COUNT(*) AS n FROM message_parts WHERE transfer_state='complete';", true)
		if rows[0]["n"] != float64(3) {
			t.Error("MMS acknowledged before durable storage")
		}
		ack, _ := io.ReadAll(r.Body)
		if !bytes.HasPrefix(ack, []byte{0x8c, 0x83, 0x98}) {
			t.Error("invalid MMS notification response")
		}
		if posts == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	if err := db.incomingMMS(testMMSPush(testMMSNotification(server.URL+"/message"), false), "01000000001", "01000000002", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	carrier := mmsCarrier{mmsc: server.URL}
	verify := func(context.Context) error { return nil }
	if err := db.processMMS(context.Background(), carrier, server.Client(), verify); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT m.body,a.state FROM messages m JOIN transport_attempts a ON a.message_id=m.id;", true)
	if rows[0]["body"] != "한글 본문\n둘째 줄" || rows[0]["state"] != "mms_ack_failed" {
		t.Fatal("stored MMS lost when the acknowledgement failed")
	}
	_, _ = db.run("UPDATE transport_attempts SET updated_at='2000-01-01';", false)
	if err := db.processMMS(context.Background(), carrier, server.Client(), verify); err != nil {
		t.Fatal(err)
	}
	if gets != 1 || posts != 2 {
		t.Fatal("MMS was downloaded again while retrying acknowledgement")
	}
	rows, _ = db.run("SELECT id,local_path FROM message_parts WHERE mime_type='image/png';", true)
	partID := rows[0]["id"].(string)
	result, err := db.messageMethod("read_part", messageRequest{ID: partID}, messagingRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	read, _ := base64.StdEncoding.DecodeString(result.(map[string]any)["data"].(string))
	if !bytes.Equal(read, image) {
		t.Fatal("attachment read-back changed its bytes")
	}
	threadRows, _ := db.run("SELECT id FROM conversations;", true)
	threadID := threadRows[0]["id"].(string)
	thread, err := db.messageMethod("thread", messageRequest{ID: threadID}, messagingRuntime{})
	if err != nil || thread.(map[string]any)["messages"].([]map[string]any)[0]["attachments"] != float64(1) {
		t.Fatal("SMIL and text incorrectly counted as attachments")
	}
	_, _ = db.run("UPDATE transport_attempts SET state='mms_downloading';", false)
	if _, err := db.messageMethod("delete_thread", messageRequest{ID: threadID}, messagingRuntime{}); err == nil {
		t.Fatal("conversation deleted during an MMS transfer")
	}
	_, _ = db.run("UPDATE transport_attempts SET state='mms_received';", false)
	if _, err := db.messageMethod("delete_thread", messageRequest{ID: threadID}, messagingRuntime{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(db.partDirectory(), partID)); !os.IsNotExist(err) {
		t.Fatal("deleted attachment file was retained")
	}
	if _, err := db.readPart(partID, 0); err == nil {
		t.Fatal("deleted attachment is still readable")
	}
}

func TestMMSFailedDecodeRetainsOriginalAndDoesNotAcknowledge(t *testing.T) {
	db := testMessageDB(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Error("undecoded MMS was acknowledged")
		}
		_, _ = w.Write([]byte("unrecognized MMS encoding"))
	}))
	defer server.Close()
	if err := db.incomingMMS(testMMSPush(testMMSNotification(server.URL), false), "01000000001", "01000000002", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.processMMS(context.Background(), mmsCarrier{mmsc: server.URL}, server.Client(), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		_, _ = db.run("UPDATE transport_attempts SET updated_at='2000-01-01';", false)
	}
	rows, _ := db.run("SELECT byte_size FROM mms_pdus WHERE message_type='m-retrieve-conf';", true)
	if requests != 1 || len(rows) != 1 || rows[0]["byte_size"] != float64(len("unrecognized MMS encoding")) {
		t.Fatal("unrecognized original was lost or downloaded repeatedly")
	}
	if _, err := db.readPart(strings.Repeat("a", 64), 0); err == nil {
		t.Fatal("unavailable attachment accepted")
	}
}

func TestMMSRetriesTransientServerPDUWithoutAcknowledgingIt(t *testing.T) {
	db := testMessageDB(t)
	data, _ := testMMSRetrieval(t)
	gets, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			return
		}
		gets++
		if gets == 1 {
			_, _ = w.Write([]byte{0x8c, 0x84, 0x8d, 0x91, 0x99, 0xc0})
		} else {
			_, _ = w.Write(data)
		}
	}))
	defer server.Close()
	if err := db.incomingMMS(testMMSPush(testMMSNotification(server.URL), false), "01000000001", "01000000002", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	verify := func(context.Context) error { return nil }
	if err := db.processMMS(context.Background(), mmsCarrier{mmsc: server.URL}, server.Client(), verify); err != nil || posts != 0 {
		t.Fatal("transient server error was acknowledged", err)
	}
	_, _ = db.run("UPDATE transport_attempts SET updated_at='2000-01-01';", false)
	if err := db.processMMS(context.Background(), mmsCarrier{mmsc: server.URL}, server.Client(), verify); err != nil {
		t.Fatal(err)
	}
	if gets != 2 || posts != 1 {
		t.Fatal("transient server response prevented recovery")
	}
}
