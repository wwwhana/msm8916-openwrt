// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	ims "github.com/damonto/ims-go"
	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
	imssms "github.com/damonto/ims-go/ims/sms"
	"github.com/damonto/ims-go/lte"
	"github.com/damonto/wwan-go/qcom"
	"github.com/damonto/wwan-go/sim"
	simcard "github.com/damonto/wwan-go/sim/card"
	"github.com/damonto/wwan-go/sim/command"
	"github.com/damonto/wwan-go/sim/simfile"
)

// wwan-go prefers USIM AKA. Korean TTA uses the ISIM application when present.
// Embedding retains QCOMClient's IMS PDN method and ownership of the reader.
type ttaReader struct {
	*ims.QCOMClient
	isimAID     []byte
	modemClient *qcom.Client
	expectedSIM string
}

func (r *ttaReader) Authenticate3G(ctx context.Context, req simcard.AuthenticateRequest) ([]byte, error) {
	if err := verifySIMSession(ctx, r.modemClient, r.QCOMClient, r.expectedSIM); err != nil {
		return nil, err
	}
	if len(r.isimAID) != 0 && !req.EAPAKA {
		req.AID = r.isimAID
	}
	return r.QCOMClient.Authenticate3G(ctx, req)
}

func (r *ttaReader) AKA(ctx context.Context, rand, autn []byte) (sim.AKAResult, error) {
	data, err := r.Authenticate3G(ctx, simcard.AuthenticateRequest{AID: r.isimAID, Rand: rand, AUTN: autn})
	if err != nil {
		return sim.AKAResult{}, errors.New("SIM authentication unavailable")
	}
	var result command.Authenticate3GResult
	if result.UnmarshalBinary(data) != nil {
		return sim.AKAResult{}, errors.New("SIM authentication invalid")
	}
	return sim.AKAResult{RES: result.RES, CK: result.CK, IK: result.IK, AUTS: result.AUTS, Reject: result.Reject}, nil
}

// The R6 SHA-1 fallback must advertise TTA 3.0, never the SHA-256 TTA 4.0 version.
func ttaSecurity(mode string) (agent, integrity, encryption string, err error) {
	version := "4.0"
	integrity = "hmac-sha2-256-128"
	switch mode {
	case "tta4-null", "tta4-aes":
	case "tta3-null", "tta3-aes":
		version, integrity = "3.0", "hmac-sha-1-96"
	default:
		return "", "", "", errors.New("unsupported TTA security mode")
	}
	encryption = "null"
	if strings.HasSuffix(mode, "-aes") {
		encryption = "aes-cbc"
	}
	return "TTA-VoLTE/" + version + " A8/OpenWrt25.12.6 Device_Type/Router OMD", integrity, encryption, nil
}

// Baseline Joan CarrierSettings, before runtime provisioning or overrides.
func androidCarrierPolicy(plmn string) (disableIPSec bool, sipPort int, err error) {
	switch plmn {
	case "45005", "45006":
		return true, 5060, nil
	case "45008":
		return false, 5080, nil
	default:
		return false, 0, errors.New("unsupported Android carrier policy")
	}
}

type nasIMSSession struct {
	db             *messageDB
	scope, session string
	registered     atomic.Bool
}

func registrationSession(ctx context.Context, mode string, window time.Duration, outgoing *outgoingSMS) error {
	return registrationSessionWithNAS(ctx, mode, window, outgoing, nil)
}

