package controllers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const roomWriteMaxWait = 2 * time.Second
const roomWriteMaxQueued = 32 // In addition to the writer holding the token.

var errRoomWriteBusy = errors.New("room write queue busy")

func (s *CommunityController) acquireRoomWrite(c *fiber.Ctx, room string) (func(), error) {
	release, err := s.roomWrites.acquire(c.Context(), room)
	if errors.Is(err, errRoomWriteBusy) {
		c.Set("Retry-After", "2")
		c.Set("Cache-Control", "no-store")
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "This chat is busy. Please retry with the same message reference.")
	}
	return release, err
}

// A hot room already serializes writes in PostgreSQL. Queue its local writers
// BEFORE taking a pooled connection so they cannot occupy the entire API pool
// while waiting for that same row. This is only scheduling: the database lock,
// fresh authorization, idempotency and atomic notifications remain mandatory.
// Other replicas/closure/membership changes still synchronize in PostgreSQL.
type roomWriteQueue struct {
	mu    sync.Mutex
	rooms map[string]*roomWriteSlot
}

type roomWriteSlot struct {
	token chan struct{}
	refs  int
}

func (q *roomWriteQueue) acquire(ctx context.Context, room string) (func(), error) {
	// fasthttp's request context is not cancelled on an individual disconnect.
	// Bound queue residence independently; this deadline does not cancel a write
	// that has already acquired the token or relax any latency/error gate.
	waitCtx, cancel := context.WithTimeout(ctx, roomWriteMaxWait)
	defer cancel()
	// Different textual UUID encodings refer to the same database row.
	if id, err := uuid.Parse(room); err == nil {
		room = id.String()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.mu.Lock()
	if q.rooms == nil {
		q.rooms = make(map[string]*roomWriteSlot)
	}
	slot := q.rooms[room]
	if slot == nil {
		slot = &roomWriteSlot{token: make(chan struct{}, 1)}
		slot.token <- struct{}{}
		q.rooms[room] = slot
	}
	if slot.refs >= roomWriteMaxQueued+1 {
		q.mu.Unlock()
		return nil, errRoomWriteBusy
	}
	slot.refs++
	q.mu.Unlock()
	drop := func() {
		q.mu.Lock()
		slot.refs--
		if slot.refs == 0 {
			delete(q.rooms, room)
		}
		q.mu.Unlock()
	}
	select {
	case <-waitCtx.Done():
		drop()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errRoomWriteBusy
	case <-slot.token:
		if err := waitCtx.Err(); err != nil {
			slot.token <- struct{}{}
			drop()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errRoomWriteBusy
		}
		var once sync.Once
		return func() { once.Do(func() { slot.token <- struct{}{}; drop() }) }, nil
	}
}
