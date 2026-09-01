package localbridge

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"mobile-egress/internal/relayadmin"
)

func TestRelayAdminHelperConvertsSetupFields(t *testing.T) {
	wantRequest := relayadmin.SetupRequest{
		PublicName:  "relay.example.ts.net",
		PublicURL:   "https://relay.example.ts.net:8443",
		OwnerCSRPEM: "owner-csr",
	}
	wantResult := OwnerBootstrapResult{
		CertificatePEM:   "owner-certificate",
		CACertificatePEM: "ca-certificate",
		Serial:           "A1B2",
		Role:             "owner",
	}
	client := &relayAdminClientFake{
		setup: func(_ context.Context, request relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error) {
			if !reflect.DeepEqual(request, wantRequest) {
				t.Fatalf("Setup request = %#v, want %#v", request, wantRequest)
			}
			return relayadmin.OwnerBootstrapResult{
				CertificatePEM:   wantResult.CertificatePEM,
				CACertificatePEM: wantResult.CACertificatePEM,
				Serial:           wantResult.Serial,
				Role:             wantResult.Role,
			}, nil
		},
	}
	helper, err := NewRelayAdminHelper(client)
	if err != nil {
		t.Fatalf("NewRelayAdminHelper: %v", err)
	}

	got, err := helper.Setup(context.Background(), SetupRequest{
		PublicName:  wantRequest.PublicName,
		PublicURL:   wantRequest.PublicURL,
		OwnerCSRPEM: wantRequest.OwnerCSRPEM,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !reflect.DeepEqual(got, wantResult) {
		t.Fatalf("Setup result = %#v, want %#v", got, wantResult)
	}
}

func TestRelayAdminHelperConvertsRotateFields(t *testing.T) {
	wantRequest := relayadmin.RotateRequest{
		PublicName: "next.example.ts.net",
		PublicURL:  "https://next.example.ts.net:8443",
	}
	wantResult := EndpointRotationResult{PublicURL: wantRequest.PublicURL, Serial: "C3D4"}
	client := &relayAdminClientFake{
		rotate: func(_ context.Context, request relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error) {
			if !reflect.DeepEqual(request, wantRequest) {
				t.Fatalf("Rotate request = %#v, want %#v", request, wantRequest)
			}
			return relayadmin.EndpointRotationResult{
				PublicURL: wantResult.PublicURL,
				Serial:    wantResult.Serial,
			}, nil
		},
	}
	helper, err := NewRelayAdminHelper(client)
	if err != nil {
		t.Fatalf("NewRelayAdminHelper: %v", err)
	}

	got, err := helper.Rotate(context.Background(), RotateRequest{
		PublicName: wantRequest.PublicName,
		PublicURL:  wantRequest.PublicURL,
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !reflect.DeepEqual(got, wantResult) {
		t.Fatalf("Rotate result = %#v, want %#v", got, wantResult)
	}
}

func TestRelayAdminHelperRequiresCompleteRepairAcknowledgement(t *testing.T) {
	tests := []struct {
		name   string
		result relayadmin.RepairResult
		want   string
	}{
		{name: "not ready", result: relayadmin.RepairResult{Ready: false, Restarting: true}, want: relayAdminRepairUnavailable},
		{name: "not restarting", result: relayadmin.RepairResult{Ready: true, Restarting: false}, want: relayAdminRepairUnavailable},
		{name: "neither", result: relayadmin.RepairResult{}, want: relayAdminRepairUnavailable},
		{name: "complete", result: relayadmin.RepairResult{Ready: true, Restarting: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &relayAdminClientFake{
				repair: func(context.Context) (relayadmin.RepairResult, error) {
					return test.result, nil
				},
			}
			helper, err := NewRelayAdminHelper(client)
			if err != nil {
				t.Fatalf("NewRelayAdminHelper: %v", err)
			}
			err = helper.Repair(context.Background())
			if test.want == "" && err != nil {
				t.Fatalf("Repair: %v", err)
			}
			if test.want != "" && (err == nil || err.Error() != test.want) {
				t.Fatalf("Repair error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRelayAdminHelperMapsFailuresToOperationSpecificLocalErrors(t *testing.T) {
	remoteSentinel := errors.New("/var/run/private.sock request=secret credential=private")
	failures := []error{
		remoteSentinel,
		relayadmin.ErrTransport,
		relayadmin.ErrInvalidResponse,
		&relayadmin.PublicError{Code: relayadmin.ErrorUnauthorized},
		&relayadmin.PublicError{Code: relayadmin.ErrorBusy},
	}
	for _, failure := range failures {
		t.Run(failure.Error(), func(t *testing.T) {
			client := &relayAdminClientFake{
				setup: func(context.Context, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error) {
					return relayadmin.OwnerBootstrapResult{}, failure
				},
				rotate: func(context.Context, relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error) {
					return relayadmin.EndpointRotationResult{}, failure
				},
				repair: func(context.Context) (relayadmin.RepairResult, error) {
					return relayadmin.RepairResult{}, failure
				},
			}
			helper, err := NewRelayAdminHelper(client)
			if err != nil {
				t.Fatalf("NewRelayAdminHelper: %v", err)
			}

			_, setupErr := helper.Setup(context.Background(), SetupRequest{})
			if setupErr == nil || setupErr.Error() != relayAdminSetupUnavailable {
				t.Fatalf("Setup error = %v, want %q", setupErr, relayAdminSetupUnavailable)
			}
			_, rotateErr := helper.Rotate(context.Background(), RotateRequest{})
			if rotateErr == nil || rotateErr.Error() != relayAdminRotateUnavailable {
				t.Fatalf("Rotate error = %v, want %q", rotateErr, relayAdminRotateUnavailable)
			}
			repairErr := helper.Repair(context.Background())
			if repairErr == nil || repairErr.Error() != relayAdminRepairUnavailable {
				t.Fatalf("Repair error = %v, want %q", repairErr, relayAdminRepairUnavailable)
			}
		})
	}
}

func TestNewRelayAdminHelperRejectsNilClient(t *testing.T) {
	if helper, err := NewRelayAdminHelper(nil); helper != nil || err == nil || err.Error() != relayAdminClientRequired {
		t.Fatalf("NewRelayAdminHelper(nil) = (%v, %v), want (nil, %q)", helper, err, relayAdminClientRequired)
	}
}

type relayAdminClientFake struct {
	setup  func(context.Context, relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error)
	rotate func(context.Context, relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error)
	repair func(context.Context) (relayadmin.RepairResult, error)
}

func (*relayAdminClientFake) Status(context.Context) (relayadmin.StatusResult, error) {
	return relayadmin.StatusResult{}, nil
}

func (client *relayAdminClientFake) Setup(ctx context.Context, request relayadmin.SetupRequest) (relayadmin.OwnerBootstrapResult, error) {
	if client.setup == nil {
		return relayadmin.OwnerBootstrapResult{}, nil
	}
	return client.setup(ctx, request)
}

func (client *relayAdminClientFake) Rotate(ctx context.Context, request relayadmin.RotateRequest) (relayadmin.EndpointRotationResult, error) {
	if client.rotate == nil {
		return relayadmin.EndpointRotationResult{}, nil
	}
	return client.rotate(ctx, request)
}

func (client *relayAdminClientFake) Repair(ctx context.Context) (relayadmin.RepairResult, error) {
	if client.repair == nil {
		return relayadmin.RepairResult{}, nil
	}
	return client.repair(ctx)
}
