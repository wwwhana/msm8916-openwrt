package voice

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/damonto/ims-go/ims/registration"
	"github.com/damonto/ims-go/ims/session"
	"github.com/damonto/ims-go/ims/sip"
)

func koFailureEndpoint(t *testing.T) (*Endpoint, *voiceStubTransport, <-chan IncomingCall) {
	t.Helper()
	transport := newVoiceStubTransport()
	sess, err := session.Open(session.Config{
		TransactionTimers: session.TransactionTimers{T1: 10 * time.Millisecond, T2: 20 * time.Millisecond},
	}, transport)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := newTestRegistrar(registration.Config{Logger: testLogger(),
		PrivateIdentity: "001010000000001@ims.example.test",
		PublicIdentity:  "sip:001010000000001@ims.example.test", HomeDomain: "ims.example.test",
	}, sess, voiceStubCard{})
	if err != nil {
		t.Fatal(err)
	}
	incoming := make(chan IncomingCall, 4)
	svc, err := New(Config{Logger: testLogger(),
		PrepareMedia: func(_ context.Context, c MediaConfig) (MediaTransport, error) {
			return &voiceMediaTransport{local: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.LocalPort}, onPacket: c.OnPacket}, nil
		},
		OnIncomingCall: func(event IncomingCall) { incoming <- event },
	}, sess, reg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx) }()
	if err := sess.WaitStarted(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); closeEndpointAndStackAfterTest(t, svc, sess); <-done })
	return svc, transport, incoming
}

func TestModemConfigKOOutgoingInvalidAnswerEndsRemoteCall(t *testing.T) {
	for _, kind := range []string{"missing-sdp", "invalid-sdp", "unsupported-codec"} {
		t.Run(kind, func(t *testing.T) {
			svc, transport, _ := koFailureEndpoint(t)
			var final sip.Message
			transport.onWrite = func(msg sip.Message) {
				switch msg.Method() {
				case "INVITE":
					final = callOK(msg)
					final.Headers.Add("Record-Route", "<sip:proxy.example.test;lr>")
					switch kind {
					case "missing-sdp":
						final.Body = nil
					case "invalid-sdp":
						final.Body = []byte("invalid-session-description\r\n")
					case "unsupported-codec":
						final.Body = []byte("v=0\r\no=- 1 1 IN IP4 192.0.2.10\r\ns=-\r\nc=IN IP4 192.0.2.10\r\nt=0 0\r\nm=audio 50000 RTP/AVP 111\r\na=rtpmap:111 unsupported/48000\r\n")
					}
					transport.queue(final)
				case "BYE":
					transport.queue(callStatus(msg, 200, "OK"))
				}
			}
			ctx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			call, err := svc.Dial(ctx, DialRequest{To: "sip:remote@example.test"})
			if err == nil || call != nil {
				t.Fatal("unusable answer was accepted")
			}
			if transport.countMethod("ACK") != 1 || transport.countMethod("BYE") != 1 {
				t.Fatalf("failed answered call sent ACK/BYE %d/%d, want 1/1", transport.countMethod("ACK"), transport.countMethod("BYE"))
			}
			for _, method := range []string{"ACK", "BYE"} {
				message := transport.messagesByMethod(method)[0]
				if sip.HeaderTag(message.Header("To")) != sip.HeaderTag(final.Header("To")) || !strings.Contains(message.Header("Route"), "proxy.example.test") {
					t.Fatal("failed call cleanup lost its remote dialog or proxy route")
				}
			}
			if svc.lookupCall(final.Header("Call-ID")) != nil {
				t.Fatal("failed dialog was retained as an active call")
			}
			transport.queue(final)
			time.Sleep(30 * time.Millisecond)
			if transport.countMethod("INVITE") != 1 {
				t.Fatal("failed call redialed automatically")
			}
		})
	}
}

