// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/xml"
	"testing"
)

func TestRCSRequestedReceiptsPersistAndRespectReadAndSIM(t *testing.T) {
	db := testMessageDB(t)
	scope := db.subscription
	data := makeCPIM("sip:anonymous@anonymous.invalid", "sip:anonymous@anonymous.invalid", "tta-example", "text/plain", []byte("한글"), true)
	for range 2 {
		if _, err := db.acceptRCS("tel:+821000000001", "tel:+821000000002", "network", data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.acceptRCS("sip:anonymous@anonymous.invalid", "tel:+821000000002", "network", data); err == nil {
		t.Fatal("anonymous SIP dialog supplied no trusted sender")
	}
	reopened, err := openMessageDB(db.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.subscription = scope
	rows, err := reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 1 || rows[0]["kind"] != "delivered" {
		t.Fatal("delivery receipt was lost or read receipt sent before reading")
	}
	if err = reopened.rcsReceiptResult(rows[0], true); err != nil {
		t.Fatal(err)
	}
	rows, err = reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 0 {
		t.Fatal("display receipt preceded local read")
	}
	_, err = reopened.run("UPDATE messages SET local_read_at="+sqlText(timestamp())+" WHERE direction='incoming';", false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 1 || rows[0]["kind"] != "displayed" {
		t.Fatal("reading failed to schedule displayed receipt")
	}
	event := rows[0]["receipt_id"]
	if err = reopened.rcsReceiptResult(rows[0], false); err != nil {
		t.Fatal(err)
	}
	rows, err = reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 0 {
		t.Fatal("failed receipt ignored retry delay")
	}
	_, err = reopened.run("UPDATE rcs_receipts SET next_attempt_at='2000' WHERE kind='displayed';", false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 1 || rows[0]["receipt_id"] != event {
		t.Fatal("retry changed receipt identity or duplicated it")
	}
	reopened.subscription = "other-sim"
	rows, err = reopened.pendingRCSReceipts()
	if err != nil || len(rows) != 0 {
		t.Fatal("receipt crossed SIM scope")
	}
	for _, kind := range []string{"delivered", "displayed"} {
		var report rcsIMDN
		if xml.Unmarshal(rcsReceiptXML("original", kind), &report) != nil || report.ID != "original" {
			t.Fatal("invalid IMDN XML")
		}
		if kind == "displayed" && (report.Display == nil || report.Delivery != nil) {
			t.Fatal("displayed receipt used delivery-notification")
		}
	}
}
