// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type messagingRuntime struct {
	PID          int            `json:"pid"`
	Updated      int64          `json:"updated_at"`
	Slot         uint8          `json:"slot"`
	Registration string         `json:"registration"`
	Subscription string         `json:"subscription"`
	Session      string         `json:"session"`
	PLMN         string         `json:"plmn"`
	SMSTransport string         `json:"sms_transport"`
	SMSReady     bool           `json:"sms_ready"`
	MMSReady     bool           `json:"mms_ready"`
	RCS          map[string]any `json:"rcs"`
}

func (state messagingRuntime) rcsReady() bool {
	return state.RCS != nil && state.RCS["state"] == "registered" && state.RCS["chat"] == true
}

func (state messagingRuntime) smsReady() bool {
	if state.SMSTransport == "" {
		return state.Registration == "registered"
	}
	return (state.SMSTransport == "ims" || state.SMSTransport == "nas") && state.SMSReady
}

func (state messagingRuntime) mmsReady() bool {
	if state.SMSTransport == "" {
		return state.Registration == "registered"
	}
	return (state.SMSTransport == "ims" || state.SMSTransport == "nas") && state.MMSReady
}

func currentMessagingRuntime(path string) messagingRuntime {
	var state messagingRuntime
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return state
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return messagingRuntime{}
	}
	age := time.Now().Unix() - state.Updated
	if age < 0 || age > 8 || state.PID < 1 || syscall.Kill(state.PID, 0) != nil || !hexID(state.Subscription, 32) || !hexID(state.Session, 32) {
		return messagingRuntime{}
	}
	return state
}

type messageRequest struct {
	RPCSession      string       `json:"ubus_rpc_session"`
	ID              string       `json:"id"`
	Subscription    string       `json:"subscription"`
	Session         string       `json:"session"`
	RequestID       string       `json:"request_id"`
	To              string       `json:"to"`
	Text            string       `json:"text"`
	Attachments     []string     `json:"attachments"`
	AttachmentCount int          `json:"attachment_count"`
	Filename        string       `json:"filename"`
	MimeType        string       `json:"mime_type"`
	Size            int64        `json:"size"`
	Data            string       `json:"data"`
	Before          int64        `json:"before"`
	Offset          int64        `json:"offset"`
	Through         int64        `json:"through"`
	Sequence        int64        `json:"sequence"`
	Protocol        string       `json:"protocol"`
	Audio           string       `json:"audio"`
	Device          string       `json:"device"`
	Owner           string       `json:"owner"`
	Reason          string       `json:"reason"`
	Contact         savedContact `json:"contact"`
}

func readMessageRequest(reader io.Reader) (messageRequest, error) {
	var request messageRequest
	data, err := io.ReadAll(io.LimitReader(reader, 16385))
	if err != nil || len(data) > 16384 {
		return request, errors.New("invalid-request")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return request, errors.New("invalid-request")
	}
	return request, nil
}

// rpcd supplies authentication/ACLs. This root-only entry point never logs input.
func messagesCommand(method string, reader io.Reader) int {
	request, err := readMessageRequest(reader)
	if err != nil {
		emit(map[string]any{"error": "invalid-request"})
		return 0
	}
	db, err := openMessageDB(messageDatabasePath)
	if err != nil {
		emit(map[string]any{"error": "database-unavailable"})
		return 0
	}
	result, err := db.messageMethod(method, request, currentMessagingRuntime(runtimeStatusPath))
	if err != nil {
		code := err.Error()
		switch code {
		case "invalid-contact", "contact-conflict", "invalid-message", "invalid-request", "request-conflict", "sim-unavailable", "busy", "attachment-unavailable", "invalid-attachment", "attachment-storage", "attachment-type", "attachment-dimensions", "message-too-long", "message-too-large", "unsupported-carrier":
		default:
			code = "database-unavailable"
		}
		emit(map[string]any{"error": code})
		return 0
	}
	emit(result)
	return 0
}

