package workers

import (
	"testing"
	"time"
)

func TestMediaHealthAllowsBoundedVideoAndDetectsStall(t *testing.T) {
	m, err := NewMonitorForRoles([]string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	m.last["media"] = time.Now().Add(-5*time.Minute - 15*time.Second)
	if healthy, _ := m.Snapshot(); !healthy {
		t.Fatal("bounded five-minute video plus cleanup triggers restart")
	}
	m.last["media"] = time.Now().Add(-6*time.Minute - time.Second)
	if healthy, _ := m.Snapshot(); healthy {
		t.Fatal("stalled media worker is indefinitely healthy")
	}
}
