package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// Cache normalization relies on the real feed handler ignoring these fields.
// Fail this test if pagination/search support is later added without new keys.
func TestCatalogIgnoredPaginationAndSearchPreserveResponse(t *testing.T) {
	db := catalogCompareTestDB(t)
	ctx := context.Background()
	seller := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name)VALUES($1,'Seller')`, seller); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,category_path,created_at)SELECT gen_random_uuid(),$1,ARRAY['Food'],'2026-01-01'::timestamptz+i*interval '1 second' FROM generate_series(1,25)i`, seller); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO product_delivery_areas(product_id,country_code)SELECT id,'NG' FROM products`); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	controller := &CatalogController{db: db}
	app.Get("/products", controller.ListProducts)
	var original map[string]any
	for _, query := range []string{"country_code=NG", "country_code=NG&limit=1&search=NO_MATCH", "country_code=NG&limit=20&search=hotel"} {
		res, err := app.Test(httptest.NewRequest("GET", "/products?"+query, nil), 5000)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("response %d %v", res.StatusCode, err)
		}
		if len(body["products"].([]any)) != 25 {
			t.Fatal("feed parameters changed returned inventory")
		}
		if original == nil {
			original = body
		} else if !reflect.DeepEqual(original, body) {
			t.Fatal("ignored feed parameters changed ordered full response")
		}
	}
}
