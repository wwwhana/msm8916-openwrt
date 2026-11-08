package registration

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/sip"
)

func TestModemConfigKOSMSBootstrap(t *testing.T) {
	transport := &stubTransport{
		local:  &net.UDPAddr{IP: net.ParseIP("2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"), Port: 55000},
		remote: &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 5060},
	}
	for _, mode := range []struct {
		version, integrity string
		encryption         []string
	}{
		{"4.0", "hmac-sha2-256-128", []string{"aes-cbc", "null"}},
		{"4.0", "hmac-sha2-256-128", []string{"null"}},
		{"4.0", "hmac-sha2-256-128", []string{"aes-cbc"}},
		{"3.0", "hmac-sha-1-96", []string{"null"}},
		{"3.0", "hmac-sha-1-96", []string{"aes-cbc"}},
	} {
		svc := newTestService(t, Config{
			PrivateIdentity: "450060000000000@lte-lguplus.co.kr",
			PublicIdentity:  "sip:01000000000@lte-lguplus.co.kr",
			HomeDomain:      "lte-lguplus.co.kr", IMEI: "000000000000000",
			UserAgent:         "TTA-VoLTE/" + mode.version + " A8/OpenWrt25.12.6 Device_Type/Router OMD",
			AccessNetworkInfo: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=4500612340ABCDEF",
			RegisterExpires:   time.Hour, AuthAlgorithm: "AKAv1-MD5", SMSOnly: true,
			IPSecIntegrityAlgorithms:  []string{mode.integrity},
			IPSecEncryptionAlgorithms: mode.encryption, IPSecModes: []string{"trans"},
		}, transport, stubCard{})
		message := svc.buildRegister(nil, nil)
		data, err := message.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("bootstrap: version=%s encryption proposals=%d bytes=%d", mode.version, len(mode.encryption), len(data))
		contact := message.Header("Contact")
		if !strings.Contains(contact, "+g.3gpp.smsip") || !strings.Contains(contact, "+sip.instance") || strings.Contains(contact, "audio") || strings.Contains(contact, "mmtel") {
			t.Fatal("SMS contact advertises incorrect capabilities")
		}
		if len(mode.encryption) == 1 && len(data) > 1300 {
			t.Fatal("SMS bootstrap requires reliable transport")
		}
	}
}

func koreanCarrierChallenge(request sip.Message) sip.Message {
	return newResponse(request, 401, "Unauthorized", sip.Header{Name: "WWW-Authenticate",
		Value: `Digest realm="ims.mnc001.mcc001.3gppnetwork.org",nonce="` + testNonce + `",algorithm=AKAv1-MD5,qop="auth"`})
}

func TestModemConfigKONASVoiceRegistrationKeepsSMSOnNAS(t *testing.T) {
	transport := newUDPTransport(55000, 5060)
	svc := newTestService(t, Config{DisableIPSec: true, DisableSMS: true}, transport, stubCard{})
	contact := svc.buildRegister(nil, nil).Header("Contact")
	if strings.Contains(contact, "smsip") || !strings.Contains(contact, "mmtel") || !strings.Contains(contact, "audio") {
		t.Fatal("NAS voice contact advertises incorrect services")
	}
}

func TestModemConfigKOCarrierAKARegistrationLifecycle(t *testing.T) {
	for _, rechallenge := range []bool{false, true} {
		name := "cached-renewal"
		if rechallenge {
			name = "challenged-renewal"
		}
		t.Run(name, func(t *testing.T) {
			transport := newUDPTransport(55000, 5060)
			var card Card = stubCard{}
			if !rechallenge {
				card = &failAfterFirstAKACard{}
			}
			svc := newTestService(t, Config{DisableIPSec: true, SMSOnly: true}, transport, card)
			closeRegistrarAfterTest(t, svc)
			transport.onWrite = func(data []byte) {
				request := mustParseMessage(t, data)
				if request.Header("Security-Client") != "" || request.Header("Security-Verify") != "" || request.HeaderHasToken("Require", "sec-agree") || !strings.Contains(request.Header("Via"), ":55000;") {
					t.Fatal("carrier AKA changed socket or advertised SIP IPsec")
				}
				count := countWrites(transport)
				if count == 1 || (rechallenge && count == 3) {
					transport.queue(koreanCarrierChallenge(request))
					return
				}
				authorization := request.Header("Authorization")
				if digestTestDirective(t, authorization, "algorithm") != "AKAv1-MD5" || digestTestDirective(t, authorization, "integrity-protected") != "no" || digestTestDirective(t, authorization, "response") == "" {
					t.Fatal("carrier AKA authorization missing or claims IPsec integrity")
				}
				transport.queue(newResponse(request, 200, "OK",
					sip.Header{Name: "Expires", Value: "600"},
					sip.Header{Name: "Authentication-Info", Value: authenticationInfoForRequest(t, request, testNonce)}))
			}
			if err := svc.Register(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !svc.State().Registered || svc.State().MessagePort != 55000 || svc.State().SecurityVerify != nil || len(svc.authIK) != 0 || len(svc.authCK) != 0 {
				t.Fatal("carrier registration did not preserve its unprotected SIP flow")
			}
			if err := svc.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := svc.Deregister(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := 4
			if rechallenge {
				want = 5
			}
			if svc.State().Registered || countWrites(transport) != want {
				t.Fatal("carrier registration lifecycle incomplete")
			}
		})
	}
}

func TestModemConfigKOCarrierSecurityNeverAutomaticallyDowngrades(t *testing.T) {
	strict := newTestService(t, Config{}, newUDPTransport(55000, 5060), stubCard{})
	closeRegistrarAfterTest(t, strict)
	if _, err := strict.applyRegisterChallenge(koreanCarrierChallenge(strict.buildRegister(nil, nil))); err == nil {
		t.Fatal("default security accepted a missing server proposal")
	}
	carrier := newTestService(t, Config{DisableIPSec: true}, newUDPTransport(55001, 5060), stubCard{})
	closeRegistrarAfterTest(t, carrier)
	for _, header := range []sip.Header{
		{Name: "Security-Server", Value: "ipsec-3gpp"},
		{Name: "Require", Value: "sec-agree"},
		{Name: "Proxy-Require", Value: "sec-agree"},
	} {
		response := koreanCarrierChallenge(carrier.buildRegister(nil, nil))
		response.Headers.Add(header.Name, header.Value)
		if _, err := carrier.applyRegisterChallenge(response); err == nil {
			t.Fatal("carrier policy ignored the server's security requirement")
		}
	}
}

func TestModemConfigKOCarrierRefreshRejectsInvalidServerProof(t *testing.T) {
	transport := newUDPTransport(55000, 5060)
	svc := newTestService(t, Config{DisableIPSec: true}, transport, stubCard{})
	closeRegistrarAfterTest(t, svc)
	transport.onWrite = func(data []byte) {
		request := mustParseMessage(t, data)
		count := countWrites(transport)
		if count == 1 || count == 3 {
			transport.queue(koreanCarrierChallenge(request))
			return
		}
		proof := authenticationInfoForRequest(t, request, testNonce)
		if count == 4 {
			proof = `rspauth="00000000000000000000000000000000"`
		}
		transport.queue(newResponse(request, 200, "OK", sip.Header{Name: "Authentication-Info", Value: proof}))
	}
	if err := svc.Register(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Refresh(t.Context()); err == nil {
		t.Fatal("invalid server authentication proof accepted on refresh")
	}
}
