// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	ims "github.com/damonto/ims-go"
	imssms "github.com/damonto/ims-go/ims/sms"
	"github.com/damonto/wwan-go/qcom"
)

func TestSIMPCSCFResolutionUsesSelectedDNSAndFiltersAddresses(t *testing.T) {
	var records [][]byte
	for _, encoded := range []string{"800501c0000201ffff", "80110220010db8000000000000000000000001ff", "800e0070637363662e746573742e6b72ff", "810501c0000201"} {
		data, _ := hex.DecodeString(encoded)
		records = append(records, data)
	}
	lookups := 0
	lookup := func(ctx context.Context, name string) ([]net.IPAddr, error) {
		lookups++
		if name != "pcscf.test.kr" {
			t.Fatal("lookup received a literal or invalid SIM record")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("DNS lookup has no deadline")
		}
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("2001:db8::2")}, {IP: net.ParseIP("::1")}, {IP: net.ParseIP("fe80::1")}}, nil
	}
	resolved := resolvePCSCF(t.Context(), records, lookup)
	if lookups != 1 || len(resolved) != 3 || !resolved[2].Equal(net.ParseIP("2001:db8::2")) {
		t.Fatal("selected DNS, duplicate or non-global address handling failed")
	}
	if got := resolvePCSCF(t.Context(), records, nil); len(got) != 2 {
		t.Fatal("literal-only discovery used DNS")
	}
	if got := resolvePCSCF(t.Context(), records, func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("DNS unavailable") }); len(got) != 2 {
		t.Fatal("DNS failure discarded literal P-CSCF addresses")
	}
	network := &imsNetwork{pcscf: resolved}
	copy := network.PCSCFIPs()
	copy[0][0] ^= 0xff
	if !network.pcscf[0].Equal(net.ParseIP("192.0.2.1")) {
		t.Fatal("caller modified the owned route addresses")
	}
}

func TestOutgoingSMSRequiresOneValidPrivateRequest(t *testing.T) {
	valid := `{"to":"+821000000000","text":"하이"}`
	request, err := readOutgoingSMS(strings.NewReader(valid))
	if err != nil || request.Text != "하이" || request.To != "+821000000000" {
		t.Fatal("valid single-message input was changed or rejected")
	}
	for _, input := range []string{
		valid + valid, strings.Repeat("x", 4097),
		`{"to":"+821000000000","text":"하이","extra":true}`,
		`{"to":"+821000000000\r\nInjected","text":"하이"}`,
		`{"to":"+821000000000","text":" "}`,
		`{"to":"+821000000000","text":"` + strings.Repeat("한", 71) + `"}`,
		`{"to":"+821000000000","text":"` + strings.Repeat("😀", 36) + `"}`,
	} {
		if _, err := readOutgoingSMS(strings.NewReader(input)); err == nil {
			t.Fatal("invalid or multipart test request accepted")
		}
	}
}

func TestRuntimeStatusIsAtomicAndPrivate(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "status.json")
	if err := writeRuntimeStatus(path, 2, true, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	var state map[string]any
	if err != nil || json.Unmarshal(data, &state) != nil || state["registration"] != "registered" || state["sms"] != "ready" || state["slot"] != float64(2) {
		t.Fatal("runtime status lost registration or SIM selection")
	}
	info, _ := os.Stat(path)
	files, _ := os.ReadDir(directory)
	if info.Mode().Perm() != 0600 || len(files) != 1 {
		t.Fatal("runtime state is public or leaves temporary files")
	}
}

func TestLibraryDiagnosticNeverCopiesPrivateAttributes(t *testing.T) {
	for _, message := range []string{"rejecting inbound sms deliver", "reconnecting client", "sms-pp data download delivered"} {
		record := slog.NewRecord(time.Now(), slog.LevelDebug, message, 0)
		record.AddAttrs(slog.Any("err", errors.New("truncated private-subscriber-message")), slog.String("from", "private-subscriber-message"))
		data, err := json.Marshal(libraryDiagnostic(record))
		if err != nil || strings.Contains(string(data), "private-subscriber-message") {
			t.Fatal("library diagnostics exposed private metadata")
		}
	}
}

