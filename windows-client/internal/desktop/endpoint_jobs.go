package desktop

import (
	"context"
	"time"

	"mobile-egress/windows-client/internal/cloud"
)

// Delivery never holds pairedMu: standalone pairing, status and exports remain
// available during an AWS outage. provisioning serializes desired state with
// EC2 install/repair/revocation and in-flight legacy generation receipts.
func (app *DesktopApp) startAWSEndpointUpdates() {
	aws := app.currentAWSClient()
	if aws == nil {
		return
	}
	app.mu.Lock()
	if app.endpointJobRunning || time.Now().Before(app.endpointJobNext) {
		app.mu.Unlock()
		return
	}
	app.endpointJobRunning = true
	app.mu.Unlock()
	go func() {
		defer func() {
			app.mu.Lock()
			app.endpointJobRunning = false
			app.endpointJobNext = time.Now().Add(time.Minute)
			app.mu.Unlock()
		}()
		app.applyAWSEndpointUpdates(aws)
	}()
}

func (app *DesktopApp) applyAWSEndpointUpdates(runner cloud.CommandRunner) {
	defer app.monitorAction(componentMetadata)()
	nodes, err := app.cloudRepository.Nodes(app.operationContext())
	if err != nil {
		return
	}
	for _, candidate := range nodes {
		if candidate.Management != cloud.ManagementAWS || candidate.Health != "configuring" {
			continue
		}
		app.provisioning.Lock()
		current, loadErr := app.cloudRepository.Nodes(app.operationContext())
		if loadErr != nil {
			app.provisioning.Unlock()
			return
		}
		for _, node := range current {
			if node.NodeID != candidate.NodeID || node.Management != cloud.ManagementAWS || node.Health != "configuring" {
				continue
			}
			ctx, cancel := context.WithTimeout(app.operationContext(), 45*time.Second)
			// A per-Client delivery failure leaves its queue pending, but must
			// not prevent other Clients from receiving the new endpoint.
			_, _ = cloud.NewOrchestrator(runner, nil, app.cloudRepository).ReapplyConfiguration(ctx, node)
			cancel()
			break
		}
		app.provisioning.Unlock()
	}
}
