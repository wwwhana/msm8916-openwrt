// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/damonto/wwan-go/qcom"
	"github.com/damonto/wwan-go/qcom/tlv"
)

type fakeWire struct {
	requests []qcom.Request
	fail     qcom.MessageID
}

func TestProfileErrorRequiresFailedResultAndCompleteNumericCode(t *testing.T) {
	response := qcom.Response{TLVs: tlv.TLVs{tlv.Bytes(2, []byte{1, 0, 81, 0}), tlv.Bytes(0xe0, []byte{6, 16})}}
	if code, ok := profileExtendedError(response); !ok || code != 4102 {
		t.Fatal("extended profile failure was discarded")
	}
	for _, response := range []qcom.Response{
		{TLVs: tlv.TLVs{tlv.Bytes(2, []byte{0, 0, 0, 0}), tlv.Bytes(0xe0, []byte{6, 16})}},
		{TLVs: tlv.TLVs{tlv.Bytes(2, []byte{1, 0, 81, 0}), tlv.Bytes(0xe0, []byte{6})}},
	} {
		if _, ok := profileExtendedError(response); ok {
			t.Fatal("success or malformed extension reported as a profile failure")
		}
	}
}

func (f *fakeWire) Do(_ context.Context, req qcom.Request) (qcom.Response, error) {
	f.requests = append(f.requests, req)
	if req.MessageID == f.fail {
		return qcom.Response{}, errors.New("transport failed")
	}
	return qcom.Response{TLVs: tlv.TLVs{tlv.Bytes(2, []byte{0, 0, 0, 0})}}, nil
}
func (*fakeWire) Close() error         { return nil }
func (*fakeWire) TerminalError() error { return nil }
func (*fakeWire) Indications(context.Context, qcom.ServiceType, uint8, qcom.MessageID) (<-chan qcom.Indication, error) {
	return nil, nil
}

func TestModemConfigKOIPv6SequenceAndInternetIsolation(t *testing.T) {
	f := &fakeWire{}
	w := legacyIPv6Transport{wire: f}
	bind := qcom.WDSLegacyBindMuxDataPortRequest{ClientID: 4, DataPort: qcom.WDSSIOPortA2MuxRMNET1}.Request()
	if _, err := w.Do(context.Background(), bind); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 || f.requests[1].MessageID != qcom.MessageWDSSetClientIPFamily {
		t.Fatal("IPv6 selection must follow legacy binding")
	}
	value, _ := tlv.Value(f.requests[1].TLVs, 1)
	if len(value) != 1 || value[0] != 6 {
		t.Fatal("wrong IP family")
	}
	before := len(f.requests)
	bind = qcom.WDSLegacyBindMuxDataPortRequest{ClientID: 9, DataPort: qcom.WDSSIOPortA2MuxRMNET0}.Request()
	if _, err := w.Do(context.Background(), bind); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != before+1 {
		t.Fatal("Internet port must not receive IPv6 override")
	}
}

func TestModemConfigKOBindingFailureStopsSequence(t *testing.T) {
	for _, fail := range []qcom.MessageID{qcom.MessageWDSLegacyBindMuxDataPort, qcom.MessageWDSSetClientIPFamily} {
		f := &fakeWire{fail: fail}
		w := legacyIPv6Transport{wire: f}
		bind := qcom.WDSLegacyBindMuxDataPortRequest{ClientID: 4, DataPort: qcom.WDSSIOPortA2MuxRMNET1}.Request()
		if _, err := w.Do(context.Background(), bind); err == nil {
			t.Fatal("failed binding accepted")
		}
		if fail == qcom.MessageWDSLegacyBindMuxDataPort && len(f.requests) != 1 {
			t.Fatal("family selected after failed bind")
		}
	}
}

func TestDiagnosticsExcludePrivateErrorText(t *testing.T) {
	err := fmt.Errorf("subscriber-private-value: %w", qcom.QMIErrorCallFailed)
	data, errJSON := json.Marshal(diagnostic("ims-pdn", err))
	if errJSON != nil {
		t.Fatal(errJSON)
	}
	if strings.Contains(string(data), "subscriber-private-value") {
		t.Fatal("private error text leaked")
	}
	if !strings.Contains(string(data), `"qmi_error":14`) {
		t.Fatal("numeric error missing")
	}
}

func TestRejectMalformedQMIResult(t *testing.T) {
	for _, value := range [][]byte{nil, {0}, {0, 0, 0}, {0, 0, 0, 0, 0}} {
		if qmiResult(qcom.Response{TLVs: tlv.TLVs{tlv.Bytes(2, value)}}) == nil {
			t.Fatal("malformed QMI result accepted")
		}
	}
	if err := qmiResult(qcom.Response{TLVs: tlv.TLVs{tlv.Bytes(2, []byte{1, 0, 48, 0})}}); !errors.Is(err, qcom.QMIErrorInvalidArgument) {
		t.Fatal("protocol failure lost")
	}
}

func TestKoreanIMSProfile(t *testing.T) {
	for _, plmn := range []string{"45005", "45008", "45006"} {
		cfg, err := koreanIMSProfile(plmn)
		if err != nil || cfg.Name != "ModemConfig-KO" || cfg.APN != "ims" || cfg.PDPType != qcom.WDSPDPTypeIPv4v6 || !*cfg.PCSCFUsingPCO || !*cfg.IMCN {
			t.Fatalf("invalid IMS profile for %s", plmn)
		}
	}
	if _, err := koreanIMSProfile("450001"); err == nil {
		t.Fatal("unknown carrier accepted")
	}
}

func TestSIMPCSCFRecords(t *testing.T) {
	for _, sample := range []struct{ data, want string }{
		{"800501c0000201ffff", "192.0.2.1"},
		{"80110220010db8000000000000000000000001ff", "2001:db8::1"},
		{"800e0070637363662e746573742e6b72ff", "pcscf.test.kr"},
	} {
		raw, _ := hex.DecodeString(sample.data)
		got, err := pcscfAddress(raw)
		if err != nil || got != sample.want {
			t.Fatalf("PCSCF decode: %q %v", got, err)
		}
	}
	for _, bad := range []string{"", "ffff", "800501c00002", "800402010203", "8003006120", "810501c0000201"} {
		raw, _ := hex.DecodeString(bad)
		if _, err := pcscfAddress(raw); err == nil {
			t.Fatalf("invalid PCSCF accepted: %s", bad)
		}
	}
}

func TestTrafficQueryFailureDoesNotPreventSessionClose(t *testing.T) {
	f := &fakeWire{fail: qcom.MessageWDSGetPacketStatistics}
	w := legacyIPv6Transport{wire: f}
	_, err := w.Do(context.Background(), qcom.Request{Service: qcom.ServiceWDS, ClientID: 7, TransactionID: 9, MessageID: qcom.MessageWDSStopNetworkInterface})
	if err != nil || len(f.requests) != 2 || f.requests[0].ClientID != 7 || f.requests[0].TransactionID != 9 || f.requests[1].MessageID != qcom.MessageWDSStopNetworkInterface {
		t.Fatal("traffic query prevented owned session cleanup")
	}
}
