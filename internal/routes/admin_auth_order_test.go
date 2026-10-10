package routes

import (
	"net/http/httptest"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

// Exercise the registered auth chain without controller database dependencies.
// Every admin route must reach its handler with admin credentials alone; the
// broad customer group must never demand a users-table session from an admin.
func TestAdminRoutesDoNotInheritCustomerAuth(t *testing.T) {
	app := fiber.New()
	Register(app, nil, nil, nil, config.Config{AdminBootstrapToken: "route-test-admin"})
	tested := 0
	for _, stack := range app.Stack() {
		for _, route := range stack {
			if route.Method == "HEAD" || route.Path == "/api/v1/admin/login" || route.Path == "/api/v1/admin/pricing/calculate" || len(route.Path) < 14 || route.Path[:14] != "/api/v1/admin/" {
				continue
			}
			route.Handlers[len(route.Handlers)-1] = func(c *fiber.Ctx) error { return c.SendStatus(204) }
			req := httptest.NewRequest(route.Method, route.Path, nil)
			req.Header.Set("X-Admin-Token", "route-test-admin")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 204 {
				t.Fatalf("%s %s demanded a customer session: %d", route.Method, route.Path, resp.StatusCode)
			}
			unauthorized, err := app.Test(httptest.NewRequest(route.Method, route.Path, nil))
			if err != nil {
				t.Fatal(err)
			}
			unauthorized.Body.Close()
			if unauthorized.StatusCode != 401 {
				t.Fatalf("%s %s missing admin auth: %d", route.Method, route.Path, unauthorized.StatusCode)
			}
			tested++
		}
	}
	if tested < 40 {
		t.Fatalf("only %d admin routes exercised", tested)
	}
}
