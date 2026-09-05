package localbridge

import (
	"context"
	"errors"
	"fmt"

	"mobile-egress/internal/relayadmin"
)

const (
	relayAdminClientRequired    = "relay admin client is required"
	relayAdminSetupUnavailable  = "local relay setup is unavailable"
	relayAdminRotateUnavailable = "local relay rotation is unavailable"
	relayAdminRepairUnavailable = "local relay repair is unavailable"
)

// RelayAdminClient is the strictly typed relay-admin v1 client surface used by
// the macOS controller.
type RelayAdminClient interface {
	Status(context.Context) (relayadmin.StatusResult, error)
	Setup(context.Context, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error)
	Rotate(context.Context, relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error)
	Repair(context.Context) (relayadmin.RepairResult, error)
}

type relayAdminHelper struct {
	client RelayAdminClient
}

func NewRelayAdminHelper(client RelayAdminClient) (ElevatedHelper, error) {
	if client == nil {
		return nil, errors.New(relayAdminClientRequired)
	}
	return &relayAdminHelper{client: client}, nil
}

func (helper *relayAdminHelper) Setup(ctx context.Context, request SetupRequest) (OwnerBootstrapResult, error) {
	params := relayadmin.SetupRequest{
		PublicName:  request.PublicName,
		PublicURL:   request.PublicURL,
		OwnerCSRPEM: request.OwnerCSRPEM,
	}
	var result relayadmin.OwnerBootstrapResult
	var err error
	if request.RequestID != "" {
		client, ok := helper.client.(interface {
			SetupWithRequestID(context.Context, string, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error)
		})
		if !ok {
			return OwnerBootstrapResult{}, errors.New("local relay does not support resumable setup")
		}
		result, err = client.SetupWithRequestID(ctx, request.RequestID, params)
	} else {
		result, err = helper.client.Setup(ctx, params)
	}
	if err != nil {
		return OwnerBootstrapResult{}, safeRelayAdminError(relayAdminSetupUnavailable, err)
	}
	return OwnerBootstrapResult{
		CertificatePEM:   result.CertificatePEM,
		CACertificatePEM: result.CACertificatePEM,
		Serial:           result.Serial,
		Role:             result.Role,
	}, nil
}

func safeRelayAdminError(stage string, err error) error {
	var public *relayadmin.PublicError
	if errors.As(err, &public) || errors.Is(err, relayadmin.ErrTransport) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", stage, err)
	}
	return errors.New(stage)
}

func (helper *relayAdminHelper) Rotate(ctx context.Context, request RotateRequest) (EndpointRotationResult, error) {
	result, err := helper.client.Rotate(ctx, relayadmin.RotateRequest{
		PublicName: request.PublicName,
		PublicURL:  request.PublicURL,
	})
	if err != nil {
		return EndpointRotationResult{}, errors.New(relayAdminRotateUnavailable)
	}
	return EndpointRotationResult{
		PublicURL: result.PublicURL,
		Serial:    result.Serial,
	}, nil
}

func (helper *relayAdminHelper) Repair(ctx context.Context) error {
	result, err := helper.client.Repair(ctx)
	if err != nil {
		return safeRelayAdminError(relayAdminRepairUnavailable, err)
	}
	if !result.Ready || !result.Restarting {
		return errors.New(relayAdminRepairUnavailable)
	}
	return nil
}
