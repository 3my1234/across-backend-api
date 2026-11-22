package realtime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Redis only carries a target user ID; never send message text or credentials.
const redisChannel = "atl:realtime:wake:v1"

type RedisBridge struct {
	client *redis.Client
	hub    *Hub
	ready  atomic.Bool
}

func NewRedisBridge(client *redis.Client, hub *Hub) *RedisBridge {
	return &RedisBridge{client: client, hub: hub}
}

func (b *RedisBridge) Ready() bool { return b.ready.Load() }

// Publish is called only after the operation that changed data has committed.
// It is best-effort: authoritative HTTP catch-up handles a failed publish.
func (b *RedisBridge) Publish(ctx context.Context, userID string) error {
	if b.client == nil || b.hub == nil {
		return errors.New("real-time bridge unavailable")
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return errors.New("invalid recipient")
	}
	return b.client.Publish(ctx, redisChannel, id.String()).Err()
}

// Run subscribes once per API process, not once per device. A missing Redis
// subscription is explicitly surfaced so clients can use polling fallback.
func (b *RedisBridge) Run(ctx context.Context) {
	if b.client == nil || b.hub == nil {
		return
	}
	for ctx.Err() == nil {
		b.consume(ctx)
		b.ready.Store(false)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (b *RedisBridge) consume(ctx context.Context) {
	sub := b.client.Subscribe(ctx, redisChannel)
	defer sub.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = sub.Close()
		case <-finished:
		}
	}()
	if _, err := sub.Receive(ctx); err != nil {
		return
	}
	b.ready.Store(true)
	defer b.ready.Store(false)
	for ctx.Err() == nil {
		msg, err := sub.ReceiveMessage(ctx)
		if err != nil {
			return
		}
		if msg.Payload == "hall" {
			b.hub.SignalHall()
			continue
		}
		id, err := uuid.Parse(msg.Payload)
		if err == nil {
			b.hub.Signal(id.String())
		}
	}
}
