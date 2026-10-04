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
	if _, err := normalizeProductDeliveryAreas(areas, "NG", "merchant_local", "NGN", 40); err == nil {
		t.Fatal("foreign delivery for local stock must fail")
	}
	if _, err := normalizeProductDeliveryAreas([]productDeliveryArea{{CountryCode: "US", City: "New York", DeliveredPrice: 42, CurrencyCode: "USD"}}, "US", "merchant_local", "USD", 40); err == nil {
		t.Fatal("city without state must fail")
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
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM product_delivery_areas`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected only NG local and imported offers to be backfilled, got %d", count)
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