func registrationSessionWithNAS(ctx context.Context, mode string, window time.Duration, outgoing *outgoingSMS, nas *nasIMSSession) (result error) {
	if nas != nil {
		defer nas.registered.Store(false)
	}
	agent, integrity, encryption, err := ttaSecurity(mode)
	if mode == "android-carrier" {
		agent = "A8/OpenWrt25.12.6 Device_Type/Router OMD"
		integrity, encryption, err = "hmac-sha-1-96", "aes-cbc", nil
	}
	if err != nil {
		return failed("tta-security-mode", err)
	}
	client, err := openQCOM(ctx)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = client.Close()
		}
	}()
	reader, err := sim.NewQCOM(client)
	if err != nil {
		return failed("sim-reader", err)
	}
	card, err := sim.New(ctx, reader, slog.Default())
	if err != nil {
		return failed("sim-read", err)
	}
	disableIPSec, sipPort := false, 5060
	if mode == "android-carrier" {
		disableIPSec, sipPort, err = androidCarrierPolicy(card.MCC() + card.MNC())
		if err != nil {
			return failed("android-carrier-policy", err)
		}
	}
	scope, err := simScope(card.ICCID())
	if err != nil {
		return failed("sim-scope", err)
	}
	if nas != nil && nas.scope != scope {
		return failed("sim-changed", errors.New("SIM session changed"))
	}
	if mode == "android-carrier" && nas == nil {
		transport, err := smsTransport(simStateDirectory, scope, card.MCC()+card.MNC())
		if err != nil {
			return failed("sms-transport-policy", err)
		}
		if transport == "nas" {
			if outgoing != nil {
				return failed("sms-input", errors.New("use the persisted SMS queue for NAS sends"))
			}
			return nasSession(ctx, client, reader, scope, card.MCC()+card.MNC(), mode, window)
		}
	}
	profile, err := ensureIMSProfile(ctx, client, card.MCC()+card.MNC(), scope)
	if err != nil {
		return err
	}
	identity, err := client.SerialNumbers(ctx)
	if err != nil || !identity.IMEIKnown || identity.IMEI == "" {
		return failed("device-identity", errors.New("device identity unavailable"))
	}
	location, err := client.CellLocationInfo(ctx)
	if err != nil {
		return failed("lte-cell", err)
	}
	pani, err := accessNetworkInfo(location)
	if err != nil {
		return failed("lte-cell", err)
	}
	aid, aidErr := (command.FindAID{Prefix: []byte{0xa0, 0, 0, 0, 0x87, 0x10, 4}}).Run(ctx, reader)
	if aidErr != nil && card.HomeDomain() != "" {
		return failed("isim-application", aidErr)
	}
	publicIdentity := card.PublicIdentity()
	var pcscfRecords [][]byte
	if aidErr == nil {
		app := command.App{Reader: reader, AID: aid}
		records, err := app.LinearFixed(ctx, []byte{0x6f, 4})
		if err != nil {
			return failed("isim-public-identity", err)
		}
		publicIdentity, err = normalPublicIdentity(records)
		if err != nil {
			return failed("isim-public-identity", err)
		}
		if records, err := app.LinearFixed(ctx, []byte{0x6f, 9}); err == nil {
			pcscfRecords = records
		}
	}
	// Create a private inbox before advertising SMS support. A received message
	// is stored even during this bounded registration test, never just counted.
	var db *messageDB
	var sessionID string
	if nas != nil {
		db, sessionID = nas.db, nas.session
	} else {
		db, sessionID, err = sessionMessageDB(scope, card.MCC()+card.MNC(), client.Slot(), publicIdentity, card.PublicIdentity())
		if err != nil {
			return failed("sms-database", err)
		}
	}
	network := &imsNetwork{pcscfRecords: pcscfRecords, sipServerPort: sipPort}
	adapter, err := ims.NewQCOMClient(client, ims.QCOMClientConfig{
		LegacyMuxDataPort: qcom.WDSSIOPortA2MuxRMNET1, Network: network,
	})
	if err != nil {
		return failed("ims-reader", err)
	}
	owned = false
	cfg := &ims.Config{
		Logger:   slog.New(protocolLog{allowSIP: network.AllowSIP}),
		Terminal: ims.TerminalInfo{ID: identity.IMEI, Model: "A8", SoftwareVersion: "OpenWrt25.12.6", Type: "Router"},
		Access: ims.VoLTE(lte.Config{
			APN: "ims", PDNType: "ipv6", ProfileIndex: profile.Index,
			InterfaceName:     "wwan1",
			AccessNetworkInfo: pani, RequireVoPS: true,
		}),
		IMS: ims.ServiceConfig{
			PrivateIdentity: card.PrivateIdentity(), PublicIdentity: publicIdentity,
			HomeDomain: card.HomeDomain(), PhoneContextDomain: card.HomeDomain(),
			UserAgent:       agent,
			RegisterExpires: time.Hour, AuthAlgorithm: "AKAv1-MD5",
			DisableIPSec: disableIPSec, SIPServerPort: sipPort, SMSOnly: false, DisableSMS: nas != nil,
			SIPDatagramOnly:           mode == "android-carrier" && card.MCC()+card.MNC() == "45006",
			Voice:                     koreanVoiceConfig(),
			IPSecIntegrityAlgorithms:  []string{integrity},
			IPSecEncryptionAlgorithms: []string{encryption},
			IPSecModes:                []string{"trans"}, GeolocationCountry: "KR",
			ServiceCenter: normalizedPSI(card.ServiceCenter().PSI),
			AcceptIncomingSMS: func(ctx context.Context, message imssms.Incoming) error {
				if err := db.acceptSegment(message); err != nil {
					return errors.Join(err, &imssms.RPError{Cause: imssms.RPCauseMemoryCapacityExceeded})
				}
				return nil
			},
		},
	}
	secureReader := &ttaReader{QCOMClient: adapter, isimAID: aid, modemClient: client, expectedSIM: scope}
	imsClient, err := ims.New(secureReader, cfg)
	if err != nil {
		return failed("ims-client", err)
	}
	events := imsClient.SMS().Events()
	storeErrors := make(chan error, 1)
	var received atomic.Bool
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range events.Incoming {
			received.Store(true)
			emit(map[string]any{"stage": "sms-receive", "state": "stored"})
		}
	}()
	defer func() {
		if err := imsClient.Close(); err != nil {
			result = registrationFailure("ims-close", err)
		}
		events.Close()
		<-drained
		select {
		case err := <-storeErrors:
			result = failed("sms-inbox-write", err)
		default:
		}
	}()
	write := func(registered, received bool) error {
		if nas != nil {
			nas.registered.Store(registered)
			return nil
		}
		return writeRuntimeStatus(runtimeStatusPath, client.Slot(), registered, received, db.subscription, sessionID, card.MCC()+card.MNC())
	}
	if err := write(false, false); err != nil {
		return failed("ims-status-write", err)
	}
	if nas == nil {
		defer os.Remove(runtimeStatusPath)
	}
	emit(map[string]any{"stage": "ims-registration", "state": "connecting", "sim_pcscf_records": len(pcscfRecords), "isim": aidErr == nil, "security_mode": mode, "ipsec": !disableIPSec, "sip_port": sipPort})
	connectCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	err = imsClient.Connect(connectCtx)
	cancel()
	if err != nil {
		return registrationFailure("ims-registration", err)
	}
	if !imsClient.Registration().Registered {
		return failed("ims-registration", errors.New("registration not confirmed"))
	}
	if err := write(true, false); err != nil {
		return failed("ims-status-write", err)
	}
	emit(map[string]any{"stage": "ims-registration", "state": "registered", "receive_window_seconds": int(window.Seconds()), "continuous": window == 0})
	queueCtx, stopQueue := context.WithCancel(ctx)
	defer stopQueue()
	verifyServices := func(ctx context.Context) error { return verifySIMSession(ctx, client, adapter, scope) }
	if err := db.recoverCalls(); err != nil {
		return failed("voice-history", err)
	}
	voiceService, closeVoice := startVoice(queueCtx, imsClient, verifyServices, db)
	defer closeVoice()
	rcsService := startRCS(queueCtx, db, scope, card.MCC()+card.MNC(), card.IMSI(), identity.IMEI, canonicalPeer(publicIdentity), card.PrivateIdentity(), publicIdentity, sessionID, secureReader, verifyServices)
	closeControl, err := serveServices(queueCtx, voiceService, rcsService, db.subscription, sessionID)
	if err != nil {
		return failed("service-control", err)
	}
	defer closeControl()
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		if nas != nil {
			<-queueCtx.Done()
			return
		}
		err := runSMSQueue(queueCtx, db, sessionID, func(ctx context.Context) error {
			if !imsClient.Registration().Registered {
				return errors.New("IMS registration was lost")
			}
			return verifySIMSession(ctx, client, adapter, scope)
		}, imsClient.SMS().Send)
		if err != nil {
			select {
			case storeErrors <- err:
			default:
			}
		}
	}()
	defer func() { stopQueue(); <-queueDone }()
	mmsCtx, stopMMS := context.WithCancel(ctx)
	mmsDone, mmsErrors := make(chan struct{}), make(chan error, 1)
	go func() {
		defer close(mmsDone)
		if nas != nil {
			<-mmsCtx.Done()
			return
		}
		if err := runMMSQueue(mmsCtx, db, card.MCC()+card.MNC(), sessionID, func(ctx context.Context) error {
			return verifySIMSession(ctx, client, adapter, scope)
		}); err != nil && mmsCtx.Err() == nil {
			mmsErrors <- err
		}
	}()
	defer func() { stopMMS(); <-mmsDone }()
	var updates <-chan ims.SMSSubmissionUpdate
	accepted := outgoing == nil
	if outgoing != nil {
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		submission, err := imsClient.SMS().Send(sendCtx, outgoing.To, outgoing.Text)
		cancel()
		if err != nil {
			return registrationFailure("sms-send", err)
		}
		defer submission.Close()
		updates = submission.Updates
		emit(map[string]any{"stage": "sms-send", "state": "submitted-to-ims", "segments": submission.SegmentCount})
	}
	var deadline <-chan time.Time
	if window > 0 {
		timer := time.NewTimer(window)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	simCheck := time.NewTicker(5 * time.Second)
	defer simCheck.Stop()
	for {
		complete := false
		select {
		case err := <-mmsErrors:
			return failed("mms-receive", err)
		case err := <-storeErrors:
			return failed("sms-inbox-write", err)
		case <-ctx.Done():
			if window == 0 && errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return failed("ims-listen", ctx.Err())
		case <-deadline:
			complete = true
		case update, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			emit(map[string]any{"stage": "sms-send", "state": update.State.String()})
			switch update.State {
			case ims.SMSAcceptedBySMSC, ims.SMSDelivered:
				accepted = true
			case ims.SMSRejectedBySMSC, ims.SMSDeliveryFailed:
				return failed("sms-send", errors.New("SMS rejected by carrier"))
			}
		case <-ticker.C:
		case <-simCheck.C:
			checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := verifySIMSession(checkCtx, client, adapter, scope)
			cancel()
			if err != nil {
				return failed("sim-changed", err)
			}
		}
		state := imsClient.State()
		if !state.Active || !imsClient.Registration().Registered {
			return registrationFailure("ims-listen", errors.Join(errors.New("IMS registration was lost"), state.LastError))
		}
		if err := write(true, received.Load()); err != nil {
			return failed("ims-status-write", err)
		}
		if complete {
			if !accepted {
				return failed("sms-send", errors.New("SMS service center acceptance unconfirmed"))
			}
			emit(map[string]any{"stage": "ims-listen", "state": "completed", "registered": true})
			return nil
		}
	}
}

