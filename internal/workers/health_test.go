package workers

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMonitorDetectsAndRecoversStalledLoop(t *testing.T) {
	m := NewMonitor()
	if healthy, _ := m.Snapshot(); !healthy {
		t.Fatal("startup grace should be healthy")
	}
	m.last["email"] = time.Now().Add(-11 * time.Minute)
	if healthy, checks := m.Snapshot(); healthy || checks["email"] {
		t.Fatal("stalled email consumer must be unhealthy")
	}
	heartbeat(WithMonitor(context.Background(), m), "email")
	if healthy, _ := m.Snapshot(); !healthy {
		t.Fatal("completed polling should restore heartbeat")
	}
	heartbeat(context.Background(), "email") // inline API mode has no monitor
}

func TestMonitorConcurrentPollingAndProbes(t *testing.T) {
	m := NewMonitor()
	ctx := WithMonitor(context.Background(), m)
	var group sync.WaitGroup
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 100; n++ {
				heartbeat(ctx, "email")
				if healthy, _ := m.Snapshot(); !healthy {
					t.Error("active concurrent polling should remain healthy")
				}
			}
		}()
	}
	group.Wait()
}
