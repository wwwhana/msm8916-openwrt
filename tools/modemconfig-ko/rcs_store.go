// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxRCSText = 8192

func validRCSText(text string) bool {
	return strings.TrimSpace(text) != "" && len(text) <= maxRCSText && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}

func (db *messageDB) queueRCS(session, requestID, to, text string) (string, error) {
	to = canonicalPeer(to)
	if !hexID(requestID, 32) || !validPhone(to) || !validRCSText(text) {
		return "", errors.New("invalid-message")
	}
	id, digest := stableID(db.subscription, "outgoing", requestID), stableID("rcs", to, text)
	err := db.withUploadLock(func() error {
		rows, err := db.run("SELECT m.protocol,a.request_digest FROM messages m JOIN transport_attempts a ON a.message_id=m.id WHERE m.id="+sqlText(id)+";", true)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			if rows[0]["protocol"] != "rcs" || rows[0]["request_digest"] != digest {
				return errors.New("request-conflict")
			}
			return nil
		}
		conversation, err := db.conversation(to)
		if err != nil {
			return err
		}
		at := sqlText(timestamp())
		_, err = db.run("BEGIN IMMEDIATE; INSERT INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,created_at,local_read_at,cpim_message_id) VALUES("+sqlText(id)+","+sqlText(conversation)+","+sqlText(requestID)+",'rcs','outgoing','self',"+sqlText(text)+","+at+","+at+","+sqlText(opaqueID())+");"+
			"INSERT INTO transport_attempts(message_id,protocol,request_id,request_digest,session_id,state,created_at,updated_at) VALUES("+sqlText(id)+",'rcs',"+sqlText(requestID)+","+sqlText(digest)+","+sqlText(session)+",'queued',"+at+","+at+");"+
			"INSERT INTO message_recipients(message_id,address,role) VALUES("+sqlText(id)+","+sqlText(to)+",'to'); COMMIT;", false)
		return err
	})
	return id, err
}

// CPIM identities must agree with the authenticated SIP dialog and this SIM.
// The durable insert finishes before the MSRP success response is sent.
func (db *messageDB) acceptRCS(peer, own, networkID string, data []byte) (cpimMessage, error) {
	m, err := parseCPIM(data)
	if err != nil {
		return m, err
	}
	peer = canonicalPeer(peer)
	// TTA R9 §7.2.5 uses anonymous CPIM identities. The asserted SIP dialog
	// peer remains authoritative; a different explicit CPIM identity is rejected.
	if !validPhone(peer) || (m.from != "sip:anonymous@anonymous.invalid" && canonicalPeer(m.from) != peer) ||
		(m.to != "sip:anonymous@anonymous.invalid" && canonicalPeer(m.to) != canonicalPeer(own)) {
		return m, errors.New("rcs-identity-invalid")
	}
	if m.kind == "message/imdn+xml" {
		return m, db.acceptIMDN(peer, m.id, m.body)
	}
	if !validRCSText(string(m.body)) {
		return m, errors.New("rcs-message-invalid")
	}
	db.incomingMu.Lock()
	defer db.incomingMu.Unlock()
	conversation, err := db.conversation(peer)
	if err != nil {
		return m, err
	}
	id, at := stableID(db.subscription, "rcs", peer, m.id), sqlText(timestamp())
	rows, err := db.run("SELECT body,sender FROM messages WHERE id="+sqlText(id)+";", true)
	if err != nil {
		return m, err
	}
	if len(rows) > 0 {
		if rows[0]["body"] != string(m.body) || rows[0]["sender"] != peer {
			return m, errors.New("rcs-message-conflict")
		}
		return m, nil
	}
	receipts := ""
	for kind, requested := range map[string]bool{"delivered": m.delivery, "displayed": m.display} {
		if requested {
			receipts += "INSERT INTO rcs_receipts(message_id,kind,receipt_id,requested_at,next_attempt_at) VALUES(" + sqlText(id) + "," + sqlText(kind) + "," + sqlText(opaqueID()) + "," + at + "," + at + ");"
		}
	}
	_, err = db.run("BEGIN IMMEDIATE; INSERT INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,received_at,created_at,network_message_id,cpim_message_id) VALUES("+sqlText(id)+","+sqlText(conversation)+","+sqlText("rcs:"+m.id)+",'rcs','incoming',"+sqlText(peer)+","+sqlText(string(m.body))+","+at+","+at+","+sqlText(networkID)+","+sqlText(m.id)+");"+
		"INSERT INTO message_recipients(message_id,address,role) VALUES("+sqlText(id)+","+sqlText(canonicalPeer(own))+",'to');"+receipts+" COMMIT;", false)
	return m, err
}

type rcsIMDN struct {
	XMLName  xml.Name       `xml:"urn:ietf:params:xml:ns:imdn imdn"`
	ID       string         `xml:"message-id"`
	Delivery *rcsIMDNStatus `xml:"delivery-notification"`
	Display  *rcsIMDNStatus `xml:"display-notification"`
}
type rcsIMDNStatus struct {
	Status struct {
		Delivered *struct{} `xml:"delivered"`
		Displayed *struct{} `xml:"displayed"`
		Failed    *struct{} `xml:"failed"`
		Forbidden *struct{} `xml:"forbidden"`
		Error     *struct{} `xml:"error"`
	} `xml:"status"`
}

