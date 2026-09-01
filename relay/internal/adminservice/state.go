package adminservice

import (
	"context"
	"errors"

	"mobile-egress/internal/relayadmin"
	"mobile-egress/relay/internal/service"
)

// newMutationFinishedCallback builds the service lifecycle hook used by the
// daemon composition root. The getter is late-bound because Supervisor can be
// constructed before OpenAdminState while AdminStateOptions requires the hook
// during the open call. Slice 4 owns the public production composition entry
// point once its Darwin path and startup ordering inputs are concrete.
func newMutationFinishedCallback(
	state func() *service.AdminState,
	supervisor *Supervisor,
) (func(relayadmin.ReplayKey), error) {
	if state == nil || supervisor == nil {
		return nil, errors.New("invalid relay mutation-finished callback configuration")
	}
	return func(relayadmin.ReplayKey) {
		adminState := state()
		if adminState == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), relayadmin.OperationTimeout)
		defer cancel()
		snapshot, err := adminState.Snapshot(ctx)
		if err != nil || normalizeAdminSnapshot(snapshot).Class != service.AdminStateReady {
			return
		}
		// Runtime availability is deliberately separate from durable mutation
		// completion. A bind/open failure leaves the typed completed response
		// cached and is surfaced only by RelayRunning=false.
		_ = supervisor.Reconcile(ctx)
	}, nil
}

func normalizeAdminSnapshot(snapshot service.AdminSnapshot) service.AdminSnapshot {
	switch snapshot.Class {
	case service.AdminStateAbsent:
		if snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID != 0 {
			snapshot.Class = service.AdminStateIncompatible
		}
	case service.AdminStateReady:
		if !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID == 0 {
			snapshot.Class = service.AdminStateIncompatible
		}
	case service.AdminStateIncompatible:
		if !snapshot.OwnerUIDBound || snapshot.AdministrativeOwnerUID == 0 {
			snapshot.OwnerUIDBound = false
			snapshot.AdministrativeOwnerUID = 0
		}
	default:
		snapshot = service.AdminSnapshot{Class: service.AdminStateIncompatible}
	}
	return snapshot
}

func snapshotAuthorizationError(snapshot service.AdminSnapshot, peer relayadmin.Peer) error {
	snapshot = normalizeAdminSnapshot(snapshot)
	if snapshot.Class == service.AdminStateIncompatible &&
		(peer.UID() == 0 || snapshot.OwnerUIDBound && peer.UID() == snapshot.AdministrativeOwnerUID) {
		return &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	}
	return &relayadmin.PublicError{Code: relayadmin.ErrorUnauthorized}
}

func mapServiceAdminError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrAdminNotInitialized):
		return &relayadmin.PublicError{Code: relayadmin.ErrorNotInitialized}
	case errors.Is(err, service.ErrAdminAlreadyInitialized):
		return &relayadmin.PublicError{Code: relayadmin.ErrorAlreadyInitialized}
	case errors.Is(err, service.ErrAdminStateIncompatible):
		return &relayadmin.PublicError{Code: relayadmin.ErrorStateIncompatible}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		// Keep implementation details inside the process. Task 3A converts an
		// unexpected mutation error to its fixed indeterminate response.
		return err
	}
}
