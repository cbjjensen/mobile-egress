package service

import (
	"context"
	"maps"
	"sync"
	"time"
)

// Traffic accounting never holds this lock across database I/O.
type trafficMetrics struct {
	mu            sync.Mutex
	totals        metricsSnapshot
	revision      uint64
	flushMu       sync.Mutex
	savedRevision uint64
}

func (metrics *trafficMetrics) addBytes(count int64) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.totals.ByteCount += count
	metrics.revision++
}

func (metrics *trafficMetrics) addStream() {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.totals.TotalStreams++
	metrics.revision++
}

func (metrics *trafficMetrics) addError(code string) {
	if !validErrorCode(code) {
		return
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if metrics.totals.ErrorCounts == nil {
		metrics.totals.ErrorCounts = make(map[string]int64)
	}
	metrics.totals.ErrorCounts[code]++
	metrics.revision++
}

func (metrics *trafficMetrics) snapshot() (metricsSnapshot, uint64) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	snapshot := metrics.totals
	snapshot.ErrorCounts = maps.Clone(snapshot.ErrorCounts)
	return snapshot, metrics.revision
}

func (metrics *trafficMetrics) flush(ctx context.Context, state *store) error {
	metrics.flushMu.Lock()
	defer metrics.flushMu.Unlock()
	snapshot, revision := metrics.snapshot()
	if revision == metrics.savedRevision {
		return nil
	}
	if err := state.saveMetrics(ctx, snapshot); err != nil {
		return err
	}
	metrics.savedRevision = revision
	return nil
}

func (service *Service) runMetricsWriter(ctx context.Context) {
	defer close(service.metricsDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			flushContext, cancel := context.WithTimeout(ctx, 5*time.Second)
			// A failed snapshot remains dirty and is retried on the next tick.
			_ = service.metrics.flush(flushContext, service.store)
			cancel()
		}
	}
}

// Absolute values are safe to retry even if a commit's outcome was uncertain.
// Only the running service writes these aggregate tables.
func (state *store) saveMetrics(ctx context.Context, snapshot metricsSnapshot) error {
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE metrics SET total_streams = ?, byte_count = ? WHERE singleton_id = 1`, snapshot.TotalStreams, snapshot.ByteCount); err != nil {
		return err
	}
	for code, count := range snapshot.ErrorCounts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO error_metrics(code, count) VALUES (?, ?) ON CONFLICT(code) DO UPDATE SET count = excluded.count`, code, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}
