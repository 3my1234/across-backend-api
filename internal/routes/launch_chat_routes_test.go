package routes

import (
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"testing"
)

func TestLaunchChatRoutes(t *testing.T) {
	app := fiber.New()
	Register(app, nil, nil, nil, config.Config{})
	routes := map[string]bool{}
	for _, stack := range app.Stack() {
		for _, route := range stack {
			routes[route.Method+" "+route.Path] = true
		}
	}
	for _, removed := range []string{"GET /api/v1/chat/friends", "POST /api/v1/chat/friends", "GET /api/v1/chat/users", "PATCH /api/v1/chat/profile", "POST /api/v1/chat/conversations", "GET /api/v1/chat/statuses", "POST /api/v1/chat/statuses", "GET /api/v1/chat/promotions", "GET /api/v1/community/invite/:handle"} {
		if routes[removed] {
			t.Errorf("retired launch route remains: %s", removed)
		}
	}
	for _, kept := range []string{"GET /api/v1/chat/inbox", "POST /api/v1/chat/conversations/:conversation_id/messages", "POST /api/v1/chat/conversations/:conversation_id/read", "POST /api/v1/chat/town-hall", "POST /api/v1/chat/town-hall/:room_id/choose", "POST /api/v1/chat/blocks/:user_id"} {
		if !routes[kept] {
			t.Errorf("required chat route absent: %s", kept)
		}
	}
}
