package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestXPCheckoutQuoteAndRepeatedVerifiedPayment(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	// A minimal marketplace fixture exercises the real quote and payment controllers.
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text DEFAULT 'buyer@example.test',full_name text DEFAULT 'Buyer',phone text DEFAULT '+2348012345678',address text DEFAULT 'Street',city text DEFAULT 'Abuja',state text DEFAULT 'FCT',postal_code text DEFAULT '',country_id uuid,is_active boolean DEFAULT true);
 CREATE TABLE xp_transactions(user_id uuid,amount int,reason text,reference_id text,UNIQUE(user_id,reason,reference_id));CREATE TABLE notifications(type text,body text);
 CREATE TABLE countries_config(id uuid,country_code text,currency_code text,is_active boolean DEFAULT true,active_payment_gateways text[] DEFAULT ARRAY['flutterwave']);
 CREATE TABLE provider_organizations(id uuid,verification_status text DEFAULT 'approved',is_active boolean DEFAULT true);
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 ALTER TABLE provider_payout_accounts ADD COLUMN payment_provider text DEFAULT 'flutterwave',ADD COLUMN status text DEFAULT 'active';
 CREATE TABLE products(id uuid,sku text,provider_id uuid,title text DEFAULT 'Watch',description text DEFAULT '',cost_price_rmb numeric DEFAULT 0,image_urls text[] DEFAULT '{}',factory_details jsonb DEFAULT '{}',origin_hub_id uuid,is_flash_sale boolean DEFAULT false,flash_sale_price numeric DEFAULT 0,local_currency_code text DEFAULT 'NGN',local_selling_price numeric DEFAULT 65000,inventory_count int DEFAULT 10,inventory_country_code text DEFAULT 'NG',inventory_city text DEFAULT 'Abuja',inventory_location text DEFAULT 'Market',stock_state text DEFAULT 'locally_available',delivery_max_days int DEFAULT 3,delivery_min_days int DEFAULT 1,handling_time_hours int DEFAULT 24,delivery_methods text[] DEFAULT ARRAY['delivery'],return_policy text DEFAULT '',is_active boolean DEFAULT true,moderation_status text DEFAULT 'approved',fulfillment_mode text DEFAULT 'merchant_local',sold_count int DEFAULT 0,updated_at timestamptz DEFAULT now());
 CREATE TABLE product_delivery_areas(product_id uuid,country_code text,state_key text DEFAULT '',city_key text DEFAULT '',currency_code text DEFAULT 'NGN',delivered_price numeric,delivery_fee numeric DEFAULT 0);
 CREATE TABLE logistics_hubs(id uuid,code text,name text,city text,address text);
 ALTER TABLE orders ALTER COLUMN id SET DEFAULT gen_random_uuid(), ADD COLUMN user_id uuid,ADD COLUMN country_id uuid,ADD COLUMN total_amount numeric,ADD COLUMN shipping_fee numeric,ADD COLUMN customs_fee numeric,ADD COLUMN vat_fee numeric,ADD COLUMN stamp_duty_fee numeric,ADD COLUMN platform_fee numeric,ADD COLUMN delivery_promised_at timestamptz,ADD COLUMN fulfillment_contact_snapshot jsonb,ADD COLUMN fulfillment_mode text,ADD COLUMN order_status text DEFAULT 'Pending',ADD COLUMN batch_id uuid,ADD COLUMN package_label text,ADD COLUMN flutterwave_transaction_id text,ADD COLUMN paid_at timestamptz,ADD COLUMN updated_at timestamptz DEFAULT now();
 CREATE TABLE order_fulfillments(order_id uuid,provider_id uuid,route text,owner text,status text,origin_snapshot jsonb,delivery_snapshot jsonb,current_location text,estimated_delivery_at timestamptz);
 CREATE TABLE order_items(order_id uuid,product_id uuid,origin_hub_id uuid,sku text,title text,variant jsonb,quantity int,unit_price numeric,product_snapshot jsonb,provider_id uuid,fulfillment_mode text);
 CREATE TABLE tracking_events(order_id uuid,stage text,notes text);
 ALTER TABLE payments ADD COLUMN user_id uuid,ADD COLUMN country_code text,ADD COLUMN amount numeric,ADD COLUMN idempotency_key text,ADD COLUMN payment_method text,ADD COLUMN checkout_url text,ADD COLUMN provider_status text,ADD COLUMN failure_code text,ADD COLUMN failure_message text;
 ALTER TABLE merchant_ledger ADD COLUMN gross_amount numeric,ADD COLUMN platform_fee numeric,ADD COLUMN available_at timestamptz;`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "059_xp_service_fee_redemption.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	user, country, provider, product := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO countries_config(id,country_code,currency_code) VALUES($1,'NG','NGN')`, []any{country}},
		{`INSERT INTO users(id,country_id) VALUES($1,$2)`, []any{user, country}},
		{`INSERT INTO provider_organizations(id) VALUES($1)`, []any{provider}},
		{`INSERT INTO provider_payout_accounts(provider_id,flutterwave_subaccount_id) VALUES($1,'RS-seller')`, []any{provider}},
		{`INSERT INTO products(id,sku,provider_id) VALUES($1,'WATCH',$2)`, []any{product, provider}},
		{`INSERT INTO product_delivery_areas(product_id,country_code,delivered_price) VALUES($1,'NG',65000)`, []any{product}},
		{`INSERT INTO xp_transactions VALUES($1,650,'welcome','welcome')`, []any{user}},
	} {
		if _, err = db.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	controller := NewOrderController(db, true, func() bool { return false })
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", user); return c.Next() })
	app.Post("/quote", controller.QuoteCheckout)
	quote := func(use bool) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"country_code": "NG", "use_xp": use, "items": []map[string]any{{"product_id": product, "sku": "WATCH", "quantity": 1}}})
		req := httptest.NewRequest("POST", "/quote", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		if err = json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode, result)
		}
		return result
	}
	first := quote(true)
	if first["xp_discount"] != float64(650) || first["grand_total"] != float64(65000) || first["platform_fee"] != float64(0) {
		t.Fatal(first)
	}
	noXP := quote(false)
	if noXP["grand_total"] != float64(65650) || noXP["xp_available"] != float64(650) {
		t.Fatal(noXP)
	}
	if err = recordOrderPaymentAttempt(ctx, db, "flutterwave", first["order_id"].(string), user, "NG", 65000, "NGN", "expired", "card", "RS-seller"); err == nil {
		t.Fatal("replaced quote accepted")
	}
	final := quote(true)
	order := final["order_id"].(string)
	ref := newPaymentReference(order)
	if err = recordOrderPaymentAttempt(ctx, db, "flutterwave", order, user, "NG", 65000, "NGN", ref, "card", "RS-seller"); err != nil {
		t.Fatal(err)
	}
	if err = recordOrderPaymentAttempt(ctx, db, "flutterwave", order, user, "NG", 65000, "NGN", "second-charge", "card", "RS-seller"); err == nil {
		t.Fatal("duplicate discounted payment attempt accepted")
	}
	payments := NewPaymentController(db, config.Config{})
	if err = payments.settleOrderPayment(ctx, order, ref, "123", 64999, "NGN"); err == nil {
		t.Fatal("underpayment accepted")
	}
	if err = payments.settleOrderPayment(ctx, order, ref, "123", 65000, "USD"); err == nil {
		t.Fatal("wrong currency accepted")
	}
	for i := 0; i < 2; i++ {
		if err = payments.settleOrderPayment(ctx, order, ref, "123", 65000, "NGN"); err != nil {
			t.Fatal(err)
		}
	}
	var seller, fee float64
	if err = db.QueryRow(ctx, `SELECT expected_net_amount,platform_fee FROM merchant_ledger WHERE order_id=$1`, order).Scan(&seller, &fee); err != nil || seller != 65000 || fee != 0 {
		t.Fatal(seller, fee, err)
	}
	var remaining, debits int
	if err = db.QueryRow(ctx, `SELECT SUM(amount)::int,count(*) FILTER(WHERE amount<0) FROM xp_transactions WHERE user_id=$1`, user).Scan(&remaining, &debits); err != nil || remaining != 0 || debits != 1 {
		t.Fatal(remaining, debits, err)
	}
}
