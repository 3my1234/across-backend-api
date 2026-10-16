package middleware

import (
	"sync/atomic"

	"github.com/gofiber/fiber/v2"
)

// Admission bounds active application requests, including when Redis is down.
// It never queues or replays writes. Rejections happen before route handlers.
type Admission struct {
	slots    chan struct{}
	draining atomic.Bool
	rejected atomic.Uint64
}

func NewAdmission(limit int) *Admission {
	if limit < 1 {
		limit = 128
	}
	return &Admission{slots: make(chan struct{}, limit)}
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
		a.rejected.Add(1)
		return a.unavailable(c, "Service busy. Please retry shortly.")
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
