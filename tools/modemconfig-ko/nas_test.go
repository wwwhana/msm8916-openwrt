// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
	"github.com/damonto/wwan-go/qcom"
)

func TestSMSTransportDefaultsAndSIMProvisioning(t *testing.T) {
	root, scope := t.TempDir(), strings.Repeat("a", 64)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for plmn, want := range map[string]string{"45005": "nas", "45008": "ims", "45006": "ims"} {
		actual, err := smsTransport(root, scope, plmn)
		if err != nil || actual != want {
			t.Fatalf("carrier %s: %s %v", plmn, actual, err)
		}
	}
	policy := filepath.Join(root, scope, "sms-transport")
	if err := os.WriteFile(policy, []byte("ims\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := smsTransport(root, scope, "45005"); err != nil || got != "ims" {
		t.Fatal("provisioning ignored")
	}
	if got, err := smsTransport(root, strings.Repeat("b", 64), "45005"); err != nil || got != "nas" {
		t.Fatal("policy leaked to another SIM")
	}
	if err := os.WriteFile(policy, []byte("cs"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := smsTransport(root, scope, "45005"); err == nil {
		t.Fatal("CS fallback accepted")
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sms-transport", policy); err != nil {
		t.Fatal(err)
	}
	if _, err := smsTransport(root, scope, "45005"); err == nil {
		t.Fatal("symlink policy accepted")
	}
	if _, err := smsTransport(root, "../outside", "45005"); err == nil {
		t.Fatal("invalid SIM scope accepted")
	}
}

type fakeNASModem struct {
	selection  qcom.NASSystemSelectionPreference
	serving    qcom.NASServingSystem
	domain     qcom.WMSDomainPreference
	routes     qcom.WMSRoutes
	primary    bool
	bound      qcom.WMSSubscription
	failRoutes bool
	sends      atomic.Int32
	sendError  error
	sentPDU    []byte
	ack        func(qcom.WMSACKRequest) error
}

func newFakeNAS() *fakeNASModem {
	return &fakeNASModem{selection: qcom.NASSystemSelectionPreference{ModePreferenceKnown: true, ModePreference: qcom.NASModePreferenceLTE},
		serving: qcom.NASServingSystem{RegistrationState: qcom.NASRegistrationRegistered, PSAttachState: qcom.NASAttachAttached, RadioInterfaces: []qcom.NASRadioInterface{qcom.NASRadioInterfaceLTE}},
		domain:  qcom.WMSDomainPreferenceCS, routes: qcom.WMSRoutes{Routes: []qcom.WMSRoute{{MessageClass: qcom.WMSMessageClass2, Storage: qcom.WMSStorageUIM, Action: qcom.WMSReceiptStoreAndNotify}}, StatusReportKnown: true, StatusReportTransfer: qcom.WMSStatusReportToSIM}}
}
func (m *fakeNASModem) SystemSelectionPreference(context.Context) (qcom.NASSystemSelectionPreference, error) {
	return m.selection, nil
}
func (m *fakeNASModem) NASServingSystem(context.Context) (qcom.NASServingSystem, error) {
	return m.serving, nil
}
func (m *fakeNASModem) WMSBindSubscription(_ context.Context, b qcom.WMSSubscription) error {
	m.bound = b
	return nil
}
func (m *fakeNASModem) WMSBoundSubscription(context.Context) (qcom.WMSSubscription, error) {
	return m.bound, nil
}
func (m *fakeNASModem) WMSDomainPreference(context.Context) (qcom.WMSDomainPreference, error) {
	return m.domain, nil
}
func (m *fakeNASModem) WMSSetDomainPreference(_ context.Context, d qcom.WMSDomainPreference) error {
	m.domain = d
	return nil
}
func (m *fakeNASModem) WMSPrimaryClient(context.Context) (bool, error) { return m.primary, nil }
func (m *fakeNASModem) WMSSetPrimaryClient(_ context.Context, p bool) error {
	m.primary = p
	return nil
}
func (m *fakeNASModem) WMSGetRoutes(context.Context) (qcom.WMSRoutes, error) { return m.routes, nil }
func (m *fakeNASModem) WMSServiceReady(context.Context) (qcom.WMSServiceReadyState, error) {
	return qcom.WMSServiceReadyState{StatusKnown: true, Status: qcom.WMSReady3GPP}, nil
}
func (m *fakeNASModem) WMSSetRoutes(_ context.Context, r []qcom.WMSRoute, s *qcom.WMSStatusReportTransfer) error {
	if m.failRoutes {
		return errors.New("route write failed")
	}
	m.routes.Routes = append([]qcom.WMSRoute(nil), r...)
	if s != nil {
		m.routes.StatusReportKnown = true
		m.routes.StatusReportTransfer = *s
	}
	return nil
}
func (m *fakeNASModem) WMSSendRaw(_ context.Context, f qcom.WMSMessageFormat, pdu []byte, opts qcom.WMSSendOptions) (qcom.WMSSendResult, error) {
	m.sends.Add(1)
	if f != qcom.WMSMessageFormatGWPointToPoint || opts.SMSOnIMS == nil || *opts.SMSOnIMS {
		return qcom.WMSSendResult{}, errors.New("NAS was not explicitly selected")
	}
	m.sentPDU = append([]byte(nil), pdu...)
	return qcom.WMSSendResult{MessageID: 17, MessageIDKnown: m.sendError == nil}, m.sendError
}
func (m *fakeNASModem) WMSAcknowledge(_ context.Context, r qcom.WMSACKRequest) error {
	if m.ack != nil {
		return m.ack(r)
	}
	return nil
}

func TestNASRequiresLTEAndRestoresOnlyOwnedConfiguration(t *testing.T) {
	ctx, m := context.Background(), newFakeNAS()
	original := m.routes
	lease, err := acquireNAS(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.domain != qcom.WMSDomainPreferencePSOnly || m.bound != qcom.WMSSubscriptionPrimary || !m.primary {
		t.Fatal("packet-only subscription setup failed")
	}
	if !reflect.DeepEqual(m.routes.Routes[0], original.Routes[0]) {
		t.Fatal("SIM data-download route was changed")
	}
	reordered := m.routes
	reordered.Routes = append([]qcom.WMSRoute(nil), m.routes.Routes...)
	for i, j := 0, len(reordered.Routes)-1; i < j; i, j = i+1, j-1 {
		reordered.Routes[i], reordered.Routes[j] = reordered.Routes[j], reordered.Routes[i]
	}
	if !sameNASRoutes(reordered, m.routes) {
		t.Fatal("firmware route ordering changed ownership")
	}
	for _, r := range m.routes.Routes[1:] {
		if r.Action != qcom.WMSReceiptTransferOnly {
			t.Fatal("modem can ACK before commit")
		}
	}
	if err := lease.restore(ctx); err != nil || !reflect.DeepEqual(m.routes, original) || m.domain != qcom.WMSDomainPreferenceCS || m.primary {
		t.Fatal("configuration was not restored", err)
	}
	lease, err = acquireNAS(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	m.domain = qcom.WMSDomainPreferencePS // Another owner changed the setting.
	if err := lease.verify(ctx); err == nil {
		t.Fatal("PS-preferred permits CS fallback")
	}
	if err := lease.restore(ctx); err != nil || m.domain != qcom.WMSDomainPreferencePS {
		t.Fatal("overwrote another owner's setting")
	}
	for _, radio := range []qcom.NASRadioInterface{qcom.NASRadioInterfaceUMTS, qcom.NASRadioInterfaceGSM} {
		m = newFakeNAS()
		m.serving.RadioInterfaces = []qcom.NASRadioInterface{radio}
		if _, err := acquireNAS(ctx, m); err == nil || m.primary {
			t.Fatal("3G/2G NAS was accepted")
		}
	}
	m = newFakeNAS()
	m.selection.ModePreference |= qcom.NASModePreferenceUMTS
	if _, err := acquireNAS(ctx, m); err == nil {
		t.Fatal("RAT fallback remains enabled")
	}
	m = newFakeNAS()
	m.failRoutes = true
	lease, err = acquireNAS(ctx, m)
	if err == nil || lease == nil {
		t.Fatal("partial setup failure lost rollback")
	}
	m.failRoutes = false
	if err := lease.restore(ctx); err != nil || m.primary || m.domain != qcom.WMSDomainPreferenceCS {
		t.Fatal("partial setup leaked settings")
	}
}

func TestNASSenderAndAmbiguousSubmissionNeverResend(t *testing.T) {
	m := newFakeNAS()
	submission, err := sendNASSMS(context.Background(), m, "+821000000001", "한글")
	if err != nil {
		t.Fatal(err)
	}
	defer submission.Close()
	if len(m.sentPDU) < 4 || m.sentPDU[0] != 0 || m.sentPDU[1]&0x25 != 0x25 {
		t.Fatal("SMSC envelope, duplicate rejection or report request missing")
	}
	if _, _, err := imssms.DecodeNASMessage(m.sentPDU); err == nil {
		t.Fatal("mobile-originated TPDU accepted as received")
	}
	update := <-submission.Updates
	if update.State != ims.SMSDeliveryUnknown {
		t.Fatal("raw-send was presented as delivery")
	}
	if _, err := sendNASSMS(context.Background(), m, "+821000000001", strings.Repeat("한", 71)); err == nil {
		t.Fatal("LMS was silently split or truncated")
	}
	db := testMessageDB(t)
	session := opaqueID()
	if _, err := db.queueSMS(session, opaqueID(), "+821000000001", "불명확한 응답"); err != nil {
		t.Fatal(err)
	}
	m = newFakeNAS()
	m.sendError = context.DeadlineExceeded
	ctx, cancel := context.WithTimeout(context.Background(), 2300*time.Millisecond)
	defer cancel()
	err = runSMSQueue(ctx, db, session, func(context.Context) error { return nil }, func(ctx context.Context, to, text string) (ims.SMSSubmission, error) {
		return sendNASSMS(ctx, m, to, text)
	}, "nas")
	if err != nil || m.sends.Load() != 1 {
		t.Fatal("ambiguous send was retried", err)
	}
	rows, _ := db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] != "submit_unknown" {
		t.Fatal("ambiguous send state lost")
	}
}

func nasDeliverPDU(binary bool, data []byte) []byte {
	first, dcs := byte(0), byte(8)
	if binary {
		first, dcs = 0x40, 4
	}
	// SMSC omitted; +821000000001, 2026-11-07 12:00:00 UTC.
	pdu := []byte{0, first, 12, 0x91, 0x28, 0x01, 0, 0, 0, 0x10, 0, dcs, 0x62, 0x11, 0x70, 0x21, 0, 0, 0, byte(len(data))}
	return append(pdu, data...)
}

func TestNASReceiveCommitsBeforeACKAndPreservesMMSPush(t *testing.T) {
	db, m := testMessageDB(t), newFakeNAS()
	acks := 0
	m.ack = func(r qcom.WMSACKRequest) error {
		acks++
		rows, err := db.run("SELECT COUNT(*) AS n FROM sms_segments;", true)
		if err != nil || rows[0]["n"] == float64(0) || !r.Success || r.TransactionID != 42 {
			t.Fatal("ACK preceded durable storage")
		}
		return nil
	}
	event := qcom.WMSIncomingMessage{Format: qcom.WMSMessageFormatGWPointToPoint, Data: nasDeliverPDU(false, []byte{0xd5, 0x5c, 0xae, 0}), ACKIndicatorKnown: true, ACKIndicator: qcom.WMSACKRequired, TransactionID: 42}
	verify := func(context.Context) error { return nil }
	for i := 0; i < 2; i++ {
		if err := acceptNAS(context.Background(), m, db, event, verify); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.run("SELECT body FROM messages;", true)
	if err != nil || len(rows) != 1 || rows[0]["body"] != "한글" || acks != 2 {
		t.Fatal("text decode or duplicate detection failed", err)
	}
	header := []byte{6, 5, 4, 0x0b, 0x84, 0x23, 0xf0}
	data := append(header, testMMSPush(testMMSNotification("http://omammsc.uplus.co.kr:9095/test"), false)...)
	event.Data = nasDeliverPDU(true, data)
	if err := acceptNAS(context.Background(), m, db, event, verify); err != nil {
		t.Fatal(err)
	}
	rows, err = db.run("SELECT COUNT(*) AS n FROM mms_pdus;", true)
	if err != nil || rows[0]["n"] != float64(1) {
		t.Fatal("binary WAP push did not reach MMS queue", err)
	}
	before := acks
	if err := acceptNAS(context.Background(), m, db, event, func(context.Context) error { return errors.New("SIM changed") }); err == nil || acks != before {
		t.Fatal("ACK sent on another SIM")
	}
	broken := &messageDB{path: filepath.Join(t.TempDir(), "missing", "messages.sqlite3")}
	m.ack = func(r qcom.WMSACKRequest) error {
		if r.Success || r.Failure3GPP == nil || r.Failure3GPP.RPCause != 22 {
			t.Fatal("storage failure was positively ACKed")
		}
		return nil
	}
	if err := acceptNAS(context.Background(), m, broken, event, verify); err == nil {
		t.Fatal("storage failure hidden")
	}
	// NV may contain another SIM's messages. Ignore its backlog without deleting.
	event.Stored = true
	event.Reference.Storage = qcom.WMSStorageNV
	m.ack = func(qcom.WMSACKRequest) error { t.Fatal("shared NV backlog acknowledged"); return nil }
	if err := acceptNAS(context.Background(), m, db, event, verify); err != nil {
		t.Fatal(err)
	}
}

func TestNASReportsSurviveRestartMatchSIMAndRejectReferenceCollisions(t *testing.T) {
	db := testMessageDB(t)
	now := time.Now()
	report := imssms.StatusReport{Recipient: "+821000000001", MessageReference: 7, Status: imssms.ReportStatus(0), ServiceCenterTime: now, RawTPDU: []byte{2, 7, 0}}
	if err := db.acceptNASReport(report); err != nil {
		t.Fatal(err)
	}
	if err := db.recoverNASReports(); err != nil {
		t.Fatal(err)
	} // Report precedes send response.
	session := opaqueID()
	add := func() int64 {
		if _, err := db.queueSMS(session, opaqueID(), report.Recipient, "전달 확인"); err != nil {
			t.Fatal(err)
		}
		message, err := db.claimSMS(session)
		if err != nil || message == nil {
			t.Fatal("queue claim", err)
		}
		attempt := int64(message["attempt"].(float64))
		if err := db.submission(attempt, ims.SMSSubmission{SubmittedAt: now, SegmentCount: 1, Segments: []ims.SMSSubmissionSegment{{Index: 1, TPReference: 7, CallID: "nas-" + opaqueID()}}}, "nas"); err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	first := add()
	reopened, err := openMessageDB(db.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.subscription = db.subscription
	if err := reopened.recoverNASReports(); err != nil {
		t.Fatal(err)
	}
	if err := db.submissionUpdate(first, ims.SMSSubmissionUpdate{SegmentIndex: 1, State: ims.SMSDeliveryUnknown, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.run("SELECT state FROM transport_attempts;", true)
	if rows[0]["state"] != "delivered" {
		t.Fatal("report lost across restart or late unknown overwrote delivery")
	}
	add()
	report.RawTPDU = []byte{2, 7, 1}
	report.Status = imssms.ReportStatus(0x40)
	if err := db.acceptNASReport(report); err != nil {
		t.Fatal(err)
	}
	if err := db.recoverNASReports(); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT processed FROM nas_status_reports WHERE id="+sqlText(stableID(db.subscription, string(report.RawTPDU)))+";", true)
	if rows[0]["processed"] != float64(0) {
		t.Fatal("ambiguous wrapping TP-MR matched")
	}
	other := db.subscription
	if err := db.bind("test-sim-two", 2, "45005"); err != nil {
		t.Fatal(err)
	}
	add()
	if err := db.recoverNASReports(); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.run("SELECT state FROM transport_attempts ORDER BY id DESC LIMIT 1;", true)
	if rows[0]["state"] != "submitted_to_modem" || db.subscription == other {
		t.Fatal("report leaked across SIMs")
	}
}

func TestNASRuntimeDoesNotRequireIMSAndStaleSessionsCannotSend(t *testing.T) {
	db := testMessageDB(t)
	session := opaqueID()
	state := messagingRuntime{Registration: "not-registered", SMSTransport: "nas", SMSReady: true, MMSReady: true, Subscription: db.subscription, Session: session, PLMN: "45005"}
	result, err := db.messageMethod("snapshot", messageRequest{}, state)
	if err != nil || result.(map[string]any)["service"].(map[string]any)["ims_registered"] != false || !state.smsReady() {
		t.Fatal("NAS readiness depends on IMS")
	}
	request := messageRequest{Subscription: db.subscription, Session: session, RequestID: opaqueID(), To: "+821000000001", Text: "한글"}
	if _, err := db.messageMethod("send", request, state); err != nil {
		t.Fatal(err)
	}
	request.Session = opaqueID()
	if _, err := db.messageMethod("send", request, state); err == nil {
		t.Fatal("stale session can send")
	}
	request.Session = session
	state.SMSReady = false
	if _, err := db.messageMethod("send", request, state); err == nil {
		t.Fatal("unready SMS can send")
	}
	request.RequestID = opaqueID()
	request.Text = strings.Repeat("한", 71)
	if _, err := db.messageMethod("send", request, state); err != nil {
		t.Fatal("LMS was tied to SMS/IMS readiness", err)
	}
}
