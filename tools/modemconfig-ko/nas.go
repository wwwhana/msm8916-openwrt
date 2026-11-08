// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"reflect"
	"slices"
	"sync"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
	"github.com/damonto/wwan-go/qcom"
	simcard "github.com/damonto/wwan-go/sim/card"
)

type nasSettings interface {
	SystemSelectionPreference(context.Context) (qcom.NASSystemSelectionPreference, error)
	NASServingSystem(context.Context) (qcom.NASServingSystem, error)
	WMSBindSubscription(context.Context, qcom.WMSSubscription) error
	WMSBoundSubscription(context.Context) (qcom.WMSSubscription, error)
	WMSDomainPreference(context.Context) (qcom.WMSDomainPreference, error)
	WMSSetDomainPreference(context.Context, qcom.WMSDomainPreference) error
	WMSPrimaryClient(context.Context) (bool, error)
	WMSSetPrimaryClient(context.Context, bool) error
	WMSGetRoutes(context.Context) (qcom.WMSRoutes, error)
	WMSSetRoutes(context.Context, []qcom.WMSRoute, *qcom.WMSStatusReportTransfer) error
	WMSServiceReady(context.Context) (qcom.WMSServiceReadyState, error)
}

type nasLease struct {
	modem                                 nasSettings
	domain                                qcom.WMSDomainPreference
	original, routes                      qcom.WMSRoutes
	domainChanged, routesChanged, primary bool
}

func ltePacketService(selection qcom.NASSystemSelectionPreference, serving qcom.NASServingSystem) bool {
	if !selection.ModePreferenceKnown || selection.ModePreference != qcom.NASModePreferenceLTE ||
		serving.RegistrationState != qcom.NASRegistrationRegistered || serving.PSAttachState != qcom.NASAttachAttached ||
		len(serving.RadioInterfaces) == 0 {
		return false
	}
	for _, radio := range serving.RadioInterfaces {
		if radio != qcom.NASRadioInterfaceLTE {
			return false
		}
	}
	return true
}

func verifyLTE(ctx context.Context, modem nasSettings) error {
	selection, err := modem.SystemSelectionPreference(ctx)
	if err != nil {
		return err
	}
	serving, err := modem.NASServingSystem(ctx)
	if err != nil {
		return err
	}
	if !ltePacketService(selection, serving) {
		return errors.New("LTE-only packet service is unavailable")
	}
	return nil
}

// Keep SIM data-download (class 2), broadcast and vendor routes intact. Host
// transfer without automatic ACK lets SQLite commit before the network ACK.
func sameNASRoutes(left, right qcom.WMSRoutes) bool {
	if left.StatusReportKnown != right.StatusReportKnown || (left.StatusReportKnown && left.StatusReportTransfer != right.StatusReportTransfer) {
		return false
	}
	key := func(route qcom.WMSRoute) uint32 {
		return uint32(route.MessageType)<<24 | uint32(route.MessageClass)<<16 | uint32(route.Storage)<<8 | uint32(route.Action)
	}
	order := func(a, b qcom.WMSRoute) int {
		if key(a) < key(b) {
			return -1
		}
		if key(a) > key(b) {
			return 1
		}
		return 0
	}
	a, b := slices.Clone(left.Routes), slices.Clone(right.Routes)
	slices.SortFunc(a, order)
	slices.SortFunc(b, order)
	return reflect.DeepEqual(a, b)
}

func nasRoutes(original qcom.WMSRoutes) qcom.WMSRoutes {
	routes := original
	routes.Routes = append([]qcom.WMSRoute(nil), original.Routes...)
	for _, class := range []qcom.WMSMessageClass{qcom.WMSMessageClass0, qcom.WMSMessageClass1, qcom.WMSMessageClass3, qcom.WMSMessageClassNone} {
		found := false
		for i := range routes.Routes {
			if routes.Routes[i].MessageType == qcom.WMSMessageTypePointToPoint && routes.Routes[i].MessageClass == class {
				routes.Routes[i].Action = qcom.WMSReceiptTransferOnly
				found = true
			}
		}
		if !found {
			routes.Routes = append(routes.Routes, qcom.WMSRoute{MessageType: qcom.WMSMessageTypePointToPoint, MessageClass: class, Storage: qcom.WMSStorageNV, Action: qcom.WMSReceiptTransferOnly})
		}
	}
	if routes.StatusReportKnown {
		routes.StatusReportTransfer = qcom.WMSStatusReportToClient
	}
	return routes
}