func TestNetworkDiagnosticReportsFamiliesWithoutAddresses(t *testing.T) {
	record := slog.NewRecord(time.Now(), slog.LevelWarn, "reconnecting client", 0)
	record.AddAttrs(slog.Any("cause", &net.OpError{
		Op: "write", Net: "udp",
		Source: &net.UDPAddr{IP: net.ParseIP("2001:db8::1234"), Port: 43210},
		Addr:   &net.UDPAddr{IP: net.ParseIP("192.0.2.123"), Port: 54321},
		Err:    syscall.ENETUNREACH,
	}))
	event := libraryDiagnostic(record)
	data, err := json.Marshal(event)
	if err != nil || event["network_op"] != "write" || event["local_family"] != 6 || event["target_family"] != 4 || event["errno"] != uint64(syscall.ENETUNREACH) {
		t.Fatal("network failure diagnostics lost operation, address families or error code")
	}
	for _, private := range []string{"2001:db8", "192.0.2", "43210", "54321"} {
		if strings.Contains(string(data), private) {
			t.Fatal("network diagnostic exposed an endpoint")
		}
	}
	if addressFamily((*net.UDPAddr)(nil)) != 0 || addressFamily(nil) != 0 {
		t.Fatal("unknown address reported as a known IP family")
	}
}

