package desktop

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"mobile-egress/internal/clientcontrol"
	"mobile-egress/windows-client/internal/cloud"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestInvitationOriginRefreshRetainsExpiryAndOnlyResumesApprovedClient(t *testing.T) {
	ctx := context.Background()
	repo := cloud.NewRepository(securestore.NewMemoryStore())
	node := pairedNodeForDesktopTest(t)
	expires := time.Now().Add(-time.Minute).UTC()
	invitation := clientcontrol.Invitation{Version: 1, Type: clientcontrol.InvitationType, ID: node.EnrollmentID, NodeID: node.NodeID, DisplayName: node.DisplayName, RelayURL: node.RelayURL, CACertificatePEM: string(desktopTestCA(t)), Capability: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ExpiresAt: expires}
	encoded, err := clientcontrol.EncodeInvitation(invitation)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ReserveClient(ctx, node.NodeID, node.DisplayName); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveClientInvitation(ctx, cloud.PendingClient{NodeID: node.NodeID, DisplayName: node.DisplayName, EnrollmentID: node.EnrollmentID, Invitation: encoded, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	app := &DesktopApp{cloudRepository: repo}
	owner := relayclient.Identity{RelayURL: "https://new.example:8443"}
	view, err := app.resumeClientInvitation(ctx, owner, node.NodeID)
	if err != nil || view.Resuming {
		t.Fatalf("unapproved expired invitation is not resumable: %v", err)
	}
	if err = repo.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	view, err = app.resumeClientInvitation(ctx, owner, node.NodeID)
	if err != nil || !view.Resuming {
		t.Fatalf("approved pending Client cannot resume: %v", err)
	}
	refreshed, err := clientcontrol.DecodeInvitation(view.Invitation)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ExpiresAt.Equal(expires) || refreshed.RelayURL != owner.RelayURL {
		t.Fatal("incorrect refresh deadline or origin")
	}
	refreshed.RelayURL = invitation.RelayURL
	if refreshed != invitation {
		t.Fatal("origin refresh changed invitation authority or expiry")
	}
}
