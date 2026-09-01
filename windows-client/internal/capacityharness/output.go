//go:build capacityharness

package capacityharness

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
)

const maxReportedCount = 512

type Phase string

const (
	PhaseInput       Phase = "input"
	PhasePreflight   Phase = "preflight"
	PhaseProvision   Phase = "provision"
	PhaseOpen        Phase = "open"
	PhaseVerify      Phase = "verify"
	PhaseLimit       Phase = "limit"
	PhaseHold        Phase = "hold"
	PhaseReplacement Phase = "replacement"
	PhaseCleanup     Phase = "cleanup"
	PhaseTarget      Phase = "target"
	PhaseComplete    Phase = "complete"
)

type FailureCategory string

const (
	FailureNone           FailureCategory = "none"
	FailureInput          FailureCategory = "input"
	FailurePreflight      FailureCategory = "preflight"
	FailureProvision      FailureCategory = "provision"
	FailureSession        FailureCategory = "session"
	FailureClientLimit    FailureCategory = "client_limit"
	FailureAgentLimit     FailureCategory = "agent_limit"
	FailureTLS            FailureCategory = "tls"
	FailureAuthentication FailureCategory = "authentication"
	FailureEcho           FailureCategory = "echo"
	FailureProtocol       FailureCategory = "protocol"
	FailureCanceled       FailureCategory = "canceled"
	FailureTimeout        FailureCategory = "timeout"
	FailureCleanup        FailureCategory = "cleanup"
	FailureInternal       FailureCategory = "internal"
)

type Event struct {
	Phase     Phase           `json:"phase"`
	Attempted int             `json:"attempted"`
	Open      int             `json:"open"`
	Verified  int             `json:"verified"`
	Closed    int             `json:"closed"`
	Failure   FailureCategory `json:"failure"`
}

type Emitter interface {
	Emit(Event) error
}

type JSONEmitter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func NewJSONEmitter(writer io.Writer) *JSONEmitter {
	return &JSONEmitter{encoder: json.NewEncoder(writer)}
}

func (emitter *JSONEmitter) Emit(event Event) error {
	if emitter == nil || emitter.encoder == nil || !event.valid() {
		return errors.New("capacity harness output event is invalid")
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	return emitter.encoder.Encode(event)
}

func (event Event) valid() bool {
	if !validPhase(event.Phase) || !validFailure(event.Failure) {
		return false
	}
	for _, count := range []int{event.Attempted, event.Open, event.Verified, event.Closed} {
		if count < 0 || count > maxReportedCount {
			return false
		}
	}
	return true
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseInput, PhasePreflight, PhaseProvision, PhaseOpen, PhaseVerify, PhaseLimit, PhaseHold, PhaseReplacement, PhaseCleanup, PhaseTarget, PhaseComplete:
		return true
	default:
		return false
	}
}

func validFailure(category FailureCategory) bool {
	switch category {
	case FailureNone, FailureInput, FailurePreflight, FailureProvision, FailureSession, FailureClientLimit,
		FailureAgentLimit, FailureTLS, FailureAuthentication, FailureEcho, FailureProtocol,
		FailureCanceled, FailureTimeout, FailureCleanup, FailureInternal:
		return true
	default:
		return false
	}
}
