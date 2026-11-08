// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func rawDigest(data []byte) string { return stableID(string(data)) }

func (db *messageDB) incomingMMS(push []byte, from, to string, segmentIDs []string, at time.Time) error {
	data, err := decodeMMSPush(push)
	if err != nil {
		// The original segments are already durable; malformed WAP data must
		// not stop unrelated SMS reception or trigger an RP memory-full error.
		return nil
	}
	pdu, err := decodeMMS(data, at)
	if err != nil || pdu.kind != 0x82 || pdu.location == "" || pdu.transaction == "" {
		return nil
	}
	peer := mmsAddress(pdu.from)
	if peer == "" {
		peer = canonicalPeer(from)
	}
	conversation, err := db.conversation(peer)
	if err != nil {
		return err
	}
	key := stableID("mms-notification", pdu.location)
	id := stableID(db.subscription, "incoming", key)
	timeText := at.UTC().Format(databaseTime)
	expires := ""
	if !pdu.expires.IsZero() {
		expires = pdu.expires.UTC().Format(databaseTime)
	}
	sql := "BEGIN IMMEDIATE; INSERT OR IGNORE INTO messages(id,conversation_id,source_key,protocol,direction,sender,subject,received_at,created_at) VALUES(" +
		sqlText(id) + "," + sqlText(conversation) + "," + sqlText(key) + ",'mms','incoming'," + sqlText(peer) + "," + sqlText(pdu.subject) + "," + sqlText(timeText) + "," + sqlText(timeText) + ");"
	sql += "INSERT OR IGNORE INTO mms_metadata(message_id,transaction_id,mms_version,message_type,content_location,expires_at,message_size) VALUES(" +
		sqlText(id) + "," + sqlText(pdu.transaction) + "," + sqlText(mmsVersion(pdu.version)) + ",'m-notification-ind'," + sqlText(pdu.location) + "," + sqlText(expires) + "," + strconv.FormatUint(pdu.size, 10) + ");"
	sql += "INSERT OR IGNORE INTO transport_attempts(message_id,protocol,state,created_at,updated_at) VALUES(" + sqlText(id) + ",'mms','mms_waiting'," + sqlText(timeText) + "," + sqlText(timeText) + ");"
	sql += "INSERT OR IGNORE INTO message_recipients(message_id,address,role) VALUES(" + sqlText(id) + "," + sqlText(canonicalPeer(to)) + ",'to');"
	sql += db.mmsPDUSQL(id, pdu.transaction, "m-notification-ind", "incoming", data)
	if len(segmentIDs) > 0 {
		sql += "UPDATE sms_segments SET message_id=" + sqlText(id) + " WHERE subscription_id=" + sqlText(db.subscription) + " AND id IN(" + strings.Join(segmentIDs, ",") + ");"
	}
	_, err = db.run(sql+"COMMIT;", false)
	return err
}

func (db *messageDB) mmsPDUSQL(message, transaction, kind, direction string, data []byte) string {
	return "INSERT OR IGNORE INTO mms_pdus(id,subscription_id,message_id,transaction_id,message_type,direction,payload,byte_size,sha256,occurred_at) VALUES(" +
		sqlText(stableID(db.subscription, message, kind, hex.EncodeToString(data))) + "," + sqlText(db.subscription) + "," + sqlText(message) + "," + sqlText(transaction) + "," + sqlText(kind) + "," + sqlText(direction) + "," + sqlBlob(data) + "," + strconv.Itoa(len(data)) + "," + sqlText(rawDigest(data)) + "," + sqlText(timestamp()) + ");"
}

func (db *messageDB) partDirectory() string { return filepath.Join(filepath.Dir(db.path), "parts") }

