package service

import (
	"fmt"
	"testing"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestOutboundMailboxPrioritizesControlsAndRoundRobinsData(t *testing.T) {
	mailbox := newOutboundMailbox(4, 4, 2)
	frames := []protocol.Envelope{
		{Version: 1, Type: protocol.TypeData, StreamID: "alpha", Payload: "YQ"},
		{Version: 1, Type: protocol.TypeData, StreamID: "alpha", Payload: "Yg"},
		{Version: 1, Type: protocol.TypeData, StreamID: "bravo", Payload: "Yw"},
		{Version: 1, Type: protocol.TypeClose, StreamID: "alpha", Payload: encodeRelayError("client_closed")},
	}
	for _, frame := range frames {
		if result := mailbox.enqueue(frame); result != outboundAdmitted {
			t.Fatalf("enqueue(%s/%s) = %v, want admitted", frame.Type, frame.StreamID, result)
		}
	}

	want := []struct {
		messageType protocol.MessageType
		streamID    string
		payload     string
	}{
		{protocol.TypeClose, "alpha", encodeRelayError("client_closed")},
		{protocol.TypeData, "alpha", "YQ"},
		{protocol.TypeData, "bravo", "Yw"},
		{protocol.TypeData, "alpha", "Yg"},
	}
	for index, expected := range want {
		frame, ok := mailbox.poll()
		if !ok {
			t.Fatalf("poll %d returned empty", index+1)
		}
		if frame.Type != expected.messageType || frame.StreamID != expected.streamID || frame.Payload != expected.payload {
			t.Fatalf("poll %d = %#v, want %s/%s/%s", index+1, frame, expected.messageType, expected.streamID, expected.payload)
		}
	}
}

func TestOutboundMailboxEnforcesSeparateControlAndDataBounds(t *testing.T) {
	mailbox := newOutboundMailbox(2, 3, 2)
	for index := 0; index < 2; index++ {
		frame := protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: fmt.Sprintf("control-%d", index), Payload: encodeRelayError("client_closed")}
		if result := mailbox.enqueue(frame); result != outboundAdmitted {
			t.Fatalf("control enqueue %d = %v, want admitted", index+1, result)
		}
	}
	if result := mailbox.enqueue(protocol.Envelope{Version: 1, Type: protocol.TypePong}); result != outboundControlSaturated {
		t.Fatalf("control overflow = %v, want control saturation", result)
	}

	data := func(streamID, payload string) protocol.Envelope {
		return protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: streamID, Payload: payload}
	}
	if result := mailbox.enqueue(data("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("first alpha enqueue = %v, want admitted", result)
	}
	if result := mailbox.enqueue(data("alpha", "Yg")); result != outboundAdmitted {
		t.Fatalf("second alpha enqueue = %v, want admitted", result)
	}
	if result := mailbox.enqueue(data("alpha", "Yw")); result != outboundDataSaturated {
		t.Fatalf("per-stream overflow = %v, want data saturation", result)
	}
	if result := mailbox.enqueue(data("bravo", "ZA")); result != outboundAdmitted {
		t.Fatalf("first bravo enqueue = %v, want admitted", result)
	}
	if result := mailbox.enqueue(data("charlie", "ZQ")); result != outboundDataSaturated {
		t.Fatalf("aggregate overflow = %v, want data saturation", result)
	}
}

func TestProductionOutboundMailboxLimitsMatchSessionRole(t *testing.T) {
	tests := []struct {
		name            string
		role            enrollment.Role
		controlCapacity int
		dataCapacity    int
	}{
		{name: "Agent", role: enrollment.RoleAgent, controlCapacity: 512, dataCapacity: 256},
		{name: "Client", role: enrollment.RoleClient, controlCapacity: 64, dataCapacity: 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controls := newSessionOutboundMailbox(test.role)
			for index := 0; index < test.controlCapacity; index++ {
				frame := protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: fmt.Sprintf("control-%d", index), Payload: encodeRelayError("client_closed")}
				if result := controls.enqueue(frame); result != outboundAdmitted {
					t.Fatalf("control enqueue %d = %v, want admitted", index+1, result)
				}
			}
			if result := controls.enqueue(protocol.Envelope{Version: 1, Type: protocol.TypePong}); result != outboundControlSaturated {
				t.Fatalf("control enqueue %d = %v, want saturation", test.controlCapacity+1, result)
			}

			data := newSessionOutboundMailbox(test.role)
			for index := 0; index < test.dataCapacity; index++ {
				frame := protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: fmt.Sprintf("stream-%d", index), Payload: "YQ"}
				if result := data.enqueue(frame); result != outboundAdmitted {
					t.Fatalf("data enqueue %d = %v, want admitted", index+1, result)
				}
			}
			if result := data.enqueue(protocol.Envelope{Version: 1, Type: protocol.TypeData, StreamID: "overflow", Payload: "Yg"}); result != outboundDataSaturated {
				t.Fatalf("data enqueue %d = %v, want saturation", test.dataCapacity+1, result)
			}
		})
	}
}
