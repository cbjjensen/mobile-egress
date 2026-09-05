package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"mobile-egress/relay/internal/protocol"
)

func TestEstablishedTrafficDoesNotWaitForDatabaseConnection(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	_, devices := enrollDevices(t, fixture, "client", "agent")
	client := mustDialSession(t, fixture, devices[0].client)
	defer client.Close()
	agent := mustDialSession(t, fixture, devices[1].client)
	defer agent.Close()
	writeEnvelope(t, client, openEnvelope("no-sql", "8.8.8.8", 443))
	_ = readEnvelope(t, agent)
	writeEnvelope(t, agent, protocol.Envelope{Version: 1, Type: protocol.TypeOpened, StreamID: "no-sql"})
	_ = readEnvelope(t, client)

	// Exhaust the single SQLite connection, as a slow persistence transaction does.
	held, err := fixture.service.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_ = agent.SetReadDeadline(time.Now().Add(time.Second))
	for range 2 {
		writeEnvelope(t, client, dataEnvelope("no-sql", "Zg"))
		if frame := readEnvelope(t, agent); frame.Type != protocol.TypeData {
			t.Fatalf("got %s", frame.Type)
		}
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	for range 2 {
		writeEnvelope(t, agent, dataEnvelope("no-sql", "Zg"))
		if frame := readEnvelope(t, client); frame.Type != protocol.TypeData {
			t.Fatalf("got %s", frame.Type)
		}
	}
}

func TestMetricSnapshotsRetryWithoutDoubleCountingAndSkipUnchanged(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	metrics := &trafficMetrics{}
	metrics.addStream()
	metrics.addBytes(17)
	metrics.addError("dns_failure")
	// Fail after the aggregate UPDATE to prove the whole snapshot rolls back.
	_, err := fixture.service.store.db.Exec(`CREATE TRIGGER fail_metrics BEFORE INSERT ON error_metrics BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := metrics.flush(context.Background(), fixture.service.store); err == nil {
		t.Fatal("flush unexpectedly succeeded")
	}
	persisted, err := fixture.service.store.metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ByteCount != 0 || persisted.TotalStreams != 0 {
		t.Fatalf("partial snapshot persisted: %+v", persisted)
	}
	if _, err := fixture.service.store.db.Exec(`DROP TRIGGER fail_metrics`); err != nil {
		t.Fatal(err)
	}
	metrics.addBytes(5)
	if err := metrics.flush(context.Background(), fixture.service.store); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := metrics.snapshot()
	// Reapply the same snapshot, modeling retry of a commit with unknown outcome.
	if err := fixture.service.store.saveMetrics(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	persisted, err = fixture.service.store.metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := metricsSnapshot{TotalStreams: 1, ByteCount: 22, ErrorCounts: map[string]int64{"dns_failure": 1}}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("persisted = %+v, want %+v", persisted, want)
	}
	held, err := fixture.service.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := metrics.flush(ctx, fixture.service.store); err != nil {
		t.Fatalf("unchanged snapshot attempted database work: %v", err)
	}
}

func TestMetricsRemainWritableWhileSnapshotPersistenceBlocked(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	metrics := &trafficMetrics{}
	metrics.addBytes(7)
	held, err := fixture.service.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flushed := make(chan error, 1)
	go func() { flushed <- metrics.flush(ctx, fixture.service.store) }()
	deadline := time.After(2 * time.Second)
	for fixture.service.store.db.Stats().WaitCount == 0 {
		select {
		case <-deadline:
			t.Fatal("flush did not reach database wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	updated := make(chan struct{})
	go func() { metrics.addBytes(9); metrics.addStream(); metrics.addError("target_failure"); close(updated) }()
	select {
	case <-updated:
	case <-time.After(time.Second):
		t.Fatal("accounting blocked behind persistence")
	}
	cancel()
	if err := <-flushed; err == nil {
		t.Fatal("blocked flush did not fail on cancellation")
	}
	held.Close()
	if err := metrics.flush(context.Background(), fixture.service.store); err != nil {
		t.Fatal(err)
	}
	persisted, err := fixture.service.store.metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ByteCount != 16 || persisted.TotalStreams != 1 || persisted.ErrorCounts["target_failure"] != 1 {
		t.Fatalf("lost concurrent updates: %+v", persisted)
	}
}

func TestMetricsLiveHealthPeriodicFlushAndShutdownRestore(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.metrics.addBytes(11)
	fixture.service.metrics.addStream()
	fixture.service.metrics.addError("dns_failure")
	response := httptest.NewRecorder()
	fixture.service.handleHealth(response, httptest.NewRequest("GET", "/healthz", nil))
	var health healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.ByteCount != 11 || health.TotalStreams != 1 || health.ErrorCounts["dns_failure"] != 1 {
		t.Fatalf("live health = %+v", health)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, err := fixture.service.store.metrics(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.ByteCount == 11 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic writer did not persist statistics")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fixture.service.metrics.addBytes(3)
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	snapshot, _ := restored.metrics.snapshot()
	if snapshot.ByteCount != 14 || snapshot.TotalStreams != 1 || snapshot.ErrorCounts["dns_failure"] != 1 {
		t.Fatalf("restored = %+v", snapshot)
	}
}

func TestShutdownReturnsFinalMetricPersistenceFailure(t *testing.T) {
	fixture := newRelayFixture(t)
	defer fixture.Close()
	fixture.service.metrics.addBytes(1)
	_, err := fixture.service.store.db.Exec(`CREATE TRIGGER fail_metrics BEFORE UPDATE ON metrics BEGIN SELECT RAISE(ABORT, 'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Close(); err == nil {
		t.Fatal("shutdown hid final flush failure")
	}
	if err := fixture.service.Close(); err == nil {
		t.Fatal("repeated shutdown lost failure")
	}
}
