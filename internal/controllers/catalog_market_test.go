package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The feed resolves one unique destination market before its per-product
// delivery lookup. Ineligible currencies must not shadow eligible fallbacks.
func TestCatalogMarketEligibilityAndDeliverySpecificity(t *testing.T) {
	db := catalogCompareTestDB(t)
	ctx := context.Background()
	seller, product, wrong := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `ALTER TABLE countries_config ADD UNIQUE(country_code);
INSERT INTO provider_organizations(id,business_name) VALUES($1,'Seller');
INSERT INTO products(id,provider_id,category_path) VALUES($2,$1,ARRAY['Food']),($3,$1,ARRAY['Food']);
INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,currency_code) VALUES
($2,'NG','','',100,'NGN'),($2,'NG','lagos','',120,'NGN'),($2,'NG','lagos','ikeja',130,'NGN'),
($2,'NG','lagos','yaba',999,'USD'),($3,'NG','','',999,'USD');`, pgx.QueryExecModeSimpleProtocol, seller, product, wrong)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/products", (&CatalogController{db: db}).ListProducts)
	check := func(query string, count int, price float64) {
		t.Helper()
		res, e := app.Test(httptest.NewRequest("GET", "/products?"+query, nil), 5000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var body struct {
			Products []struct {
				ID       string  `json:"id"`
				Price    float64 `json:"price"`
				Currency string  `json:"currency"`
			} `json:"products"`
		}
		if e = json.NewDecoder(res.Body).Decode(&body); e != nil || res.StatusCode != 200 {
			t.Fatalf("status=%d decode=%v", res.StatusCode, e)
		}
		if len(body.Products) != count {
			t.Fatalf("%s: got %d products, want %d", query, len(body.Products), count)
		}
		if count > 0 && (body.Products[0].ID != product || body.Products[0].Price != price || body.Products[0].Currency != "NGN") {
			t.Fatalf("%s: wrong eligible offer: %+v", query, body.Products)
		}
	}
	check("country_code=NG", 1, 100)
	check("country_code=NG&state=Lagos", 1, 120)
	check("country_code=NG&state=Lagos&city=Ikeja", 1, 130)
	check("country_code=NG&state=Lagos&city=Yaba", 1, 120)
	check("country_code=US", 0, 0)
	for _, update := range []string{
		`UPDATE countries_config SET is_active=false`,
		`UPDATE countries_config SET is_active=true,active_payment_gateways=ARRAY['other']`,
		`UPDATE countries_config SET active_payment_gateways=ARRAY['flutterwave'],currency_code='EUR'`,
	} {
		if _, err = db.Exec(ctx, update); err != nil {
			t.Fatal(err)
		}
		check("country_code=NG&state=Lagos&city=Ikeja", 0, 0)
	}
}
