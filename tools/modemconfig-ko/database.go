// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
)

//go:embed schema.sql
var messageSchema string

const messageDatabasePath = "/etc/modemconfig-ko/messages.sqlite3"

type messageDB struct {
	path, subscription string
	incomingMu         sync.Mutex
}

// SQL values travel only through stdin, never process arguments or diagnostics.
// Hex literals also preserve quotes, newlines and non-ASCII text without a shell.
func sqlText(value string) string {
	return "CAST(X'" + hex.EncodeToString([]byte(value)) + "' AS TEXT)"
}
func sqlBlob(value []byte) string { return "X'" + hex.EncodeToString(value) + "'" }

const databaseTime = "2006-01-02T15:04:05.000000000Z"

func timestamp() string { return time.Now().UTC().Format(databaseTime) }
func opaqueID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("random identifier unavailable")
	}
	return hex.EncodeToString(value[:])
}
func stableID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

func openMessageDB(path string) (*messageDB, error) {
	if err := prepareInbox(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600) {
		return nil, errors.New("message database permissions are invalid")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	db := &messageDB{path: path}
	rows, err := db.run("PRAGMA user_version;", true)
	if err != nil || len(rows) != 1 {
		return nil, errors.New("message database version unavailable")
	}
	if rows[0]["user_version"] == float64(0) {
		if _, err := db.run(messageSchema, false); err != nil {
			return nil, err
		}
	} else if rows[0]["user_version"] == float64(1) {
		if _, err := db.run("BEGIN IMMEDIATE; ALTER TABLE transport_attempts ADD COLUMN request_digest TEXT;"+uploadSchema+"PRAGMA user_version=2; COMMIT;", false); err != nil {
			return nil, err
		}
	} else if rows[0]["user_version"] != float64(2) && rows[0]["user_version"] != float64(3) && rows[0]["user_version"] != float64(4) && rows[0]["user_version"] != float64(5) {
		return nil, errors.New("unsupported message database version")
	}
	if rows[0]["user_version"] == float64(1) || rows[0]["user_version"] == float64(2) {
		if _, err := db.run("BEGIN IMMEDIATE;"+nasReportSchema+"PRAGMA user_version=3; COMMIT;", false); err != nil {
			return nil, err
		}
	}
	if version := rows[0]["user_version"].(float64); version >= 1 && version <= 3 {
		if _, err := db.run("BEGIN IMMEDIATE;"+rcsReceiptSchema+"PRAGMA user_version=4; COMMIT;", false); err != nil {
			return nil, err
		}
	}
	if version := rows[0]["user_version"].(float64); version >= 1 && version <= 4 {
		if _, err := db.run("BEGIN IMMEDIATE;"+callHistorySchema+"PRAGMA user_version=5; COMMIT;", false); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func (db *messageDB) run(sql string, rows bool) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-batch", "-bail", "-json", db.path)
	cmd.Stdin = strings.NewReader(".timeout 5000\nPRAGMA foreign_keys=ON;\nPRAGMA synchronous=FULL;\n" + sql)
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("message database operation failed")
	}
	if !rows || len(strings.TrimSpace(string(output))) == 0 {
		return []map[string]any{}, nil
	}
	var result []map[string]any
	if json.Unmarshal(output, &result) != nil {
		return nil, errors.New("invalid message database response")
	}
	return result, nil
}

func (db *messageDB) bind(scope string, slot uint8, plmn string) error {
	provider := map[string]string{"45005": "SKT", "45006": "LG U+", "45008": "KT"}[plmn]
	if provider == "" {
		provider = "SIM"
	}
	rows, err := db.run("INSERT INTO subscriptions(id,fingerprint,slot,provider,updated_at) VALUES("+
		sqlText(opaqueID())+","+sqlText(scope)+","+strconv.Itoa(int(slot))+","+sqlText(provider)+","+sqlText(timestamp())+
		") ON CONFLICT(fingerprint) DO UPDATE SET slot=excluded.slot,provider=excluded.provider,updated_at=excluded.updated_at;"+
		"SELECT id FROM subscriptions WHERE fingerprint="+sqlText(scope)+";", true)
	if err != nil || len(rows) != 1 {
		return errors.New("SIM database binding failed")
	}
	db.subscription, _ = rows[0]["id"].(string)
	return nil
}

func canonicalPeer(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "tel:") {
		value = strings.TrimPrefix(value, "tel:")
	}
	if strings.HasPrefix(value, "sip:") {
		value = strings.TrimPrefix(value, "sip:")
		if at := strings.IndexByte(value, '@'); at >= 0 {
			value = value[:at]
		}
	}
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	clean := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(value)
	if strings.HasPrefix(clean, "0") && len(clean) >= 9 && len(clean) <= 11 && validPhone(clean) {
		return "+82" + clean[1:]
	}
	if strings.HasPrefix(clean, "+820") {
		clean = "+82" + clean[4:]
	}
	if validPhone(clean) {
		return clean
	}
	return value
}
func validPhone(value string) bool {
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	if len(value) < 3 || len(value) > 15 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (db *messageDB) conversation(peer string) (string, error) {
	id := stableID(db.subscription, "direct", peer)
	_, err := db.run("INSERT OR IGNORE INTO conversations(id,subscription_id,kind,peer,created_at) VALUES("+
		sqlText(id)+","+sqlText(db.subscription)+",'direct',"+sqlText(peer)+","+sqlText(timestamp())+");", false)
	return id, err
}

func (db *messageDB) incoming(message ims.SMS) error {
	peer := canonicalPeer(message.From)
	conversation, err := db.conversation(peer)
	if err != nil {
		return err
	}
	key := stableID(message.CallID, message.From, message.To, message.Text)
	id := stableID(db.subscription, "incoming", key)
	at := message.ReceivedAt.UTC().Format(databaseTime)
	_, err = db.run("BEGIN IMMEDIATE; INSERT OR IGNORE INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,received_at,created_at,network_message_id) VALUES("+
		sqlText(id)+","+sqlText(conversation)+","+sqlText(key)+",'sms','incoming',"+sqlText(peer)+","+sqlText(message.Text)+","+sqlText(at)+","+sqlText(at)+","+sqlText(message.CallID)+");"+
		"INSERT OR IGNORE INTO sms_metadata(message_id,smsc) VALUES("+sqlText(id)+","+sqlText(message.ServiceCenter)+");"+
		"INSERT OR IGNORE INTO message_recipients(message_id,address,role) VALUES("+sqlText(id)+","+sqlText(canonicalPeer(message.To))+",'to');"+
		"UPDATE sms_segments SET message_id="+sqlText(id)+" WHERE subscription_id="+sqlText(db.subscription)+" AND call_id="+sqlText(message.CallID)+"; COMMIT;", false)
	return err
}

func (db *messageDB) segment(message imssms.Incoming) error {
	metadata, err := smsHeader(message.UserDataHeader)
	if err != nil {
		return err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	id := stableID(db.subscription, message.CallID, hex.EncodeToString(message.RawTPDU))
	_, err = db.run("INSERT OR IGNORE INTO sms_segments(id,subscription_id,call_id,sender,recipient,concat_reference,concat_bits,part_number,part_count,destination_port,source_port,tpdu,metadata_json,received_at) VALUES("+
		sqlText(id)+","+sqlText(db.subscription)+","+sqlText(message.CallID)+","+sqlText(message.From)+","+sqlText(message.To)+","+strconv.Itoa(metadata.reference)+","+strconv.Itoa(metadata.bits)+","+strconv.Itoa(metadata.part)+","+strconv.Itoa(metadata.count)+","+strconv.Itoa(metadata.destination)+","+strconv.Itoa(metadata.source)+","+sqlBlob(message.RawTPDU)+","+sqlText(string(data))+","+sqlText(timestamp())+");", false)
	return err
}

// An unscoped legacy inbox is imported only when its recipient matches this SIM.
func (db *messageDB) importInbox(directory string, aliases []string, scoped bool) error {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type saved struct {
		path string
		sms  ims.SMS
	}
	var messages []saved
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var message ims.SMS
		if len(data) > 1<<20 || json.Unmarshal(data, &message) != nil {
			return errors.New("invalid saved SMS")
		}
		matched := scoped
		for _, alias := range aliases {
			matched = matched || canonicalPeer(message.To) == canonicalPeer(alias)
		}
		if matched {
			messages = append(messages, saved{path, message})
		}
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].sms.ReceivedAt.Before(messages[j].sms.ReceivedAt) })
	for _, message := range messages {
		if err := db.incoming(message.sms); err != nil {
			return err
		}
		key := stableID(message.sms.CallID, message.sms.From, message.sms.To, message.sms.Text)
		rows, err := db.run("SELECT body FROM messages WHERE id="+sqlText(stableID(db.subscription, "incoming", key))+";", true)
		if err != nil || len(rows) != 1 || rows[0]["body"] != message.sms.Text {
			return errors.New("saved SMS verification failed")
		}
		// Read the committed value before removing only this imported source.
		if err := os.Remove(message.path); err != nil {
			return err
		}
	}
	return nil
}

