package controllers

import (
	"context"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"testing"
)

func TestBusinessContactPolicyAndLegacyThreads(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,is_active bool DEFAULT true,verification_status text DEFAULT 'approved');
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active bool DEFAULT true);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,title text,status text DEFAULT 'approved');
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,title text,is_active bool DEFAULT true,moderation_status text DEFAULT 'approved',payment_mode text DEFAULT 'contact');
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE TABLE provider_conversations(id uuid PRIMARY KEY,provider_id uuid,user_id uuid,listing_id uuid,product_id uuid,status text DEFAULT 'open');
 CREATE TABLE provider_conversation_messages(id uuid PRIMARY KEY,conversation_id uuid,body text);
 CREATE TABLE chat_rooms(id uuid PRIMARY KEY);
 CREATE TABLE users(id uuid PRIMARY KEY,full_name text DEFAULT 'Test actor',email text DEFAULT 'actor@example.invalid',phone text);
 CREATE TABLE provider_requests(id uuid PRIMARY KEY,user_id uuid,idempotency_key text,status text);
 CREATE TABLE countries_config(country_code text,currency_code text,is_active bool,active_payment_gateways text[]);
 INSERT INTO countries_config VALUES('NG','NGN',true,ARRAY['flutterwave']);
 ALTER TABLE provider_listings ADD COLUMN listing_type text DEFAULT 'property',ADD COLUMN country_code text DEFAULT 'NG',ADD COLUMN currency_code text DEFAULT 'NGN';`)
	owner, manager, staff, inactive, outsider, provider, otherProvider := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	service, product := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO provider_organizations(id,owner_user_id)VALUES($1,$2),($3,$4)`, provider, owner, otherProvider, outsider)
	exec(`INSERT INTO provider_members VALUES($1,$2,'manager',true),($1,$3,'staff',true),($1,$4,'staff',false),($5,$6,'owner',true)`, provider, manager, staff, inactive, otherProvider, outsider)
	exec(`INSERT INTO provider_listings(id,provider_id,title)VALUES($1,$2,'Service')`, service, provider)
	exec(`INSERT INTO products(id,provider_id,title)VALUES($1,$2,'Product')`, product, provider)
	exec(`INSERT INTO provider_subscriptions VALUES($1,'active',now()+interval '1 month')`, provider)
	for _, actor := range []string{owner, manager, staff, inactive, outsider} {
		exec(`INSERT INTO users(id)VALUES($1)`, actor)
	}
	market := &ProviderMarketplaceController{db: db}
	shared := &CommunityController{db: db, market: market}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/access/:provider_id", market.BusinessContactAccess)
	app.Post("/product/:product_id", market.StartProductConversation)
	app.Post("/service/:listing_id", market.StartProviderConversation)
	app.Post("/request/:listing_id", market.CreateRequest)
	app.Post("/buyer/:conversation_id", market.SendBuyerConversationMessage)
	app.Post("/seller/:conversation_id", market.SendProviderConversationMessage)
	app.Post("/unified/:conversation_id", shared.Send)
	for _, tc := range []struct {
		actor       string
		own, manage bool
	}{{owner, true, false}, {manager, true, true}, {staff, true, false}, {inactive, false, false}, {outsider, false, false}} {
		response := communityCall(t, app, "GET", "/access/"+provider, tc.actor, "", 200)
		if response["own_business"] != tc.own || response["can_manage"] != tc.manage {
			t.Fatal("wrong target-business permissions", tc, response)
		}
	}
	communityCall(t, app, "GET", "/access/invalid", owner, "", 400)
	communityCall(t, app, "GET", "/access/"+provider, "", "", 401)
	// Owner has no membership row: primary ownership still blocks self-contact.
	for _, actor := range []string{owner, manager, staff} {
		for _, url := range []string{"/product/" + product, "/service/" + service} {
			communityCall(t, app, "POST", url, actor, `{"message":"self"}`, 403)
		}
		communityCall(t, app, "POST", "/request/"+service, actor, `{"message":"self","request_type":"enquiry","idempotency_key":"self-enquiry"}`, 403)
		thread := uuid.NewString()
		exec(`INSERT INTO provider_conversations(id,provider_id,user_id,listing_id)VALUES($1,$2,$3,$4)`, thread, provider, actor, service)
		exec(`INSERT INTO provider_conversation_messages VALUES($1,$2,'Historic self chat')`, uuid.NewString(), thread)
		communityCall(t, app, "POST", "/buyer/"+thread, actor, `{"message":"self"}`, 403)
		communityCall(t, app, "POST", "/seller/"+thread, manager, `{"message":"self"}`, 403)
		communityCall(t, app, "POST", "/unified/"+thread, actor, `{"message":"self"}`, 403)
	}
	// A membership added after thread creation is checked on every send.
	thread := uuid.NewString()
	exec(`INSERT INTO provider_conversations(id,provider_id,user_id,product_id)VALUES($1,$2,$3,$4)`, thread, provider, inactive, product)
	exec(`INSERT INTO provider_members VALUES($1,$2,'staff',true)`, provider, inactive)
	communityCall(t, app, "POST", "/buyer/"+thread, inactive, `{"message":"new member"}`, 403)
	var rows int
	if e := db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE body='Historic self chat'`).Scan(&rows); e != nil || rows != 3 {
		t.Fatal(fmt.Sprint("old history changed", rows, e))
	}
}

// Private exact lookup must not scan the first page or return another business.
func TestMerchantProductExactLookupScoped(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	columns := []string{"sku text DEFAULT 'SKU'", "description text DEFAULT ''", "local_currency_code text DEFAULT 'NGN'", "local_selling_price numeric DEFAULT 500", "compare_at_price numeric", "inventory_count integer DEFAULT 5", "is_flash_sale bool DEFAULT false", "flash_sale_price numeric", "moderation_notes text DEFAULT ''", "fulfillment_mode text DEFAULT 'merchant_local'", "inventory_country_code text DEFAULT 'NG'", "inventory_city text DEFAULT 'Abuja'", "inventory_location text DEFAULT 'Test location'", "stock_state text DEFAULT 'locally_available'", "handling_time_hours integer DEFAULT 24", "delivery_min_days integer DEFAULT 1", "delivery_max_days integer DEFAULT 7", "delivery_methods text[] DEFAULT '{}'", "return_policy text DEFAULT ''", "atlantic_last_mile bool DEFAULT false", "inventory_latitude numeric DEFAULT 9", "inventory_longitude numeric DEFAULT 7", "created_at timestamptz DEFAULT now()", "factory_details jsonb DEFAULT '{}'"}
	for _, column := range columns {
		exec("ALTER TABLE products ADD COLUMN " + column)
	}
	exec(`ALTER TABLE product_delivery_areas ADD COLUMN state_key text DEFAULT '',ADD COLUMN delivered_price numeric DEFAULT 500,ADD COLUMN delivery_fee numeric DEFAULT 0,ADD COLUMN currency_code text DEFAULT 'NGN'`)
	exec(`ALTER TABLE products ALTER COLUMN is_active SET DEFAULT true, ALTER COLUMN moderation_status SET DEFAULT 'draft'`)
	actor, other, provider, foreign, wanted, older, foreignProduct := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO provider_organizations(id,owner_user_id,business_name)VALUES($1,$2,'My business'),($3,$4,'Other')`, provider, actor, foreign, other)
	exec(`INSERT INTO provider_members(provider_id,user_id,role)VALUES($1,$2,'owner'),($3,$4,'owner')`, provider, actor, foreign, other)
	plan, sub := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO provider_subscription_plans(id,code,listing_limit)VALUES($1,'provider-diamond',40)`, plan)
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end)VALUES($1,$2,$3,'active',now()-interval '1 day',now()+interval '1 month')`, sub, provider, plan)
	exec(`INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub)
	exec(`INSERT INTO products(id,provider_id,title,created_at)VALUES($1,$2,'Exact target',now()-interval '1 day'),($3,$2,'Newest',now()),($4,$5,'Foreign',now())`, wanted, provider, older, foreignProduct, foreign)
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/products", controller.ListMyMerchantProducts)
	result := communityCall(t, app, "GET", "/products?limit=1&product_id="+wanted, actor, "", 200)
	items := result["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != wanted {
		t.Fatal("exact editor lookup returned newest/other product", result)
	}
	if len(communityCall(t, app, "GET", "/products?product_id="+foreignProduct, actor, "", 200)["items"].([]any)) != 0 {
		t.Fatal("foreign private product leaked")
	}
	communityCall(t, app, "GET", "/products?product_id=invalid", actor, "", 400)
}