func registrationFailure(stage string, err error) error {
	info := diagnostic(stage, err)
	var response *sip.ResponseError
	if errors.As(err, &response) {
		info["sip_status"] = response.StatusCode
	}
	var transaction *session.TransactionTimeoutError
	if errors.As(err, &transaction) {
		info["operation"] = "SIP response timeout"
		info["timeout"] = true
	}
	for _, operation := range []string{"P-CSCF address is unavailable", "route to P-CSCF is unavailable", "IMS PDN packet data is not ready", "IPsec", "authenticating", "opening tcp transport", "opening udp transport"} {
		if strings.Contains(err.Error(), operation) {
			info["operation"] = operation
			break
		}
	}
	emit(info)
	return err
}

func accessNetworkInfo(info qcom.NASCellLocationInfo) (string, error) {
	cell := info.LTEIntra
	if !info.LTEIntraKnown || !cell.PLMNKnown || cell.GlobalCellID > 0xfffffff {
		return "", errors.New("LTE serving cell unavailable")
	}
	value := fmt.Sprintf("3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=%s%04X%07X", cell.PLMN.String(), cell.TrackingAreaCode, cell.GlobalCellID)
	return lte.NormalizeAccessNetworkInfo(value)
}

func normalPublicIdentity(records [][]byte) (string, error) {
	var populated []string
	for _, record := range records {
		var text simfile.Text
		if text.UnmarshalBinary(record) == nil && text != "" {
			populated = append(populated, string(text))
		}
	}
	if len(populated) == 0 {
		return "", errors.New("public identity unavailable")
	}
	index := 0
	if len(populated) >= 2 {
		index = 1
	}
	identity := populated[index]
	if !(strings.HasPrefix(identity, "sip:") || strings.HasPrefix(identity, "tel:")) || strings.ContainsAny(identity, "\r\n\t <>") {
		return "", errors.New("invalid public identity")
	}
	return identity, nil
}

