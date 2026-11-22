package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestPostgresRelayDeduplicatesAcrossReplicas(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	sub := client.Subscribe(ctx, redisChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := PostgresRelay{Redis: client}, PostgresRelay{Redis: client}
	payload := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa:11111111-1111-4111-8111-111111111111"
	if err := a.forward(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if err := b.forward(ctx, payload); err != nil {
		t.Fatal(err)
	}
	read, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	message, err := sub.ReceiveMessage(read)
	if err != nil || message.Payload != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected relay %v %v", message, err)
	}
	readAgain, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if _, err = sub.ReceiveMessage(readAgain); err == nil {
		t.Fatal("duplicate event published")
	}
	if err = a.forward(ctx, "malformed"); err == nil {
		t.Fatal("malformed event accepted")
	}
}
