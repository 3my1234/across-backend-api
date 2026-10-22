package routes

import (
	"net/http/httptest"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func TestRegisteredDevelopmentLoginCannotTouchProductionDatabase(t *testing.T) {
	for _, environment := range []string{"production", "staging", " PRODUCTION "} {
		app := fiber.New()
		Register(app, nil, nil, nil, config.Config{AppEnv: environment, EnableDevLogin: true})
		response, err := app.Test(httptest.NewRequest("POST", "/api/v1/dev/login", nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("registered development login was not rejected before dependencies")
		}
	}
}