func setNASRoutes(ctx context.Context, modem nasSettings, routes qcom.WMSRoutes) error {
	var report *qcom.WMSStatusReportTransfer
	if routes.StatusReportKnown {
		report = &routes.StatusReportTransfer
	}
	return modem.WMSSetRoutes(ctx, routes.Routes, report)
}

func acquireNAS(ctx context.Context, modem nasSettings) (*nasLease, error) {
	if err := verifyLTE(ctx, modem); err != nil {
		return nil, err
	}
	// Logical primary is not physical slot 1. openQCOM follows its current
	// physical mapping; bind WMS to that same logical subscription.
	if err := modem.WMSBindSubscription(ctx, qcom.WMSSubscriptionPrimary); err != nil {
		return nil, err
	}
	bound, err := modem.WMSBoundSubscription(ctx)
	if err != nil || bound != qcom.WMSSubscriptionPrimary {
		return nil, errors.New("WMS subscription binding unconfirmed")
	}
	lease := &nasLease{modem: modem}
	lease.domain, err = modem.WMSDomainPreference(ctx)
	if err != nil {
		return nil, err
	}
	lease.original, err = modem.WMSGetRoutes(ctx)
	if err != nil {
		return nil, err
	}
	lease.routes = nasRoutes(lease.original)
	if len(lease.routes.Routes) > 10 {
		return nil, errors.New("WMS routing table is full")
	}
	// Readback also covers ambiguous QMI timeouts during setup. The caller
	// restores only values still owned by this lease, on the same SIM.
	lease.domainChanged = lease.domain != qcom.WMSDomainPreferencePSOnly
	if lease.domainChanged {
		err = modem.WMSSetDomainPreference(ctx, qcom.WMSDomainPreferencePSOnly)
	}
	if err == nil {
		err = modem.WMSSetPrimaryClient(ctx, true)
		lease.primary = true
	}
	if err == nil {
		lease.routesChanged = true
		err = setNASRoutes(ctx, modem, lease.routes)
	}
	if err == nil {
		err = lease.verify(ctx)
	}
	return lease, err
}

func (lease *nasLease) verify(ctx context.Context) error {
	if err := verifyLTE(ctx, lease.modem); err != nil {
		return err
	}
	domain, err := lease.modem.WMSDomainPreference(ctx)
	if err != nil || domain != qcom.WMSDomainPreferencePSOnly {
		return errors.New("WMS PS-only policy was lost")
	}
	bound, err := lease.modem.WMSBoundSubscription(ctx)
	if err != nil || bound != qcom.WMSSubscriptionPrimary {
		return errors.New("WMS subscription changed")
	}
	primary, err := lease.modem.WMSPrimaryClient(ctx)
	if err != nil || !primary {
		return errors.New("WMS primary ownership was lost")
	}
	routes, err := lease.modem.WMSGetRoutes(ctx)
	if err != nil || !sameNASRoutes(routes, lease.routes) {
		return errors.New("WMS receive routes changed")
	}
	ready, err := lease.modem.WMSServiceReady(ctx)
	if err != nil {
		return err
	}
	if !ready.StatusKnown || (ready.Status != qcom.WMSReady3GPP && ready.Status != qcom.WMSReady3GPPAnd3GPP2 && ready.Status != qcom.WMSReady3GPPAnd3GPP2Limited) ||
		(ready.SIMStatusKnown && ready.SIMStatus&qcom.WMSSIMReady3GPP == 0) {
		return errors.New("WMS 3GPP SMS service is unavailable")
	}
	return nil
}

