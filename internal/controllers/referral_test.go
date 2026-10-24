package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func referralFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),email text UNIQUE,full_name text DEFAULT '',phone text,password_hash text,country_id uuid,privy_user_id text,is_active boolean DEFAULT false,email_verified boolean DEFAULT false,updated_at timestamptz DEFAULT now());
 CREATE TABLE user_identities(user_id uuid,provider text,provider_subject text,email text,updated_at timestamptz DEFAULT now(),UNIQUE(provider,provider_subject));
 CREATE TABLE xp_transactions(user_id uuid REFERENCES users(id),amount int,reason text,reference_id text,UNIQUE(user_id,reason,reference_id));
 CREATE TABLE notifications(user_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid REFERENCES users(id));
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,code text,amount_ngn numeric);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid REFERENCES provider_organizations(id),plan_id uuid REFERENCES provider_subscription_plans(id),expected_amount_ngn numeric,status text DEFAULT 'pending');
 CREATE TABLE provider_subscription_payments(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),subscription_id uuid REFERENCES provider_subscriptions(id),flutterwave_transaction_id text UNIQUE,amount numeric,currency_code text);`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../migrations/102_referral_rewards.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err = db.QueryRow(ctx, `SELECT enabled FROM referral_program_settings WHERE id=true`).Scan(&enabled); err != nil || enabled {
		t.Fatal("new referral program must start paused until coordinated release")
	}
	if _, err = db.Exec(ctx, `UPDATE referral_program_settings SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	inviter, newUser := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users(id,email,is_active,email_verified)VALUES($1,'inviter@example.test',true,true),($2,'new@example.test',false,false);INSERT INTO referral_codes(user_id,code)VALUES($1,'ABCDEF123456')`, pgx.QueryExecModeSimpleProtocol, inviter, newUser); err != nil {
		t.Fatal(err)
	}
	return db, inviter, newUser
}
func attachTestReferral(t *testing.T, db *pgxpool.Pool, user string) {
	t.Helper()
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = attachReferralTx(context.Background(), tx, user, " abcdef123456 "); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func referralBalance(t *testing.T, db *pgxpool.Pool, user string) int {
	t.Helper()
	var total int
	if err := db.QueryRow(context.Background(), `SELECT COALESCE(sum(amount),0) FROM xp_transactions WHERE user_id=$1`, user).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}
func referralProvider(t *testing.T, db *pgxpool.Pool, user, code string, amount int) string {
	t.Helper()
	org, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(context.Background(), `INSERT INTO provider_organizations VALUES($1,$2);INSERT INTO provider_subscription_plans VALUES($3,$4,$5);INSERT INTO provider_subscriptions(id,provider_id,plan_id,expected_amount_ngn)VALUES($6,$1,$3,$5)`, pgx.QueryExecModeSimpleProtocol, org, user, plan, code, amount, sub)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}
func TestReferralVerifiedSignupAndPaidProviderOnce(t *testing.T) {
	db, inviter, user := referralFixture(t)
	ctx := context.Background()
	attachTestReferral(t, db, user)
	if n := referralBalance(t, db, inviter); n != 0 {
		t.Fatalf("unverified signup earned %d", n)
	}
	if _, err := db.Exec(ctx, `UPDATE users SET email_verified=true,is_active=true WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if n := referralBalance(t, db, inviter); n != 5 {
		t.Fatalf("verified signup earned %d", n)
	}
	sub := referralProvider(t, db, user, "provider-monthly", 500)
	if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET status='active' WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	if n := referralBalance(t, db, inviter); n != 5 {
		t.Fatal("status alone rewarded an unpaid subscription")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,amount,currency_code)VALUES($1,$2,500,'NGN')`, sub, uuid.NewString())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := referralBalance(t, db, inviter); n != 500 {
		t.Fatalf("concurrent payments earned %d, want 500 TOTAL", n)
	}
	upgrade := referralProvider(t, db, user, "provider-diamond", 5000)
	if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,amount,currency_code)VALUES($1,$2,5000,'NGN');UPDATE users SET email_verified=true WHERE id=$3`, pgx.QueryExecModeSimpleProtocol, upgrade, uuid.NewString(), user); err != nil {
		t.Fatal(err)
	}
	if n := referralBalance(t, db, inviter); n != 500 {
		t.Fatalf("upgrade/reverification repeated reward: %d", n)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM xp_transactions WHERE user_id=$1`, inviter).Scan(&count); err != nil || count != 2 {
		t.Fatalf("expected two ledger credits, count=%d err=%v", count, err)
	}
	app := fiber.New()
	controller := NewReferralController(db)
	app.Get("/me", func(c *fiber.Ctx) error { c.Locals("user_id", inviter); return controller.Mine(c) })
	resp, err := app.Test(httptest.NewRequest("GET", "/me", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var summary struct {
		Code   string
		Earned int `json:"earned_xp"`
		Paid   int `json:"paid_providers"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&summary); err != nil || resp.StatusCode != 200 || summary.Code != "ABCDEF123456" || summary.Earned != 500 || summary.Paid != 1 {
		t.Fatalf("summary=%+v status=%d err=%v", summary, resp.StatusCode, err)
	}
}
func TestReferralPaymentEligibilityAndVerificationOrdering(t *testing.T) {
	for _, plan := range []struct {
		code  string
		price int
	}{{"provider-monthly", 500}, {"provider-growth", 1000}, {"provider-business", 2000}, {"provider-diamond", 5000}} {
		t.Run(plan.code, func(t *testing.T) {
			db, inviter, user := referralFixture(t)
			ctx := context.Background()
			attachTestReferral(t, db, user)
			sub := referralProvider(t, db, user, plan.code, plan.price)
			for _, pay := range []struct {
				currency string
				amount   int
			}{{"USD", plan.price}, {"NGN", 1}} {
				if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,amount,currency_code)VALUES($1,$2,$3,$4)`, sub, uuid.NewString(), pay.amount, pay.currency); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(ctx, `UPDATE users SET is_active=true,email_verified=true WHERE id=$1`, user); err != nil {
				t.Fatal(err)
			}
			if n := referralBalance(t, db, inviter); n != 5 {
				t.Fatalf("bad currency/underpayment qualified: %d", n)
			}
			if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,amount,currency_code)VALUES($1,$2,$3,'NGN')`, sub, uuid.NewString(), plan.price); err != nil {
				t.Fatal(err)
			}
			if n := referralBalance(t, db, inviter); n != 500 {
				t.Fatalf("plan failed reward: %d", n)
			}
		})
	}
	t.Run("payment-before-verification", func(t *testing.T) {
		db, inviter, user := referralFixture(t)
		ctx := context.Background()
		attachTestReferral(t, db, user)
		sub := referralProvider(t, db, user, "provider-monthly", 500)
		if _, err := db.Exec(ctx, `INSERT INTO provider_subscription_payments(subscription_id,amount,currency_code)VALUES($1,500,'NGN')`, sub); err != nil {
			t.Fatal(err)
		}
		if n := referralBalance(t, db, inviter); n != 0 {
			t.Fatal("unverified payment awarded")
		}
		if _, err := db.Exec(ctx, `UPDATE users SET is_active=true,email_verified=true WHERE id=$1`, user); err != nil {
			t.Fatal(err)
		}
		if n := referralBalance(t, db, inviter); n != 500 {
			t.Fatalf("out-of-order verification lost award: %d", n)
		}
	})
	t.Run("free-provider", func(t *testing.T) {
		db, inviter, user := referralFixture(t)
		ctx := context.Background()
		attachTestReferral(t, db, user)
		sub := referralProvider(t, db, user, "provider-free", 0)
		if _, err := db.Exec(ctx, `UPDATE users SET is_active=true,email_verified=true WHERE id=$1;INSERT INTO provider_subscription_payments(subscription_id,amount,currency_code)VALUES($2,500,'NGN')`, pgx.QueryExecModeSimpleProtocol, user, sub); err != nil {
			t.Fatal(err)
		}
		if n := referralBalance(t, db, inviter); n != 5 {
			t.Fatalf("free provider earned %d", n)
		}
	})
}
func TestReferralAttributionRollbackAndGoogleIdentity(t *testing.T) {
	db, inviter, user := referralFixture(t)
	ctx := context.Background()
	for _, code := range []string{"wrong", "ZZZZZZZZZZZZ", "ABCDEF123456"} {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		target := user
		if code == "ABCDEF123456" {
			target = inviter
		}
		err = attachReferralTx(ctx, tx, target, code)
		tx.Rollback(ctx)
		if err == nil {
			t.Fatalf("invalid/self code %q accepted", code)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = attachReferralTx(ctx, tx, user, "ABCDEF123456"); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM user_referrals`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled back signup left an attribution")
	}
	service := NewIdentityService(db)
	google, created, err := service.ResolvePrivyWithReferral(ctx, uuid.NewString(), "verified-google-subject", "google@example.test", "Google user", "ABCDEF123456")
	if err != nil || !created {
		t.Fatalf("Google signup: %v created=%v", err, created)
	}
	if n := referralBalance(t, db, inviter); n != 5 {
		t.Fatalf("verified Google signup earned %d", n)
	}
	again, created, err := service.ResolvePrivyWithReferral(ctx, uuid.NewString(), "verified-google-subject", "google@example.test", "Google user", "ZZZZZZZZZZZZ")
	if err != nil || created || again != google {
		t.Fatalf("existing Google sign-in changed attribution: %v", err)
	}
	if n := referralBalance(t, db, inviter); n != 5 {
		t.Fatal("Google login repeated reward")
	}
}
func TestReferralLegacyAuthenticationGuards(t *testing.T) {
	if (config.Config{AppEnv: "development"}).DevLoginAllowed() {
		t.Fatal("development login must default off")
	}
	for _, environment := range []string{"production", "staging", "", "unknown"} {
		app := fiber.New()
		app.Post("/dev", NewDevController(nil, config.Config{AppEnv: environment, EnableDevLogin: true}).Login)
		resp, err := app.Test(httptest.NewRequest("POST", "/dev", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("dev login allowed in %q", environment)
		}
	}
	app := fiber.New()
	app.Post("/gmail", (&AuthController{}).Gmail)
	resp, err := app.Test(httptest.NewRequest("POST", "/gmail", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 410 {
		t.Fatal("unverified email-only Google login remains available")
	}
}

func TestReferralPauseAndPrivateValidation(t *testing.T) {
	db, inviter, user := referralFixture(t)
	ctx := context.Background()
	attachTestReferral(t, db, user)
	if _, err := db.Exec(ctx, `UPDATE referral_program_settings SET enabled=false;UPDATE users SET is_active=true,email_verified=true WHERE id=$1`, pgx.QueryExecModeSimpleProtocol, user); err != nil {
		t.Fatal(err)
	}
	if n := referralBalance(t, db, inviter); n != 0 {
		t.Fatal("paused program awarded XP")
	}
	controller := NewReferralController(db)
	app := fiber.New()
	app.Get("/validate", controller.Validate)
	resp, err := app.Test(httptest.NewRequest("GET", "/validate?code=ABCDEF123456", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("paused validation did not fail closed")
	}
	if _, err = db.Exec(ctx, `UPDATE referral_program_settings SET enabled=true`); err != nil {
		t.Fatal(err)
	}
	resp, err = app.Test(httptest.NewRequest("GET", "/validate?code=ABCDEF123456", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 200 || body["valid"] != true || len(body) != 1 {
		t.Fatalf("public validation leaked identity or failed: %+v %v", body, err)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("referral validation was cacheable")
	}
}
