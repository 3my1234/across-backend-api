package controllers

import (
	"net/http/httptest"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func TestDevLoginRejectedBeforeAnyDatabaseAccess(t *testing.T) {
	for _, cfg := range []config.Config{
		{}, {AppEnv: "development"}, {AppEnv: "test"},
		{AppEnv: "production", EnableDevLogin: true}, {AppEnv: "staging", EnableDevLogin: true},
		{AppEnv: "Production", EnableDevLogin: true},
	} {
		app := fiber.New()
		app.Post("/dev/login", NewDevController(nil, cfg).Login)
		response, err := app.Test(httptest.NewRequest("POST", "/dev/login", nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("environment %q: status %d", cfg.AppEnv, response.StatusCode)
		}
	}
}
