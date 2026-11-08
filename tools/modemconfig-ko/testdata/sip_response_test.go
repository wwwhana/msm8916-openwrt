package sip

import (
	"reflect"
	"testing"
)

func TestModemConfigKOIncomingDialogPreservesProxyRoutes(t *testing.T) {
	request := NewRequest("INVITE", "sip:callee@example.test")
	request.Headers.Add("To", "<sip:callee@example.test>")
	request.Headers.Add("Record-Route", `<sip:first.example.test;lr;opaque="one">`)
	request.Headers.Add("Record-Route", `<sip:second.example.test;lr>, <sip:third.example.test;lr;unknown=keep>`)
	want := request.HeadersByName("Record-Route")
	for _, code := range []int{180, 183, 200} {
		response := NewResponse(code, "Test")
		response.Headers = ResponseHeaders(request, code)
		if !reflect.DeepEqual(response.HeadersByName("Record-Route"), want) {
			t.Fatalf("%d response dropped or changed IMS proxy routes", code)
		}
	}
	for _, code := range []int{100, 486} {
		response := NewResponse(code, "Test")
		response.Headers = ResponseHeaders(request, code)
		if len(response.HeadersByName("Record-Route")) != 0 {
			t.Fatal("non-dialog response created a route set")
		}
	}
	request.StartLine = "REGISTER sip:example.test SIP/2.0"
	response := NewResponse(200, "OK")
	response.Headers = ResponseHeaders(request, 200)
	if len(response.HeadersByName("Record-Route")) != 0 {
		t.Fatal("REGISTER response acquired dialog routes")
	}
}
