//go:build darwin && !cgo

package adminservice

import (
	"context"
)

func OpenDarwinLaunchdAdminSocket(context.Context) (*AdminSocket, error) {
	return nil, errStateACLUnavailable
}