func TestModemConfigKOIncomingInvalidACKEndsRemoteCall(t *testing.T) {
	_, transport, incoming := koFailureEndpoint(t)
	transport.onWrite = func(msg sip.Message) {
		if msg.Method() == "BYE" {
			transport.queue(callStatus(msg, 200, "OK"))
		}
	}
	invite := delayedInvite()
	transport.queue(invite)
	var event IncomingCall
	select {
	case event = <-incoming:
	case <-time.After(time.Second):
		t.Fatal("incoming call did not ring")
	}
	if err := event.Call.Answer(t.Context(), MediaOffer{}); err != nil {
		t.Fatal(err)
	}
	responses := transport.messagesByStatus(200, "INVITE")
	ack := delayedOfferACK(invite, responses[0])
	ack.Body = nil
	transport.queue(ack)
	if !waitForCallTest(func() bool { return event.Call.State() == CallStateFailed }) {
		t.Fatal("invalid ACK did not fail the call")
	}
	if transport.countMethod("BYE") != 1 {
		t.Fatal("failed incoming answer did not end the remote call")
	}
}

func TestModemConfigKOEndedIncomingInviteCannotRingAgain(t *testing.T) {
	svc, transport, incoming := koFailureEndpoint(t)
	invite := delayedInvite()
	transport.queue(invite)
	var event IncomingCall
	select {
	case event = <-incoming:
	case <-time.After(time.Second):
		t.Fatal("incoming call did not ring")
	}
	if err := event.Call.Answer(t.Context(), MediaOffer{}); err != nil {
		t.Fatal(err)
	}
	event.Call.finish(CallStateFailed, "test media failure")
	transport.queue(invite)
	if !waitForCallTest(func() bool { return len(transport.messagesByStatus(200, "INVITE")) > 1 || len(incoming) > 0 }) {
		t.Fatal("retransmitted INVITE was not handled")
	}
	if len(incoming) != 0 || svc.lookupCall(invite.Header("Call-ID")) != nil {
		t.Fatal("old INVITE revived a failed call")
	}
	// A new attempt from the same caller still reaches the incoming queue.
	invite.Headers.Set("Call-ID", "new-call-attempt")
	invite.Headers.Set("Via", "SIP/2.0/TCP 192.0.2.10:5060;branch=z9hG4bKnew-attempt")
	transport.queue(invite)
	select {
	case <-incoming:
	case <-time.After(time.Second):
		t.Fatal("new incoming call was blocked with the old transaction")
	}
}

func TestModemConfigKOHangupWhileAnswerWaitsForACK(t *testing.T) {
	for _, acknowledge := range []bool{true, false} {
		t.Run(map[bool]string{true: "late-ack", false: "ack-timeout"}[acknowledge], func(t *testing.T) {
			_, transport, incoming := koFailureEndpoint(t)
			transport.onWrite = func(msg sip.Message) {
				if msg.Method() == "BYE" {
					transport.queue(callStatus(msg, 200, "OK"))
				}
			}
			invite := delayedInvite()
			transport.queue(invite)
			var event IncomingCall
			select {
			case event = <-incoming:
			case <-time.After(time.Second):
				t.Fatal("incoming call did not ring")
			}
			if err := event.Call.Answer(t.Context(), MediaOffer{}); err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer stop()
			if err := event.Call.Hangup(ctx); err != nil {
				t.Fatal("answering call refused hangup:", err)
			}
			if event.Call.State() != CallStateEnding || transport.countMethod("BYE") != 0 {
				t.Fatal("pending ACK hangup did not commit cancellation before waiting")
			}
			if acknowledge {
				responses := transport.messagesByStatus(200, "INVITE")
				transport.queue(delayedOfferACK(invite, responses[0]))
			}
			if !waitForCallTest(func() bool { return event.Call.State() == CallStateEnded || event.Call.State() == CallStateFailed }) {
				t.Fatal("canceled answer remained alive")
			}
			if transport.countMethod("BYE") != 1 || transport.countMethod("INVITE") != 0 || len(incoming) != 0 {
				t.Fatal("answer cancellation repeated the call or missed its BYE")
			}
		})
	}
}
