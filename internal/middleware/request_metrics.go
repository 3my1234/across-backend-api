package middleware

import (
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RequestMetrics is bounded per process: no raw path/user/IP/token labels.
// Install outside admission/recovery so rejections and errors are included.
type RequestMetrics struct {
	mu                                sync.Mutex
	started                           time.Time
	total, inflight, errors, rejected uint64
	statuses                          [6]uint64
	duration                          [8]uint64
}

func NewRequestMetrics() *RequestMetrics { return &RequestMetrics{started: time.Now()} }

func (m *RequestMetrics) Handler(c *fiber.Ctx) error {
	began := time.Now()
	m.mu.Lock()
	m.inflight++
	m.mu.Unlock()
	err := c.Next()
	status := c.Response().StatusCode()
	if err != nil {
		status = 500
		var httpError *fiber.Error
		if errors.As(err, &httpError) {
			status = httpError.Code
		}
	}
	elapsed := time.Since(began)
	bounds := [...]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 3 * time.Second}
	bucket := len(bounds)
	for i, bound := range bounds {
		if elapsed <= bound {
			bucket = i
			break
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inflight--
	m.total++
	m.duration[bucket]++
	if class := status / 100; class >= 1 && class <= 5 {
		m.statuses[class]++
	}
	if status >= 500 {
		m.errors++
	}
	if status == 429 || status == 503 {
		m.rejected++
	}
	return err
}

func (m *RequestMetrics) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"scope": "this API process since startup", "uptime_seconds": int64(time.Since(m.started).Seconds()), "requests": m.total, "in_flight": m.inflight, "server_errors": m.errors, "rejected_429_503": m.rejected, "status_classes": m.statuses, "duration_bucket_counts": m.duration, "duration_bucket_upper_ms": []any{10, 50, 100, 250, 500, 1000, 3000, "+Inf"}}
}
