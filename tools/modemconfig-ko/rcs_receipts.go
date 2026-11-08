// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const rcsReceiptSchema = `CREATE TABLE IF NOT EXISTS rcs_receipts(
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN('delivered','displayed')),
    receipt_id TEXT NOT NULL UNIQUE, requested_at TEXT NOT NULL, sent_at TEXT,
    attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TEXT NOT NULL,
    PRIMARY KEY(message_id,kind));`

func (db *messageDB) pendingRCSReceipts() ([]map[string]any, error) {
	return db.run(`SELECT r.message_id,r.kind,r.receipt_id,r.attempts,m.cpim_message_id,c.peer FROM rcs_receipts r
        JOIN messages m ON m.id=r.message_id JOIN conversations c ON c.id=m.conversation_id
        WHERE c.subscription_id=`+sqlText(db.subscription)+` AND r.sent_at IS NULL AND r.next_attempt_at<=`+sqlText(timestamp())+`
        AND (r.kind='delivered' OR m.local_read_at IS NOT NULL AND NOT EXISTS(
            SELECT 1 FROM rcs_receipts d WHERE d.message_id=r.message_id AND d.kind='delivered' AND d.sent_at IS NULL))
        ORDER BY r.requested_at,r.kind LIMIT 16;`, true)
}

func (db *messageDB) rcsReceiptResult(row map[string]any, sent bool) error {
	when := time.Now().UTC()
	attempts := int(row["attempts"].(float64)) + 1
	delay := min(300, 1<<min(attempts, 8))
	state := "attempts=" + strconv.Itoa(attempts) + ",next_attempt_at=" + sqlText(when.Add(time.Duration(delay)*time.Second).Format(databaseTime))
	if sent {
		state += ",sent_at=" + sqlText(when.Format(databaseTime))
	}
	_, err := db.run("UPDATE rcs_receipts SET "+state+" WHERE message_id="+sqlText(row["message_id"].(string))+" AND kind="+sqlText(row["kind"].(string))+" AND sent_at IS NULL;", false)
	return err
}

func (r *rcsService) runReceipts(ctx context.Context, db *messageDB, verify func(context.Context) error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.Lock()
		client, ready := r.client, r.readyLocked()
		r.mu.Unlock()
		if !ready || client == nil {
			continue
		}
		rows, err := db.pendingRCSReceipts()
		if err != nil || len(rows) == 0 {
			continue
		}
		if verify(ctx) != nil {
			return
		}
		for _, row := range rows {
			send, stop := context.WithTimeout(ctx, 20*time.Second)
			err = client.sendReceipt(send, row["peer"].(string), row["cpim_message_id"].(string), row["receipt_id"].(string), row["kind"].(string))
			stop()
			_ = db.rcsReceiptResult(row, err == nil)
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func (r *rcsSIP) sendReceipt(ctx context.Context, peer, id, eventID, kind string) error {
	if kind != "delivered" && kind != "displayed" {
		return errors.New("rcs-receipt-invalid")
	}
	d, err := r.openChat(ctx, "tel:"+canonicalPeer(peer))
	if err != nil {
		return err
	}
	return d.receipt(ctx, id, eventID, kind)
}
