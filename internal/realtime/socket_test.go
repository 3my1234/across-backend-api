package realtime

import (
	appmiddleware "across/backend/internal/middleware"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	ws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func TestSocketDoesNotOccupyHTTPAdmissionSlot(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Set(ctx, EnabledKey, "on", 0).Err(); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(5, 2)
	bridge := NewRedisBridge(client, hub)
	go bridge.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !bridge.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bridge.Ready() {
		t.Fatal("bridge unavailable")
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(appmiddleware.NewAdmission(1).Handler)
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", "11111111-1111-4111-8111-111111111111"); return c.Next() })
	server := &SocketServer{Hub: hub, Bridge: bridge, Redis: client, SourceReady: func() bool { return true }}
	app.Get("/socket", server.Handlers()...)
	app.Get("/ordinary", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(listener) }()
	defer app.Shutdown()
	conn, _, err := ws.DefaultDialer.Dial("ws://"+listener.Addr().String()+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + listener.Addr().String() + "/ordinary")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("ordinary request blocked by upgraded socket: %d", response.StatusCode)
	}
}

func TestSocketAuthenticatedCrossReplicaAndDisabledFlag(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := func() (*RedisBridge, string) {
		hub := NewHub(5, 2)
		bridge := NewRedisBridge(client, hub)
		go bridge.Run(ctx)
		deadline := time.Now().Add(5 * time.Second)
		for !bridge.Ready() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !bridge.Ready() {
			t.Fatal("bridge unavailable")
		}
		app := fiber.New(fiber.Config{DisableStartupMessage: true})
		server := &SocketServer{Hub: hub, Bridge: bridge, Redis: client, SourceReady: func() bool { return true }}
		app.Use(func(c *fiber.Ctx) error {
			if c.Get("Authorization") != "Bearer test-session" {
				return fiber.ErrUnauthorized
			}
			c.Locals("user_id", "11111111-1111-4111-8111-111111111111")
			return c.Next()
		})
		app.Get("/socket", server.Handlers()...)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(listener) }()
		t.Cleanup(func() { _ = app.Shutdown() })
		return bridge, "ws://" + listener.Addr().String() + "/socket"
	}
	b1, u1 := start()
	_, u2 := start()
	headers := http.Header{"Authorization": []string{"Bearer test-session"}}
	if _, response, err := ws.DefaultDialer.Dial(u1, headers); err == nil || response == nil || response.StatusCode != 503 {
		t.Fatal("disabled socket should reject upgrades")
	}
	if err := client.Set(ctx, EnabledKey, "on", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, response, err := ws.DefaultDialer.Dial(u1, nil); err == nil || response == nil || response.StatusCode != 401 {
		t.Fatal("unauthenticated upgrade accepted")
	}
	first, _, err := ws.DefaultDialer.Dial(u1, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, _, err := ws.DefaultDialer.Dial(u2, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*ws.Conn{first, second} {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, message, err := conn.ReadMessage()
		if err != nil || string(message) != `{"type":"ready"}` {
			t.Fatalf("ready: %s %v", message, err)
		}
	}
	if err := b1.Publish(ctx, "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*ws.Conn{first, second} {
		_, message, err := conn.ReadMessage()
		if err != nil || string(message) != `{"type":"changed"}` {
			t.Fatalf("change: %s %v", message, err)
		}
	}
}
