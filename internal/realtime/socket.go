package realtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// EnabledKey is deliberately off when absent or Redis is unavailable.
const EnabledKey = "atl:realtime:enabled"

type SocketServer struct {
	Hub         *Hub
	Bridge      *RedisBridge
	Redis       *redis.Client
	SourceReady func() bool
}

// Handlers must be installed after the existing session authentication. The
// peer identity comes exclusively from that middleware, never from the URL.
func (s *SocketServer) Handlers() []fiber.Handler {
	check := func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		if _, ok := c.Locals("user_id").(string); !ok {
			return fiber.ErrUnauthorized
		}
		if s.Redis == nil || s.Hub == nil || s.Bridge == nil || !s.Bridge.Ready() || s.SourceReady == nil || !s.SourceReady() {
			return fiber.ErrServiceUnavailable
		}
		ctx, cancel := context.WithTimeout(c.Context(), time.Second)
		defer cancel()
		value, err := s.Redis.Get(ctx, EnabledKey).Result()
		if err != nil || value != "on" {
			return fiber.ErrServiceUnavailable
		}
		return c.Next()
	}
	upgrade := websocket.New(func(conn *websocket.Conn) {
		defer conn.Close()
		user, _ := conn.Locals("user_id").(string)
		leaseCtx, cancelLease := context.WithTimeout(context.Background(), time.Second)
		lease, leaseErr := acquireLease(leaseCtx, s.Redis, user, 3)
		cancelLease()
		if leaseErr != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Connection unavailable"), time.Now().Add(time.Second))
			return
		}
		defer lease.release()
		sub, err := s.Hub.Subscribe(user)
		if err != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Connection limit"), time.Now().Add(time.Second))
			return
		}
		defer sub.Close()
		// A bounded lifetime forces re-authentication and refresh of revoked sessions.
		_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		conn.SetReadLimit(256)
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
		done := make(chan struct{})
		go func() {
			defer close(done)
			window := time.Now()
			count := 0
			helloSeen := false
			for {
				kind, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if kind != websocket.TextMessage {
					return
				}
				if time.Since(window) >= time.Minute {
					window = time.Now()
					count = 0
				}
				count++
				if count > 30 {
					return
				}
				var command struct {
					Type      string `json:"type"`
					Enabled   *bool  `json:"enabled"`
					Reconnect *bool  `json:"reconnect"`
				}
				if json.Unmarshal(data, &command) != nil {
					return
				}
				switch command.Type {
				case "hello":
					if helloSeen || command.Reconnect == nil {
						return
					}
					helloSeen = true
					if *command.Reconnect {
						s.Hub.ReportReconnect()
					}
				case "watch_hall":
					if command.Enabled == nil {
						return
					}
					sub.WatchHall(*command.Enabled)
				default:
					return
				}
			}
		}()
		write := func(value string) error {
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			return conn.WriteMessage(websocket.TextMessage, []byte(value))
		}
		if write(`{"type":"ready"}`) != nil {
			return
		}
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()
		lifetime := time.NewTimer(5 * time.Minute)
		defer lifetime.Stop()
		for {
			select {
			case <-done:
				return
			case _, ok := <-sub.Wakeups():
				if !ok || write(`{"type":"changed"}`) != nil {
					return
				}
			case <-heartbeat.C:
				// Closing on a disabled/unavailable flag makes the operational switch
				// effective for established connections without restarting any API.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				value, err := s.Redis.Get(ctx, EnabledKey).Result()
				cancel()
				if err != nil || value != "on" || !s.Bridge.Ready() || !s.SourceReady() {
					return
				}
				leaseCtx, cancelLease := context.WithTimeout(context.Background(), time.Second)
				leaseErr := lease.renew(leaseCtx)
				cancelLease()
				if leaseErr != nil {
					return
				}
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			case <-lifetime.C:
				return
			}
		}
	}, websocket.Config{ReadBufferSize: 512, WriteBufferSize: 512, HandshakeTimeout: 5 * time.Second})
	return []fiber.Handler{check, upgrade}
}
