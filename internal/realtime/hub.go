// Package realtime contains the bounded, process-local delivery primitive.
// Signals carry no message contents; consumers must re-read authorized data.
package realtime

import (
	"errors"
	"sync"
)

var ErrCapacity = errors.New("real-time connection limit reached")

// Hub is intentionally independent of Redis, authentication and transport.
// A subscriber receives coalesced wakeups rather than an unbounded event queue.
type Hub struct {
	mu                                                sync.Mutex
	users                                             map[string]map[*Subscription]struct{}
	hall                                              map[*Subscription]struct{}
	total, maxTotal, maxPerUser                       int
	accepted, rejected, queued, coalesced, reconnects uint64
}

type Subscription struct {
	hub    *Hub
	user   string
	wakeup chan struct{}
	closed bool
}

func NewHub(maxTotal, maxPerUser int) *Hub {
	if maxTotal < 1 || maxPerUser < 1 {
		panic("real-time limits must be positive")
	}
	return &Hub{users: make(map[string]map[*Subscription]struct{}), hall: make(map[*Subscription]struct{}), maxTotal: maxTotal, maxPerUser: maxPerUser}
}

// Subscribe accepts only a server-authenticated identity, never a client topic.
func (h *Hub) Subscribe(user string) (*Subscription, error) {
	if user == "" {
		return nil, errors.New("authenticated user required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= h.maxTotal || len(h.users[user]) >= h.maxPerUser {
		h.rejected++
		return nil, ErrCapacity
	}
	s := &Subscription{hub: h, user: user, wakeup: make(chan struct{}, 1)}
	if h.users[user] == nil {
		h.users[user] = make(map[*Subscription]struct{})
	}
	h.users[user][s] = struct{}{}
	h.total++
	h.accepted++
	return s, nil
}

func (s *Subscription) Wakeups() <-chan struct{} { return s.wakeup }

// Close is idempotent and synchronizes with Signal to avoid send-after-close.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(h.users[s.user], s)
	delete(h.hall, s)
	if len(h.users[s.user]) == 0 {
		delete(h.users, s.user)
	}
	h.total--
	close(s.wakeup)
}

// WatchHall is scoped to an authenticated socket and a fixed public topic.
func (s *Subscription) WatchHall(enabled bool) {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.closed {
		return
	}
	if enabled {
		h.hall[s] = struct{}{}
	} else {
		delete(h.hall, s)
	}
}

func (h *Hub) SignalHall() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.hall {
		select {
		case s.wakeup <- struct{}{}:
			h.queued++
		default:
			h.coalesced++
		}
	}
}

// Signal wakes all local sessions for this user, with bounded coalescing.
func (h *Hub) Signal(user string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.users[user] {
		select {
		case s.wakeup <- struct{}{}:
			h.queued++
		default:
			h.coalesced++
		}
	}
}

func (h *Hub) Active() int { h.mu.Lock(); defer h.mu.Unlock(); return h.total }

func (h *Hub) ReportReconnect() { h.mu.Lock(); h.reconnects++; h.mu.Unlock() }

func (h *Hub) Snapshot() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{"active": h.total, "max": h.maxTotal, "per_user_max": h.maxPerUser, "hall_watchers": len(h.hall), "accepted_total": h.accepted, "rejected_total": h.rejected, "queued_total": h.queued, "coalesced_total": h.coalesced, "reported_reconnects_total": h.reconnects}
}