func (lease *nasLease) restore(ctx context.Context) error {
	var failures []error
	if lease.routesChanged {
		current, err := lease.modem.WMSGetRoutes(ctx)
		if err != nil {
			failures = append(failures, err)
		} else if sameNASRoutes(current, lease.routes) {
			failures = append(failures, setNASRoutes(ctx, lease.modem, lease.original))
		}
	}
	if lease.domainChanged {
		current, err := lease.modem.WMSDomainPreference(ctx)
		if err != nil {
			failures = append(failures, err)
		} else if current == qcom.WMSDomainPreferencePSOnly {
			failures = append(failures, lease.modem.WMSSetDomainPreference(ctx, lease.domain))
		}
	}
	if lease.primary {
		primary, err := lease.modem.WMSPrimaryClient(ctx)
		if err != nil {
			failures = append(failures, err)
		} else if primary {
			failures = append(failures, lease.modem.WMSSetPrimaryClient(ctx, false))
		}
	}
	return errors.Join(failures...)
}

type nasRawSender interface {
	WMSSendRaw(context.Context, qcom.WMSMessageFormat, []byte, qcom.WMSSendOptions) (qcom.WMSSendResult, error)
}

func sendNASSMS(ctx context.Context, modem nasRawSender, to, text string) (ims.SMSSubmission, error) {
	var reference [1]byte
	if _, err := rand.Read(reference[:]); err != nil {
		return ims.SMSSubmission{}, err
	}
	pdu, err := imssms.EncodeNASSubmit(reference[0], to, text)
	if err != nil {
		return ims.SMSSubmission{}, err
	}
	nas := false
	result, err := modem.WMSSendRaw(ctx, qcom.WMSMessageFormatGWPointToPoint, pdu, qcom.WMSSendOptions{SMSOnIMS: &nas})
	if err != nil || !result.MessageIDKnown {
		return ims.SMSSubmission{}, errors.New("NAS submission unconfirmed")
	}
	// QMI raw-send success confirms submission, not delivery to the handset.
	// The network's TP-STATUS-REPORT is processed separately, even after restart.
	at, id := time.Now(), "nas-"+opaqueID()
	updates := make(chan ims.SMSSubmissionUpdate, 1)
	updates <- ims.SMSSubmissionUpdate{SubmissionID: id, SegmentIndex: 1, SegmentCount: 1, Recipient: to, CallID: id,
		TPReference: reference[0], State: ims.SMSDeliveryUnknown, ReceivedAt: at}
	close(updates)
	return ims.SMSSubmission{ID: id, To: to, Text: text, SubmittedAt: at, SegmentCount: 1,
		Segments: []ims.SMSSubmissionSegment{{Index: 1, SubmittedAt: at, Recipient: to, CallID: id, TPReference: reference[0]}}, Updates: updates}, nil
}

type nasAcknowledger interface {
	WMSAcknowledge(context.Context, qcom.WMSACKRequest) error
}

func acceptNAS(ctx context.Context, modem nasAcknowledger, db *messageDB, event qcom.WMSIncomingMessage,
	verify func(context.Context) error) error {
	if err := verify(ctx); err != nil {
		return err
	}
	// NV is shared across SIM changes. Never attribute its old backlog to the
	// currently inserted SIM; leave it intact. New messages use transfer-only.
	if event.Stored && (event.Reference.Storage != qcom.WMSStorageUIM || (event.Tag != qcom.WMSTagMTRead && event.Tag != qcom.WMSTagMTNotRead)) {
		return nil
	}
	var storeErr error
	if event.ReadError != nil || event.Format != qcom.WMSMessageFormatGWPointToPoint {
		storeErr = errors.New("unsupported NAS message")
	} else {
		message, report, err := imssms.DecodeNASMessage(event.Data)
		storeErr = err
		if err == nil && report != nil {
			storeErr = db.acceptNASReport(*report)
		}
		if err == nil && report == nil {
			message.CallID = "nas-" + stableID(string(event.Data))
			message.FromURI, message.To, message.ToURI = "tel:"+message.From, "self", "self"
			storeErr = db.acceptSegment(message)
		}
	}
	if event.ACKIndicatorKnown && event.ACKIndicator == qcom.WMSACKRequired {
		if err := verify(ctx); err != nil {
			return err
		}
		ack := qcom.WMSACKRequest{TransactionID: event.TransactionID, Protocol: qcom.WMSMessageProtocolWCDMA, Success: storeErr == nil}
		if event.SMSOnIMSKnown {
			ack.SMSOnIMS = &event.SMSOnIMS
		}
		if storeErr != nil {
			ack.Failure3GPP = &qcom.WMSACK3GPPFailure{RPCause: 22, TPCause: 0xd3}
		}
		if err := modem.WMSAcknowledge(ctx, ack); err != nil {
			return err
		}
	}
	return storeErr
}

