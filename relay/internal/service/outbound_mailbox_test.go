package service

import (
	"fmt"
	"testing"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestOutboundDataBudgetEnforcesFrameAndByteLimits(t *testing.T) {
	frameBudget := newOutboundDataBudget(2, 5)
	first, ok := frameBudget.tryReserve(3)
	if !ok {
		t.Fatal("first frame reservation was rejected")
	}
	second, ok := frameBudget.tryReserve(2)
	if !ok {
		t.Fatal("exact frame and byte boundary was rejected")
	}
	if _, ok := frameBudget.tryReserve(0); ok {
		t.Fatal("frame beyond aggregate limit was admitted")
	}
	first.release()
	if _, ok := frameBudget.tryReserve(3); !ok {
		t.Fatal("released frame and bytes were not reusable")
	}
	second.release()

	byteBudget := newOutboundDataBudget(3, 5)
	exact, ok := byteBudget.tryReserve(5)
	if !ok {
		t.Fatal("exact byte boundary was rejected")
	}
	if _, ok := byteBudget.tryReserve(1); ok {
		t.Fatal("byte beyond aggregate limit was admitted")
	}
	exact.release()
}

func TestOutboundDataReservationReleaseIsIdempotent(t *testing.T) {
	budget := newOutboundDataBudget(1, 1)
	reservation, ok := budget.tryReserve(1)
	if !ok {
		t.Fatal("initial reservation was rejected")
	}
	reservation.release()
	reservation.release()
	if _, ok := budget.tryReserve(1); !ok {
		t.Fatal("double release underflowed the budget")
	}
	if _, ok := budget.tryReserve(0); ok {
		t.Fatal("double release created an extra frame slot")
	}
}

func TestOutboundMailboxPrioritizesControlsAndRoundRobinsData(t *testing.T) {
	mailbox := newOutboundMailbox(4, 2, newOutboundDataBudget(4, 16))
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
		item, ok := mailbox.poll()
		if !ok {
			t.Fatalf("poll %d returned empty", index+1)
		}
		frame := item.envelope
		if frame.Type != expected.messageType || frame.StreamID != expected.streamID || frame.Payload != expected.payload {
			t.Fatalf("poll %d = %#v, want %s/%s/%s", index+1, frame, expected.messageType, expected.streamID, expected.payload)
		}
		item.complete()
	}
}

