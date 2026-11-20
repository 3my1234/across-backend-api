package middleware

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func TestRequestMetricsIncludesRejectionErrorsAndRecoveredPanics(t *testing.T) {
	m := NewRequestMetrics()
	app := fiber.New()
	app.Use(m.Handler)
	app.Use(recover.New())
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Get("/limited", func(c *fiber.Ctx) error { return fiber.NewError(429, "retry") })
	app.Get("/busy", func(c *fiber.Ctx) error { return c.SendStatus(503) })
	app.Get("/panic", func(c *fiber.Ctx) error { panic("test") })
	for _, path := range []string{"/ok?token=must-not-appear", "/limited", "/busy", "/panic"} {
		res, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	s := m.Snapshot()
	if s["requests"] != uint64(4) || s["server_errors"] != uint64(2) || s["rejected_429_503"] != uint64(2) || s["in_flight"] != uint64(0) {
		t.Fatalf("unexpected counters: %v", s)
	}
	var durations uint64
	for _, n := range s["duration_bucket_counts"].([8]uint64) {
		durations += n
	}
	if durations != 4 {
		t.Fatal("latency histogram lost requests")
	}
}

func TestRequestMetricsLabelsCannotContainIdentifiers(t *testing.T) {
	m := NewRequestMetrics()
	app := fiber.New()
	app.Use(m.Handler)
	app.Get("/*", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	for _, path := range []string{"/api/v1/chat/town-hall/private-customer-id?token=SECRET", "/api/v1/jobs/company-id", "/api/v1/unknown/user@example.com"} {
		r, e := app.Test(httptest.NewRequest("GET", path, nil))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
	}
	raw, _ := json.Marshal(m.Snapshot())
	for _, secret := range []string{"private-customer-id", "SECRET", "company-id", "user@example.com"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("metric label leaked identifier")
		}
	}
	if len(m.groups) != 3 {
		t.Fatal("unexpected unbounded labels", m.groups)
	}
}
