package relayservice

import (
	"context"
	"net"

	"mobile-egress/internal/relayadmin"
)

type RelayAdminClient interface {
	Status(context.Context) (relayadmin.StatusResult, error)
	Setup(context.Context, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error)
	Rotate(context.Context, relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error)
	Repair(context.Context) (relayadmin.RepairResult, error)
}

type relayAdminDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func newDarwinRelayAdminClient() RelayAdminClient {
	return newRelayAdminClient(&net.Dialer{})
}

func newRelayAdminClient(dialer relayAdminDialer) *relayadmin.Client {
	return &relayadmin.Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", relayadmin.DarwinAdminSocketPath)
		},
		OperationLimit: relayadmin.OperationTimeout,
		IOLimit:        relayadmin.OperationTimeout,
	}
}
