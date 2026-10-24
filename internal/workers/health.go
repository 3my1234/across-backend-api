package workers

import (
	"context"
	"errors"
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
	"driver_expiry": 3 * time.Minute, "town_hall_expiry": 2 * time.Minute, "web_push": 2 * time.Minute, "email": 10 * time.Minute, "push": 2 * time.Minute, "push_receipts": 2 * time.Minute,
	"settlements": 45 * time.Minute, "batch_closure": 5 * time.Minute, "auto_confirm": 2 * time.Hour,
	"play_billing": 6 * time.Minute,
}

func NewMonitor() *Monitor {
	roles := make([]string, 0, len(loopLimits))
	for name := range loopLimits {
		roles = append(roles, name)
	}
	m, _ := NewMonitorForRoles(roles)
	return m
}

func NewMonitorForRoles(roles []string) (*Monitor, error) {
	if len(roles) == 0 {
		return nil, errors.New("worker monitor selection is empty")
	}
	m := &Monitor{last: make(map[string]time.Time)}
	for _, name := range roles {
		if _, ok := loopLimits[name]; !ok {
			return nil, errors.New("worker monitor selection is invalid")
		}
		if _, exists := m.last[name]; exists {
			return nil, errors.New("worker monitor selection is duplicated")
		}
		m.last[name] = time.Now()
	}
	return m, nil
}

func WithMonitor(ctx context.Context, m *Monitor) context.Context {
	return context.WithValue(ctx, monitorKey{}, m)
}

func heartbeat(ctx context.Context, name string) {
	if m, ok := ctx.Value(monitorKey{}).(*Monitor); ok {
		m.mu.Lock()
		if _, selected := m.last[name]; selected {
			m.last[name] = time.Now()
		}
		m.mu.Unlock()
	}
}

func (m *Monitor) Snapshot() (bool, map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	healthy := true
	checks := make(map[string]bool, len(m.last))
	for name := range m.last {
		limit := loopLimits[name]
		checks[name] = time.Since(m.last[name]) <= limit
		healthy = healthy && checks[name]
	}
	return healthy, checks
}
