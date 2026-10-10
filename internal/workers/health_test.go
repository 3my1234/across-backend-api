package workers

import (
	"context"
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
