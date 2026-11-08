// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/damonto/wwan-go/qcom"
	"github.com/damonto/wwan-go/qcom/qmi"
	"github.com/damonto/wwan-go/qcom/tlv"
	"github.com/damonto/wwan-go/sim"
	simcard "github.com/damonto/wwan-go/sim/card"
	"github.com/damonto/wwan-go/sim/command"
	"github.com/damonto/wwan-go/sim/simfile"
	simtlv "github.com/damonto/wwan-go/sim/tlv"
)

// Only fixed stage names, numeric protocol errors, and capability flags may
// reach stdout. Library logs can contain subscriber identities and SMS bodies.
func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	command := ""
	if len(os.Args) >= 2 {
		command = os.Args[1]
	}
	if command == "messages" && len(os.Args) == 3 {
		os.Exit(messagesCommand(os.Args[2], os.Stdin))
	}
	if command == "control" && len(os.Args) == 3 {
		os.Exit(controlCommand(os.Args[2], os.Stdin))
	}
	sessionCommand := command == "probe-registration" || command == "run" || command == "send-test"
	if len(os.Args) < 2 || len(os.Args) > 3 || (!sessionCommand && command != "probe-pdn" && command != "probe-sim" && command != "probe-slots" && command != "probe-sms" && command != "probe-nas" && command != "configure-ims") || (len(os.Args) == 3 && !sessionCommand) {
		emit(map[string]any{"tool": "ModemConfig-KO", "state": "error", "stage": "usage", "commands": []string{"probe-pdn", "probe-sim", "probe-slots", "probe-sms", "probe-nas", "configure-ims", "probe-registration", "run", "send-test"}})
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	duration := 45 * time.Second
	if sessionCommand {
		duration = 150 * time.Second
	}
	if command != "run" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, duration)
		defer cancel()
	}
	var err error
	if command == "probe-sim" {
		err = probeSIM(ctx)
	} else if command == "probe-slots" {
		err = probeSlots(ctx)
	} else if command == "configure-ims" {
		err = configureIMS(ctx)
	} else if command == "probe-sms" || command == "probe-nas" {
		err = probeSMS(ctx, command == "probe-nas")
	} else if sessionCommand {
		mode := "tta4-null"
		if len(os.Args) == 3 {
			mode = os.Args[2]
		}
		var outgoing *outgoingSMS
		if command == "send-test" {
			outgoing, err = readOutgoingSMS(os.Stdin)
			if err != nil {
				_ = failed("sms-input", err)
				os.Exit(2)
			}
		}
		window := 45 * time.Second
		if command == "run" {
			window = 0
		}
		err = registrationSession(ctx, mode, window, outgoing)
	} else {
		err = probePDN(ctx)
	}
	if err != nil {
		os.Exit(1)
	}
}

func emit(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }

func failed(stage string, err error) error {
	emit(diagnostic(stage, err))
	return err
}

