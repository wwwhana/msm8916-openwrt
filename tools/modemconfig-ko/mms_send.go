// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func wspUintvar(value int) []byte {
	data := []byte{byte(value & 127)}
	for value >>= 7; value > 0; value >>= 7 {
		data = append([]byte{byte(value&127) | 128}, data...)
	}
	return data
}
func wspLength(value []byte) []byte {
	if len(value) <= 30 {
		return append([]byte{byte(len(value))}, value...)
	}
	return append(append([]byte{31}, wspUintvar(len(value))...), value...)
}
func wspText(text string) []byte {
	data := []byte(text)
	if len(data) > 0 && data[0] >= 128 {
		data = append([]byte{127}, data...)
	}
	return append(data, 0)
}

func composeMMS(transaction, to, text string, media []mmsPart) ([]byte, []mmsPart) {
	var content []mmsPart
	if text != "" {
		data, charset := mmsTextBytes(text)
		content = append(content, mmsPart{media: "text/plain", name: "text.txt", location: "text.txt", contentID: "<text>", charset: charset, text: text, data: data})
	}
	for index, part := range media {
		ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "audio/amr": "amr", "audio/mpeg": "mp3", "video/3gpp": "3gp", "video/mp4": "mp4"}[part.media]
		part.location = fmt.Sprintf("media-%d.%s", index+1, ext)
		part.contentID = fmt.Sprintf("<media-%d>", index+1)
		content = append(content, part)
	}
	smil := `<smil><head><layout><root-layout width="320" height="480"/><region id="Image" left="0" top="0" width="320" height="360" fit="meet"/><region id="Text" left="0" top="360" width="320" height="120"/></layout></head><body>`
	if len(media) == 0 {
		smil += `<par dur="8000ms"><text src="text.txt" region="Text"/></par>`
	}
	for _, part := range content {
		if part.media == "text/plain" {
			continue
		}
		smil += `<par dur="8000ms">`
		if text != "" {
			smil += `<text src="text.txt" region="Text"/>`
		}
		switch {
		case strings.HasPrefix(part.media, "image/"):
			smil += `<img src="` + part.location + `" region="Image"/>`
		case strings.HasPrefix(part.media, "audio/"):
			smil += `<audio src="` + part.location + `"/>`
		default:
			smil += `<video src="` + part.location + `" region="Image"/>`
		}
		smil += `</par>`
	}
	smil += `</body></smil>`
	parts := append([]mmsPart{{media: "application/smil", name: "smil.xml", location: "smil.xml", contentID: "<smil>", charset: 106, data: []byte(smil)}}, content...)
	data := append([]byte{0x8c, 0x80, 0x98}, wspText(transaction)...)
	data = append(data, 0x8d, 0x93, 0x89, 1, 0x81, 0x97)
	data = append(data, wspText(to+"/TYPE=PLMN")...)
	// Sender is inserted by the MMSC. No read or delivery reports are requested,
	// matching the Korean Android carrier profiles.
	data = append(data, 0x8a, 0x80, 0x8f, 0x81, 0x86, 0x81, 0x90, 0x81, 0x84)
	contentType := append([]byte{0xb3, 0x89}, wspText("application/smil")...)
	contentType = append(contentType, 0x8a)
	contentType = append(contentType, wspText("<smil>")...)
	data = append(data, wspLength(contentType)...)
	data = append(data, wspUintvar(len(parts))...)
	for _, part := range parts {
		typeValue := wspText(part.media)
		if part.charset != 0 {
			typeValue = append(typeValue, 0x81, byte(part.charset)|128)
		}
		typeValue = append(typeValue, 0x85)
		typeValue = append(typeValue, wspText(part.name)...)
		headers := wspLength(typeValue)
		headers = append(headers, 0xc0, '"')
		headers = append(headers, wspText(part.contentID)...)
		headers = append(headers, 0x8e)
		headers = append(headers, wspText(part.location)...)
		data = append(data, wspUintvar(len(headers))...)
		data = append(data, wspUintvar(len(part.data))...)
		data = append(data, headers...)
		data = append(data, part.data...)
	}
	return data, parts
}

