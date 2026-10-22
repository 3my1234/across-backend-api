package controllers

import (
	"net/http/httptest"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
)

func TestDevLoginRejectedBeforeDatabaseAccess(t *testing.T) {
	for _, cfg := range []config.Config{
		{AppEnv: "production"}, {AppEnv: "production", EnableDevLogin: true},
		{AppEnv: " PRODUCTION ", EnableDevLogin: true}, {AppEnv: "staging", EnableDevLogin: true},
		{AppEnv: "", EnableDevLogin: true}, {AppEnv: "unknown", EnableDevLogin: true},
		{AppEnv: "development"}, {AppEnv: "test"},
	} {
		app := fiber.New()
		app.Post("/api/v1/dev/login", NewDevController(nil, cfg).Login)
		response, err := app.Test(httptest.NewRequest("POST", "/api/v1/dev/login", nil))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 404 {
			t.Fatal("development login was not rejected")
		}
		response.Body.Close()
	}
	for _, environment := range []string{"development", "test"} {
		if !(config.Config{AppEnv: environment, EnableDevLogin: true}).DevLoginAllowed() {
			t.Fatal("explicit local fixture opt-in should remain available")
		}
	}
}
