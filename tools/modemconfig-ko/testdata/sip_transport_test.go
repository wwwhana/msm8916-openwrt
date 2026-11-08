package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/damonto/ims-go/ims/sip"
)

func TestModemConfigKOCarrierUDPStaysOnRegisteredFlow(t *testing.T) {
	udp := newStubTransport(5062, 5060)
	opened := false
	stack, err := Open(Config{DatagramOnly: true, OpenReliableTransport: func(context.Context) (Transport, error) {
		opened = true
		t.Fatal("carrier UDP policy opened an unregistered TCP flow")
		return nil, nil
	}}, udp)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStackAfterTest(t, stack)
	request := newRequest("INVITE", "carrier-udp", "1 INVITE")
	request.Body = make([]byte, 4096)
	request.Headers.Set("Content-Length", fmt.Sprint(len(request.Body)))
	if err := stack.Send(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	writes := udp.snapshotWrites()
	if opened || len(writes) != 1 || stack.PrimaryTransport() != udp {
		t.Fatal("large INVITE did not use the registered UDP flow")
	}
	message, err := sip.ParseMessage(writes[0])
	if err != nil || message.Header("Via") != request.Header("Via") {
		t.Fatal("UDP flow changed the INVITE Via")
	}
}