func (db *messageDB) queueMessage(session, requestID, to, text string, uploads []string, policy messagePolicy) (string, error) {
	to = canonicalPeer(to)
	plan := planMessage(policy, text, len(uploads))
	if !hexID(requestID, 32) || !validPhone(to) || !plan.Valid {
		if plan.Error != "" {
			return "", errors.New(plan.Error)
		}
		return "", errors.New("invalid-message")
	}
	seen := map[string]bool{}
	for _, id := range uploads {
		if !hexID(id, 64) || seen[id] {
			return "", errors.New("invalid-attachment")
		}
		seen[id] = true
	}
	id := stableID(db.subscription, "outgoing", requestID)
	digest := stableID(to, text, strings.Join(uploads, ","))
	err := db.withUploadLock(func() error {
		existing, err := db.run("SELECT m.body,c.peer,a.request_digest FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN transport_attempts a ON a.message_id=m.id WHERE m.id="+sqlText(id)+";", true)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			row := existing[0]
			if row["body"] != text || row["peer"] != to || (row["request_digest"] != digest && (row["request_digest"] != nil || len(uploads) > 0)) {
				return errors.New("request-conflict")
			}
			return nil
		}
		if plan.Kind == "SMS" {
			_, err := db.queueSMS(session, requestID, to, text)
			if err != nil {
				return err
			}
			_, err = db.run("UPDATE transport_attempts SET request_digest="+sqlText(digest)+" WHERE message_id="+sqlText(id)+";", false)
			return err
		}
		var media []mmsPart
		for _, upload := range uploads {
			rows, err := db.run("SELECT filename,mime_type,byte_size,sha256 FROM message_uploads WHERE id="+sqlText(upload)+" AND subscription_id="+sqlText(db.subscription)+" AND session_id="+sqlText(session)+" AND received_bytes=byte_size AND sha256 IS NOT NULL;", true)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return errors.New("attachment-unavailable")
			}
			path := filepath.Join(db.uploadDirectory(), upload)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > int64(policy.MessageBytes) || float64(info.Size()) != rows[0]["byte_size"] {
				return errors.New("attachment-unavailable")
			}
			data, err := os.ReadFile(path)
			if err != nil || rawDigest(data) != rows[0]["sha256"] {
				return errors.New("attachment-unavailable")
			}
			kind, err := outgoingMedia(data, policy)
			if err != nil {
				return err
			}
			media = append(media, mmsPart{media: kind, name: rows[0]["filename"].(string), data: data})
		}
		transaction := opaqueID()
		data, parts := composeMMS(transaction, to, text, media)
		if len(data) > policy.MessageBytes {
			return errors.New("message-too-large")
		}
		conversation, err := db.conversation(to)
		if err != nil {
			return err
		}
		sql, created, _, err := db.storeMMSParts(id, parts)
		stored := false
		defer func() {
			if !stored {
				for _, path := range created {
					_ = os.Remove(path)
				}
			}
		}()
		if err != nil {
			return err
		}
		at := sqlText(timestamp())
		head := "BEGIN IMMEDIATE; INSERT INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,created_at,local_read_at) VALUES(" + sqlText(id) + "," + sqlText(conversation) + "," + sqlText(requestID) + ",'mms','outgoing','self'," + sqlText(text) + "," + at + "," + at + ");"
		head += "INSERT INTO transport_attempts(message_id,protocol,request_id,request_digest,session_id,state,created_at,updated_at) VALUES(" + sqlText(id) + ",'mms'," + sqlText(requestID) + "," + sqlText(digest) + "," + sqlText(session) + ",'queued'," + at + "," + at + ");"
		head += "INSERT INTO mms_metadata(message_id,transaction_id,mms_version,message_type,message_size,delivery_report_requested,read_report_requested) VALUES(" + sqlText(id) + "," + sqlText(transaction) + ",'1.3','m-send-req'," + strconv.Itoa(len(data)) + ",0,0);"
		head += "INSERT INTO message_recipients(message_id,address,role) VALUES(" + sqlText(id) + "," + sqlText(to) + ",'to');"
		if _, err := db.run(head+sql+db.mmsPDUSQL(id, transaction, "m-send-req", "outgoing", data)+"COMMIT;", false); err != nil {
			return err
		}
		stored = true
		for _, upload := range uploads {
			_ = os.Remove(filepath.Join(db.uploadDirectory(), upload))
			_, _ = db.run("DELETE FROM message_uploads WHERE id="+sqlText(upload)+";", false)
		}
		return nil
	})
	return id, err
}