func (db *messageDB) messageMethod(method string, request messageRequest, state messagingRuntime) (any, error) {
	switch method {
	case "call_history":
		return db.callHistory(request.Before)
	case "snapshot":
		subscriptions, err := db.run("SELECT id,slot,provider FROM subscriptions ORDER BY updated_at DESC;", true)
		if err != nil {
			return nil, err
		}
		contacts, err := db.contacts()
		if err != nil {
			return nil, err
		}
		threads, err := db.run(`SELECT c.id,c.subscription_id,c.peer,c.kind,COALESCE(c.local_subject,c.subject,k.name,c.peer) AS title,
			m.body AS preview,m.direction,m.protocol,COALESCE(m.received_at,m.sent_at,m.created_at) AS updated_at,
            (SELECT COUNT(*) FROM messages u WHERE u.conversation_id=c.id AND u.direction='incoming' AND u.local_read_at IS NULL) AS unread,
            (SELECT a.state FROM transport_attempts a WHERE a.message_id=m.id ORDER BY a.attempt_number DESC LIMIT 1) AS state
            FROM conversations c JOIN messages m ON m.seq=(SELECT z.seq FROM messages z WHERE z.conversation_id=c.id ORDER BY z.seq DESC LIMIT 1)
            LEFT JOIN contact_addresses ca ON ca.address=c.peer LEFT JOIN contacts k ON k.id=ca.contact_id
            ORDER BY updated_at DESC,m.seq DESC LIMIT 500;`, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"subscriptions": subscriptions, "contacts": contacts, "threads": threads,
			"service": map[string]any{"registered": state.smsReady() || state.mmsReady() || state.rcsReady(), "ims_registered": state.Registration == "registered", "sms_ready": state.smsReady(), "mms_ready": state.mmsReady(), "sms_transport": state.SMSTransport, "rcs_ready": state.rcsReady(), "rcs": state.RCS, "subscription": state.Subscription, "session": state.Session, "slot": state.Slot, "policy": koreanMessagePolicy(state.PLMN)}}, nil
	case "thread":
		if !hexID(request.ID, 64) || request.Before < 0 {
			return nil, errors.New("invalid-request")
		}
		before := ""
		if request.Before > 0 {
			before = " AND m.seq<" + strconv.FormatInt(request.Before, 10)
		}
		rows, err := db.run(`SELECT m.seq,m.id,m.protocol,m.direction,m.sender,m.body,m.subject,m.canceled_at,
            COALESCE(m.received_at,m.sent_at,m.created_at) AS timestamp,m.local_read_at,
            (SELECT a.state FROM transport_attempts a WHERE a.message_id=m.id ORDER BY a.attempt_number DESC LIMIT 1) AS state,
			(SELECT COUNT(*) FROM message_parts p WHERE p.message_id=m.id AND p.mime_type NOT IN('text/plain','application/smil','application/smil+xml')) AS attachments
            FROM messages m WHERE m.conversation_id=`+sqlText(request.ID)+before+" ORDER BY m.seq DESC LIMIT 101;", true)
		if err != nil {
			return nil, err
		}
		more := len(rows) > 100
		if more {
			rows = rows[:100]
		}
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		var ids []string
		for _, message := range rows {
			ids = append(ids, sqlText(message["id"].(string)))
		}
		var parts []map[string]any
		if len(ids) > 0 {
			parts, err = db.run("SELECT id,message_id,mime_type,filename,byte_size FROM message_parts WHERE message_id IN("+strings.Join(ids, ",")+") AND mime_type NOT IN('text/plain','application/smil','application/smil+xml') ORDER BY position;", true)
			if err != nil {
				return nil, err
			}
		}
		for _, message := range rows {
			message["parts"] = []map[string]any{}
			for _, part := range parts {
				if part["message_id"] == message["id"] {
					message["parts"] = append(message["parts"].([]map[string]any), part)
				}
			}
		}
		return map[string]any{"messages": rows, "more": more}, nil
	case "read_part":
		return db.readPart(request.ID, request.Offset)
	case "measure", "begin_upload", "write_upload", "delete_upload", "send":
		if (!state.smsReady() && !state.mmsReady() && !state.rcsReady()) || request.Subscription == "" || request.Subscription != state.Subscription || request.Session != state.Session {
			return nil, errors.New("sim-unavailable")
		}
		db.subscription = state.Subscription
		if request.Protocol != "" && request.Protocol != "auto" && request.Protocol != "rcs" {
			return nil, errors.New("invalid-request")
		}
		useRCS := request.Protocol == "rcs"
		if (request.Protocol == "" || request.Protocol == "auto") && (method == "measure" || method == "send") && len(request.Attachments) == 0 && request.AttachmentCount == 0 {
			// Resolve an existing request before consulting changing peer state.
			// A retry must keep the original transport and cannot submit twice.
			var existing []map[string]any
			if method == "send" && hexID(request.RequestID, 32) {
				var err error
				existing, err = db.run("SELECT protocol FROM messages WHERE id="+sqlText(stableID(db.subscription, "outgoing", request.RequestID))+";", true)
				if err != nil {
					return nil, err
				}
			}
			if len(existing) > 0 {
				useRCS = existing[0]["protocol"] == "rcs"
			} else {
				useRCS = autoRCSPeer(state, request)
			}
		}
		if useRCS {
			if (method != "measure" && method != "send") || len(request.Attachments) != 0 || request.AttachmentCount != 0 {
				return nil, errors.New("invalid-attachment")
			}
			if method == "measure" && (!state.rcsReady() || !rcsEnabled()) {
				return nil, errors.New("sim-unavailable")
			}
			if method == "measure" {
				return map[string]any{"kind": "RCS", "valid": validRCSText(request.Text), "bytes": len(request.Text), "limit": maxRCSText}, nil
			}
			id, err := db.queueRCS(state.Session, request.RequestID, request.To, request.Text)
			return map[string]any{"id": id}, err
		}
		policy := koreanMessagePolicy(state.PLMN)
		if policy.Provider == "" {
			return nil, errors.New("unsupported-carrier")
		}
		if method == "measure" {
			return planMessage(policy, request.Text, request.AttachmentCount), nil
		}
		if method != "send" {
			if !state.mmsReady() {
				return nil, errors.New("sim-unavailable")
			}
			return db.upload(method, request, policy)
		}
		plan := planMessage(policy, request.Text, len(request.Attachments))
		if (plan.Kind == "SMS" && !state.smsReady()) || (plan.Kind != "SMS" && !state.mmsReady()) {
			return nil, errors.New("sim-unavailable")
		}
		id, err := db.queueMessage(state.Session, request.RequestID, request.To, request.Text, request.Attachments, policy)
		return map[string]any{"id": id}, err
	case "mark_read":
		if !hexID(request.ID, 64) || request.Through < 1 {
			return nil, errors.New("invalid-request")
		}
		_, err := db.run("UPDATE messages SET local_read_at="+sqlText(timestamp())+" WHERE conversation_id="+sqlText(request.ID)+" AND direction='incoming' AND local_read_at IS NULL AND seq<="+strconv.FormatInt(request.Through, 10)+";", false)
		return map[string]any{"ok": err == nil}, err
	case "delete_thread":
		if !hexID(request.ID, 64) {
			return nil, errors.New("invalid-request")
		}
		// Claiming a transfer and deleting its conversation must not race.
		rows, err := db.run("BEGIN IMMEDIATE; CREATE TEMP TABLE deleting_parts AS SELECT p.id FROM message_parts p JOIN messages m ON m.id=p.message_id WHERE m.conversation_id="+sqlText(request.ID)+";"+
			"DELETE FROM conversations WHERE id="+sqlText(request.ID)+" AND NOT EXISTS(SELECT 1 FROM transport_attempts a JOIN messages m ON m.id=a.message_id WHERE m.conversation_id="+sqlText(request.ID)+" AND a.state IN('queued','sending','submitted_to_ims','submitted_to_modem','mms_sending','rcs_sending','mms_downloading','mms_acknowledging'));"+
			"SELECT changes() AS deleted,EXISTS(SELECT 1 FROM conversations WHERE id="+sqlText(request.ID)+") AS remains,(SELECT json_group_array(id) FROM deleting_parts) AS parts; COMMIT;", true)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 || (rows[0]["deleted"] != float64(1) && rows[0]["remains"] != float64(0)) {
			return nil, errors.New("busy")
		}
		var parts []string
		if err := json.Unmarshal([]byte(rows[0]["parts"].(string)), &parts); err != nil {
			return nil, err
		}
		for _, id := range parts {
			if hexID(id, 64) {
				if err := os.Remove(filepath.Join(db.partDirectory(), id)); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
		return map[string]any{"ok": true}, nil
	case "save_contact":
		id, err := db.saveContact(request.Contact)
		return map[string]any{"id": id}, err
	case "delete_contact":
		if !hexID(request.ID, 32) {
			return nil, errors.New("invalid-contact")
		}
		_, err := db.run("DELETE FROM contacts WHERE id="+sqlText(request.ID)+";", false)
		return map[string]any{"ok": err == nil}, err
	default:
		return nil, errors.New("invalid-request")
	}
}
