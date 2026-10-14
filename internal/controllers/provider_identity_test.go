package controllers

import (
	"across/backend/internal/config"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"testing"
)

func TestProductCheckoutDisabledBeforeDatabaseOrGateway(t *testing.T) {
	app := fiber.New()
	payment := &PaymentController{}
	order := &OrderController{}
	app.Post("/quote", order.QuoteCheckout)
	app.Post("/checkout", payment.FlutterwaveCheckout)
	app.Post("/token", payment.TokenizedCharge)
	for _, path := range []string{"/quote", "/checkout", "/token"} {
		response, err := app.Test(httptest.NewRequest("POST", path, nil))
		if err != nil || response.StatusCode != 403 {
			t.Fatalf("%s did not reject product payment before any DB/gateway access: %v", path, err)
		}
	}
}

func TestProviderIdentityEligibilityAndLifecycle(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,avatar_url text);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,business_name text,logo_url text DEFAULT '',is_active boolean DEFAULT true,verification_status text);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),code text UNIQUE,name text,description text,amount_ngn numeric,listing_limit int,features jsonb);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid,plan_id uuid,status text,starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_subscription_payments(subscription_id uuid,amount numeric,currency_code text);
 CREATE TABLE products(id uuid PRIMARY KEY,provider_id uuid,payment_mode text DEFAULT 'flutterwave',catalog_version int DEFAULT 1,updated_at timestamptz DEFAULT now(),moderation_status text,is_active boolean,published_at timestamptz);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,status text,published_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/077_provider_identity_contact_products.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	migration, err = os.ReadFile("../../migrations/079_permanent_free_provider_tier.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	codes := []string{"provider-monthly", "provider-growth", "provider-business", "provider-diamond"}
	for _, code := range codes {
		provider, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO provider_organizations(id,business_name,verification_status) VALUES($1,'Sample provider','approved')`, []any{provider}},
			{`INSERT INTO provider_subscription_plans(id,code,badge_tier,amount_ngn,listing_limit) VALUES($1,$2,$3,500,5)`, []any{plan, code, providerPlanTier(code)}},
			{`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,starts_at,current_period_end) VALUES($1,$2,$3,'active',now()-interval '1 day',now()+interval '1 month')`, []any{sub, provider, plan}},
		} {
			if _, err = db.Exec(ctx, statement.sql, statement.args...); err != nil {
				t.Fatal(err)
			}
		}

		read := func(wantVerified, wantPaid bool, wantTier string) {
			t.Helper()
			var verified, paid bool
			var tier string
			err := db.QueryRow(ctx, `SELECT verified,paid_active,badge_tier FROM provider_public_identity WHERE provider_id=$1`, provider).Scan(&verified, &paid, &tier)
			if err != nil || verified != wantVerified || paid != wantPaid || tier != wantTier {
				t.Fatalf("%s identity got %v/%v/%q err=%v; wanted %v/%v/%q", code, verified, paid, tier, err, wantVerified, wantPaid, wantTier)
			}
		}
		read(true, false, "")
		if _, err = db.Exec(ctx, `INSERT INTO provider_subscription_payments VALUES($1,500,'NGN')`, sub); err != nil {
			t.Fatal(err)
		}
		read(true, true, providerPlanTier(code))
		m := &ProviderMarketplaceController{db: db, cfg: config.Config{}}
		// Publishing still requires document approval; Free does not confer a paid badge.
		if err = m.requirePublishingEligibility(ctx, provider); err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"pending", "rejected", "suspended"} {
			if _, err = db.Exec(ctx, `UPDATE provider_organizations SET verification_status=$2 WHERE id=$1`, provider, status); err != nil {
				t.Fatal(err)
			}
			read(false, true, "")
			if err = m.requirePublishingEligibility(ctx, provider); err == nil {
				t.Fatalf("%s provider bypassed publishing guard", status)
			}
		}
		_, _ = db.Exec(ctx, `UPDATE provider_organizations SET verification_status='approved' WHERE id=$1`, provider)
		_, _ = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub)
		read(true, false, "")
		_, _ = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()+interval '1 month' WHERE id=$1`, sub)
		read(true, true, providerPlanTier(code))
		for _, status := range []string{"pending", "expired", "cancelled", "past_due", "grace"} {
			_, _ = db.Exec(ctx, `UPDATE provider_subscriptions SET status=$2 WHERE id=$1`, sub, status)
			read(true, false, "")
		}
		_, _ = db.Exec(ctx, `UPDATE provider_subscriptions SET status='active',starts_at=now()+interval '1 day' WHERE id=$1`, sub)
		read(true, false, "")
		_, _ = db.Exec(ctx, `UPDATE provider_subscriptions SET starts_at=now()-interval '1 day' WHERE id=$1`, sub)
		read(true, true, providerPlanTier(code))
	}
	// An effective server-side upgrade/downgrade changes identity without any client tier input.
	var changedProvider, changedSubscription string
	if err = db.QueryRow(ctx, `SELECT sub.provider_id::text,sub.id::text FROM provider_subscriptions sub JOIN provider_subscription_plans plan ON plan.id=sub.plan_id WHERE plan.code='provider-monthly'`).Scan(&changedProvider, &changedSubscription); err != nil {
		t.Fatal(err)
	}
	for _, nextCode := range []string{"provider-diamond", "provider-growth", "provider-monthly"} {
		if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET plan_id=(SELECT id FROM provider_subscription_plans WHERE code=$2) WHERE id=$1`, changedSubscription, nextCode); err != nil {
			t.Fatal(err)
		}
		var tier string
		if err = db.QueryRow(ctx, `SELECT badge_tier FROM provider_public_identity WHERE provider_id=$1`, changedProvider).Scan(&tier); err != nil || tier != providerPlanTier(nextCode) {
			t.Fatal("effective plan change", tier, err)
		}
	}
	// A paid provider's already-published posts survive expiry; Free blocks additional creation.
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,payment_mode,moderation_status,is_active,published_at) VALUES($1,$2,'contact','approved',true,now())`, uuid.NewString(), changedProvider); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, changedSubscription); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status) VALUES($1,$2,'draft')`, uuid.NewString(), changedProvider); err == nil {
		t.Fatal("expired provider created over Free capacity")
	}
	if _, err = db.Exec(ctx, `UPDATE products SET published_at=published_at WHERE provider_id=$1`, changedProvider); err != nil {
		t.Fatal("grandfathered publication blocked", err)
	}
	var preserved int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM products WHERE provider_id=$1 AND moderation_status='approved' AND is_active`, changedProvider).Scan(&preserved); err != nil || preserved != 2 {
		t.Fatal("existing published listings changed", preserved, err)
	}
	// Direct SQL cannot restore provider checkout options after the migration.
	provider := uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name,verification_status) VALUES($1,'Free provider','approved')`, provider); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,payment_mode) VALUES($1,$2,'both')`, uuid.NewString(), provider)
	if err == nil {
		t.Fatal("direct product payment-mode bypass accepted")
	}
	_, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,payment_mode,moderation_status,is_active) VALUES($1,$2,'contact','approved',true)`, uuid.NewString(), provider)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status) VALUES($1,$2,'draft')`, uuid.NewString(), provider); err == nil {
		t.Fatal("free provider created both product and service")
	}
	concurrentProvider := uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name,verification_status) VALUES($1,'Concurrent free provider','approved')`, concurrentProvider); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	out := make(chan error, 2)
	go func() {
		<-start
		_, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,payment_mode,moderation_status) VALUES($1,$2,'contact','draft')`, uuid.NewString(), concurrentProvider)
		out <- err
	}()
	go func() {
		<-start
		_, err := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status) VALUES($1,$2,'draft')`, uuid.NewString(), concurrentProvider)
		out <- err
	}()
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-out == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent free capacity bypass: successful inserts=%d", success)
	}

}

func TestProviderIdentityDiamondPlanMigration(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),code text UNIQUE,name text,description text,amount_ngn numeric,listing_limit int,features jsonb,badge_tier text);
 INSERT INTO provider_subscription_plans(code,name,amount_ngn,listing_limit,badge_tier) VALUES ('provider-monthly','Basic',500,5,'bronze'),('provider-growth','Growth',1000,15,'silver'),('provider-business','Business',2000,40,'gold');`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/078_diamond_provider_plan.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var price, limit int
	var tier string
	if err = db.QueryRow(ctx, `SELECT amount_ngn::int,listing_limit,badge_tier FROM provider_subscription_plans WHERE code='provider-diamond'`).Scan(&price, &limit, &tier); err != nil || price != 5000 || limit != 80 || tier != "diamond" {
		t.Fatal(price, limit, tier, err)
	}
	var unchanged int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_plans WHERE (code='provider-monthly' AND amount_ngn=500 AND listing_limit=5) OR (code='provider-growth' AND amount_ngn=1000 AND listing_limit=15) OR (code='provider-business' AND amount_ngn=2000 AND listing_limit=40)`).Scan(&unchanged); err != nil || unchanged != 3 {
		t.Fatal("existing plans changed", unchanged, err)
	}
}
