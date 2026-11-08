// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	ims "github.com/damonto/ims-go"
)

func (db *messageDB) submission(attempt int64, submission ims.SMSSubmission, transport ...string) error {
	state := "submitted_to_ims"
	if len(transport) > 0 && transport[0] == "nas" {
		state = "submitted_to_modem"
	}
	number := strconv.FormatInt(attempt, 10)
	at := sqlText(submission.SubmittedAt.UTC().Format(databaseTime))
	query := "BEGIN IMMEDIATE; UPDATE transport_attempts SET state=" + sqlText(state) + ",submitted_at=" + at + ",updated_at=" + at + " WHERE id=" + number + ";"
	query += "UPDATE messages SET sent_at=" + at + " WHERE id=(SELECT message_id FROM transport_attempts WHERE id=" + number + ");"
	query += "INSERT OR REPLACE INTO sms_metadata(message_id,part_count,status_report_requested) SELECT message_id," + strconv.Itoa(submission.SegmentCount) + ",1 FROM transport_attempts WHERE id=" + number + ";"
	for _, part := range submission.Segments {
		query += "INSERT INTO sms_submission_segments(attempt_id,part_number,part_count,call_id,rp_reference,tp_reference,state,updated_at) VALUES(" +
			number + "," + strconv.Itoa(part.Index) + "," + strconv.Itoa(submission.SegmentCount) + "," + sqlText(part.CallID) + "," + strconv.Itoa(int(part.RPReference)) + "," + strconv.Itoa(int(part.TPReference)) + "," + sqlText(state) + "," + at + ");"
	}
	_, err := db.run(query+"COMMIT;", false)
	return err
}

func (db *messageDB) submissionUpdate(attempt int64, update ims.SMSSubmissionUpdate) error {
	number := strconv.FormatInt(attempt, 10)
	at := sqlText(update.ReceivedAt.UTC().Format(databaseTime))
	state := update.State.String()
	code := "NULL"
	if update.HasRPCause {
		code = strconv.Itoa(int(update.RPCause))
	}
	if update.HasStatus {
		code = strconv.Itoa(int(update.Status))
	}
	query := "BEGIN IMMEDIATE; UPDATE sms_submission_segments SET state=" + sqlText(state) + ",updated_at=" + at + " WHERE attempt_id=" + number + " AND part_number=" + strconv.Itoa(update.SegmentIndex) + " AND state NOT IN('delivered','delivery_failed','delivery_completed');"
	query += "INSERT OR IGNORE INTO delivery_events(message_id,attempt_id,recipient,kind,remote_event_id,occurred_at,code) SELECT message_id,id," +
		sqlText(canonicalPeer(update.Recipient)) + "," + sqlText(state) + "," + sqlText(stableID(update.CallID, strconv.Itoa(update.SegmentIndex), state)) + "," + at + "," + code + " FROM transport_attempts WHERE id=" + number + ";"
	// Report per-message success only after every submitted part agrees.
	query += `UPDATE transport_attempts SET state=CASE
        WHEN EXISTS(SELECT 1 FROM sms_submission_segments WHERE attempt_id=` + number + ` AND state IN('rejected_by_smsc','delivery_failed')) THEN 'delivery_failed'
        WHEN NOT EXISTS(SELECT 1 FROM sms_submission_segments WHERE attempt_id=` + number + ` AND state!='delivered') THEN 'delivered'
        WHEN NOT EXISTS(SELECT 1 FROM sms_submission_segments WHERE attempt_id=` + number + ` AND state NOT IN('accepted_by_smsc','delivered','delivery_completed','delivery_unknown','delivery_retrying')) THEN
            CASE WHEN EXISTS(SELECT 1 FROM sms_submission_segments WHERE attempt_id=` + number + ` AND state='delivery_unknown') THEN 'delivery_unknown' ELSE 'accepted_by_smsc' END
        WHEN EXISTS(SELECT 1 FROM sms_submission_segments WHERE attempt_id=` + number + ` AND state='submit_unknown') THEN 'submit_unknown'
        ELSE 'submitted_to_ims' END,updated_at=` + at + " WHERE id=" + number + "; COMMIT;"
	_, err := db.run(query, false)
	return err
}

// Only the active SIM session consumes its queue. Restarted/unknown submissions
// are retained for the user to inspect and are never automatically resent.
func runSMSQueue(ctx context.Context, db *messageDB, session string,
	verify func(context.Context) error, send func(context.Context, string, string) (ims.SMSSubmission, error), transport ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	var watchers sync.WaitGroup
	defer func() { cancel(); watchers.Wait() }()
	slots := make(chan struct{}, 8)
	failures := make(chan error, 1)
	report := func(err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failures:
			cancel()
			return err
		case <-ticker.C:
		}
		if len(slots) == cap(slots) {
			continue
		}
		message, err := db.claimSMS(session)
		if err != nil {
			return err
		}
		if message == nil {
			continue
		}
		attempt := int64(message["attempt"].(float64))
		check, cancelCheck := context.WithTimeout(ctx, 4*time.Second)
		err = verify(check)
		cancelCheck()
		if err != nil {
			_ = db.attemptState(attempt, "not-sent")
			cancel()
			return errors.New("SIM changed before SMS send")
		}
		sending, cancelSend := context.WithTimeout(ctx, 30*time.Second)
		submission, err := send(sending, message["peer"].(string), message["body"].(string))
		cancelSend()
		if err != nil {
			// A timeout can follow a successful network submission.
			if err := db.attemptState(attempt, "submit_unknown"); err != nil {
				return err
			}
			continue
		}
		if err := db.submission(attempt, submission, transport...); err != nil {
			submission.Close()
			cancel()
			return err
		}
		slots <- struct{}{}
		watchers.Add(1)
		go func() {
			defer watchers.Done()
			defer func() { <-slots }()
			defer submission.Close()
			for {
				select {
				case <-ctx.Done():
					return
				case update, ok := <-submission.Updates:
					if !ok {
						return
					}
					if err := db.submissionUpdate(attempt, update); err != nil {
						report(err)
						return
					}
				}
			}
		}()
	}
}
