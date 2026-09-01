package adminservice

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

type HandlerConfig struct {
	State         *service.AdminState
	Supervisor    *Supervisor
	AdminGID      uint32
	HelperVersion string
}

type Handler struct {
	state         *service.AdminState
	supervisor    *Supervisor
	adminGID      uint32
	helperVersion string
}

func NewHandler(config HandlerConfig) (*Handler, error) {
	if config.State == nil || config.Supervisor == nil || config.AdminGID == 0 ||
		strings.TrimSpace(config.HelperVersion) == "" || !utf8.ValidString(config.HelperVersion) {
		return nil, errors.New("invalid relay admin handler configuration")
	}
	return &Handler{
		state: config.State, supervisor: config.Supervisor,
		adminGID: config.AdminGID, helperVersion: config.HelperVersion,
	}, nil
}

func (handler *Handler) Authorize(ctx context.Context, peer relayadmin.Peer, operation relayadmin.Operation) bool {
	if handler == nil || handler.state == nil || !operation.Valid() {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := handler.state.Snapshot(ctx)
	if err != nil {
		return false
	}
	return authorizeSnapshot(normalizeAdminSnapshot(snapshot), peer, operation, handler.adminGID)
}

func (handler *Handler) Status(ctx context.Context, peer relayadmin.Peer) (relayadmin.StatusResult, error) {
	snapshot, err := handler.authorizedSnapshot(ctx, peer, relayadmin.OperationStatus)
	if err != nil {
		return relayadmin.StatusResult{}, err
	}
	switch snapshot.Class {
	case service.AdminStateAbsent:
		return relayadmin.StatusResult{
			ProtocolVersion: relayadmin.Version,
			HelperVersion:   handler.helperVersion,
			Initialized:     false,
			RelayRunning:    handler.supervisor.Snapshot().RelayRunning,
		}, nil
	case service.AdminStateReady:
		return relayadmin.StatusResult{
			ProtocolVersion: relayadmin.Version,
			HelperVersion:   handler.helperVersion,
			Initialized:     true,
			RelayRunning:    handler.supervisor.Snapshot().RelayRunning,
		}, nil
	default:
		return relayadmin.StatusResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
}

func (handler *Handler) Setup(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
	request relayadmin.SetupRequest,
) (relayadmin.OwnerBootstrapResult, error) {
	snapshot, err := handler.authorizedSnapshot(ctx, peer, relayadmin.OperationSetup)
	if err != nil {
		return relayadmin.OwnerBootstrapResult{}, err
	}
	switch snapshot.Class {
	case service.AdminStateReady:
		return relayadmin.OwnerBootstrapResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorAlreadyInitialized}
	case service.AdminStateIncompatible:
		return relayadmin.OwnerBootstrapResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	case service.AdminStateAbsent:
		if peer.UID() == 0 {
			return relayadmin.OwnerBootstrapResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorUnauthorized}
		}
	default:
		return relayadmin.OwnerBootstrapResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
	result, err := handler.state.BootstrapOwner(ctx, mutation.Transaction, service.AdminBootstrapOwnerOptions{
		PublicName:             request.PublicName,
		PublicURL:              request.PublicURL,
		CSRPEM:                 request.OwnerCSRPEM,
		AdministrativeOwnerUID: peer.UID(),
	})
	if err != nil {
		return relayadmin.OwnerBootstrapResult{}, mapServiceAdminError(err)
	}
	return relayadmin.OwnerBootstrapResult{
		CertificatePEM: result.CertificatePEM, CACertificatePEM: result.CACertificatePEM,
		Serial: result.Serial, Role: string(result.Role),
	}, nil
}

func (handler *Handler) Rotate(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
	request relayadmin.RotateRequest,
) (relayadmin.EndpointRotationResult, error) {
	snapshot, err := handler.authorizedSnapshot(ctx, peer, relayadmin.OperationRotate)
	if err != nil {
		return relayadmin.EndpointRotationResult{}, err
	}
	switch snapshot.Class {
	case service.AdminStateAbsent:
		return relayadmin.EndpointRotationResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorNotInitialized}
	case service.AdminStateIncompatible:
		return relayadmin.EndpointRotationResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	case service.AdminStateReady:
	default:
		return relayadmin.EndpointRotationResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
	if err := handler.supervisor.Stop(ctx); err != nil {
		return relayadmin.EndpointRotationResult{}, err
	}
	result, err := handler.state.RotateEndpoint(ctx, mutation.Transaction, service.RotateEndpointOptions{
		PublicName: request.PublicName, PublicURL: request.PublicURL,
	})
	if err != nil {
		return relayadmin.EndpointRotationResult{}, mapServiceAdminError(err)
	}
	return relayadmin.EndpointRotationResult{PublicURL: result.PublicURL, Serial: result.Serial}, nil
}

func (handler *Handler) Repair(
	ctx context.Context,
	peer relayadmin.Peer,
	mutation relayadmin.Mutation,
) (relayadmin.RepairResult, error) {
	snapshot, err := handler.authorizedSnapshot(ctx, peer, relayadmin.OperationRepair)
	if err != nil {
		return relayadmin.RepairResult{}, err
	}
	if snapshot.Class == service.AdminStateAbsent {
		return relayadmin.RepairResult{}, &relayadmin.PublicError{Code: relayadmin.ErrorNotInitialized}
	}
	if err := handler.state.Repair(ctx, mutation.Transaction); err != nil {
		return relayadmin.RepairResult{}, mapServiceAdminError(err)
	}
	return relayadmin.RepairResult{Ready: true, Restarting: true}, nil
}

func (handler *Handler) authorizedSnapshot(
	ctx context.Context,
	peer relayadmin.Peer,
	operation relayadmin.Operation,
) (service.AdminSnapshot, error) {
	if handler == nil || handler.state == nil {
		return service.AdminSnapshot{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot, err := handler.state.Snapshot(ctx)
	if err != nil {
		return service.AdminSnapshot{}, &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
	snapshot = normalizeAdminSnapshot(snapshot)
	if !authorizeSnapshot(snapshot, peer, operation, handler.adminGID) {
		return snapshot, snapshotAuthorizationError(snapshot, peer)
	}
	return snapshot, nil
}

var _ relayadmin.Handler = (*Handler)(nil)