func (db *messageDB) importSegments(directory string) error {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var message imssms.Incoming
		if len(data) > 1<<20 || json.Unmarshal(data, &message) != nil {
			return errors.New("invalid saved SMS segment")
		}
		if err := db.segment(message); err != nil {
			return err
		}
		id := stableID(db.subscription, message.CallID, hex.EncodeToString(message.RawTPDU))
		rows, err := db.run("SELECT hex(tpdu) AS data FROM sms_segments WHERE id="+sqlText(id)+";", true)
		if err != nil || len(rows) != 1 || !strings.EqualFold(rows[0]["data"].(string), hex.EncodeToString(message.RawTPDU)) {
			return errors.New("saved segment verification failed")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

type contactAddress struct {
	Address string `json:"address"`
	Label   string `json:"label"`
}
type savedContact struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Addresses []contactAddress `json:"addresses"`
}

func (db *messageDB) saveContact(contact savedContact) (string, error) {
	contact.Name = strings.TrimSpace(contact.Name)
	if contact.Name == "" || utf8.RuneCountInString(contact.Name) > 80 || strings.ContainsAny(contact.Name, "\x00\r\n") || len(contact.Addresses) < 1 || len(contact.Addresses) > 10 {
		return "", errors.New("invalid-contact")
	}
	if contact.ID == "" {
		contact.ID = opaqueID()
	}
	if !hexID(contact.ID, 32) {
		return "", errors.New("invalid-contact")
	}
	seen := map[string]bool{}
	query := "BEGIN IMMEDIATE; INSERT INTO contacts(id,name,updated_at) VALUES(" + sqlText(contact.ID) + "," + sqlText(contact.Name) + "," + sqlText(timestamp()) + ") ON CONFLICT(id) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at; DELETE FROM contact_addresses WHERE contact_id=" + sqlText(contact.ID) + ";"
	for _, address := range contact.Addresses {
		address.Address = canonicalPeer(address.Address)
		if !validPhone(address.Address) || seen[address.Address] || utf8.RuneCountInString(address.Label) > 24 || strings.ContainsAny(address.Label, "\x00\r\n") {
			return "", errors.New("invalid-contact")
		}
		seen[address.Address] = true
		query += "INSERT INTO contact_addresses(contact_id,address,label) VALUES(" + sqlText(contact.ID) + "," + sqlText(address.Address) + "," + sqlText(address.Label) + ");"
	}
	_, err := db.run(query+"COMMIT;", false)
	if err != nil {
		return "", errors.New("contact-conflict")
	}
	return contact.ID, nil
}

func (db *messageDB) contacts() ([]savedContact, error) {
	rows, err := db.run("SELECT c.id,c.name,a.address,a.label FROM contacts c JOIN contact_addresses a ON a.contact_id=c.id ORDER BY c.name,c.id,a.rowid;", true)
	result := []savedContact{}
	indexes := map[string]int{}
	for _, row := range rows {
		id := row["id"].(string)
		index, ok := indexes[id]
		if !ok {
			index = len(result)
			indexes[id] = index
			result = append(result, savedContact{ID: id, Name: row["name"].(string), Addresses: []contactAddress{}})
		}
		result[index].Addresses = append(result[index].Addresses, contactAddress{Address: row["address"].(string), Label: row["label"].(string)})
	}
	return result, err
}

func (db *messageDB) importContacts(path string) error {
	rows, err := db.run("SELECT source FROM migrations WHERE source='legacy-phonebook';", true)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ";")
		if len(parts) < 2 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		number := canonicalPeer(parts[1])
		if !validPhone(number) || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		rows, err := db.run("SELECT address FROM contact_addresses WHERE address="+sqlText(number)+";", true)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			continue
		}
		if _, err := db.saveContact(savedContact{ID: stableID("legacy-contact", number)[:32], Name: parts[0], Addresses: []contactAddress{{Address: number}}}); err != nil {
			return err
		}
	}
	_, err = db.run("INSERT OR IGNORE INTO migrations(source,completed_at) VALUES('legacy-phonebook',"+sqlText(timestamp())+");", false)
	return err
}

