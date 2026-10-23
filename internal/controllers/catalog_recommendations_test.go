package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func catalogCompareTestDB(t *testing.T) *pgxpool.Pool {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,business_name text,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
 CREATE VIEW provider_public_identity AS SELECT id provider_id,business_name name,''::text avatar_url,true verified,false paid_active,''::text badge_tier,1 listing_limit FROM provider_organizations;
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 ALTER TABLE provider_payout_accounts ADD COLUMN payment_provider text, ADD COLUMN status text;
 CREATE TABLE countries_config(country_code text,currency_code text,is_active boolean,active_payment_gateways text[]);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 CREATE TABLE logistics_hubs(id uuid,name text,city text);
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,sku text DEFAULT 'sku',title text DEFAULT 'Product',description text DEFAULT '',category_path text[],image_urls text[] DEFAULT '{}',local_currency_code text DEFAULT 'NGN',local_selling_price numeric DEFAULT 100,compare_at_price numeric DEFAULT 0,is_active boolean DEFAULT true,moderation_status text DEFAULT 'approved',fulfillment_mode text DEFAULT 'merchant_local',payment_mode text DEFAULT 'contact',inventory_count int DEFAULT 5,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(),catalog_version int DEFAULT 1,factory_details jsonb DEFAULT '{}',origin_hub_id uuid,is_flash_sale boolean DEFAULT false,flash_sale_price numeric DEFAULT 0,review_count bigint DEFAULT 0,sold_count bigint DEFAULT 0,review_rating_sum bigint DEFAULT 0,inventory_country_code text DEFAULT 'NG',inventory_city text DEFAULT 'Abuja',inventory_location text DEFAULT '',stock_state text DEFAULT 'locally_available',handling_time_hours int DEFAULT 24,delivery_min_days int DEFAULT 1,delivery_max_days int DEFAULT 3,delivery_methods text[] DEFAULT '{}',plan_slot integer DEFAULT 1);
 CREATE TABLE product_delivery_areas(product_id uuid,country_code text,state_key text DEFAULT '',city_key text DEFAULT '',delivered_price numeric DEFAULT 100,delivery_fee numeric DEFAULT 0,currency_code text DEFAULT 'NGN');`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestProductRecommendationsStayRelatedAndRespectEligibility(t *testing.T) {
	db := catalogCompareTestDB(t)
	ctx := context.Background()
	var err error
	provider, source, exact, clothes, unrelated, hidden, empty := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name) VALUES($1,'Seller')`, provider); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id         string
		categories []string
	}{{source, []string{"Clothing", "Trousers"}}, {exact, []string{"Clothing", "Trousers"}}, {clothes, []string{"Clothing", "Shirts"}}, {unrelated, []string{"Electronics", "Phones"}}, {hidden, []string{"Clothing", "Trousers"}}, {empty, []string{}}} {
		if _, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,category_path) VALUES($1,$2,$3)`, item.id, provider, item.categories); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, `INSERT INTO product_delivery_areas(product_id,country_code) VALUES($1,'NG')`, item.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `UPDATE products SET moderation_status='pending' WHERE id=$1`, hidden); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	controller := &CatalogController{db: db}
	app.Get("/products/:product_id/recommendations", controller.ListRecommendations)
	check := func(id string) []string {
		t.Helper()
		resp, e := app.Test(httptest.NewRequest("GET", "/products/"+id+"/recommendations?country_code=NG", nil))
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var body struct {
			Products []struct {
				ID string `json:"id"`
			} `json:"products"`
		}
		if e = json.NewDecoder(resp.Body).Decode(&body); e != nil || resp.StatusCode != 200 {
			t.Fatalf("recommendations status %d: %v", resp.StatusCode, e)
		}
		ids := []string{}
		for _, p := range body.Products {
			ids = append(ids, p.ID)
		}
		return ids
	}
	ids := check(source)
	if len(ids) != 2 || ids[0] != exact || ids[1] != clothes {
		t.Fatalf("wanted exact trousers then other clothing only, got %v", ids)
	}
	if ids = check(empty); len(ids) != 0 {
		t.Fatalf("uncategorized source must not fill with unrelated products: %v", ids)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_organizations SET verification_status='revoked' WHERE id=$1`, provider); err != nil {
		t.Fatal(err)
	}
	if ids = check(source); len(ids) != 0 {
		t.Fatalf("revoked provider recommended: %v", ids)
	}
}