func resolvePCSCF(ctx context.Context, records [][]byte, lookup func(context.Context, string) ([]net.IPAddr, error)) []net.IP {
	var ips []net.IP
	seen := map[string]bool{}
	appendIP := func(ip net.IP) {
		if ip.IsGlobalUnicast() && !seen[ip.String()] {
			seen[ip.String()] = true
			ips = append(ips, ip)
		}
	}
	for _, record := range records {
		address, err := pcscfAddress(record)
		if err != nil {
			continue
		}
		if ip := net.ParseIP(address); ip != nil {
			appendIP(ip)
		} else if lookup != nil {
			queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			resolved, err := lookup(queryCtx, address)
			cancel()
			if err == nil {
				for _, ip := range resolved {
					appendIP(ip.IP)
				}
			}
		}
	}
	return ips
}

func normalizedPSI(psi string) string {
	psi = strings.TrimSpace(psi)
	if psi == "" || strings.ContainsAny(psi, "\r\n\t <>") {
		return ""
	}
	if strings.HasPrefix(psi, "sip:") || strings.HasPrefix(psi, "sips:") || strings.HasPrefix(psi, "tel:") {
		return psi
	}
	return "sip:" + psi
}

func storeSMS(directory string, message ims.SMS) error {
	identity, err := json.Marshal([]string{message.CallID, message.From, message.To, message.Text})
	if err != nil {
		return err
	}
	return storePrivateJSON(directory, identity, message)
}

func storeIncomingSegment(directory string, message imssms.Incoming) error {
	identity, err := json.Marshal([]any{message.CallID, message.RawTPDU})
	if err != nil {
		return err
	}
	return storePrivateJSON(directory, identity, message)
}

func storePrivateJSON(directory string, identity []byte, value any) error {
	key := sha256.Sum256(identity)
	file, err := os.CreateTemp(directory, ".incoming-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(directory, hex.EncodeToString(key[:])+".json")); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func prepareInbox(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("inbox directory is not private")
	}
	return nil
}