func hexID(value string, size int) bool {
	if len(value) != size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func (db *messageDB) queueSMS(session, requestID, to, text string) (string, error) {
	to = canonicalPeer(to)
	if !hexID(requestID, 32) || !validPhone(to) || strings.TrimSpace(text) == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(utf16.Encode([]rune(text))) > 670 {
		return "", errors.New("invalid-message")
	}
	conversation, err := db.conversation(to)
	if err != nil {
		return "", err
	}
	id := stableID(db.subscription, "outgoing", requestID)
	at := sqlText(timestamp())
	_, err = db.run("BEGIN IMMEDIATE; INSERT OR IGNORE INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,created_at,local_read_at) VALUES("+
		sqlText(id)+","+sqlText(conversation)+","+sqlText(requestID)+",'sms','outgoing','self',"+sqlText(text)+","+at+","+at+");"+
		"INSERT OR IGNORE INTO transport_attempts(message_id,protocol,request_id,session_id,state,created_at,updated_at) VALUES("+sqlText(id)+",'sms',"+sqlText(requestID)+","+sqlText(session)+",'queued',"+at+","+at+");"+
		"INSERT OR IGNORE INTO message_recipients(message_id,address,role) SELECT id,"+sqlText(to)+",'to' FROM messages WHERE id="+sqlText(id)+" AND conversation_id="+sqlText(conversation)+"; COMMIT;", false)
	if err != nil {
		return "", err
	}
	rows, err := db.run("SELECT m.body,c.peer FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id="+sqlText(id)+";", true)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0]["body"] != text || rows[0]["peer"] != to {
		return "", errors.New("request-conflict")
	}
	return id, nil
}