func (db *messageDB) acceptIMDN(peer, eventID string, body []byte) error {
	var report rcsIMDN
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if len(body) > 8192 || decoder.Decode(&report) != nil || !rcsField(report.ID) || len(report.ID) > 256 {
		return errors.New("rcs-imdn-invalid")
	}
	kind := ""
	if report.Delivery != nil {
		s := report.Delivery.Status
		if s.Delivered != nil {
			kind = "delivered"
		}
		if s.Failed != nil || s.Forbidden != nil || s.Error != nil {
			if kind != "" {
				return errors.New("rcs-imdn-invalid")
			}
			kind = "failed"
		}
	}
	if report.Display != nil && report.Display.Status.Displayed != nil {
		if kind != "" {
			return errors.New("rcs-imdn-invalid")
		}
		kind = "displayed"
	}
	if kind == "" {
		return errors.New("rcs-imdn-invalid")
	}
	return db.rcsEvent(report.ID, peer, kind, eventID)
}

func (db *messageDB) rcsTransportEvent(cpimID, kind string) error {
	return db.rcsEvent(cpimID, "", kind, cpimID)
}
func (db *messageDB) rcsEvent(cpimID, peer, kind, eventID string) error {
	condition := ""
	if peer != "" {
		condition = " AND c.peer=" + sqlText(peer)
	}
	rows, err := db.run("SELECT m.id,c.peer,a.id AS attempt FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN transport_attempts a ON a.message_id=m.id WHERE m.protocol='rcs' AND m.direction='outgoing' AND m.cpim_message_id="+sqlText(cpimID)+" AND c.subscription_id="+sqlText(db.subscription)+condition+" ORDER BY a.attempt_number DESC LIMIT 1;", true)
	if err != nil || len(rows) == 0 {
		return err
	}
	row := rows[0]
	at := sqlText(timestamp())
	attempt := strconv.FormatInt(int64(row["attempt"].(float64)), 10)
	_, err = db.run("BEGIN IMMEDIATE; INSERT OR IGNORE INTO delivery_events(message_id,attempt_id,recipient,kind,remote_event_id,occurred_at) VALUES("+sqlText(row["id"].(string))+","+attempt+","+sqlText(row["peer"].(string))+","+sqlText(kind)+","+sqlText(eventID)+","+at+");"+
		"UPDATE transport_attempts SET state=CASE WHEN EXISTS(SELECT 1 FROM delivery_events WHERE message_id="+sqlText(row["id"].(string))+" AND kind='displayed') THEN 'displayed' WHEN EXISTS(SELECT 1 FROM delivery_events WHERE message_id="+sqlText(row["id"].(string))+" AND kind='delivered') THEN 'delivered' ELSE "+sqlText(kind)+" END,updated_at="+at+" WHERE id="+attempt+"; COMMIT;", false)
	return err
}

func (r *rcsService) runQueue(ctx context.Context, db *messageDB, session string, verify func(context.Context) error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.Lock()
		client := r.client
		ready := r.readyLocked()
		r.mu.Unlock()
		if !ready || client == nil {
			continue
		}
		if verify(ctx) != nil {
			return
		}
		rows, err := db.run("UPDATE transport_attempts SET state='rcs_sending',updated_at="+sqlText(timestamp())+" WHERE id=(SELECT a.id FROM transport_attempts a JOIN messages m ON m.id=a.message_id JOIN conversations c ON c.id=m.conversation_id WHERE a.protocol='rcs' AND a.state='queued' AND a.session_id="+sqlText(session)+" AND c.subscription_id="+sqlText(db.subscription)+" ORDER BY a.id LIMIT 1) RETURNING id,message_id;", true)
		if err != nil || len(rows) == 0 {
			continue
		}
		attempt := int64(rows[0]["id"].(float64))
		id := rows[0]["message_id"].(string)
		message, e := db.run("SELECT c.peer,m.body,m.cpim_message_id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id="+sqlText(id)+";", true)
		state := "not-sent"
		if e == nil && len(message) == 1 {
			send, stop := context.WithTimeout(ctx, 60*time.Second)
			if e = verify(send); e == nil {
				e = client.send(send, message[0]["peer"].(string), message[0]["cpim_message_id"].(string), message[0]["body"].(string))
			}
			stop()
			if e == nil {
				_ = db.rcsTransportEvent(message[0]["cpim_message_id"].(string), "accepted_by_service")
				_, _ = db.run("UPDATE messages SET sent_at="+sqlText(timestamp())+" WHERE id="+sqlText(id)+";", false)
				continue
			}
			// A broken media exchange can follow partial or complete submission.
			// It is never retried automatically, avoiding duplicate user messages.
			if strings.HasPrefix(e.Error(), "rcs-media-") {
				state = "submit_unknown"
			}
		}
		_ = db.rcsAttemptFailure(attempt, state)
	}
}

func (db *messageDB) rcsAttemptFailure(attempt int64, state string) error {
	_, err := db.run("UPDATE transport_attempts SET state="+sqlText(state)+",updated_at="+sqlText(timestamp())+" WHERE id="+strconv.FormatInt(attempt, 10)+" AND state NOT IN('delivered','displayed','accepted_by_service');", false)
	return err
}
