package desktop

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/nodeservice"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/sealedconfig"
)

type pairedControlAPI struct {
	Create   func(context.Context, relayclient.Identity, string, string) (clientcontrol.Invitation, error)
	Cancel   func(context.Context, relayclient.Identity, string) error
	List     func(context.Context, relayclient.Identity) ([]clientcontrol.Enrollment, error)
	Approve  func(context.Context, relayclient.Identity, string) (clientcontrol.Enrollment, error)
	Deliver  func(context.Context, relayclient.Identity, string, uint64, sealedconfig.Envelope) error
	Statuses func(context.Context, relayclient.Identity) ([]clientcontrol.Status, error)
	Revoke   func(context.Context, relayclient.Identity, string) error
}

func (app *DesktopApp) pairedControl() *pairedControlAPI {
	if app.pairedAPI != nil {
		return app.pairedAPI
	}
	return &pairedControlAPI{Create: relayclient.CreateClientInvitation, Cancel: relayclient.CancelClientInvitation, List: relayclient.ClientEnrollments, Approve: relayclient.ApproveClientEnrollment, Deliver: relayclient.DeliverClientConfiguration, Statuses: relayclient.OwnerClientStatuses, Revoke: relayclient.Revoke}
}

type ClientInvitationView struct {
	NodeID     string    `json:"nodeId"`
	Invitation string    `json:"invitation"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Resuming   bool      `json:"resuming"`
}

type ClientFleetView struct {
	Nodes           []cloud.ManagedNodeView   `json:"nodes"`
	Pending         []cloud.PendingClientView `json:"pending"`
	ConnectionError string                    `json:"connectionError,omitempty"`
}

func (app *DesktopApp) IssueClientInvitation(displayName string) (ClientInvitationView, error) {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	defer app.monitorAction(componentMetadata)()
	ctx, cancel := context.WithTimeout(app.operationContext(), 20*time.Second)
	defer cancel()
	owner, _, err := app.ownerRepository.LoadOwnerIdentity(ctx)
	if err != nil {
		return ClientInvitationView{}, errors.New("Set up the local bridge before adding a Client.")
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return ClientInvitationView{}, errors.New("Unable to create Client invitation.")
	}
	id := "paired-" + hex.EncodeToString(raw)
	if err = app.cloudRepository.ReserveClient(ctx, id, strings.TrimSpace(displayName)); err != nil {
		return ClientInvitationView{}, err
	}
	return app.resumeClientInvitation(ctx, owner, id)
}

// ClientInvitation is an explicit reveal action, never included in snapshots.
func (app *DesktopApp) ClientInvitation(nodeID string) (ClientInvitationView, error) {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	ctx, cancel := context.WithTimeout(app.operationContext(), 20*time.Second)
	defer cancel()
	owner, _, err := app.ownerRepository.LoadOwnerIdentity(ctx)
	if err != nil {
		return ClientInvitationView{}, errors.New("Local bridge identity is unavailable.")
	}
	return app.resumeClientInvitation(ctx, owner, nodeID)
}

func (app *DesktopApp) resumeClientInvitation(ctx context.Context, owner relayclient.Identity, nodeID string) (ClientInvitationView, error) {
	pending, err := app.cloudRepository.PendingClients(ctx)
	if err != nil {
		return ClientInvitationView{}, errors.New("Unable to read saved Client invitations.")
	}
	for _, p := range pending {
		if p.NodeID == nodeID {
			if p.Invitation != "" {
				invite, err := clientcontrol.DecodeInvitation(p.Invitation)
				if err != nil {
					return ClientInvitationView{}, errors.New("Saved Client invitation is invalid. Cancel it and create another.")
				}
				if invite.RelayURL != owner.RelayURL {
					invite.RelayURL = owner.RelayURL
					p.Invitation, err = clientcontrol.EncodeInvitation(invite)
					if err != nil {
						return ClientInvitationView{}, errors.New("Unable to refresh the Client invitation endpoint.")
					}
					if err = app.cloudRepository.SaveClientInvitation(ctx, p); err != nil {
						return ClientInvitationView{}, errors.New("Unable to save the refreshed Client invitation.")
					}
				}
				nodes, err := app.cloudRepository.Nodes(ctx)
				if err != nil {
					return ClientInvitationView{}, errors.New("Unable to read saved Client configuration.")
				}
				resuming := false
				for _, node := range nodes {
					if node.NodeID == p.NodeID && node.EnrollmentID == p.EnrollmentID {
						resuming = true
						break
					}
				}
				return ClientInvitationView{NodeID: p.NodeID, Invitation: p.Invitation, ExpiresAt: p.ExpiresAt, Resuming: resuming}, nil
			}
			invite, err := app.pairedControl().Create(ctx, owner, p.NodeID, p.DisplayName)
			if err != nil {
				return ClientInvitationView{}, errors.New("Unable to issue the invitation. The Client slot is saved; retry from Clients.")
			}
			if invite.NodeID != p.NodeID || invite.DisplayName != p.DisplayName {
				return ClientInvitationView{}, errors.New("Relay returned an incompatible Client invitation.")
			}
			encoded, err := clientcontrol.EncodeInvitation(invite)
			if err != nil {
				return ClientInvitationView{}, errors.New("Relay returned an invalid Client invitation.")
			}
			p.EnrollmentID = invite.ID
			p.Invitation = encoded
			p.ExpiresAt = invite.ExpiresAt
			if err = app.cloudRepository.SaveClientInvitation(ctx, p); err != nil {
				return ClientInvitationView{}, errors.New("Unable to save the Client invitation. Retry from Clients.")
			}
			return ClientInvitationView{NodeID: p.NodeID, Invitation: encoded, ExpiresAt: p.ExpiresAt}, nil
		}
	}
	return ClientInvitationView{}, errors.New("That Client invitation is no longer pending.")
}

func (app *DesktopApp) CancelClientInvitation(nodeID string) error {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	defer app.monitorAction(componentMetadata)()
	ctx, cancel := context.WithTimeout(app.operationContext(), 20*time.Second)
	defer cancel()
	owner, _, err := app.ownerRepository.LoadOwnerIdentity(ctx)
	if err != nil {
		return errors.New("Local bridge identity is unavailable.")
	}
	pending, err := app.cloudRepository.PendingClients(ctx)
	if err != nil {
		return errors.New("Unable to read pending Clients.")
	}
	for _, p := range pending {
		if p.NodeID == nodeID {
			// An unanswered creation may have reached the relay. Recover its ID before cancellation.
			if p.EnrollmentID == "" {
				if _, err = app.resumeClientInvitation(ctx, owner, nodeID); err != nil {
					return err
				}
				return app.cancelRecoveredInvitation(ctx, owner, nodeID)
			}
			if err = app.pairedControl().Cancel(ctx, owner, p.EnrollmentID); err != nil {
				return errors.New("Client cancellation was not confirmed. Retry when the bridge is reachable.")
			}
			return app.cloudRepository.RemoveClient(ctx, nodeID)
		}
	}
	return errors.New("That Client invitation is no longer pending.")
}

func (app *DesktopApp) cancelRecoveredInvitation(ctx context.Context, owner relayclient.Identity, nodeID string) error {
	pending, err := app.cloudRepository.PendingClients(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.NodeID == nodeID {
			if err = app.pairedControl().Cancel(ctx, owner, p.EnrollmentID); err != nil {
				return errors.New("Client cancellation was not confirmed. Retry when the bridge is reachable.")
			}
			return app.cloudRepository.RemoveClient(ctx, nodeID)
		}
	}
	return nil
}

func (app *DesktopApp) RevokeClient(nodeID string) error {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	defer app.monitorAction(componentMetadata)()
	ctx, cancel := context.WithTimeout(app.operationContext(), 20*time.Second)
	defer cancel()
	owner, _, err := app.ownerRepository.LoadOwnerIdentity(ctx)
	if err != nil {
		return errors.New("Local bridge identity is unavailable.")
	}
	nodes, err := app.cloudRepository.Nodes(ctx)
	if err != nil {
		return errors.New("Unable to read Clients.")
	}
	for _, node := range nodes {
		if node.NodeID == nodeID {
			if node.Management == cloud.ManagementAWS {
				if !app.provisioning.TryLock() {
					return errors.New("An EC2 management operation is still running. Retry this revocation when it finishes.")
				}
				defer app.provisioning.Unlock()
			}
			if err = app.pairedControl().Revoke(ctx, owner, node.ClientSerial); err != nil {
				return errors.New("Client revocation was not confirmed. Retry when the bridge is reachable.")
			}
			return app.cloudRepository.RemoveClient(ctx, nodeID)
		}
	}
	return errors.New("Client was not found.")
}

// RefreshClients performs bounded control-plane reconciliation independently of
// AWS. Only this live result claims connection knowledge; persisted metadata does not.
func (app *DesktopApp) RefreshClients() (ClientFleetView, error) {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	defer app.monitorAction(componentMetadata)()
	ctx, cancel := context.WithTimeout(app.operationContext(), 12*time.Second)
	defer cancel()
	connectionError := ""
	var statuses []clientcontrol.Status
	statusKnown := false
	owner, _, ownerErr := app.ownerRepository.LoadOwnerIdentity(ctx)
	if ownerErr == nil {
		if app.provisioning.TryLock() {
			_, err := app.cloudRepository.StageClientEndpoints(ctx, owner.RelayURL)
			app.provisioning.Unlock()
			if err != nil {
				connectionError = "Client endpoint updates could not be saved. Restore secure storage to resume."
			}
		}
		if err := app.syncPairedClients(ctx, owner); err != nil {
			connectionError = "Client pairing could not finish. Keep the bridge connected; retry or cancel the saved invitation."
		}
		var err error
		statuses, err = app.pairedControl().Statuses(ctx, owner)
		statusKnown = err == nil
		if err != nil {
			connectionError = "Client connection status is unavailable. Saved configuration is shown."
			statuses = nil
		}
	}
	app.startAWSEndpointUpdates()
	nodes, err := app.cloudRepository.NodeViews(ctx)
	if err != nil {
		return ClientFleetView{}, errors.New("Unable to read saved Clients.")
	}
	pending, err := app.cloudRepository.PendingClients(ctx)
	if err != nil {
		return ClientFleetView{}, errors.New("Unable to read pending Clients.")
	}
	for i := range nodes {
		if statusKnown {
			nodes[i].ConnectionKnown = true
		}
		for _, status := range statuses {
			if strings.EqualFold(status.ClientSerial, nodes[i].ClientSerial) {
				nodes[i].Connected = status.Connected
				break
			}
		}
	}
	views := make([]cloud.PendingClientView, 0, len(pending))
	for _, p := range pending {
		views = append(views, cloud.PendingClientView{NodeID: p.NodeID, DisplayName: p.DisplayName, ExpiresAt: p.ExpiresAt})
	}
	return ClientFleetView{Nodes: nodes, Pending: views, ConnectionError: connectionError}, nil
}

func (app *DesktopApp) syncPairedClients(ctx context.Context, owner relayclient.Identity) error {
	pending, err := app.cloudRepository.PendingClients(ctx)
	if err != nil {
		return err
	}
	control := app.pairedControl()
	if len(pending) > 0 {
		enrollments, err := control.List(ctx, owner)
		if err != nil {
			return err
		}
		for _, p := range pending {
			if p.EnrollmentID == "" {
				if _, err := app.resumeClientInvitation(ctx, owner, p.NodeID); err != nil {
					return err
				}
				continue
			}
			for _, enrollment := range enrollments {
				if enrollment.ID != p.EnrollmentID || enrollment.NodeID != p.NodeID {
					continue
				}
				nodes, err := app.cloudRepository.Nodes(ctx)
				if err != nil {
					return err
				}
				var node *cloud.ManagedNode
				for i := range nodes {
					if nodes[i].NodeID == p.NodeID {
						node = &nodes[i]
						break
					}
				}
				if enrollment.State == "canceled" || enrollment.State == "expired" {
					if node == nil {
						if err = control.Cancel(ctx, owner, p.EnrollmentID); err != nil {
							return err
						}
						if err = app.cloudRepository.RemoveClient(ctx, p.NodeID); err != nil {
							return err
						}
					}
					continue
				}
				if enrollment.State == "invited" {
					continue
				}
				if node == nil {
					approved, err := control.Approve(ctx, owner, enrollment.ID)
					if err != nil {
						return err
					}
					if approved.ID != p.EnrollmentID || approved.NodeID != p.NodeID || approved.Bootstrap == nil || approved.ClientSerial == "" || approved.CertificatePEM == "" || approved.CACertificatePEM == "" {
						return errors.New("incomplete Client enrollment")
					}
					username, err := newProxyCredential()
					if err != nil {
						return err
					}
					password, err := newProxyCredential()
					if err != nil {
						return err
					}
					created := cloud.ManagedNode{NodeID: p.NodeID, DisplayName: p.DisplayName, Platform: approved.Bootstrap.Platform, Architecture: approved.Bootstrap.Architecture, Management: cloud.ManagementPaired, EnrollmentID: approved.ID, ClientSerial: strings.ToUpper(approved.ClientSerial), ConfigurationPublicKey: approved.Bootstrap.ConfigurationPublicKey, ConfigurationGeneration: 1, ServiceVersion: approved.Bootstrap.ServiceVersion, Health: "configuring", SOCKSUsername: username, SOCKSPassword: password, SOCKSPort: 1080, RelayURL: owner.RelayURL, CertificatePEM: approved.CertificatePEM, CACertificatePEM: approved.CACertificatePEM}
					envelope, err := sealPairedConfiguration(created)
					if err != nil {
						return err
					}
					sealed, err := json.Marshal(envelope)
					if err != nil {
						return err
					}
					created.SealedConfiguration = string(sealed)
					if err = app.cloudRepository.SaveNode(ctx, created); err != nil {
						return err
					}
					node = &created
				}
				if node.AppliedGeneration == 0 && enrollment.State != "acknowledged" {
					if err = app.deliverPairedConfiguration(ctx, owner, *node); err != nil {
						return err
					}
				}
			}
		}
	}
	statuses, err := control.Statuses(ctx, owner)
	if err != nil {
		return err
	}
	nodes, err := app.cloudRepository.Nodes(ctx)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Management != cloud.ManagementPaired {
			continue
		}
		for _, status := range statuses {
			if status.NodeID != node.NodeID || status.EnrollmentID != node.EnrollmentID || !strings.EqualFold(status.ClientSerial, node.ClientSerial) || status.AppliedGeneration == 0 || status.AppliedGeneration > node.ConfigurationGeneration || status.AppliedGeneration < node.AppliedGeneration {
				continue
			}
			previous := node
			node.AppliedGeneration = status.AppliedGeneration
			node.SealedConfiguration = ""
			if status.ServiceVersion != "" {
				node.ServiceVersion = status.ServiceVersion
			}
			if status.AppliedGeneration == node.ConfigurationGeneration {
				node.Health = "installed"
			}
			if node != previous {
				if err = app.cloudRepository.SaveNode(ctx, node); err != nil {
					return err
				}
			}
			if err = app.cloudRepository.CompleteClientInvitation(ctx, node.NodeID); err != nil {
				return err
			}
		}
	}
	return nil
}

func newProxyCredential() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func pairedConfiguration(node cloud.ManagedNode) nodeservice.Configuration {
	return nodeservice.Configuration{Version: 1, Generation: node.ConfigurationGeneration, RelayURL: node.RelayURL, Role: "client", Serial: node.ClientSerial, CertificatePEM: node.CertificatePEM, CACertificatePEM: node.CACertificatePEM, SOCKSUsername: node.SOCKSUsername, SOCKSPassword: node.SOCKSPassword, SOCKSPort: node.SOCKSPort}
}

func sealPairedConfiguration(node cloud.ManagedNode) (sealedconfig.Envelope, error) {
	if node.SealedConfiguration != "" {
		var envelope sealedconfig.Envelope
		if err := clientcontrol.DecodeJSON([]byte(node.SealedConfiguration), &envelope); err != nil {
			return envelope, err
		}
		_, err := envelope.Fingerprint()
		return envelope, err
	}
	raw, err := json.Marshal(pairedConfiguration(node))
	if err != nil {
		return sealedconfig.Envelope{}, err
	}
	defer clear(raw)
	return sealedconfig.Seal(node.ConfigurationPublicKey, raw)
}

func (app *DesktopApp) deliverPairedConfiguration(ctx context.Context, owner relayclient.Identity, node cloud.ManagedNode) error {
	envelope, err := sealPairedConfiguration(node)
	if err != nil {
		return errors.New("Unable to seal Client configuration.")
	}
	// Enrollment always delivers the original generation. Later endpoints are
	// imported separately, even if the Funnel changed before its receipt arrived.
	if err = app.pairedControl().Deliver(ctx, owner, node.EnrollmentID, 1, envelope); err != nil {
		return errors.New("Client configuration delivery is pending. Keep the controller open to retry.")
	}
	return nil
}

func (app *DesktopApp) ExportClientEndpointUpdate(nodeID string) (string, error) {
	app.pairedMu.Lock()
	defer app.pairedMu.Unlock()
	nodes, err := app.cloudRepository.Nodes(app.operationContext())
	if err != nil {
		return "", errors.New("Unable to read saved Clients.")
	}
	for _, node := range nodes {
		if node.NodeID == nodeID && node.Management == cloud.ManagementPaired {
			node.SealedConfiguration = "" // Export the desired endpoint, never a retained enrollment delivery.
			envelope, err := sealPairedConfiguration(node)
			if err != nil {
				return "", errors.New("Unable to seal the connection update.")
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				return "", err
			}
			return clientcontrol.EncodeEndpointUpdate(clientcontrol.EndpointUpdate{Version: 1, Type: clientcontrol.EndpointUpdateType, NodeID: node.NodeID, EnrollmentID: node.EnrollmentID, Envelope: raw})
		}
	}
	return "", errors.New("Paired Client was not found.")
}
