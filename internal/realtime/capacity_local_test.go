package realtime

import (
	"context"
	"fmt"
	"github.com/alicebob/miniredis/v2"
	ws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"
)

// Opt-in loopback exploration only. The artificial cap, stub auth, in-process
// Redis and client mean this is not a production capacity measurement.
func TestLocalSocketExploration(t *testing.T) {
	if os.Getenv("ATL_REALTIME_LOCAL_EXPLORATION") != "yes" {
		t.Skip("opt-in local exploration")
	}
	m := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: m.Addr()})
	defer cache.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := cache.Set(ctx, EnabledKey, "on", 0).Err(); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(2100, 3)
	bridge := NewRedisBridge(cache, hub)
	go bridge.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !bridge.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !bridge.Ready() {
		t.Fatal("bridge")
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	server := &SocketServer{Hub: hub, Bridge: bridge, Redis: cache, SourceReady: func() bool { return true }}
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-Test-User")); return c.Next() })
	app.Get("/socket", server.Handlers()...)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(listener) }()
	defer app.Shutdown()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const count = 2000
	connections := make([]*ws.Conn, 0, count)
	defer func() {
		for _, c := range connections {
			_ = c.Close()
		}
	}()
	cpuBefore := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}}
	metrics.Read(cpuBefore)
	start := time.Now()
	for i := 0; i < count; i++ {
		headers := http.Header{"X-Test-User": []string{fmt.Sprintf("00000000-0000-4000-8000-%012d", i)}}
		c, _, e := ws.DefaultDialer.Dial("ws://"+listener.Addr().String()+"/socket", headers)
		if e != nil {
			t.Fatalf("opened %d: %v", i, e)
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, e := c.ReadMessage()
		if e != nil || string(msg) != `{"type":"ready"}` {
			t.Fatalf("ready %d: %s %v", i, msg, e)
		}
		connections = append(connections, c)
	}
	elapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("local exploratory sockets=%d open_time=%s combined_process_heap_delta_bytes=%d combined_delta_bytes_per_socket=%.0f (includes client, server and mock Redis)", count, elapsed, int64(after.HeapAlloc)-int64(before.HeapAlloc), float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/count)
	for _, c := range connections {
		_ = c.Close()
	}
	connections = connections[:0]
	deadline = time.Now().Add(15 * time.Second)
	for hub.Snapshot()["active"].(int) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Snapshot()["active"].(int) != 0 {
		t.Fatal("closed connections did not drain")
	}
	reconnectStart := time.Now()
	for i := 0; i < count; i++ {
		headers := http.Header{"X-Test-User": []string{fmt.Sprintf("00000000-0000-4000-8000-%012d", i+count)}}
		c, _, e := ws.DefaultDialer.Dial("ws://"+listener.Addr().String()+"/socket", headers)
		if e != nil {
			t.Fatalf("reconnected %d: %v", i, e)
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, e := c.ReadMessage()
		if e != nil || string(msg) != `{"type":"ready"}` {
			t.Fatalf("reconnect %d: %s %v", i, msg, e)
		}
		connections = append(connections, c)
	}
	cpuAfter := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}}
	metrics.Read(cpuAfter)
	t.Logf("local sequential reconnects=%d duration=%s go_user_cpu_seconds=%.2f (includes test client/server/mock Redis; admission gate bypassed)", count, time.Since(reconnectStart), cpuAfter[0].Value.Float64()-cpuBefore[0].Value.Float64())
}
