package service

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"mobile-egress/relay/internal/enrollment"
	"mobile-egress/relay/internal/protocol"
)

func TestSlowDNSDoesNotBlockEstablishedTrafficOrKeepalive(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	fixture.service.lookupNetIP = func(ctx context.Context, _, host string) ([]netip.Addr, error) {
		if host == "slow.test" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()
	writeEnvelope(t, client, openEnvelope("established", "fast.test", 443))
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "established"})
	_ = readEnvelope(t, client)
	writeEnvelope(t, client, openEnvelope("slow-open", "slow.test", 443))
	waitForSignal(t, started, "DNS start")
	writeEnvelope(t, client, dataEnvelope("established", "Zg"))
	writeEnvelope(t, client, protocol.Envelope{Version: 1, Type: protocol.TypePing})
	if frame := readEnvelope(t, agent); frame.Type != protocol.TypeData || frame.StreamID != "established" {
		t.Fatalf("expected established data, got %+v", frame)
	}
	if frame := readEnvelope(t, client); frame.Type != protocol.TypePong {
		t.Fatalf("expected pong, got %+v", frame)
	}
	writeEnvelope(t, client, openEnvelope("other-open", "fast.test", 443))
	if frame := readEnvelope(t, agent); frame.Type != protocol.TypeOpen || frame.StreamID != "other-open" {
		t.Fatalf("unrelated open blocked: %+v", frame)
	}
}

func waitForAdmittedStream(t *testing.T, service *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		admitted := service.streams[id] != nil
		service.mu.RUnlock()
		if admitted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream %s was not admitted", id)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPendingDNSReservesCapacityAndRejectsDuplicates(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.maxResolverWorkers = 3
	started := make(chan struct{}, 4)
	fixture.service.lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	first := newDormantSession(fixture.service, "first", enrollment.RoleClient)
	second := newDormantSession(fixture.service, "second", enrollment.RoleClient)
	agent := newDormantSession(fixture.service, "agent", enrollment.RoleAgent)
	registerTestSessions(fixture.service, first, second, agent)
	defer closeTestSessions(first, second, agent)
	for _, id := range []string{"one", "two"} {
		fixture.service.handleClientOpen(first, openEnvelope(id, "slow.test", 443))
		waitForSignal(t, started, "resolver start")
	}
	for _, tc := range []struct {
		client   *session
		id, code string
	}{
		{first, "one", "stream_in_use"},
	} {
		fixture.service.handleClientOpen(tc.client, openEnvelope(tc.id, "slow.test", 443))
		item, ok := tc.client.outbound.poll()
		if !ok || item.envelope.Type != protocol.TypeRejected || decodedErrorCode(t, item.envelope) != tc.code {
			t.Fatalf("expected %s rejection, got %+v", tc.code, item)
		}
	}
	fixture.service.handleClientOpen(second, openEnvelope("three", "slow.test", 443))
	waitForSignal(t, started, "third resolver start")
	fixture.service.handleClientOpen(second, openEnvelope("global-over", "slow.test", 443))
	item, ok := second.outbound.poll()
	if !ok || decodedErrorCode(t, item.envelope) != "agent_unavailable" {
		t.Fatalf("expected global limit, got %+v", item)
	}
	if err := fixture.service.handleClientStreamFrame(first, streamCloseEnvelope("one", "client_closed")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		fixture.service.mu.RLock()
		workers := fixture.service.resolverWorkers
		fixture.service.mu.RUnlock()
		if workers == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled resolver did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	fixture.service.handleClientOpen(second, openEnvelope("replacement", "slow.test", 443))
	waitForSignal(t, started, "replacement resolver start")
	fixture.service.mu.RLock()
	pending, active := len(fixture.service.pendingOpens), fixture.service.activeStreams
	fixture.service.mu.RUnlock()
	snapshot, _ := fixture.service.metrics.snapshot()
	if pending != 3 || active != 0 || snapshot.TotalStreams != 0 {
		t.Fatalf("pending=%d active=%d total=%d", pending, active, snapshot.TotalStreams)
	}
}

func TestLateCanceledDNSCannotForwardOrReleaseAnotherReservation(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	started, release := make(chan struct{}), make(chan struct{})
	releaseLookup := sync.OnceFunc(func() { close(release) })
	defer releaseLookup()
	fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
		close(started)
		<-release // Deliberately model a resolver completing after cancellation.
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	client := newDormantSession(fixture.service, "client", enrollment.RoleClient)
	agent := newDormantSession(fixture.service, "agent", enrollment.RoleAgent)
	registerTestSessions(fixture.service, client, agent)
	defer closeTestSessions(client, agent)
	fixture.service.handleClientOpen(client, openEnvelope("late", "slow.test", 443))
	waitForSignal(t, started, "resolver start")
	if err := fixture.service.handleClientStreamFrame(client, streamCloseEnvelope("late", "client_closed")); err != nil {
		t.Fatal(err)
	}
	// Retransmitted close is harmless, and the tombstone prevents immediate reuse.
	if err := fixture.service.handleClientStreamFrame(client, streamCloseEnvelope("late", "client_closed")); err != nil {
		t.Fatal(err)
	}
	fixture.service.handleClientOpen(client, openEnvelope("late", "slow.test", 443))
	item, ok := client.outbound.poll()
	if !ok || decodedErrorCode(t, item.envelope) != "stream_in_use" {
		t.Fatalf("canceled ID reused: %+v", item)
	}
	releaseLookup()
	fixture.service.workers.Wait()
	if frame, ok := agent.outbound.poll(); ok {
		t.Fatalf("late completion forwarded: %+v", frame.envelope)
	}
	if frame, ok := client.outbound.poll(); ok {
		t.Fatalf("late completion rejected canceled stream: %+v", frame.envelope)
	}
	fixture.service.mu.RLock()
	retained := len(fixture.service.pendingOpens) + len(fixture.service.streams)
	fixture.service.mu.RUnlock()
	if retained != 0 {
		t.Fatalf("late completion retained %d reservations", retained)
	}
}

func TestPendingDNSRejectsPrematureDataAndWrongOwnerClose(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) { <-ctx.Done(); return nil, ctx.Err() }
	client := newDormantSession(fixture.service, "client", enrollment.RoleClient)
	other := newDormantSession(fixture.service, "other", enrollment.RoleClient)
	registerTestSessions(fixture.service, client, other)
	defer closeTestSessions(client, other)
	fixture.service.handleClientOpen(client, openEnvelope("pending", "slow.test", 443))
	if err := fixture.service.handleClientStreamFrame(client, dataEnvelope("pending", "Zg")); err == nil {
		t.Fatal("accepted data before open")
	}
	if err := fixture.service.handleClientStreamFrame(other, streamCloseEnvelope("pending", "client_closed")); err == nil {
		t.Fatal("accepted wrong owner close")
	}
	fixture.service.mu.RLock()
	retained := fixture.service.pendingOpens["pending"] != nil
	fixture.service.mu.RUnlock()
	if !retained {
		t.Fatal("wrong owner removed pending stream")
	}
}