func nasSession(ctx context.Context, client *qcom.Client, reader simcard.Reader, scope, plmn, mode string, window time.Duration) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	db, session, err := sessionMessageDB(scope, plmn, client.Slot())
	if err != nil {
		return failed("sms-database", err)
	}
	verifySIM := func(ctx context.Context) error { return verifySIMSession(ctx, client, reader, scope) }
	if err := verifySIM(ctx); err != nil {
		return failed("sim-changed", err)
	}
	// Subscribe before changing routes: no unobserved transfer-only window.
	events, err := client.WMSWatchIncoming(ctx)
	if err != nil {
		return failed("nas-listen", err)
	}
	lease, err := acquireNAS(ctx, client)
	if lease != nil {
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 12*time.Second)
			defer stop()
			if verifySIM(cleanup) == nil {
				if err := lease.restore(cleanup); err != nil {
					result = failed("nas-restore", err)
				}
			}
		}()
	}
	if err != nil {
		return failed("nas-configure", err)
	}
	// A previous storage failure may have made the SMSC wait for memory
	// availability. The private database is writable before clearing that wait.
	if err := client.WMSSetMemoryAvailable(ctx, true); err != nil {
		return failed("nas-memory-available", err)
	}
	verify := func(ctx context.Context) error {
		if err := verifySIM(ctx); err != nil {
			return err
		}
		return lease.verify(ctx)
	}
	workers, stopWorkers := context.WithCancel(ctx)
	var running sync.WaitGroup
	shared := &nasIMSSession{db: db, scope: scope, session: session}
	// NAS remains available while IMS registers or reconnects. IMS uses its
	// own QMI client but shares this SIM's database session and status writer.
	running.Add(1)
	go func() {
		defer running.Done()
		for workers.Err() == nil {
			_ = registrationSessionWithNAS(workers, mode, 0, nil, shared)
			select {
			case <-workers.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
	failures := make(chan error, 2)
	for _, worker := range []func() error{
		func() error {
			return runSMSQueue(workers, db, session, verify, func(ctx context.Context, to, text string) (ims.SMSSubmission, error) {
				return sendNASSMS(ctx, client, to, text)
			}, "nas")
		},
		func() error { return runMMSQueue(workers, db, plmn, session, verifySIM) },
	} {
		running.Add(1)
		go func() {
			defer running.Done()
			if err := worker(); err != nil {
				failures <- err
			}
		}()
	}
	defer func() { stopWorkers(); running.Wait() }()
	defer os.Remove(runtimeStatusPath)
	write := func() error {
		registration := "not-registered"
		if shared.registered.Load() {
			registration = "registered"
		}
		return writeMessagingStatus(runtimeStatusPath, client.Slot(), registration, "nas", true, true, db.subscription, session, plmn)
	}
	if err := write(); err != nil {
		return failed("nas-status-write", err)
	}
	emit(map[string]any{"stage": "sms-transport", "transport": "nas", "state": "ready", "lte_only": true, "ps_only": true})
	var deadline <-chan time.Time
	if window > 0 {
		timer := time.NewTimer(window)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	checks := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			return nil
		case err := <-failures:
			return failed("nas-worker", err)
		case event, ok := <-events:
			if !ok {
				return failed("nas-listen", errors.New("WMS event stream closed"))
			}
			check, stop := context.WithTimeout(ctx, 12*time.Second)
			err := acceptNAS(check, client, db, event, verifySIM)
			stop()
			if err != nil {
				return failed("nas-receive", err)
			}
		case <-ticker.C:
			checks++
			if checks%5 == 0 {
				check, stop := context.WithTimeout(ctx, 8*time.Second)
				err := verify(check)
				stop()
				if err != nil {
					return failed("nas-session-changed", err)
				}
			}
			if err := db.recoverNASReports(); err != nil {
				return failed("nas-report", err)
			}
			if err := write(); err != nil {
				return failed("nas-status-write", err)
			}
		}
	}
}