func (db *messageDB) recoverPending(session string) error {
	_, err := db.run("UPDATE transport_attempts SET state=CASE WHEN state='queued' THEN 'not-sent' ELSE 'submit_unknown' END,updated_at="+sqlText(timestamp())+
		" WHERE message_id IN(SELECT m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE c.subscription_id="+sqlText(db.subscription)+") AND session_id!="+sqlText(session)+" AND state IN('queued','sending','mms_sending','rcs_sending','submitted-to-ims','submitted_to_ims','submitted_to_modem');", false)
	return err
}

func (db *messageDB) claimSMS(session string) (map[string]any, error) {
	rows, err := db.run("UPDATE transport_attempts SET state='sending',updated_at="+sqlText(timestamp())+" WHERE id=(SELECT a.id FROM transport_attempts a JOIN messages m ON m.id=a.message_id JOIN conversations c ON c.id=m.conversation_id WHERE a.protocol='sms' AND a.state='queued' AND a.session_id="+sqlText(session)+" AND c.subscription_id="+sqlText(db.subscription)+" ORDER BY a.id LIMIT 1) RETURNING id,message_id;", true)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	message, err := db.run("SELECT c.peer,m.body,m.id FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id="+sqlText(rows[0]["message_id"].(string))+";", true)
	if err != nil || len(message) != 1 {
		return nil, errors.New("queued message unavailable")
	}
	message[0]["attempt"] = rows[0]["id"]
	return message[0], nil
}

func (db *messageDB) attemptState(attempt int64, state string) error {
	_, err := db.run("UPDATE transport_attempts SET state="+sqlText(state)+",updated_at="+sqlText(timestamp())+" WHERE id="+strconv.FormatInt(attempt, 10)+";", false)
	return err
}