func TestPendingDNSCanceledOnDisconnectRevocationAndShutdown(t *testing.T) {
	for _, action := range []string{"disconnect", "revoke", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			fixture := newRelayFixture(t)
			defer fixture.Close()
			started, canceled := make(chan struct{}), make(chan struct{})
			fixture.service.lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				return nil, ctx.Err()
			}
			_, devices := enrollDevices(t, fixture, "client")
			client := mustDialSession(t, fixture, devices[0].client)
			defer client.Close()
			writeEnvelope(t, client, openEnvelope("cancel", "slow.test", 443))
			waitForSignal(t, started, "resolver start")
			switch action {
			case "disconnect":
				client.Close()
			case "revoke":
				if err := fixture.service.revokeIdentity(context.Background(), devices[0].serial, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				if err := fixture.service.Close(); err != nil {
					t.Fatal(err)
				}
			}
			waitForSignal(t, canceled, "resolver cancellation")
			fixture.service.mu.RLock()
			retained := len(fixture.service.pendingOpens) + len(fixture.service.streams)
			fixture.service.mu.RUnlock()
			if retained != 0 {
				t.Fatalf("%s retained %d streams", action, retained)
			}
		})
	}
}

func TestPendingDNSTimeoutRejectsAndReleasesReservation(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.openingTimeout = 20 * time.Millisecond
	fixture.service.lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) { <-ctx.Done(); return nil, ctx.Err() }
	_, devices := enrollDevices(t, fixture, "client")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	writeEnvelope(t, client, openEnvelope("timeout", "slow.test", 443))
	frame := readEnvelope(t, client)
	if frame.Type != protocol.TypeRejected || decodedErrorCode(t, frame) != "dns_failure" {
		t.Fatalf("timeout outcome: %+v", frame)
	}
	fixture.service.mu.RLock()
	pending := len(fixture.service.pendingOpens)
	fixture.service.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("timeout retained %d reservations", pending)
	}
}