func (db *messageDB) sendMMS(ctx context.Context, session string, carrier mmsCarrier, client *http.Client, verify func(context.Context) error) error {
	if err := verify(ctx); err != nil {
		return err
	}
	rows, err := db.run(`UPDATE transport_attempts SET state='mms_sending',updated_at=`+sqlText(timestamp())+` WHERE id=(
 SELECT a.id FROM transport_attempts a JOIN messages m ON m.id=a.message_id JOIN conversations c ON c.id=m.conversation_id
 WHERE a.protocol='mms' AND m.direction='outgoing' AND a.state='queued' AND a.session_id=`+sqlText(session)+` AND c.subscription_id=`+sqlText(db.subscription)+` ORDER BY a.id LIMIT 1) RETURNING id,message_id;`, true)
	if err != nil || len(rows) == 0 {
		return err
	}
	attempt, message := int64(rows[0]["id"].(float64)), rows[0]["message_id"].(string)
	rows, err = db.run("SELECT transaction_id,hex(payload) AS data FROM mms_pdus WHERE message_id="+sqlText(message)+" AND message_type='m-send-req' AND direction='outgoing';", true)
	if err != nil || len(rows) != 1 {
		_ = db.attemptState(attempt, "not-sent")
		return errors.New("MMS submission unavailable")
	}
	data, err := hex.DecodeString(rows[0]["data"].(string))
	if err != nil {
		_ = db.attemptState(attempt, "not-sent")
		return errors.New("MMS submission invalid")
	}
	if err := verify(ctx); err != nil {
		_ = db.attemptState(attempt, "not-sent")
		return err
	}
	response, networkErr := mmsRequest(ctx, client, http.MethodPost, carrier.mmsc, data)
	if networkErr != nil {
		return db.attemptState(attempt, "submit_unknown")
	}
	// A response is durable before interpretation. An ambiguous result is never
	// resent automatically: the MMSC may already have accepted the message.
	if _, err := db.run(db.mmsPDUSQL(message, rows[0]["transaction_id"].(string), "m-send-conf", "incoming", response), false); err != nil {
		return err
	}
	pdu, err := decodeMMS(response, time.Now())
	if err != nil || pdu.kind != 0x81 || pdu.transaction != rows[0]["transaction_id"] || pdu.responseStatus == 0 {
		return db.attemptState(attempt, "submit_unknown")
	}
	state := "delivery_failed"
	if pdu.responseStatus == 0x80 {
		if pdu.messageID == "" {
			return db.attemptState(attempt, "submit_unknown")
		}
		state = "accepted_by_mmsc"
	}
	at := sqlText(timestamp())
	code := "NULL"
	if state == "delivery_failed" {
		code = strconv.Itoa(int(pdu.responseStatus))
	}
	_, err = db.run("BEGIN IMMEDIATE; UPDATE transport_attempts SET state="+sqlText(state)+",submitted_at="+at+",updated_at="+at+",failure_code="+code+" WHERE id="+strconv.FormatInt(attempt, 10)+";"+
		"UPDATE mms_metadata SET mms_message_id="+sqlText(pdu.messageID)+",response_status="+strconv.Itoa(int(pdu.responseStatus))+" WHERE message_id="+sqlText(message)+";"+
		"UPDATE messages SET sent_at="+at+",network_message_id="+sqlText(pdu.messageID)+" WHERE id="+sqlText(message)+"; COMMIT;", false)
	return err
}
