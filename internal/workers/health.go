package workers

import (
	"context"
	"sync"
	"time"
)

type monitorKey struct{}

// Monitor observes completed polling cycles, independently of individual jobs
// succeeding. External delivery failures use durable retries, not restart loops.
type Monitor struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var loopLimits = map[string]time.Duration{
	// A 25-message SMTP batch can take nearly five minutes at its timeout
	// limits. Settlement batches can make up to 100 twenty-second requests.
	"email": 10 * time.Minute, "push": 2 * time.Minute, "push_receipts": 2 * time.Minute,
	"settlements": 45 * time.Minute, "batch_closure": 5 * time.Minute, "auto_confirm": 2 * time.Hour,
}

func NewMonitor() *Monitor {
	m := &Monitor{last: make(map[string]time.Time)}
	for name := range loopLimits {
		m.last[name] = time.Now()
	}
	return m
}

func WithMonitor(ctx context.Context, m *Monitor) context.Context {
	return context.WithValue(ctx, monitorKey{}, m)
}

func heartbeat(ctx context.Context, name string) {
	if m, ok := ctx.Value(monitorKey{}).(*Monitor); ok {
		m.mu.Lock()
		m.last[name] = time.Now()
		m.mu.Unlock()
	}
}

func (m *Monitor) Snapshot() (bool, map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	healthy := true
	checks := make(map[string]bool, len(loopLimits))
	for name, limit := range loopLimits {
		checks[name] = time.Since(m.last[name]) <= limit
		healthy = healthy && checks[name]
	}
	return healthy, checks
}