func TestCanceledResolverKeepsWorkerPermitUntilItExits(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.maxResolverWorkers = 1
	started, release := make(chan struct{}, 2), make(chan struct{})
	releaseLookup := sync.OnceFunc(func() { close(release) })
	defer releaseLookup()
	fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
		started <- struct{}{}
		<-release
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	client := newDormantSession(fixture.service, "client", enrollment.RoleClient)
	agent := newDormantSession(fixture.service, "agent", enrollment.RoleAgent)
	registerTestSessions(fixture.service, client, agent)
	defer closeTestSessions(client, agent)
	fixture.service.handleClientOpen(client, openEnvelope("cancel", "slow.test", 443))
	waitForSignal(t, started, "resolver start")
	if err := fixture.service.handleClientStreamFrame(client, streamCloseEnvelope("cancel", "client_closed")); err != nil {
		t.Fatal(err)
	}
	fixture.service.handleClientOpen(client, openEnvelope("too-soon", "slow.test", 443))
	item, ok := client.outbound.poll()
	if !ok || decodedErrorCode(t, item.envelope) != "agent_unavailable" {
		t.Fatalf("launched another resolver before canceled worker exited: %+v", item)
	}
	releaseLookup()
	fixture.service.workers.Wait()
	fixture.service.handleClientOpen(client, openEnvelope("after-exit", "slow.test", 443))
	waitForAdmittedStream(t, fixture.service, "after-exit")
}

func TestClientCloseCancelsPendingDNS(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	started, canceled := make(chan struct{}), make(chan struct{})
	fixture.service.lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()
	writeEnvelope(t, client, openEnvelope("cancel-dns", "slow.test", 443))
	waitForSignal(t, started, "DNS start")
	writeEnvelope(t, client, streamCloseEnvelope("cancel-dns", "client_closed"))
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("client close did not cancel DNS")
	}
	writeEnvelope(t, client, protocol.Envelope{Version: 1, Type: protocol.TypePing})
	if frame := readEnvelope(t, client); frame.Type != protocol.TypePong {
		t.Fatalf("cancel emitted unexpected response: %+v", frame)
	}
}

func TestEstablishedStreamsDoNotConsumeDNSWorkersOrLoseOwnership(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.lookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	client := newDormantSession(fixture.service, "client", enrollment.RoleClient)
	other := newDormantSession(fixture.service, "other", enrollment.RoleClient)
	agent := newDormantSession(fixture.service, "agent", enrollment.RoleAgent)
	registerTestSessions(fixture.service, client, other, agent)
	defer closeTestSessions(client, other, agent)
	for i := 0; i < 1100; i++ {
		id := fmt.Sprintf("held-%d", i)
		fixture.service.handleClientOpen(client, openEnvelope(id, "fast.test", 443))
		fixture.service.workers.Wait()
		item, ok := agent.outbound.poll()
		if !ok || item.envelope.StreamID != id {
			t.Fatalf("open %d not admitted", i)
		}
	}
	fixture.service.handleClientOpen(other, openEnvelope("extra", "fast.test", 443))
	fixture.service.workers.Wait()
	if item, ok := agent.outbound.poll(); !ok || item.envelope.StreamID != "extra" {
		t.Fatal("additional Client was capped")
	}
	fixture.service.handleClientOpen(other, openEnvelope("held-0", "fast.test", 443))
	item, ok := other.outbound.poll()
	if !ok || decodedErrorCode(t, item.envelope) != "stream_in_use" {
		t.Fatal("live ownership evicted")
	}
	fixture.service.mu.RLock()
	active, workers := fixture.service.activeStreams, fixture.service.resolverWorkers
	fixture.service.mu.RUnlock()
	if active != 1101 || workers != 0 {
		t.Fatalf("active=%d workers=%d", active, workers)
	}
	for i := 0; i < 1100; i++ {
		if err := fixture.service.handleClientStreamFrame(client, streamCloseEnvelope(fmt.Sprintf("held-%d", i), "client_closed")); err != nil {
			t.Fatal(err)
		}
		agent.outbound.poll()
	}
	fixture.service.mu.RLock()
	remaining := len(fixture.service.streams)
	fixture.service.mu.RUnlock()
	if remaining != 1 {
		t.Fatalf("cleanup retained %d", remaining)
	}
}
