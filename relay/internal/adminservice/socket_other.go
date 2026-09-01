//go:build !darwin

package adminservice

import (
	"context"
	"errors"
)

var errDarwinAdminSocketUnavailable = errors.New("Darwin relay admin socket is unavailable on this platform")

func OpenDarwinAdminSocket(context.Context, uint32) (*AdminSocket, error) {
	return nil, errDarwinAdminSocketUnavailable
}
