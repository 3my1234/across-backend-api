package middleware

import (
	"github.com/gofiber/fiber/v2"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBoundedPasswordWait(t *testing.T) {
	gate := NewBoundedPasswordAdmission(1, 1, 80*time.Millisecond)
	gate.slots <- struct{}{}
	app := fiber.New()
	app.Post("/login", gate.Handler, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	done := make(chan int, 1)
	go func() {
		r, e := app.Test(httptest.NewRequest("POST", "/login", nil), -1)
		if e != nil {
			done <- 0
			return
		}
		defer r.Body.Close()
		done <- r.StatusCode
	}()
	deadline := time.Now().Add(time.Second)
	for len(gate.waiters) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r, e := app.Test(httptest.NewRequest("POST", "/login", nil), -1)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 503 || r.Header.Get("Retry-After") != "2" {
		t.Fatal("queue limit", r.StatusCode)
	}
	if status := <-done; status != 503 {
		t.Fatal("wait timeout", status)
	}
	<-gate.slots
	r, e = app.Test(httptest.NewRequest("POST", "/login", nil), -1)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("recovery", r.StatusCode)
	}
}

// fasthttp's request context is not cancelled when a client disconnects.
// This documents the bounded queue's actual behavior with a real TCP socket.
func TestDisconnectedPasswordWaitCanStillReachHandler(t *testing.T) {
	gate := NewBoundedPasswordAdmission(1, 1, time.Second)
	gate.slots <- struct{}{}
	app := fiber.New(fiber.Config{BodyLimit: 4096})
	called := make(chan struct{}, 1)
	app.Post("/login", func(c *fiber.Ctx) error {
		if len(c.Body()) == 0 {
			return c.SendStatus(400)
		}
		return c.Next()
	}, gate.Handler, func(c *fiber.Ctx) error {
		if c.Context().Err() != nil || c.UserContext().Err() != nil {
			return c.SendStatus(499)
		}
		called <- struct{}{}
		return c.SendStatus(200)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(listener) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("POST /login HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\n{}")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(gate.waiters) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.waiters) == 0 {
		t.Fatal("request did not enter wait queue")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	<-gate.slots
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run after disconnected request was admitted")
	}
}

func TestReservedAdminAdmissionWhenCustomerSlotsAreBusy(t *testing.T) {
	customer := NewBoundedPasswordAdmission(1, 1, 30*time.Millisecond)
	admin := NewBoundedPasswordAdmission(1, 1, 30*time.Millisecond)
	customer.slots <- struct{}{}
	app := fiber.New()
	app.Post("/login", customer.Handler, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Post("/admin/login", admin.Handler, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	r, err := app.Test(httptest.NewRequest("POST", "/admin/login", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("admin admission status=%d", r.StatusCode)
	}
	if len(customer.slots) != 1 {
		t.Fatal("admin changed customer admission")
	}
}
