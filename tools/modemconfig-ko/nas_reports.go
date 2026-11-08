// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"errors"
	"strconv"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
)

// A status report may arrive before raw-send returns, or after a restart.
// Retain the original TPDU before ACK, separately from its eventual match.
const nasReportSchema = `CREATE TABLE IF NOT EXISTS nas_status_reports(
    id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    recipient TEXT NOT NULL, tp_reference INTEGER NOT NULL, status INTEGER NOT NULL,
    service_center_time TEXT NOT NULL, received_at TEXT NOT NULL, tpdu BLOB NOT NULL,
    processed INTEGER NOT NULL DEFAULT 0, attempt_id INTEGER REFERENCES transport_attempts(id) ON DELETE SET NULL
);`

func (db *messageDB) acceptNASReport(report imssms.StatusReport) error {
	if len(report.RawTPDU) == 0 || report.FromCommand || !validPhone(canonicalPeer(report.Recipient)) || report.ServiceCenterTime.IsZero() {
		return errors.New("invalid NAS status report")
	}
	_, err := db.run("INSERT OR IGNORE INTO nas_status_reports(id,subscription_id,recipient,tp_reference,status,service_center_time,received_at,tpdu) VALUES("+
		sqlText(stableID(db.subscription, string(report.RawTPDU)))+","+sqlText(db.subscription)+","+sqlText(canonicalPeer(report.Recipient))+","+
		strconv.Itoa(int(report.MessageReference))+","+strconv.Itoa(int(report.Status))+","+sqlText(report.ServiceCenterTime.UTC().Format(databaseTime))+","+sqlText(timestamp())+","+sqlBlob(report.RawTPDU)+");", false)
	return err
}

func (db *messageDB) recoverNASReports() error {
	reports, err := db.run("SELECT id,recipient,tp_reference,status,service_center_time,received_at FROM nas_status_reports WHERE processed=0 AND subscription_id="+sqlText(db.subscription)+" ORDER BY received_at,id;", true)
	if err != nil {
		return err
	}
	for _, report := range reports {
		at, err := time.Parse(databaseTime, report["service_center_time"].(string))
		if err != nil {
			return err
		}
		// TP-MR wraps at 256. Require SIM, recipient and the SMSC's original
		// submit time; an ambiguous reference must never mark another SMS read.
		matches, err := db.run(`SELECT s.attempt_id,s.part_number,s.part_count,s.call_id FROM sms_submission_segments s
            JOIN transport_attempts a ON a.id=s.attempt_id JOIN messages m ON m.id=a.message_id JOIN conversations c ON c.id=m.conversation_id
            WHERE s.call_id LIKE 'nas-%' AND c.subscription_id=`+sqlText(db.subscription)+" AND c.peer="+sqlText(report["recipient"].(string))+
			" AND s.tp_reference="+strconv.Itoa(int(report["tp_reference"].(float64)))+" AND a.submitted_at BETWEEN "+sqlText(at.Add(-10*time.Minute).UTC().Format(databaseTime))+" AND "+sqlText(at.Add(2*time.Minute).UTC().Format(databaseTime))+" LIMIT 2;", true)
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			continue
		}
		match := matches[0]
		status := imssms.ReportStatus(byte(report["status"].(float64)))
		state := ims.SMSDeliveryUnknown
		switch {
		case status.Delivered():
			state = ims.SMSDelivered
		case status.Completed():
			state = ims.SMSDeliveryCompleted
		case status.Retrying():
			state = ims.SMSDeliveryRetrying
		case status.Failed():
			state = ims.SMSDeliveryFailed
		}
		received, err := time.Parse(databaseTime, report["received_at"].(string))
		if err != nil {
			return err
		}
		attempt := int64(match["attempt_id"].(float64))
		if err := db.submissionUpdate(attempt, ims.SMSSubmissionUpdate{SegmentIndex: int(match["part_number"].(float64)), SegmentCount: int(match["part_count"].(float64)),
			Recipient: report["recipient"].(string), CallID: match["call_id"].(string), State: state, Status: status, HasStatus: true, ReceivedAt: received}); err != nil {
			return err
		}
		if _, err := db.run("UPDATE nas_status_reports SET processed=1,attempt_id="+strconv.FormatInt(attempt, 10)+" WHERE id="+sqlText(report["id"].(string))+";", false); err != nil {
			return err
		}
	}
	return nil
}
