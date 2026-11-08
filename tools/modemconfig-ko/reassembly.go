// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
)

type smsHeaderInfo struct{ reference, bits, part, count, destination, source int }

func (db *messageDB) acceptSegment(message imssms.Incoming) error {
	db.incomingMu.Lock()
	defer db.incomingMu.Unlock()
	if err := db.segment(message); err != nil {
		return err
	}
	return db.recoverSegments()
}

func smsHeader(header []byte) (smsHeaderInfo, error) {
	info := smsHeaderInfo{part: 1, count: 1, destination: -1, source: -1}
	if len(header) == 0 {
		return info, nil
	}
	end := int(header[0]) + 1
	if end != len(header) {
		return info, errors.New("invalid SMS header")
	}
	for offset := 1; offset < end; {
		if offset+2 > end {
			return info, errors.New("invalid SMS header")
		}
		kind, size := header[offset], int(header[offset+1])
		offset += 2
		if offset+size > end {
			return info, errors.New("invalid SMS header")
		}
		value := header[offset : offset+size]
		offset += size
		switch {
		case kind == 0 && size == 3:
			info.reference, info.bits, info.count, info.part = int(value[0]), 8, int(value[1]), int(value[2])
		case kind == 8 && size == 4:
			info.reference, info.bits, info.count, info.part = int(binary.BigEndian.Uint16(value)), 16, int(value[2]), int(value[3])
		case kind == 4 && size == 2:
			info.destination, info.source = int(value[0]), int(value[1])
		case kind == 5 && size == 4:
			info.destination, info.source = int(binary.BigEndian.Uint16(value)), int(binary.BigEndian.Uint16(value[2:]))
		}
	}
	if info.count < 1 || info.part < 1 || info.part > info.count {
		return info, errors.New("invalid SMS parts")
	}
	return info, nil
}

// Reassemble from committed segments, including across process restarts.
// WAP pushes on port 2948 are reassembled independently of plain text SMS.
func (db *messageDB) recoverSegments() error {
	rows, err := db.run("SELECT id,metadata_json,received_at FROM sms_segments WHERE subscription_id="+sqlText(db.subscription)+" AND message_id IS NULL ORDER BY received_at,id;", true)
	if err != nil {
		return err
	}
	type part struct {
		id      string
		message imssms.Incoming
		at      time.Time
		header  smsHeaderInfo
	}
	groups := map[string][]part{}
	for _, row := range rows {
		var message imssms.Incoming
		if json.Unmarshal([]byte(row["metadata_json"].(string)), &message) != nil {
			return errors.New("invalid stored SMS segment")
		}
		header, err := smsHeader(message.UserDataHeader)
		if err != nil {
			return err
		}
		if (header.destination >= 0 && header.destination != 2948) || message.ReservedMTI {
			continue
		}
		at, err := time.Parse(databaseTime, row["received_at"].(string))
		if err != nil {
			return err
		}
		key := row["id"].(string)
		if header.bits > 0 {
			key = stableID(message.From, message.To, strconv.Itoa(header.reference), strconv.Itoa(header.bits), strconv.Itoa(header.count), strconv.Itoa(int(message.DataCodingScheme)), strconv.Itoa(header.destination), strconv.Itoa(header.source))
		}
		group := groups[key]
		if len(group) > 0 && at.Sub(group[len(group)-1].at) > 10*time.Minute {
			group = nil
		}
		groups[key] = append(group, part{row["id"].(string), message, at, header})
	}
	sorted := make([][]part, 0, len(groups))
	for _, group := range groups {
		sorted = append(sorted, group)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0].at.Before(sorted[j][0].at) })
	for _, group := range sorted {
		last := group[len(group)-1]
		ordered := make([]*part, last.header.count)
		for index := range group {
			part := &group[index]
			ordered[part.header.part-1] = part
		}
		text := strings.Builder{}
		var payload []byte
		complete := true
		ids := []string{}
		for _, part := range ordered {
			if part == nil {
				complete = false
				break
			}
			text.WriteString(part.message.Text)
			if last.header.destination == 2948 {
				if !bytes.HasPrefix(part.message.UserData, part.message.UserDataHeader) {
					complete = false
					break
				}
				payload = append(payload, part.message.UserData[len(part.message.UserDataHeader):]...)
			}
			ids = append(ids, sqlText(part.id))
		}
		if !complete {
			continue
		}
		if last.header.destination == 2948 {
			if err := db.incomingMMS(payload, last.message.From, last.message.To, ids, group[0].at); err != nil {
				return err
			}
			continue
		}
		message := ims.SMS{CallID: last.message.CallID, From: last.message.From, To: last.message.To,
			FromURI: last.message.FromURI, ToURI: last.message.ToURI, ServiceCenter: last.message.ServiceCenter,
			Text: text.String(), ReceivedAt: group[0].at}
		if err := db.incoming(message); err != nil {
			return err
		}
		id := stableID(db.subscription, "incoming", stableID(message.CallID, message.From, message.To, message.Text))
		_, err := db.run("BEGIN IMMEDIATE; UPDATE sms_segments SET message_id="+sqlText(id)+" WHERE id IN("+strings.Join(ids, ",")+");"+
			"UPDATE sms_metadata SET part_count="+strconv.Itoa(last.header.count)+",protocol_identifier="+strconv.Itoa(int(last.message.ProtocolIdentifier))+",data_coding_scheme="+strconv.Itoa(int(last.message.DataCodingScheme))+",concatenation_reference="+strconv.Itoa(last.header.reference)+" WHERE message_id="+sqlText(id)+"; COMMIT;", false)
		if err != nil {
			return err
		}
	}
	return nil
}
