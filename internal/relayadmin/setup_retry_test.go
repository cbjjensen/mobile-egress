package relayadmin

import (
	"context"
	"testing"
)

func TestSetupRetryAcrossClientsReusesCommittedResponse(t *testing.T) {
	calls := 0
	handler := &behaviorHandler{setup: func(context.Context, Peer, Mutation, SetupRequest) (OwnerBootstrapResult, error) {
		calls++
		return OwnerBootstrapResult{CertificatePEM: "owner", CACertificatePEM: "ca", Serial: "1", Role: "owner"}, nil
	}}
	server := newTestServer(handler, NewMemoryReplayStore(MemoryReplayConfig{}))
	request := SetupRequest{PublicName: "name", PublicURL: "url", OwnerCSRPEM: "csr"}
	for i := 0; i < 2; i++ {
		client := &Client{Dial: serverDialer(t, server, NewPeer(501, nil), nil)}
		result, err := client.SetupWithRequestID(context.Background(), "12345678901234567890123456789012", request)
		if err != nil || result.Serial != "1" {
			t.Fatalf("retry: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("setup executed %d times", calls)
	}
}
