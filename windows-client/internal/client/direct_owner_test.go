package client

import (
	"context"
	"testing"

	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
)

func TestAdoptOwnerIdentityStoresControllerWithoutCreatingLocalClient(t *testing.T) {
	t.Parallel()

	core, err := NewCore(context.Background(), securestore.NewMemoryStore(), &fakeGateway{})
	if err != nil {
		t.Fatal(err)
	}
	owner := relayclient.Identity{
		RelayURL: "https://bridge.tail123.ts.net:8443", DialAddress: "127.0.0.1:8443", Role: "owner", Serial: "A1",
		PrivateKeyPEM: "private", CertificatePEM: "certificate", CACertificatePEM: "ca",
	}
	if err := core.AdoptOwnerIdentity(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	status := core.Status()
	if !status.OwnerReady || status.ClientReady || status.Paired {
		t.Fatalf("status after direct Owner adoption = %#v", status)
	}
	if err := core.AdoptOwnerIdentity(context.Background(), owner); err == nil {
		t.Fatal("AdoptOwnerIdentity() replaced an existing Owner")
	}
}
