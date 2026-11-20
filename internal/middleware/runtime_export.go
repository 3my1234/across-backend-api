package middleware

import (
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"runtime"
)

// Local root-owned monitoring reads only this aggregate file, never tokens or logs.
// Replace atomically so a collector cannot mistake a partial write for a counter reset.
func (m *RequestMetrics) ExportRuntime(pool *pgxpool.Pool) error {
	p := pool.Stat()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	data := map[string]any{"http": m.Snapshot(), "go": map[string]any{"heap_bytes": mem.HeapAlloc, "goroutines": runtime.NumGoroutine(), "gc_pause_ns_total": mem.PauseTotalNs}, "pool": map[string]any{"max": p.MaxConns(), "acquired": p.AcquiredConns(), "acquires": p.AcquireCount(), "empty_acquires": p.EmptyAcquireCount(), "cancelled": p.CanceledAcquireCount(), "wait_ms_total": p.AcquireDuration().Milliseconds(), "empty_wait_ms_total": p.EmptyAcquireWaitTime().Milliseconds()}}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err = os.WriteFile("/tmp/atl-api-metrics.pending", b, 0600); err != nil {
		return err
	}
	return os.Rename("/tmp/atl-api-metrics.pending", "/tmp/atl-api-metrics.json")
}
