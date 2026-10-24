package controllers

import (
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogDatabaseFailureDoesNotMasqueradeAsMissingProduct(t *testing.T) {
	db := settlementTestDB(t) // This isolated schema intentionally has no products.
	app := fiber.New()
	catalog := NewCatalogController(db, config.Config{})
	app.Get("/products/:product_id", catalog.GetProduct)
	response, err := app.Test(httptest.NewRequest("GET", "/products/11111111-1111-4111-8111-111111111111?country_code=NG", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 503 || strings.Contains(string(raw), "SQLSTATE") || strings.Contains(string(raw), "relation") {
		t.Fatalf("Database failure became missing data or leaked internals: status=%d", response.StatusCode)
	}
}

func TestCatalogMalformedProductDoesNotQueryDatabase(t *testing.T) {
	app := fiber.New()
	catalog := NewCatalogController(nil, config.Config{})
	app.Get("/products/:product_id", catalog.GetProduct)
	response, err := app.Test(httptest.NewRequest("GET", "/products/not-a-product?country_code=NG", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("Malformed product URL became a database outage", response.StatusCode)
	}
}
