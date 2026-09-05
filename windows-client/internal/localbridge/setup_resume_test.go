package localbridge

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/relayclient"
	"mobile-egress/windows-client/internal/securestore"
	"mobile-egress/windows-client/internal/tailscale"
	"testing"
)

type retryOwnerSink struct {
	fakeOwnerSink
	fail bool
}

func (s *retryOwnerSink) SaveOwnerIdentity(ctx context.Context, identity relayclient.Identity) error {
	if s.fail {
		return errors.New("Keychain locked")
	}
	return s.fakeOwnerSink.SaveOwnerIdentity(ctx, identity)
}

type replaySetupHelper struct {
	fakeElevatedHelper
	result       OwnerBootstrapResult
	calls        int
	id           string
	csr          string
	lostResponse bool
}

func (h *replaySetupHelper) Setup(ctx context.Context, req SetupRequest) (OwnerBootstrapResult, error) {
	h.calls++
	if h.calls == 1 {
		h.id = req.RequestID
		h.csr = req.OwnerCSRPEM
		var err error
		h.result, err = h.fakeElevatedHelper.Setup(ctx, req)
		if err == nil && h.lostResponse {
			return OwnerBootstrapResult{}, errors.New("connection lost after commit")
		}
		return h.result, err
	}
	if req.RequestID == "" || req.RequestID != h.id || req.OwnerCSRPEM != h.csr {
		return OwnerBootstrapResult{}, errors.New("retry changed identity")
	}
	return h.result, nil
}

func TestSetupResumesAfterLostRelayResponse(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	bridge := &fakeTailscaleBridge{status: tailscale.Status{Online: true, FunnelReady: true, FQDN: "bridge.tail123.ts.net", PublicURL: "https://bridge.tail123.ts.net:8443"}}
	helper := &replaySetupHelper{fakeElevatedHelper: fakeElevatedHelper{t: t}, lostResponse: true}
	sink := &fakeOwnerSink{}
	manager := NewResumableManager(bridge, helper, sink, store)
	if _, err := manager.Setup(ctx); err == nil {
		t.Fatal("expected lost response")
	}
	manager = NewResumableManager(bridge, helper, sink, store)
	if _, err := manager.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	assertIdentityCertificateMatchesPrivateKey(t, sink.identity)
}

type unavailableSetupStore struct{ *securestore.MemoryStore }

func (store unavailableSetupStore) Put(context.Context, string, []byte) error {
	return errors.New("Keychain unavailable")
}
func TestSetupDoesNotInitializeRelayUntilPendingKeyIsDurable(t *testing.T) {
	bridge := &fakeTailscaleBridge{status: tailscale.Status{Online: true, FunnelReady: true, FQDN: "bridge.tail123.ts.net", PublicURL: "https://bridge.tail123.ts.net:8443"}}
	helper := &replaySetupHelper{fakeElevatedHelper: fakeElevatedHelper{t: t}}
	manager := NewResumableManager(bridge, helper, &fakeOwnerSink{}, unavailableSetupStore{securestore.NewMemoryStore()})
	if _, err := manager.Setup(context.Background()); err == nil {
		t.Fatal("expected secure storage failure")
	}
	if helper.calls != 0 {
		t.Fatal("relay was initialized before persisting its key")
	}
}
func TestSetupResumesAfterOwnerSaveFailureAndAppRestart(t *testing.T) {
	ctx := context.Background()
	store := securestore.NewMemoryStore()
	bridge := &fakeTailscaleBridge{status: tailscale.Status{Online: true, FunnelReady: true, FQDN: "bridge.tail123.ts.net", PublicURL: "https://bridge.tail123.ts.net:8443"}}
	helper := &replaySetupHelper{fakeElevatedHelper: fakeElevatedHelper{t: t}}
	sink := &retryOwnerSink{fail: true}
	manager := NewResumableManager(bridge, helper, sink, store)
	if _, err := manager.Setup(ctx); err == nil {
		t.Fatal("expected failed Owner save")
	}
	manager = NewResumableManager(bridge, helper, sink, store)
	if pending, err := manager.HasPendingSetup(ctx); err != nil || !pending {
		t.Fatal("pending setup lost across restart")
	}
	sink.fail = false
	if _, err := manager.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	assertIdentityCertificateMatchesPrivateKey(t, sink.identity)
	if pending, err := manager.HasPendingSetup(ctx); err != nil || pending {
		t.Fatal("completed setup still pending")
	}
}
