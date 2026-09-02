// Package relayservice connects macOS Service Management authorization with
// the authenticated relay-admin status proof used by the desktop workflow.
package relayservice

import (
	"context"

	"mobile-egress/internal/relayadmin"
)

type State string

const (
	StateNotRegistered    State = "not-registered"
	StateApprovalRequired State = "approval-required"
	StateEnabled          State = "enabled"
	StateVersionMismatch  State = "version-mismatch"
	StateUnavailable      State = "unavailable"
)

type FailureClass string

const (
	FailureNone         FailureClass = ""
	FailureNative       FailureClass = "native-unavailable"
	FailureTransport    FailureClass = "transport-unavailable"
	FailureProtocol     FailureClass = "protocol-unavailable"
	FailureRemote       FailureClass = "relay-unavailable"
	FailureUnauthorized FailureClass = "unauthorized"
	FailureInconsistent FailureClass = "inconsistent-status"
	FailureCancelled    FailureClass = "cancelled"
)

type NativeStatus uint8

const (
	NativeNotRegistered NativeStatus = iota
	NativeApprovalRequired
	NativeEnabled
	NativeNotFound
	NativeUnknown
)

type NativeErrorClass uint8

const (
	NativeErrorNone NativeErrorClass = iota
	NativeErrorAlreadyRegistered
	NativeErrorLaunchDenied
	NativeErrorUnavailable
	NativeErrorOther
)

type Observation struct {
	State        State
	Native       NativeStatus
	StrictV1     bool
	ExactHelper  bool
	Initialized  bool
	RelayRunning bool
	Repairable   bool
	Failure      FailureClass
}

type SetupDecision uint8

const (
	SetupBlocked SetupDecision = iota
	SetupAwaitingApproval
	SetupProceed
)

type SetupGate struct {
	Observation Observation
	Decision    SetupDecision
}

type RotateGate struct {
	Observation Observation
	Proceed     bool
}

type RepairGate struct {
	Observation Observation
	Proceed     bool
}

type Native interface {
	Status(context.Context) (NativeStatus, NativeErrorClass)
	Register(context.Context) NativeErrorClass
	Refresh(context.Context) NativeErrorClass
	OpenLoginItems(context.Context) NativeErrorClass
}

type Admin interface {
	Status(context.Context) (relayadmin.StatusResult, error)
}

type Controller interface {
	Observe(context.Context) Observation
	PrepareSetup(context.Context) SetupGate
	GateRotate(context.Context) RotateGate
	GateRepair(context.Context) RepairGate
	WaitForExactHelper(context.Context) Observation
}
