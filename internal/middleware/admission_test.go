package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAdmissionRejectsBeforeWriteAndRecovers(t *testing.T) {
	gate := NewAdmission(1)
	app := fiber.New()
	app.Use(gate.Handler)
	entered, release := make(chan struct{}), make(chan struct{})
	app.Get("/slow", func(c *fiber.Ctx) error { close(entered); <-release; return c.SendStatus(200) })
	writes := 0
	app.Post("/write", func(c *fiber.Ctx) error { writes++; return c.SendStatus(201) })
	app.Get("/api/v1/health", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Get("/api/v1/ready", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	done := make(chan error, 1)
	go func() {
		response, err := app.Test(httptest.NewRequest("GET", "/slow", nil), -1)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-entered
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for _, path := range []string{"/api/v1/health", "/api/v1/ready"} {
		response, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("probe %s rejected", path)
		}
	}
	response, err := app.Test(httptest.NewRequest("POST", "/write", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 || response.Header.Get("Retry-After") != "2" || writes != 0 {
		t.Fatalf("overload status=%d writes=%d", response.StatusCode, writes)
	}
	gate.Drain()
	response, err = app.Test(httptest.NewRequest("GET", "/api/v1/ready", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("draining replica stayed ready")
	}
	response, err = app.Test(httptest.NewRequest("GET", "/api/v1/health", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("draining replica failed liveness")
	}
}

func TestAdmissionReleasesAfterErrors(t *testing.T) {
	gate := NewAdmission(1)
	app := fiber.New()
	app.Use(gate.Handler)
	app.Get("/failure", func(c *fiber.Ctx) error { return fiber.ErrBadRequest })
	for i := 0; i < 3; i++ {
		response, err := app.Test(httptest.NewRequest("GET", "/failure", nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("slot leaked: %d", response.StatusCode)
		}
	}
}