func TestOutboundMailboxEnforcesSeparateControlAndDataBounds(t *testing.T) {
	mailbox := newOutboundMailbox(2, 2, newOutboundDataBudget(3, 16))
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

func TestOutboundMailboxEnforcesAggregateFrameAndByteBudgets(t *testing.T) {
	frameLimited := newOutboundMailbox(4, 2, newOutboundDataBudget(2, 100))
	if result := frameLimited.enqueue(dataEnvelope("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("first frame enqueue = %v, want admitted", result)
	}
	if result := frameLimited.enqueue(dataEnvelope("bravo", "Yg")); result != outboundAdmitted {
		t.Fatalf("second frame enqueue = %v, want admitted", result)
	}
	if result := frameLimited.enqueue(dataEnvelope("charlie", "Yw")); result != outboundDataSaturated {
		t.Fatalf("aggregate frame overflow = %v, want data saturation", result)
	}

	byteLimited := newOutboundMailbox(4, 2, newOutboundDataBudget(4, 5))
	if result := byteLimited.enqueue(dataEnvelope("alpha", "abc")); result != outboundAdmitted {
		t.Fatalf("first byte-boundary enqueue = %v, want admitted", result)
	}
	if result := byteLimited.enqueue(dataEnvelope("bravo", "de")); result != outboundAdmitted {
		t.Fatalf("exact byte-boundary enqueue = %v, want admitted", result)
	}
	if result := byteLimited.enqueue(dataEnvelope("charlie", "f")); result != outboundDataSaturated {
		t.Fatalf("aggregate byte overflow = %v, want data saturation", result)
	}
}

func TestOutboundMailboxRetainsReservationUntilCompletion(t *testing.T) {
	mailbox := newOutboundMailbox(4, 1, newOutboundDataBudget(1, 8))
	if result := mailbox.enqueue(dataEnvelope("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("first enqueue = %v, want admitted", result)
	}
	item, ok := mailbox.poll()
	if !ok || item.envelope.StreamID != "alpha" {
		t.Fatalf("poll = %#v/%t, want alpha", item, ok)
	}
	if result := mailbox.enqueue(dataEnvelope("bravo", "Yg")); result != outboundDataSaturated {
		t.Fatalf("enqueue while alpha is in flight = %v, want data saturation", result)
	}
	item.complete()
	item.complete()
	if result := mailbox.enqueue(dataEnvelope("bravo", "Yg")); result != outboundAdmitted {
		t.Fatalf("enqueue after completion = %v, want admitted", result)
	}
	if result := mailbox.enqueue(dataEnvelope("charlie", "Yw")); result != outboundDataSaturated {
		t.Fatalf("enqueue after double completion = %v, want no extra frame slot", result)
	}
}

func TestClientMailboxesShareAgentToClientsBudget(t *testing.T) {
	shared := newOutboundDataBudget(1, 8)
	clientOne := newOutboundMailbox(4, 1, shared)
	clientTwo := newOutboundMailbox(4, 1, shared)
	if result := clientOne.enqueue(dataEnvelope("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("first Client enqueue = %v, want admitted", result)
	}
	if result := clientTwo.enqueue(dataEnvelope("bravo", "Yg")); result != outboundDataSaturated {
		t.Fatalf("second Client enqueue = %v, want shared-budget saturation", result)
	}
	clientOne.close()
	if result := clientTwo.enqueue(dataEnvelope("bravo", "Yg")); result != outboundAdmitted {
		t.Fatalf("second Client enqueue after first close = %v, want admitted", result)
	}
}

func TestOutboundMailboxSaturationIsStreamLocal(t *testing.T) {
	mailbox := newOutboundMailbox(4, 2, newOutboundDataBudget(3, 16))
	if result := mailbox.enqueue(dataEnvelope("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("first alpha enqueue = %v, want admitted", result)
	}
	if result := mailbox.enqueue(dataEnvelope("alpha", "Yg")); result != outboundAdmitted {
		t.Fatalf("second alpha enqueue = %v, want admitted", result)
	}
	if result := mailbox.enqueue(dataEnvelope("alpha", "Yw")); result != outboundDataSaturated {
		t.Fatalf("third alpha enqueue = %v, want stream-local saturation", result)
	}
	if result := mailbox.enqueue(dataEnvelope("bravo", "ZA")); result != outboundAdmitted {
		t.Fatalf("peer stream enqueue = %v, want admitted", result)
	}
}

func TestOutboundMailboxDiscardRefundsQueuedReservations(t *testing.T) {
	budget := newOutboundDataBudget(1, 2)
	mailbox := newOutboundMailbox(4, 1, budget)
	if result := mailbox.enqueue(dataEnvelope("alpha", "YQ")); result != outboundAdmitted {
		t.Fatalf("alpha enqueue = %v, want admitted", result)
	}
	mailbox.discardStreamData("alpha")
	if result := mailbox.enqueue(dataEnvelope("bravo", "Yg")); result != outboundAdmitted {
		t.Fatalf("bravo enqueue after discard = %v, want admitted", result)
	}
}

func TestProductionOutboundMailboxLimitsMatchSessionRole(t *testing.T) {
	tests := []struct {
		name            string
		role            enrollment.Role
		controlCapacity int
		dataCapacity    int
	}{
		{name: "Agent", role: enrollment.RoleAgent, controlCapacity: 512, dataCapacity: 8_192},
		{name: "Client", role: enrollment.RoleClient, controlCapacity: 512, dataCapacity: 8_192},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controls := newSessionOutboundMailbox(test.role, newOutboundDataBudget(test.dataCapacity, 64<<20))
			for index := 0; index < test.controlCapacity; index++ {
				frame := protocol.Envelope{Version: 1, Type: protocol.TypeClose, StreamID: fmt.Sprintf("control-%d", index), Payload: encodeRelayError("client_closed")}
				if result := controls.enqueue(frame); result != outboundAdmitted {
					t.Fatalf("control enqueue %d = %v, want admitted", index+1, result)
				}
			}
			if result := controls.enqueue(protocol.Envelope{Version: 1, Type: protocol.TypePong}); result != outboundControlSaturated {
				t.Fatalf("control enqueue %d = %v, want saturation", test.controlCapacity+1, result)
			}

			perStream := newSessionOutboundMailbox(test.role, newOutboundDataBudget(test.dataCapacity, 64<<20))
			for index := 0; index < 32; index++ {
				if result := perStream.enqueue(dataEnvelope("one-stream", "YQ")); result != outboundAdmitted {
					t.Fatalf("per-stream data enqueue %d = %v, want admitted", index+1, result)
				}
			}
			if result := perStream.enqueue(dataEnvelope("one-stream", "Yg")); result != outboundDataSaturated {
				t.Fatalf("per-stream data enqueue 33 = %v, want saturation", result)
			}

			data := newSessionOutboundMailbox(test.role, newOutboundDataBudget(test.dataCapacity, 64<<20))
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
