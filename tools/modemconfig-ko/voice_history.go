// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"errors"
	"strconv"
	"time"
)

const callHistorySchema = `CREATE TABLE IF NOT EXISTS calls(
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 subscription_id TEXT NOT NULL REFERENCES subscriptions(id), peer TEXT NOT NULL,
 direction TEXT NOT NULL CHECK(direction IN('incoming','outgoing')),
 started_at TEXT NOT NULL, connected_at TEXT, ended_at TEXT,
 duration_seconds INTEGER NOT NULL DEFAULT 0 CHECK(duration_seconds>=0),
 audio TEXT NOT NULL, result TEXT CHECK(result IN('completed','missed','rejected','canceled','failed','interrupted')),
 failure TEXT);
 CREATE INDEX IF NOT EXISTS calls_started ON calls(started_at DESC,seq DESC);`

type callRecord struct {
	id, peer, direction, audio, result, failure string
	started, connected, ended                   time.Time
	duration                                    int64
}

func sqlCallTime(at time.Time) string {
	if at.IsZero() {
		return "NULL"
	}
	return sqlText(at.UTC().Format(databaseTime))
}

func (db *messageDB) saveCall(c callRecord) error {
	if !hexID(c.id, 32) || c.started.IsZero() || (c.direction != "incoming" && c.direction != "outgoing") {
		return errors.New("invalid-call")
	}
	result := "NULL"
	if c.result != "" {
		result = sqlText(c.result)
	}
	_, err := db.run(`INSERT INTO calls(id,subscription_id,peer,direction,started_at,connected_at,ended_at,duration_seconds,audio,result,failure) VALUES(`+
		sqlText(c.id)+","+sqlText(db.subscription)+","+sqlText(canonicalPeer(c.peer))+","+sqlText(c.direction)+","+sqlCallTime(c.started)+","+sqlCallTime(c.connected)+","+sqlCallTime(c.ended)+","+strconv.FormatInt(c.duration, 10)+","+sqlText(c.audio)+","+result+","+sqlText(c.failure)+`) ON CONFLICT(id) DO UPDATE SET
 connected_at=COALESCE(calls.connected_at,excluded.connected_at),ended_at=excluded.ended_at,
 duration_seconds=excluded.duration_seconds,audio=excluded.audio,result=excluded.result,failure=excluded.failure
 WHERE calls.subscription_id=excluded.subscription_id AND (calls.ended_at IS NULL OR excluded.ended_at IS NOT NULL);`, false)
	return err
}

func (db *messageDB) recoverCalls() error {
	_, err := db.run("UPDATE calls SET ended_at="+sqlText(timestamp())+",result='interrupted',failure='service-restarted' WHERE ended_at IS NULL;", false)
	return err
}

func (db *messageDB) callHistory(before int64) (any, error) {
	if before < 0 {
		return nil, errors.New("invalid-request")
	}
	condition := ""
	if before > 0 {
		condition = " WHERE h.seq<" + strconv.FormatInt(before, 10)
	}
	rows, err := db.run(`SELECT h.seq,h.id,h.subscription_id,h.peer,h.direction,h.started_at,h.connected_at,h.ended_at,
 h.duration_seconds,h.audio,h.result,h.failure,k.name,s.slot,s.provider
 FROM calls h JOIN subscriptions s ON s.id=h.subscription_id
 LEFT JOIN contact_addresses a ON a.address=h.peer LEFT JOIN contacts k ON k.id=a.contact_id`+condition+` ORDER BY h.seq DESC LIMIT 101;`, true)
	if err != nil {
		return nil, err
	}
	more := len(rows) > 100
	if more {
		rows = rows[:100]
	}
	return map[string]any{"calls": rows, "more": more}, nil
}

// Serialize snapshots so a late write cannot replace the final result with ringing.
func (v *voiceService) persistHistory() error {
	if v.db == nil {
		return nil
	}
	v.historyMu.Lock()
	defer v.historyMu.Unlock()
	v.mu.Lock()
	if v.id == "" || v.startedAt.IsZero() {
		v.mu.Unlock()
		return nil
	}
	c := callRecord{id: v.id, peer: v.peer, direction: v.direction, audio: v.audio, started: v.startedAt, connected: v.connectedAt, ended: v.endedAt, duration: v.duration(time.Now()), failure: v.detail}
	if !c.ended.IsZero() {
		switch {
		case v.state == "failed":
			c.result = "failed"
		case !c.connected.IsZero():
			c.result = "completed"
		case c.direction == "incoming":
			c.result = "missed"
			if v.endReason != "" {
				c.result = "rejected"
			}
		case v.endReason != "":
			c.result = "canceled"
		default:
			c.result = "failed"
		}
	}
	signature := c.id + "|" + v.state + "|" + c.failure + "|" + v.endReason
	v.mu.Unlock()
	if signature == v.historySaved {
		return nil
	}
	if err := v.db.saveCall(c); err != nil {
		return err
	}
	v.historySaved = signature
	return nil
}
