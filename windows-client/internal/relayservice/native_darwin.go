//go:build darwin && cgo && !bindings

package relayservice

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Foundation -framework ServiceManagement
#include "native_darwin.h"
*/
import "C"

import "context"

type darwinNative struct{}

func (darwinNative) Status(ctx context.Context) (NativeStatus, NativeErrorClass) {
	if ctx != nil && ctx.Err() != nil {
		return NativeUnknown, NativeErrorUnavailable
	}
	status := boundedNativeStatus(int(C.mobile_egress_relay_service_status()))
	if ctx != nil && ctx.Err() != nil {
		return NativeUnknown, NativeErrorUnavailable
	}
	return status, NativeErrorNone
}

func (darwinNative) Register(ctx context.Context) NativeErrorClass {
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	class := boundedNativeError(int(C.mobile_egress_relay_service_register()))
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	return class
}

func (darwinNative) Refresh(ctx context.Context) NativeErrorClass {
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	class := boundedNativeError(int(C.mobile_egress_relay_service_refresh()))
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	return class
}

func (darwinNative) OpenLoginItems(ctx context.Context) NativeErrorClass {
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	class := boundedNativeError(int(C.mobile_egress_relay_service_open_login_items()))
	if ctx != nil && ctx.Err() != nil {
		return NativeErrorUnavailable
	}
	return class
}

func boundedNativeStatus(value int) NativeStatus {
	status := NativeStatus(value)
	switch status {
	case NativeNotRegistered, NativeApprovalRequired, NativeEnabled, NativeNotFound:
		return status
	default:
		return NativeUnknown
	}
}

func boundedNativeError(value int) NativeErrorClass {
	class := NativeErrorClass(value)
	switch class {
	case NativeErrorNone, NativeErrorAlreadyRegistered, NativeErrorLaunchDenied, NativeErrorUnavailable:
		return class
	default:
		return NativeErrorOther
	}
}

func NewDarwin(expectedHelperVersion string) (Controller, RelayAdminClient, error) {
	admin := newDarwinRelayAdminClient()
	service, err := New(darwinNative{}, admin, expectedHelperVersion)
	if err != nil {
		return nil, nil, err
	}
	return service, admin, nil
}