func TestTTASelectsNormalIdentity(t *testing.T) {
	first := []byte("sip:emergency@example.invalid")
	second := []byte("sip:normal@example.invalid")
	third := []byte("tel:+820000000000")
	identity, err := normalPublicIdentity([][]byte{first, second, third, {0xff, 0xff}})
	if err != nil || identity != string(second) {
		t.Fatal("normal identity was not selected")
	}
	identity, err = normalPublicIdentity([][]byte{first})
	if err != nil || identity != string(first) {
		t.Fatal("single identity must serve both roles")
	}
	for _, records := range [][][]byte{nil, {{0xff}}, {[]byte("sip:bad\r\nInjected: value")}, {[]byte("not-a-uri")}} {
		if _, err := normalPublicIdentity(records); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestTTAAccessInfoUsesActualServingPLMN(t *testing.T) {
	var plmn qcom.NASPLMN
	if err := plmn.UnmarshalText([]byte("45006")); err != nil {
		t.Fatal(err)
	}
	info := qcom.NASCellLocationInfo{LTEIntraKnown: true, LTEIntra: qcom.NASLTEIntraFrequency{
		PLMN: plmn, PLMNKnown: true, TrackingAreaCode: 0x1234, GlobalCellID: 0xabcdef,
	}}
	value, err := accessNetworkInfo(info)
	if err != nil || value != "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=4500612340ABCDEF" {
		t.Fatal("incorrect serving cell encoding")
	}
	info.LTEIntra.GlobalCellID = 0x10000000
	if _, err := accessNetworkInfo(info); err == nil {
		t.Fatal("invalid E-UTRAN cell accepted")
	}
	if _, err := accessNetworkInfo(qcom.NASCellLocationInfo{}); err == nil {
		t.Fatal("missing serving cell accepted")
	}
}

func TestSMSPSIUsesSIPURI(t *testing.T) {
	for input, want := range map[string]string{
		"ipsmgw.example.invalid":     "sip:ipsmgw.example.invalid",
		"sip:ipsmgw.example.invalid": "sip:ipsmgw.example.invalid",
		"tel:+820000000000":          "tel:+820000000000", "": "", "bad\r\nvalue": "",
	} {
		if got := normalizedPSI(input); got != want {
			t.Fatal("incorrect SMS service URI")
		}
	}
}

func TestIncomingSMSIsPrivateAndDurable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	if err := prepareInbox(dir); err != nil {
		t.Fatal(err)
	}
	message := ims.SMS{CallID: "test-call", From: "test-sender", Text: "test-body", ReceivedAt: time.Unix(1, 0)}
	for range 2 {
		if err := storeSMS(dir, message); err != nil {
			t.Fatal(err)
		}
		message.ReceivedAt = message.ReceivedAt.Add(time.Second)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("message missing, duplicated or temporary file retained")
	}
	info, _ := files[0].Info()
	if info.Mode().Perm() != 0600 || strings.Contains(files[0].Name(), "test-") {
		t.Fatal("private message metadata exposed")
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	var saved ims.SMS
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Text != message.Text || saved.From != message.From {
		t.Fatal("message contents not preserved")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := prepareInbox(dir); err == nil {
		t.Fatal("nonprivate inbox accepted")
	}
}

func TestSIMStorageSeparatesCardsAndRetainsIncomingSegments(t *testing.T) {
	first, err := simScope("8900000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := simScope("8900000000000000001")
	second, _ := simScope("8900000000000000002")
	if first != again || first == second || strings.Contains(first, "890000") {
		t.Fatal("SIM scopes are unstable, shared or expose the identifier")
	}
	firstName, err := simProfileName(first)
	secondName, secondErr := simProfileName(second)
	if err != nil || secondErr != nil || len(firstName) > 15 || firstName == secondName {
		t.Fatal("modem profile names exceed the legacy limit or share a SIM scope")
	}
	root := filepath.Join(t.TempDir(), "sims")
	inbox, segments, err := scopedSIMPaths(root, first)
	if err != nil {
		t.Fatal(err)
	}
	otherInbox, _, err := scopedSIMPaths(root, second)
	if err != nil || inbox == otherInbox {
		t.Fatal("different SIMs share an inbox")
	}
	message := imssms.Incoming{CallID: "part-call", Text: "first segment", RawTPDU: []byte{1, 2, 3}}
	for range 2 {
		if err := storeIncomingSegment(segments, message); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(segments)
	if err != nil || len(files) != 1 {
		t.Fatal("incoming segment was lost or duplicated")
	}
	data, err := os.ReadFile(filepath.Join(segments, files[0].Name()))
	var saved imssms.Incoming
	if err != nil || json.Unmarshal(data, &saved) != nil || !bytes.Equal(saved.RawTPDU, message.RawTPDU) {
		t.Fatal("durable segment lost its TPDU")
	}
	if _, _, err := scopedSIMPaths(root, "../invalid"); err == nil {
		t.Fatal("invalid storage scope accepted")
	}
	for _, id := range []string{"", "8900", "890000000000000000x", "8900000000000000001\n"} {
		if _, err := simScope(id); err == nil {
			t.Fatal("invalid SIM identifier accepted")
		}
	}
}

func TestSIMSelectionFollowsPrimaryLogicalSlot(t *testing.T) {
	present := qcom.Slot{PhysicalCardStatus: qcom.PhysicalCardStatePresent, PhysicalSlotStatus: qcom.SlotStateActive, LogicalSlot: 1}
	secondary := present
	secondary.LogicalSlot = 2
	selected, err := primaryPhysicalSlot(qcom.SlotStatus{Slots: []qcom.Slot{secondary, present}})
	if err != nil || selected != 2 {
		t.Fatal("primary data SIM was confused with physical slot 1")
	}
	for _, slots := range [][]qcom.Slot{nil, {secondary}, {present, present}, {{PhysicalSlotStatus: qcom.SlotStateActive, LogicalSlot: 1}}} {
		if _, err := primaryPhysicalSlot(qcom.SlotStatus{Slots: slots}); err == nil {
			t.Fatal("missing, ambiguous or empty SIM mapping accepted")
		}
	}
}

func TestNetworkCleanupRemovesOnlySuccessfulAdditions(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	program := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$A8_IMS_TEST_LOG\"\ncase \"$*\" in *reject*) exit 1;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "ip"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("A8_IMS_TEST_LOG", log)
	network := &imsNetwork{}
	if err := network.add(context.Background(), []string{"owned-add"}, []string{"owned-delete"}); err != nil {
		t.Fatal(err)
	}
	if err := network.add(context.Background(), []string{"reject-add"}, []string{"unowned-delete"}); err == nil {
		t.Fatal("failed add accepted")
	}
	if err := network.Close(); err != nil {
		t.Fatal(err)
	}
	if err := network.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || string(data) != "owned-add\nreject-add\nowned-delete\n" {
		t.Fatal("cleanup removed an unowned change or ran twice")
	}
}

func TestIMSSourceRoutePreservesOtherTrafficAndRejectsUsedTable(t *testing.T) {
	for _, used := range []bool{false, true} {
		t.Run(strconv.FormatBool(used), func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "commands")
			program := "#!/bin/sh\ncase \"$*\" in *' rule show'|*' route show table all') printf '%s\\n' \"$IMS_TEST_TABLES\";; *) printf '%s\\n' \"$*\" >> \"$IMS_TEST_COMMANDS\";; esac\n"
			if err := os.WriteFile(filepath.Join(dir, "ip"), []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("IMS_TEST_COMMANDS", log)
			table := strconv.Itoa(100000 + os.Getpid())
			inventory := `[{"table":255},{"table":254}]`
			if used {
				inventory = `[{"table":` + table + `}]`
			}
			t.Setenv("IMS_TEST_TABLES", inventory)
			network := &imsNetwork{}
			err := network.addSourceRoute(t.Context(), net.ParseIP("2001:db8::1"))
			if (err != nil) != used {
				t.Fatal("source routing did not reject an occupied table")
			}
			if err := network.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if used {
				if !os.IsNotExist(err) {
					t.Fatal("occupied table was modified")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			commands := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(commands) != 4 || !strings.Contains(commands[0], "route add default dev wwan1 src 2001:db8::1 table "+table) || !strings.Contains(commands[1], "rule add priority 10000 from 2001:db8::1/128 lookup "+table) || !strings.Contains(commands[2], "rule del") || !strings.Contains(commands[3], "route del") {
				t.Fatal("IMS source rule or its ordered cleanup changed other routing")
			}
		})
	}
}

func TestSIPDiagnosticExcludesIdentitiesAndResponseText(t *testing.T) {
	for _, line := range []string{
		"REGISTER sip:subscriber-private@example.invalid SIP/2.0",
		"SIP/2.0 403 subscriber-private rejection",
	} {
		data, err := json.Marshal(sipDiagnostic("recv", line))
		if err != nil || strings.Contains(string(data), "subscriber-private") || strings.Contains(string(data), "example.invalid") {
			t.Fatal("private SIP data leaked")
		}
	}
	for _, line := range []string{"SIP/2.0 900 invalid", "SIP/2.0 invalid", "private-method private-value", ""} {
		if sipDiagnostic("recv", line) != nil {
			t.Fatal("invalid SIP metadata accepted")
		}
	}
}

func TestICMPDiagnosticNeverExposesQuotedSIP(t *testing.T) {
	data := append([]byte{1, 4, 0, 0, 0, 0, 0, 0}, []byte("subscriber-private SIP request")...)
	encoded, err := json.Marshal(icmpDiagnostic(data))
	if err != nil || strings.Contains(string(encoded), "subscriber-private") || !strings.Contains(string(encoded), `"code":4`) {
		t.Fatal("ICMP diagnostic exposed private data or lost its error code")
	}
	if icmpDiagnostic([]byte{1}) != nil || icmpDiagnostic(make([]byte, 8)) != nil {
		t.Fatal("malformed ICMP accepted")
	}
}

func TestOwnedSIPFirewallRuleIsNarrowAndRemoved(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	program := `#!/bin/sh
printf '%s\n' "$*" >> "$A8_IMS_TEST_LOG"
if [ "$1" = -j ]; then
    printf '{"nftables":[{"rule":{"handle":900,"comment":"another-service"}},{"rule":{"handle":901,"comment":"modemconfig-ko-probe-%s-%s"}}]}\n' "$A8_IMS_TEST_PID" "$A8_IMS_TEST_PORT"
fi
`
	if err := os.WriteFile(filepath.Join(dir, "nft"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("A8_IMS_TEST_LOG", log)
	t.Setenv("A8_IMS_TEST_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("A8_IMS_TEST_PORT", "43000")
	include := filepath.Join(dir, "sip.nft")
	network := &imsNetwork{localIPv6: net.ParseIP("2001:db8::1"), firewallPath: include}
	for range 2 {
		if err := network.AllowSIP(43000); err != nil {
			t.Fatal(err)
		}
	}
	if err := network.AllowSIP(0); err == nil {
		t.Fatal("invalid socket port accepted")
	}
	rules, err := os.ReadFile(include)
	info, statErr := os.Stat(include)
	if err != nil || statErr != nil || info.Mode().Perm() != 0600 || strings.Count(string(rules), "\n") != 1 || !strings.Contains(string(rules), "udp sport 5060 udp dport 43000 counter accept") {
		t.Fatal("fw4 reload lost its private, narrow SIP include")
	}
	if err := network.Close(); err != nil {
		t.Fatal(err)
	}
	if err := network.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(lines) != 3 || !strings.Contains(lines[0], "iifname wwan1 ip6 daddr 2001:db8::1 udp sport 5060 udp dport 43000") || lines[2] != "delete rule inet fw4 input handle 901" {
		t.Fatal("SIP rule was widened, duplicated, retained or deleted by a stale handle")
	}
	if _, err := os.Stat(include); !os.IsNotExist(err) {
		t.Fatal("closed SIP connection retained its firewall include")
	}
	t.Setenv("A8_IMS_TEST_PORT", "43001")
	network = &imsNetwork{localIPv6: net.ParseIP("2001:db8::1"), sipServerPort: 5080, firewallPath: include}
	if err := network.AllowSIP(43001); err != nil {
		t.Fatal(err)
	}
	if err := network.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(log)
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(lines) != 6 || !strings.Contains(lines[3], "udp sport 5080 udp dport 43001") || lines[5] != "delete rule inet fw4 input handle 901" {
		t.Fatal("KT SIP server port was not reflected in the owned firewall rule")
	}
	// Failure to save the reload include must undo only the newly opened port.
	t.Setenv("A8_IMS_TEST_PORT", "43002")
	network = &imsNetwork{localIPv6: net.ParseIP("2001:db8::1"), firewallPath: filepath.Join(dir, "missing", "sip.nft")}
	if network.AllowSIP(43002) == nil || len(network.allowedPorts) != 0 {
		t.Fatal("failed include left an allowed SIP port")
	}
	data, err = os.ReadFile(log)
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(lines) != 9 || lines[8] != "delete rule inet fw4 input handle 901" {
		t.Fatal("failed include did not remove its live SIP rule")
	}
}

func TestSIPPortParsingRejectsInvalidEndpoints(t *testing.T) {
	if sipViaPort("Via: SIP/2.0/UDP [2001:db8::1]:43000;branch=test\r\n") != 43000 {
		t.Fatal("UDP response socket missing")
	}
	for _, value := range []string{"Via: SIP/2.0/UDP [2001:db8::1]:0", "Via: SIP/2.0/UDP [2001:db8::1]:99999", "Via: SIP/2.0/TCP [2001:db8::1]:43000", "Via: SIP/2.0/UDP invalid"} {
		if sipViaPort(value) != 0 {
			t.Fatal("invalid or unrelated endpoint accepted")
		}
	}
}

func TestTTAVersionMatchesIntegrity(t *testing.T) {
	for _, mode := range []string{"tta4-null", "tta4-aes", "tta3-null", "tta3-aes"} {
		agent, integrity, encryption, err := ttaSecurity(mode)
		if err != nil || strings.HasPrefix(mode, "tta4") != strings.Contains(agent, "TTA-VoLTE/4.0") || strings.HasPrefix(mode, "tta4") != (integrity == "hmac-sha2-256-128") || strings.HasSuffix(mode, "aes") != (encryption == "aes-cbc") {
			t.Fatal("TTA version and security algorithms do not match")
		}
	}
	if _, _, _, err := ttaSecurity("invalid"); err == nil {
		t.Fatal("invalid security mode accepted")
	}
}

func TestSecurityDiagnosticOutputsOnlyFixedCapabilities(t *testing.T) {
	message := "SIP/2.0 401 subscriber-private\r\nWWW-Authenticate: Digest realm=\"subscriber-private\",nonce=\"subscriber-private\",algorithm=AKAv1-MD5\r\nP-LGUplus-Specific-Control: ipsec=yes;private=subscriber-private\r\nSecurity-Server: ipsec-3gpp;alg=hmac-sha-1-96;ealg=null;port-c=6000;port-s=6001;spi-c=1;spi-s=2;private=subscriber-private\r\nContent-Length: 0\r\n\r\n"
	event := sipSecurityDiagnostic(message)
	data, err := json.Marshal(event)
	if err != nil || event["security_server"] != true || event["operator_ipsec_yes"] != true || strings.Contains(string(data), "subscriber-private") || strings.Contains(string(data), "6000") {
		t.Fatal("security diagnostic exposed private headers or lost capability flags")
	}
	if sipSecurityDiagnostic("invalid private data") != nil {
		t.Fatal("invalid SIP message accepted")
	}
	event = sipSecurityDiagnostic("SIP/2.0 401 Unauthorized\r\nContent-Length: 0\r\n\r\n")
	if event["security_server"] != false || event["auth_challenge"] != false || event["operator_ipsec_yes"] != false {
		t.Fatal("absent security headers reported as present")
	}
}

func TestAndroidCarrierPolicy(t *testing.T) {
	for _, plmn := range []string{"45005", "45006", "45008"} {
		disabled, port, err := androidCarrierPolicy(plmn)
		if err != nil || disabled != (plmn != "45008") || port != map[string]int{"45005": 5060, "45006": 5060, "45008": 5080}[plmn] {
			t.Fatal("Android carrier policy does not match the baseline settings")
		}
	}
	if _, _, err := androidCarrierPolicy("450001"); err == nil {
		t.Fatal("unknown carrier accepted")
	}
}

func TestFirewallCleanupFailureSurvivesSocketClose(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nft"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	network := &imsNetwork{icmp: conn, allowedPorts: map[int]bool{43000: true}, firewallPath: filepath.Join(dir, "sip.nft")}
	if network.Close() == nil {
		t.Fatal("firewall cleanup failure hidden by successful socket close")
	}
}
