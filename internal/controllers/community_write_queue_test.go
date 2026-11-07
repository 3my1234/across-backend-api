package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRoomWriteQueueCancellationAndIndependentRooms(t *testing.T) {
	var q roomWriteQueue
	room := uuid.NewString()
	release, err := q.acquire(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		r, e := q.acquire(ctx, strings.ToUpper(room))
		if r != nil {
			r()
		}
		finished <- e
	}()
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		refs := q.rooms[room].refs
		q.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter did not queue on canonical UUID")
		}
		time.Sleep(time.Millisecond)
	}
	other, err := q.acquire(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	other()
	cancel()
	if err := <-finished; err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	release()
	release() // deferred/explicit release cannot over-release.
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				r, e := q.acquire(context.Background(), room)
				if e != nil {
					t.Error(e)
					return
				}
				n := active.Add(1)
				if n > 1 {
					peak.Store(n)
				}
				time.Sleep(time.Microsecond)
				active.Add(-1)
				r()
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 0 {
		t.Fatal("same-room critical sections overlapped")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.rooms) != 0 {
		t.Fatalf("leaked %d room entries", len(q.rooms))
	}
}

func TestRoomWriteQueueBacklogBound(t *testing.T) {
	var q roomWriteQueue
	room := uuid.NewString()
	release, err := q.acquire(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, roomWriteMaxQueued)
	for i := 0; i < roomWriteMaxQueued; i++ {
		go func() {
			r, e := q.acquire(ctx, room)
			if r != nil {
				r()
			}
			results <- e
		}()
	}
	deadline := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		refs := q.rooms[room].refs
		q.mu.Unlock()
		if refs == roomWriteMaxQueued+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue did not reach its bound")
		}
		time.Sleep(time.Millisecond)
	}
	if r, e := q.acquire(context.Background(), room); !errors.Is(e, errRoomWriteBusy) || r != nil {
		t.Fatalf("full queue: %v", e)
	}
	other, e := q.acquire(context.Background(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	other()
	cancel()
	for i := 0; i < roomWriteMaxQueued; i++ {
		if e := <-results; e != context.Canceled {
			t.Fatalf("waiter: %v", e)
		}
	}
	release()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.rooms) != 0 {
		t.Fatal("queue entries leaked")
	}
}

func TestRoomWriteQueueDeadlineReturnsRetryBeforeDatabase(t *testing.T) {
	// A nil database makes any accidental Begin observable. Timeout must occur
	// before the handler attempts a transaction or persists a write.
	s := &CommunityController{}
	room := uuid.NewString()
	release, err := s.roomWrites.acquire(context.Background(), room)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/hall/:room_id/offers", s.HallOffer)
	body := fmt.Sprintf(`{"unit_price":1200,"client_message_id":"%s"}`, uuid.NewString())
	req := httptest.NewRequest("POST", "/hall/"+room+"/offers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	res, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 || res.Header.Get("Retry-After") != "2" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v", res.StatusCode, res.Header)
	}
	if time.Since(started) < roomWriteMaxWait {
		t.Fatal("request failed before its queue deadline")
	}
	release()
	next, e := s.roomWrites.acquire(context.Background(), room)
	if e != nil {
		t.Fatal(e)
	}
	next()
}

// Two independent API controllers share a deliberately small pool. An external
// transaction (another replica/closure) holds the row lock. Queued HTTP retries
// must leave connections for other operations, then persist only one message
// and one durable notification per recipient after the lock is released.
func TestCommunityHotRoomQueuePreservesPoolAndRetryAtomicity(t *testing.T) {
	db, room, actor := fanoutFixture(t, 3)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1,$2,'member')`, room, actor); err != nil {
		t.Fatal(err)
	}
	cfg := db.Config()
	cfg.MaxConns = 4
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	blocker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT id FROM chat_rooms WHERE id=$1 FOR UPDATE`, room); err != nil {
		t.Fatal(err)
	}
	controllers := []*CommunityController{{db: pool, market: &ProviderMarketplaceController{db: pool}}, {db: pool, market: &ProviderMarketplaceController{db: pool}}}
	apps := make([]*fiber.App, 2)
	for i, s := range controllers {
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", actor); return c.Next() })
		app.Post("/rooms/:conversation_id/messages", s.Send)
		apps[i] = app
	}
	body := fmt.Sprintf(`{"message":"Concurrent retry","client_message_id":"%s"}`, uuid.NewString())
	type result struct {
		status int
		id     string
		err    error
	}
	results := make(chan result, 16)
	for i := 0; i < 16; i++ {
		go func(i int) {
			req := httptest.NewRequest("POST", "/rooms/"+room+"/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			res, e := apps[i%2].Test(req, 10000)
			if e != nil {
				results <- result{err: e}
				return
			}
			defer res.Body.Close()
			raw, e := io.ReadAll(res.Body)
			var saved struct {
				ID string `json:"id"`
			}
			if e == nil {
				e = json.Unmarshal(raw, &saved)
			}
			results <- result{status: res.StatusCode, id: saved.ID, err: e}
		}(i)
	}
	// Wait for all requests to enter the queue, not a timing-dependent sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		refs := 0
		for _, s := range controllers {
			s.roomWrites.mu.Lock()
			if entry := s.roomWrites.rooms[room]; entry != nil {
				refs += entry.refs
			}
			s.roomWrites.mu.Unlock()
		}
		if refs == 16 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("requests did not enter room queues")
		}
		time.Sleep(time.Millisecond)
	}
	if n := pool.Stat().AcquiredConns(); n > 2 {
		t.Fatalf("hot room occupies %d of four connections", n)
	}
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var one int
	if e := pool.QueryRow(probe, `SELECT 1`).Scan(&one); e != nil {
		t.Fatalf("unrelated operation starved: %v", e)
	}
	if e := blocker.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	var id string
	for i := 0; i < 16; i++ {
		r := <-results
		if r.err != nil || r.status != 201 || r.id == "" {
			t.Fatalf("retry: %+v", r)
		}
		if id != "" && id != r.id {
			t.Fatal("retry created different message")
		}
		id = r.id
	}
	var messages, notifications, queued int
	if e := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM chat_messages),(SELECT count(*) FROM notifications),(SELECT count(*) FROM fanout_queue_probe)`).Scan(&messages, &notifications, &queued); e != nil || messages != 1 || notifications != 3 || queued != 3 {
		t.Fatalf("message=%d notification=%d queue=%d err=%v", messages, notifications, queued, e)
	}
	// A separate writer can close the room; the next sender must recheck state.
	if _, e := db.Exec(ctx, `UPDATE chat_rooms SET state='closed' WHERE id=$1`, room); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/rooms/"+room+"/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, e := apps[0].Test(req, 5000)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatalf("closed room returned %d", res.StatusCode)
	}
	for _, s := range controllers {
		s.roomWrites.mu.Lock()
		n := len(s.roomWrites.rooms)
		s.roomWrites.mu.Unlock()
		if n != 0 {
			t.Fatalf("leaked %d queue entries", n)
		}
	}
}
