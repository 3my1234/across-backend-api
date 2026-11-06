package controllers

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

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
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-slot.token:
		if err := ctx.Err(); err != nil {
			slot.token <- struct{}{}
			drop()
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { slot.token <- struct{}{}; drop() }) }, nil
	}
}
