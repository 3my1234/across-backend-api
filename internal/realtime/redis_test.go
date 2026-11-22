package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestCrossReplicaWakeupAndIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	first, second := NewHub(4, 2), NewHub(4, 2)
	alice := "11111111-1111-4111-8111-111111111111"
	bob := "22222222-2222-4222-8222-222222222222"
	a, _ := first.Subscribe(alice)
	defer a.Close()
	a2, _ := second.Subscribe(alice)
	defer a2.Close()
	b, _ := second.Subscribe(bob)
	defer b.Close()
	bridge1, bridge2 := NewRedisBridge(client, first), NewRedisBridge(client, second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge1.Run(ctx)
	go bridge2.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for (!bridge1.Ready() || !bridge2.Ready()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bridge1.Ready() || !bridge2.Ready() {
		t.Fatal("subscribers did not become ready")
	}
	if err := bridge1.Publish(ctx, alice); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Subscription{a, a2} {
		select {
		case <-s.Wakeups():
		case <-time.After(time.Second):
			t.Fatal("replica missed wakeup")
		}
	}
	select {
	case <-b.Wakeups():
		t.Fatal("event leaked to another user")
	default:
	}
	if err := bridge1.Publish(ctx, "not-a-user"); err == nil {
		t.Fatal("invalid recipient accepted")
	}
}

func TestUnavailableBridgeReportsNotReady(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: 0})
	defer client.Close()
	b := NewRedisBridge(client, NewHub(2, 1))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge failed to stop")
	}
	if b.Ready() {
		t.Fatal("unavailable Redis must not report ready")
	}
}
