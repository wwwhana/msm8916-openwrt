// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCallHistoryContactsDurationSIMAndRestart(t *testing.T) {
	db := testMessageDB(t)
	_, err := db.saveContact(savedContact{Name: "가족", Addresses: []contactAddress{{Address: "01000000001"}}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	c := callRecord{id: opaqueID(), peer: "01000000001", direction: "outgoing", audio: "browser", started: start}
	if err = db.saveCall(c); err != nil {
		t.Fatal(err)
	}
	c.connected = start.Add(10 * time.Second)
	c.ended = start.Add(45 * time.Second)
	c.duration = 35
	c.result = "completed"
	if err = db.saveCall(c); err != nil {
		t.Fatal(err)
	}
	late := c
	late.ended = time.Time{}
	late.connected = time.Time{}
	late.result = ""
	late.duration = 0
	if err = db.saveCall(late); err != nil {
		t.Fatal(err)
	}
	if err = db.bind("second-sim", 2, "45005"); err != nil {
		t.Fatal(err)
	}
	missed := callRecord{id: opaqueID(), peer: "01000000002", direction: "incoming", audio: "browser", started: start, ended: start.Add(5 * time.Second), result: "missed"}
	if err = db.saveCall(missed); err != nil {
		t.Fatal(err)
	}
	pending := missed
	pending.id = opaqueID()
	pending.ended = time.Time{}
	pending.result = ""
	if err = db.saveCall(pending); err != nil {
		t.Fatal(err)
	}
	if err = db.recoverCalls(); err != nil {
		t.Fatal(err)
	}
	result, err := db.callHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["calls"].([]map[string]any)
	if len(rows) != 3 || rows[0]["result"] != "interrupted" || rows[1]["result"] != "missed" || rows[1]["slot"] != float64(2) || rows[2]["name"] != "가족" || rows[2]["duration_seconds"] != float64(35) || rows[2]["slot"] != float64(1) {
		t.Fatal("history lost a contact, SIM, terminal result or answered duration")
	}
	page, err := db.callHistory(int64(rows[1]["seq"].(float64)))
	if err != nil || len(page.(map[string]any)["calls"].([]map[string]any)) != 1 {
		t.Fatal("history pagination repeated calls")
	}
}

func TestV3MigrationKeepsContactsAndMessages(t *testing.T) {
	db := testMessageDB(t)
	_, err := db.saveContact(savedContact{Name: "기존 연락처", Addresses: []contactAddress{{Address: "01000000001"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.queueSMS(opaqueID(), opaqueID(), "01000000001", "기존 문자"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.run("DROP TABLE calls; DROP TABLE rcs_receipts; PRAGMA user_version=3;", false); err != nil {
		t.Fatal(err)
	}
	reopened, err := openMessageDB(db.path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reopened.run("SELECT (SELECT COUNT(*) FROM contacts) AS contacts,(SELECT COUNT(*) FROM messages) AS messages;", true)
	if err != nil || rows[0]["contacts"] != float64(1) || rows[0]["messages"] != float64(1) {
		t.Fatal("migration lost stored data")
	}
	if _, err = reopened.run("SELECT * FROM calls; SELECT * FROM rcs_receipts;", true); err != nil {
		t.Fatal(err)
	}
}

func TestVoiceFailureNeverCopiesPrivateCause(t *testing.T) {
	for _, sample := range []struct{ cause, want string }{{"subscriber-private-value SIP/2.0 488 Not Acceptable", "sip-488"}, {"private-value cancelled", "cancelled"}, {"private-value missing answer sdp", "sdp"}, {"private-value", "call-error"}} {
		got := voiceFailure(errors.New(sample.cause))
		if got != sample.want {
			t.Fatalf("failure label %q != %q", got, sample.want)
		}
		traceVoiceState("failed", sample.cause)
	}
	data, _ := json.Marshal(snapshotVoiceTrace())
	if strings.Contains(string(data), "private-value") {
		t.Fatal("private call data entered diagnostics")
	}
}

func TestIncomingAnswerFailureIsNotRecordedAsMissed(t *testing.T) {
	db := testMessageDB(t)
	v := &voiceService{db: db, id: opaqueID(), peer: "+821000000001", direction: "incoming", audio: "browser",
		state: "failed", detail: "sdp", startedAt: time.Now().Add(-time.Second), endedAt: time.Now()}
	if err := v.persistHistory(); err != nil {
		t.Fatal(err)
	}
	result, err := db.callHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["calls"].([]map[string]any)
	if len(rows) != 1 || rows[0]["result"] != "failed" || rows[0]["failure"] != "sdp" {
		t.Fatal("answer failure was recorded as a missed call")
	}
}