func diagnostic(stage string, err error) map[string]any {
	out := map[string]any{"state": "error", "stage": stage}
	var code qcom.QMIError
	if errors.As(err, &code) {
		out["qmi_error"] = uint16(code)
	}
	var call *qcom.WDSStartNetworkError
	if errors.As(err, &call) {
		out["operation"] = "start-network"
		if call.HasCallEndReason {
			out["call_end_reason"] = uint16(call.CallEndReason)
		}
		if call.HasVerboseCallEndReason {
			out["reason_type"] = uint16(call.VerboseCallEndReason.Type)
			out["reason"] = call.VerboseCallEndReason.Reason
		}
	} else {
		for _, name := range []string{"bind WDS legacy mux data port", "bind WDS", "read runtime settings", "read NAS system info"} {
			if strings.Contains(err.Error(), name) {
				out["operation"] = name
				break
			}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		out["timeout"] = true
	}
	return out
}

func openQCOM(ctx context.Context) (*qcom.Client, error) {
	client, err := openQCOMSlot(ctx, 1)
	if err != nil {
		return nil, err
	}
	slot, info, err := activeSIMSlot(ctx, client)
	if err != nil {
		_ = client.Close()
		return nil, failed("sim-slot-selection", err)
	}
	if slot != client.Slot() {
		if err := client.Close(); err != nil {
			return nil, failed("qmi-close", err)
		}
		client, err = openQCOMSlot(ctx, slot)
		if err != nil {
			return nil, err
		}
		actual, _, err := activeSIMSlot(ctx, client)
		if err != nil || actual != slot {
			_ = client.Close()
			return nil, failed("sim-slot-selection", errors.New("SIM mapping changed"))
		}
	}
	emit(info)
	return client, nil
}

func openQCOMSlot(ctx context.Context, slot uint8) (*qcom.Client, error) {
	transport, err := qmi.Open(ctx, qmi.WithProxy("/dev/wwan0qmi0"))
	if err != nil {
		return nil, failed("qmi-proxy", err)
	}
	client, err := qcom.NewClient(&legacyIPv6Transport{wire: transport}, qcom.WithSlot(slot))
	if err != nil {
		_ = transport.Close()
		return nil, failed("qmi-client", err)
	}
	return client, nil
}

func probePDN(ctx context.Context) (result error) {
	client, err := openQCOM(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = failed("qmi-close", err)
		}
	}()
	pco, imcn := true, true
	profile, err := client.WDSCreateProfileWithConfig(ctx, qcom.WDSProfileConfig{
		Type: qcom.WDSProfileType3GPP, Name: "modemconfig-ko-probe", APN: "ims",
		PDPType: qcom.WDSPDPTypeIPv6, PCSCFUsingPCO: &pco, IMCN: &imcn,
	})
	if err != nil {
		return failed("ims-profile-create", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.WDSDeleteProfile(cleanup, profile.Index); err != nil {
			result = failed("ims-profile-delete", err)
		} else {
			emit(map[string]any{"state": "removed", "stage": "ims-profile"})
		}
	}()
	settings, err := client.WDSProfileSettings(ctx, profile)
	if err != nil {
		return failed("ims-profile-readback", err)
	}
	emit(map[string]any{"state": "available", "stage": "ims-profile", "pco": settings.PCSCFUsingPCO, "imcn": settings.IMCN})
	// RMNET0 belongs to the ordinary Internet bearer. This command owns only
	// its WDS client on RMNET1 and never changes host addresses or routes.
	session, err := client.OpenIMSPDN(ctx, qcom.IMSPDNConfig{
		APN: "ims", IPPreference: qcom.WDSIPPreferenceIPv6,
		ProfileIndex:      profile.Index,
		LegacyMuxDataPort: qcom.WDSSIOPortA2MuxRMNET1,
		RequestTimeout:    20 * time.Second,
	})
	if err != nil {
		return failed("ims-pdn", err)
	}
	info := session.Info()
	emit(map[string]any{
		"state": "connected", "stage": "ims-pdn",
		"ipv4": len(info.LocalIPv4) != 0, "ipv6": len(info.LocalIPv6) != 0,
		"pcscf_count": len(info.PCSCFIPs), "imcn": info.IMCN,
		"vops_known": info.VoPSKnown, "vops_supported": info.VoPSSupported,
	})
	if err := session.Close(); err != nil {
		return failed("ims-pdn-close", err)
	}
	emit(map[string]any{"state": "closed", "stage": "ims-pdn"})
	return nil
}

// wwan-go 0.0.1 omits Set Client IP Family for legacy SIO bindings. A8 needs
// the same bind -> select IPv6 -> start sequence used by ModemManager 1.24.
// This wrapper applies only to our dedicated RMNET1 client, never other ports.
type qmiWire interface {
	qcom.IndicationTransport
	TerminalError() error
}

type legacyIPv6Transport struct {
	wire qmiWire
}

func (t *legacyIPv6Transport) Close() error         { return t.wire.Close() }
func (t *legacyIPv6Transport) TerminalError() error { return t.wire.TerminalError() }
func (t *legacyIPv6Transport) Indications(ctx context.Context, service qcom.ServiceType, client uint8, id qcom.MessageID) (<-chan qcom.Indication, error) {
	return t.wire.Indications(ctx, service, client, id)
}

func (t *legacyIPv6Transport) Do(ctx context.Context, req qcom.Request) (qcom.Response, error) {
	if req.Service == qcom.ServiceWDS && req.MessageID == qcom.MessageWDSStopNetworkInterface {
		statsReq := (qcom.WDSGetPacketStatisticsRequest{
			ClientID: req.ClientID, TransactionID: req.TransactionID, Timeout: 3 * time.Second,
			Mask: qcom.WDSStatisticsTxPackets | qcom.WDSStatisticsRxPackets,
		}).Request()
		if response, err := t.wire.Do(ctx, statsReq); err == nil && qmiResult(response) == nil {
			var parsed qcom.WDSGetPacketStatisticsResponse
			if parsed.UnmarshalTLVs(response.TLVs) == nil {
				stats := parsed.Statistics
				if stats.TxPacketsKnown && stats.RxPacketsKnown {
					emit(map[string]any{"stage": "ims-modem-traffic", "tx_packets": stats.TxPackets, "rx_packets": stats.RxPackets})
				}
			}
		}
	}
	resp, err := t.wire.Do(ctx, req)
	if req.Service == qcom.ServiceWDS && err == nil {
		if code, ok := profileExtendedError(resp); ok {
			emit(map[string]any{"stage": "modem-profile-error", "extended_error": code, "message_id": uint16(req.MessageID)})
		}
	}
	if req.Service == qcom.ServiceWDS && req.MessageID == qcom.MessageWDSGetRuntimeSettings && err == nil {
		layout := map[uint8]int{}
		for _, item := range resp.TLVs {
			layout[item.Type] = len(item.Value)
		}
		emit(map[string]any{"stage": "ims-runtime-layout", "tlvs": layout})
	}
	if err != nil || req.Service != qcom.ServiceWDS || req.MessageID != qcom.MessageWDSLegacyBindMuxDataPort {
		return resp, err
	}

	port, ok := tlv.Value(req.TLVs, 1)
	if !ok || len(port) != 2 || binary.LittleEndian.Uint16(port) != uint16(qcom.WDSSIOPortA2MuxRMNET1) {
		return resp, nil
	}
	if err := qmiResult(resp); err != nil {
		return resp, err
	}
	family := req
	family.MessageID = qcom.MessageWDSSetClientIPFamily
	family.TLVs = tlv.TLVs{tlv.Uint(1, uint8(6))}
	selected, err := t.wire.Do(ctx, family)
	if err != nil {
		return resp, err
	}
	if err := qmiResult(selected); err != nil {
		return resp, err
	}
	return resp, nil
}

func profileExtendedError(response qcom.Response) (uint16, bool) {
	if !errors.Is(qmiResult(response), qcom.QMIErrorExtendedInternal) {
		return 0, false
	}
	value, ok := tlv.Value(response.TLVs, 0xe0)
	if !ok || len(value) != 2 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(value), true
}

func probeSIM(ctx context.Context) (result error) {
	client, err := openQCOM(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = failed("qmi-close", err)
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
	center := card.ServiceCenter()
	info := map[string]any{
		"stage": "sim-capabilities", "state": "available", "plmn": card.MCC() + card.MNC(),
		"private_identity": card.PrivateIdentity() != "", "public_identity": card.PublicIdentity() != "",
		"home_domain": card.HomeDomain() != "", "smsc": center.Address != "", "sms_psi": center.PSI != "",
	}
	aid, err := (command.FindAID{Prefix: []byte{0xa0, 0, 0, 0, 0x87, 0x10, 4}}).Run(ctx, reader)
	info["isim"] = err == nil
	if err == nil {
		app := command.App{Reader: reader, AID: aid}
		attrs, err := reader.FileAttributes(ctx, simcard.FileRef{AID: aid, Path: []byte{0x6f, 4}})
		if err == nil {
			info["impu_records"] = attrs.RecordCount
		}
		if records, err := app.LinearFixed(ctx, []byte{0x6f, 4}); err == nil {
			populated := 0
			for _, record := range records {
				var value simfile.Text
				if value.UnmarshalBinary(record) == nil && value != "" {
					populated++
				}
			}
			info["impu_populated"] = populated
		}
		attrs, err = reader.FileAttributes(ctx, simcard.FileRef{AID: aid, Path: []byte{0x6f, 9}})
		info["pcscf_file"] = err == nil
		if err == nil {
			info["pcscf_structure"] = attrs.FileStructure
			info["pcscf_records"] = attrs.RecordCount
		}
		if records, err := app.LinearFixed(ctx, []byte{0x6f, 9}); err == nil {
			counts := map[string]int{}
			for _, record := range records {
				address, err := pcscfAddress(record)
				if err != nil || address == "" {
					continue
				}
				ip := net.ParseIP(address)
				kind := "domain"
				if ip != nil {
					if ip.To4() != nil {
						kind = "ipv4"
					} else {
						kind = "ipv6"
					}
				}
				counts[kind]++
			}
			info["pcscf_populated"] = counts
		}
		if psi, err := app.FirstText(ctx, []byte{0x6f, 0xe5}); err == nil {
			info["isim_direct_psi"] = psi != ""
		}
	}
	usimAID, err := (command.FindAID{Prefix: []byte{0xa0, 0, 0, 0, 0x87, 0x10, 2}}).Run(ctx, reader)
	if err == nil {
		if psi, err := (command.App{Reader: reader, AID: usimAID}).FirstText(ctx, []byte{0x6f, 0xe5}); err == nil {
			info["usim_direct_psi"] = psi != ""
		}
	}
	emit(info)
	return nil
}

func pcscfAddress(record []byte) (string, error) {
	item, consumed, err := simtlv.Consume(record)
	if err != nil {
		return "", err
	}
	for _, padding := range record[consumed:] {
		if padding != 0xff && padding != 0 {
			return "", errors.New("invalid P-CSCF record padding")
		}
	}
	if item.Tag != 0x80 || len(item.Value) < 2 {
		return "", errors.New("invalid P-CSCF record")
	}
	data := item.Value[1:]
	switch item.Value[0] {
	case 0:
		for _, c := range data {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-') {
				return "", errors.New("invalid P-CSCF domain")
			}
		}
		return string(data), nil
	case 1:
		if len(data) == 4 {
			return net.IP(data).String(), nil
		}
	case 2:
		if len(data) == 16 {
			return net.IP(data).String(), nil
		}
	}
	return "", errors.New("invalid P-CSCF address")
}

func koreanIMSProfile(plmn string) (qcom.WDSProfileConfig, error) {
	switch plmn {
	case "45005", "45008", "45006":
	default:
		return qcom.WDSProfileConfig{}, errors.New("unsupported home network")
	}
	pco, imcn := true, true
	return qcom.WDSProfileConfig{Type: qcom.WDSProfileType3GPP, Name: "ModemConfig-KO", APN: "ims", PDPType: qcom.WDSPDPTypeIPv4v6, PCSCFUsingPCO: &pco, IMCN: &imcn}, nil
}

func configureIMS(ctx context.Context) (result error) {
	client, err := openQCOM(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = failed("qmi-close", err)
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
	scope, err := simScope(card.ICCID())
	if err != nil {
		return failed("sim-scope", err)
	}
	_, err = ensureIMSProfile(ctx, client, card.MCC()+card.MNC(), scope)
	return err
}

func ensureIMSProfile(ctx context.Context, client *qcom.Client, plmn, scope string) (qcom.WDSProfileID, error) {
	cfg, err := koreanIMSProfile(plmn)
	if err != nil {
		return qcom.WDSProfileID{}, failed("ims-home-network", err)
	}
	cfg.Name, err = simProfileName(scope)
	if err != nil {
		return qcom.WDSProfileID{}, failed("sim-scope", err)
	}
	profiles, err := client.WDSProfiles(ctx, qcom.WDSProfileType3GPP)
	if err != nil {
		return qcom.WDSProfileID{}, failed("ims-profiles", err)
	}
	var id qcom.WDSProfileID
	action := "reused"
	for _, profile := range profiles {
		if profile.Name == cfg.Name || (len(profile.Name) == len(cfg.Name)-1 && strings.HasPrefix(cfg.Name, profile.Name)) {
			settings, err := client.WDSProfileSettings(ctx, profile.ID)
			if err != nil {
				return id, failed("ims-profile-read", err)
			}
			if settings.Name == cfg.Name {
				id = profile.ID
				break
			}
			emit(map[string]any{"stage": "ims-profile-name", "requested_bytes": len(cfg.Name), "list_bytes": len(profile.Name), "settings_bytes": len(settings.Name)})
			return id, failed("ims-profile-name", errors.New("modem profile name was truncated"))
		}
	}
	if id.Index == 0 {
		action = "created"
		id, err = client.WDSCreateProfileWithConfig(ctx, cfg)
		if err != nil {
			return id, failed("ims-profile-create", err)
		}
	} else {
		settings, err := client.WDSProfileSettings(ctx, id)
		if err != nil {
			return id, failed("ims-profile-read", err)
		}
		if settings.APN != "ims" {
			return id, failed("ims-profile-name-conflict", errors.New("profile belongs to another APN"))
		}
		if !settings.PCSCFUsingPCO || !settings.IMCN || settings.PDPType != cfg.PDPType {
			action = "updated"
			err = client.WDSUpdateProfile(ctx, id, qcom.WDSProfileUpdate{PCSCFUsingPCO: cfg.PCSCFUsingPCO, IMCN: cfg.IMCN, PDPType: &cfg.PDPType})
			if err != nil {
				return id, failed("ims-profile-update", err)
			}
		}
	}
	settings, err := client.WDSProfileSettings(ctx, id)
	if err != nil {
		return id, failed("ims-profile-readback", err)
	}
	if settings.Name != cfg.Name {
		emit(map[string]any{"stage": "ims-profile-name", "requested_bytes": len(cfg.Name), "settings_bytes": len(settings.Name)})
		return id, failed("ims-profile-name", errors.New("profile name readback mismatch"))
	}
	if settings.APN != "ims" || !settings.PCSCFUsingPCO || !settings.IMCN || settings.PDPType != cfg.PDPType {
		return id, failed("ims-profile-verify", errors.New("profile readback mismatch"))
	}
	emit(map[string]any{"stage": "ims-profile", "state": "saved", "action": action, "sim_scoped": true, "profile": id.Index, "apn": "ims", "pdp": "ipv4v6", "pco": settings.PCSCFUsingPCO, "imcn": settings.IMCN})
	return id, nil
}

func qmiResult(resp qcom.Response) error {
	value, ok := tlv.Value(resp.TLVs, 2)
	if !ok || len(value) != 4 {
		return errors.New("invalid QMI result")
	}
	if binary.LittleEndian.Uint16(value) != 0 {
		return qcom.QMIError(binary.LittleEndian.Uint16(value[2:]))
	}
	return nil
}
