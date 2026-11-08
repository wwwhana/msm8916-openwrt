package ims

import (
	"github.com/damonto/wwan-go/qcom"
	"net"
	"testing"
)

func TestModemConfigKOCarrierSecurityConfig(t *testing.T) {
	client := &Client{}
	for _, disabled := range []bool{false, true} {
		config := client.buildRegistrationConfig(ServiceConfig{DisableIPSec: disabled, SMSOnly: disabled}, imsSecurity{})
		if config.DisableIPSec != disabled || config.SMSOnly != disabled {
			t.Fatal("explicit carrier security or diagnostic policy was lost")
		}
	}
}

type modemConfigKONetwork struct{ fakeIMSPDNNetwork }

func (*modemConfigKONetwork) PCSCFIPs() []net.IP { return []net.IP{net.ParseIP("2001:db8::3")} }

func TestModemConfigKOIMSPDNDNSAndResolvedProxy(t *testing.T) {
	dns := net.ParseIP("2001:db8::53")
	info := qcomPDNNetworkInfo(qcom.IMSPDNInfo{PDNInfo: qcom.PDNInfo{DNS: []net.IP{dns}}})
	if len(info.DNS) != 1 || !info.DNS[0].Equal(dns) {
		t.Fatal("IMS network lost its carrier DNS")
	}
	session := &networkedPDNSession{imsPDNSession: &fakeIMSPDNSession{}, network: &modemConfigKONetwork{}, interfaceName: "test0"}
	resolved := session.Info()
	if resolved.InterfaceName != "test0" || len(resolved.PCSCFIPs) != 1 || !resolved.PCSCFIPs[0].Equal(net.ParseIP("2001:db8::3")) {
		t.Fatal("SIP access lost the P-CSCF resolved after IMS connection")
	}
}
