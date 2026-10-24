package workers

import (
	"context"
	"sync"
	"testing"
	"time"

	"across/backend/internal/config"
)

func TestOnlySelectedConsumersStartAndDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 3)
	runners := map[string]func(context.Context){}
	for _, name := range []string{"email", "push", "settlements"} {
		name := name
		runners[name] = func(ctx context.Context) { started <- name; <-ctx.Done() }
	}
	done := make(chan error, 1)
	go func() { done <- runSelected(ctx, []string{"email", "push"}, runners) }()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatal("selected worker did not start")
		}
	}
	if seen["settlements"] || !seen["email"] || !seen["push"] {
		t.Fatal("unselected work ran")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("workers did not drain on cancellation")
	}
	select {
	case <-started:
		t.Fatal("unexpected worker started")
	default:
	}
}

func TestInvalidSelectionStartsNothing(t *testing.T) {
	var mu sync.Mutex
	count := 0
	runners := map[string]func(context.Context){"email": func(context.Context) { mu.Lock(); count++; mu.Unlock() }}
	for _, roles := range [][]string{nil, {"email", "unknown"}, {"email", "email"}} {
		if runSelected(context.Background(), roles, runners) == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Fatal("partial selection executed before validation")
	}
}

func TestSelectedMonitorIgnoresDisabledLoopsAndDetectsStall(t *testing.T) {
	roles, _ := (config.Config{}).WorkerRoleNames()
	all, err := NewMonitorForRoles(roles)
	if err != nil || len(all.last) != len(loopLimits) {
		t.Fatal("consumer/health role inventories differ")
	}
	m, err := NewMonitorForRoles([]string{"push", "push_receipts"})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat(WithMonitor(context.Background(), m), "email")
	if healthy, checks := m.Snapshot(); !healthy || len(checks) != 2 {
		t.Fatal("disabled role affected health")
	}
	m.mu.Lock()
	m.last["push"] = time.Now().Add(-loopLimits["push"] - time.Second)
	m.mu.Unlock()
	if healthy, checks := m.Snapshot(); healthy || checks["push"] {
		t.Fatal("selected stalled loop was not detected")
	}
	for _, selection := range [][]string{nil, {"unknown"}, {"push", "push"}} {
		if _, err := NewMonitorForRoles(selection); err == nil {
			t.Fatal("invalid monitor accepted")
		}
	}
}