func (db *messageDB) saveMMS(message string, attempt int64, data []byte, pdu mmsPDU) error {
	if pdu.kind != 0x84 || (pdu.retrieveStatus != 0 && pdu.retrieveStatus != 128) || len(pdu.parts) == 0 {
		return errors.New("invalid MMS retrieval")
	}
	sql, created, body, err := db.storeMMSParts(message, pdu.parts)
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
	sql = "BEGIN IMMEDIATE;" + sql
	sql += db.mmsPDUSQL(message, pdu.transaction, "m-retrieve-conf", "incoming", data)
	sql += "UPDATE messages SET body=" + sqlText(body) + ",subject=CASE WHEN " + sqlText(pdu.subject) + "!='' THEN " + sqlText(pdu.subject) + " ELSE subject END,network_message_id=" + sqlText(pdu.messageID) + " WHERE id=" + sqlText(message) + ";"
	sql += "UPDATE mms_metadata SET mms_message_id=" + sqlText(pdu.messageID) + ",message_type='m-retrieve-conf',retrieve_status=" + strconv.Itoa(int(pdu.retrieveStatus)) + " WHERE message_id=" + sqlText(message) + ";"
	sql += "UPDATE transport_attempts SET state='mms_ack_pending',updated_at=" + sqlText(timestamp()) + ",failure_reason=NULL WHERE id=" + strconv.FormatInt(attempt, 10) + "; COMMIT;"
	if _, err := db.run(sql, false); err != nil {
		return err
	}
	stored = true
	return nil
}

func (db *messageDB) storeMMSParts(message string, parts []mmsPart) (string, []string, string, error) {
	if err := prepareInbox(db.partDirectory()); err != nil {
		return "", nil, "", err
	}
	sql := ""
	var body []string
	var created []string
	for index, part := range parts {
		id := stableID(message, strconv.Itoa(index), rawDigest(part.data))
		path := filepath.Join(db.partDirectory(), id)
		// A private deterministic filename never incorporates a remote name.
		if info, err := os.Lstat(path); os.IsNotExist(err) {
			if err := writeMMSPart(path, part.data); err != nil {
				return "", created, "", err
			}
			created = append(created, path)
		} else if err != nil {
			return "", created, "", err
		} else {
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != int64(len(part.data)) {
				return "", created, "", errors.New("invalid MMS part file")
			}
			existing, err := os.ReadFile(path)
			if err != nil || rawDigest(existing) != rawDigest(part.data) {
				return "", created, "", errors.New("MMS part verification failed")
			}
		}
		if part.media == "text/plain" && part.text != "" {
			body = append(body, part.text)
		}
		name := part.name
		if name == "" {
			name = part.location
		}
		name = filepath.Base(strings.ReplaceAll(strings.ReplaceAll(name, "\\", "/"), "\x00", ""))
		if name == "." || name == "/" || name == "" {
			name = "attachment-" + strconv.Itoa(index+1)
		}
		sql += "INSERT OR IGNORE INTO message_parts(id,message_id,position,mime_type,charset,filename,content_id,content_location,disposition,text_content,local_path,byte_size,sha256,transfer_state,transferred_bytes) VALUES(" +
			sqlText(id) + "," + sqlText(message) + "," + strconv.Itoa(index) + "," + sqlText(part.media) + "," + sqlText(strconv.FormatUint(part.charset, 10)) + "," + sqlText(name) + "," + sqlText(part.contentID) + "," + sqlText(part.location) + "," + sqlText(part.disposition) + "," + sqlText(part.text) + "," + sqlText(path) + "," + strconv.Itoa(len(part.data)) + "," + sqlText(rawDigest(part.data)) + ",'complete'," + strconv.Itoa(len(part.data)) + ");"
	}
	return sql, created, strings.Join(body, "\n"), nil
}

func writeMMSPart(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".part-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = file.Close()
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (db *messageDB) readPart(id string, offset int64) (any, error) {
	if !hexID(id, 64) || offset < 0 || offset > maxMMSBytes {
		return nil, errors.New("invalid-request")
	}
	rows, err := db.run("SELECT id,filename,mime_type,byte_size FROM message_parts WHERE id="+sqlText(id)+" AND transfer_state='complete';", true)
	if err != nil || len(rows) != 1 {
		return nil, errors.New("attachment-unavailable")
	}
	// The database and remote attachment names cannot select arbitrary files.
	path := filepath.Join(db.partDirectory(), id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maxMMSBytes || float64(info.Size()) != rows[0]["byte_size"] || offset > info.Size() {
		return nil, errors.New("attachment-unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("attachment-unavailable")
	}
	defer file.Close()
	data := make([]byte, min(int64(24<<10), info.Size()-offset))
	_, err = file.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		return nil, errors.New("attachment-unavailable")
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(data), "offset": offset, "size": info.Size(), "mime_type": rows[0]["mime_type"], "filename": rows[0]["filename"]}, nil
}
