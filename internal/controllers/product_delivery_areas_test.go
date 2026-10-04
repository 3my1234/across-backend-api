package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeProductDeliveryAreas(t *testing.T) {
	areas, err := normalizeProductDeliveryAreas([]productDeliveryArea{
		{CountryCode: "us", State: "New  York", City: "New York", DeliveredPrice: 42.50, CurrencyCode: "usd"},
	}, "US", "merchant_local", "USD", 40)
	if err != nil {
		t.Fatal(err)
	}
	if areas[0].CountryCode != "US" || areas[0].State != "new york" || areas[0].CurrencyCode != "USD" {
		t.Fatalf("unexpected normalized area: %+v", areas[0])
	}
	if _, err := normalizeProductDeliveryAreas(areas, "NG", "merchant_local", "NGN", 40); err != nil {
		t.Fatalf("stocked product must be able to serve an overseas delivery area: %v", err)
	}
	if _, err := normalizeProductDeliveryAreas([]productDeliveryArea{{CountryCode: "US", City: "New York", DeliveredPrice: 42, CurrencyCode: "USD"}}, "US", "merchant_local", "USD", 40); err == nil {
		t.Fatal("city without state must fail")
	}
	priced, err := normalizeProductDeliveryAreas([]productDeliveryArea{{CountryCode: "US", ItemPrice: 30, DeliveryFee: 5, CurrencyCode: "USD"}}, "US", "merchant_local", "USD", 30)
	if err != nil || priced[0].DeliveredPrice != 35 {
		t.Fatalf("expected USD 30 item plus USD 5 delivery, got %+v: %v", priced, err)
	}
	if _, err := normalizeProductDeliveryAreas([]productDeliveryArea{{CountryCode: "US", ItemPrice: 30, DeliveryFee: -5, CurrencyCode: "USD"}}, "US", "merchant_local", "USD", 30); err == nil {
		t.Fatal("negative delivery fee must fail")
	}
}

func TestProductRouteForDestination(t *testing.T) {
	for _, tc := range []struct{ stock, state, buyer, want string }{
		{"US", "locally_available", "US", "merchant_local"},
		{"US", "locally_available", "NG", "merchant_cross_border"},
		{"CN", "import_on_demand", "CN", "merchant_cross_border"},
	} {
		if got := productRouteForDestination(tc.stock, tc.state, tc.buyer); got != tc.want {
			t.Errorf("stock=%s state=%s buyer=%s: got %s, want %s", tc.stock, tc.state, tc.buyer, got, tc.want)
		}
	}
}

func TestCatalogDeliveryFeeMatchesSpecificDestination(t *testing.T) {
	factory := map[string]any{"delivery_areas": []any{
		map[string]any{"country_code": "US", "state": "", "city": "", "delivery_fee": float64(10)},
		map[string]any{"country_code": "US", "state": "california", "city": "", "delivery_fee": float64(7)},
		map[string]any{"country_code": "US", "state": "california", "city": "los angeles", "delivery_fee": float64(5)},
	}}
	if got := catalogDeliveryFee(factory, "US", "california", "los angeles"); got != 5 {
		t.Fatalf("city delivery fee = %v, want 5", got)
	}
	if got := catalogDeliveryFee(factory, "US", "california", "san diego"); got != 7 {
		t.Fatalf("state delivery fee = %v, want 7", got)
	}
	if got := catalogDeliveryFee(factory, "US", "texas", "austin"); got != 10 {
		t.Fatalf("country delivery fee = %v, want 10", got)
	}
}

func TestDeliveryAreaMigrationBackfillAndSpecificity(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		CREATE TABLE countries_config(id UUID PRIMARY KEY DEFAULT gen_random_uuid(),country_code CHAR(2) UNIQUE NOT NULL,currency_code CHAR(3) NOT NULL,base_escrow_days INT NOT NULL,active_payment_gateways TEXT[] NOT NULL,is_active BOOLEAN NOT NULL DEFAULT true);
		CREATE TABLE products(id UUID PRIMARY KEY,provider_id UUID,fulfillment_mode TEXT,local_currency_code CHAR(3),local_selling_price NUMERIC(14,2),inventory_country_code CHAR(2));
	`)
	if err != nil {
		t.Fatal(err)
	}
	localNG, localUS, importedNG := uuid.New(), uuid.New(), uuid.New()
	providerID := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,fulfillment_mode,local_currency_code,local_selling_price,inventory_country_code) VALUES($1,$4,'merchant_local','NGN',120,'NG'),($2,$4,'merchant_local','NGN',120,'US'),($3,$4,'merchant_cross_border','NGN',200,'CN')`, localNG, localUS, importedNG, providerID)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "054_product_delivery_areas.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	feeMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "055_product_delivery_fees.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, string(feeMigration)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM product_delivery_areas`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected only NG local and imported offers to be backfilled, got %d", count)
	}
	var oldTotal, oldFee float64
	if err := db.QueryRow(ctx, `SELECT delivered_price,delivery_fee FROM product_delivery_areas WHERE product_id=$1`, localNG).Scan(&oldTotal, &oldFee); err != nil {
		t.Fatal(err)
	}
	if oldTotal != 120 || oldFee != 0 {
		t.Fatalf("migration changed existing seller total: %v + %v", oldTotal, oldFee)
	}
	if _, err := db.Exec(ctx, `INSERT INTO countries_config(country_code,currency_code,base_escrow_days,active_payment_gateways) VALUES('US','USD',14,ARRAY['flutterwave'])`); err != nil {
		t.Fatal(err)
	}
	if err := validateEnabledDeliveryMarkets(ctx, db, []productDeliveryArea{{CountryCode: "US", CurrencyCode: "USD", DeliveredPrice: 42}}); err != nil {
		t.Fatal(err)
	}
	if err := validateEnabledDeliveryMarkets(ctx, db, []productDeliveryArea{{CountryCode: "US", CurrencyCode: "NGN", DeliveredPrice: 42}}); err == nil {
		t.Fatal("US market must reject an NGN offer")
	}
	if _, err := db.Exec(ctx, `INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,currency_code) VALUES($1,'US','','',50,'USD'),($1,'US','new york','',45,'USD'),($1,'US','new york','new york',42,'USD')`, localUS); err != nil {
		t.Fatal(err)
	}
	var price float64
	if err := db.QueryRow(ctx, `SELECT delivered_price FROM product_delivery_areas WHERE product_id=$1 AND country_code='US' AND (state_key='' OR state_key='new york') AND (city_key='' OR city_key='new york') ORDER BY (city_key<>'') DESC,(state_key<>'') DESC LIMIT 1`, localUS).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 42 {
		t.Fatalf("expected most specific delivered price 42, got %v", price)
	}
}
