package realtime

import (
	"errors"
	"sync"
	"testing"
)

func TestHubIsolationCoalescingAndLimits(t *testing.T) {
	h := NewHub(3, 2)
	a, _ := h.Subscribe("alice")
	a2, _ := h.Subscribe("alice")
	b, _ := h.Subscribe("bob")
	if _, err := h.Subscribe("alice"); !errors.Is(err, ErrCapacity) {
		t.Fatal("expected cap")
	}
	for i := 0; i < 100; i++ {
		h.Signal("alice")
	}
	select {
	case <-a.Wakeups():
	default:
		t.Fatal("first session missed wakeup")
	}
	select {
	case <-a2.Wakeups():
	default:
		t.Fatal("second session missed wakeup")
	}
	select {
	case <-a.Wakeups():
		t.Fatal("signals must coalesce")
	default:
	}
	select {
	case <-b.Wakeups():
		t.Fatal("signal leaked to another user")
	default:
	}
	a.Close()
	a.Close()
	a2.Close()
	b.Close()
	if h.Active() != 0 {
		t.Fatal("subscriptions leaked")
	}
}

func TestHubConcurrentSignalAndClose(t *testing.T) {
	h := NewHub(100, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		s, err := h.Subscribe("alice")
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); h.Signal("alice"); s.Close() }()
	}
	wg.Wait()
	if h.Active() != 0 {
		t.Fatal("subscriptions leaked")
	}
}

func TestPerUserLimitDoesNotBlockAnotherUser(t *testing.T) {
	h := NewHub(4, 1)
	a, err := h.Subscribe("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Subscribe("alice"); !errors.Is(err, ErrCapacity) {
		t.Fatal("expected per-user cap")
	}
	b, err := h.Subscribe("bob")
	if err != nil {
		t.Fatal("another user should be admitted", err)
	}
	a.Close()
	b.Close()
}

func TestHallSignalsOnlyReachWatchingSockets(t *testing.T) {
	h := NewHub(4, 2)
	watcher, _ := h.Subscribe("alice")
	other, _ := h.Subscribe("bob")
	defer watcher.Close()
	defer other.Close()
	watcher.WatchHall(true)
	h.SignalHall()
	select {
	case <-watcher.Wakeups():
	default:
		t.Fatal("hall watcher missed signal")
	}
	select {
	case <-other.Wakeups():
		t.Fatal("uninterested socket received hall broadcast")
	default:
	}
	watcher.WatchHall(false)
	h.SignalHall()
	select {
	case <-watcher.Wakeups():
		t.Fatal("unsubscribed socket received hall signal")
	default:
	}
}
