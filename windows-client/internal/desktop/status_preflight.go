package desktop

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/relayclient"
)

// Privileged work uses current observations and live authenticated health;
// snapshot reads remain free of subprocess, network and storage operations.
func (app *DesktopApp) checkBridgeForNodeInstall(ctx context.Context) error {
	if app.core == nil || app.tailscale == nil {
		return errors.New("bridge unavailable")
	}
	owner, ready := app.core.OwnerSnapshot()
	if !ready {
		return errors.New("owner unavailable")
	}
	status, err := app.tailscale.Inspect(ctx)
	if err != nil {
		return err
	}
	state := app.currentRelayServiceState()
	if app.desktopPlatform() == platformMacOS && app.relayService != nil {
		state = relayStateFromObservation(app.relayService.Observe(ctx))
	}
	health, err := relayclient.Health(ctx, owner)
	if err != nil {
		return err
	}
	view := BridgeView{OwnerReady: true, TailscaleOnline: status.Online, FunnelReady: status.FunnelReady, RelayReady: health.Readiness, NeedsRotation: owner.RelayURL != status.PublicURL}
	if !bridgeReady(app.desktopPlatform(), state, view) {
		return errors.New("bridge unavailable")
	}
	return nil
}
