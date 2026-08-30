//go:build !windows

package localbridge

import (
	"context"
	"errors"
)

type UACHelper struct {
	AdminExecutable string
	RelayExecutable string
}

func (UACHelper) Setup(context.Context, SetupRequest) (OwnerBootstrapResult, error) {
	return OwnerBootstrapResult{}, errors.New("local relay service setup is only available on Windows")
}

func (UACHelper) Rotate(context.Context, RotateRequest) (EndpointRotationResult, error) {
	return EndpointRotationResult{}, errors.New("local relay endpoint rotation is only available on Windows")
}

func (UACHelper) Repair(context.Context) error {
	return errors.New("local relay repair is only available on Windows")
}
