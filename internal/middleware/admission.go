package middleware

import (
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Admission bounds active application requests, including when Redis is down.
// It never queues or replays writes. Rejections happen before route handlers.
type Admission struct {
	slots    chan struct{}
	draining atomic.Bool
	rejected atomic.Uint64
	wait     time.Duration
	waiters  chan struct{}
}

func NewAdmission(limit int) *Admission {
	if limit < 1 {
		limit = 128
	}
	return &Admission{slots: make(chan struct{}, limit)}
}

// NewBoundedPasswordAdmission limits both active hashes and waiting requests.
func NewBoundedPasswordAdmission(limit, maxWaiting int, wait time.Duration) *Admission {
	a := NewAdmission(limit)
	if wait > 0 && maxWaiting > 0 {
		a.wait = wait
		a.waiters = make(chan struct{}, maxWaiting)
	}
	return a
}

func (a *Admission) Drain() { a.draining.Store(true) }

func (a *Admission) Handler(c *fiber.Ctx) error {
	if c.Path() == "/api/v1/health" {
		return c.Next()
	}
	if a.draining.Load() {
		return a.unavailable(c, "Service restarting. Please retry shortly.")
	}
	if c.Path() == "/api/v1/ready" || c.Path() == "/" || c.Method() == fiber.MethodOptions {
		return c.Next()
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		if a.waiters == nil {
			a.rejected.Add(1)
			return a.unavailable(c, "Service busy. Please retry shortly.")
		}
		select {
		case a.waiters <- struct{}{}:
		default:
			a.rejected.Add(1)
			return a.unavailable(c, "Service busy. Please retry shortly.")
		}
		timer := time.NewTimer(a.wait)
		defer timer.Stop()
		select {
		case a.slots <- struct{}{}:
			<-a.waiters
			defer func() { <-a.slots }()
			if a.draining.Load() {
				return a.unavailable(c, "Service restarting. Please retry shortly.")
			}
		case <-timer.C:
			<-a.waiters
			a.rejected.Add(1)
			return a.unavailable(c, "Service busy. Please retry shortly.")
		}
	}
	return c.Next()
}

func (a *Admission) unavailable(c *fiber.Ctx, message string) error {
	c.Set("Retry-After", "2")
	c.Set("Cache-Control", "no-store")
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": message})
}

func (a *Admission) Snapshot() fiber.Map {
	return fiber.Map{"active": len(a.slots), "limit": cap(a.slots), "rejected": a.rejected.Load(), "draining": a.draining.Load()}
}
