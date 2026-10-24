package playbilling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func isolatedStore(t *testing.T) (*Store, string, *string, *string) {
	t.Helper()
	if os.Getenv("PLAY_BILLING_TEST_LOCAL") != "true" {
		t.Skip("isolated PostgreSQL opt-in required")
	}
	dsn := os.Getenv("PLAY_BILLING_TEST_DSN")
	target, err := url.Parse(dsn)
	if err != nil || (target.Hostname() != "127.0.0.1" && target.Hostname() != "localhost") {
		t.Fatal("Play tests require a loopback synthetic database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "play_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	_, err = db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text,email_verified bool,is_active bool);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid REFERENCES users(id),is_active bool,verification_status text,country_code text DEFAULT 'NG');
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,code text,name text,amount_ngn numeric,is_active bool);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid REFERENCES provider_organizations(id),plan_id uuid REFERENCES provider_subscription_plans(id),status text,tx_ref text UNIQUE,customer_email text,expected_amount_ngn numeric,billing_mode text,duration_months int,starts_at timestamptz,current_period_end timestamptz,last_payment_at timestamptz,updated_at timestamptz DEFAULT now(),version int DEFAULT 1);
 CREATE TABLE provider_subscription_payments(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),subscription_id uuid REFERENCES provider_subscriptions(id),flutterwave_transaction_id text NOT NULL UNIQUE,tx_ref text,amount numeric,currency_code text,paid_at timestamptz DEFAULT now());
 CREATE TABLE provider_renewal_preferences(provider_id uuid PRIMARY KEY,renewal_status text);
 CREATE TABLE xp_transactions(user_id uuid,amount int,reason text,reference_id text,UNIQUE(user_id,reason,reference_id));
 CREATE TABLE notifications(user_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);
 CREATE TABLE payments(provider text,purpose text,provider_subscription_id uuid,user_id uuid,country_code text,amount numeric,currency_code text,provider_reference text,provider_transaction_id text,idempotency_key text,payment_method text,payment_status text,provider_status text,paid_at timestamptz,UNIQUE(provider,provider_transaction_id));`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"102_referral_rewards.sql", "104_play_provider_billing.sql"} {
		raw, e := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(ctx, string(raw)); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
	}
	provider, owner, referrer, plan := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO users VALUES($1::uuid,'owner@example.invalid',true,true),($2::uuid,'referrer@example.invalid',true,true)`, owner, referrer)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,is_active,verification_status) VALUES($1::uuid,$2::uuid,true,'approved');`, provider, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_subscription_plans VALUES($1::uuid,'provider-monthly','Basic',500,true)`, plan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO play_provider_products VALUES('atl_basic','monthly',$1::uuid,1,50000,true)`, plan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `UPDATE referral_program_settings SET enabled=true;`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO user_referrals(referred_user_id,referrer_user_id) VALUES($1::uuid,$2::uuid)`, owner, referrer)
	if err != nil {
		t.Fatal(err)
	}
	state := "SUBSCRIPTION_STATE_ACTIVE"
	order := "GPA.1111-2222-3333-44444"
	var transportLock sync.Mutex
	lastToken := ""
	client, _ := New(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		transportLock.Lock()
		defer transportLock.Unlock()
		if r.Method == "POST" {
			return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if strings.Contains(r.URL.Path, "/orders/") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(orderFixture(lastToken, order)))}, nil
		}
		lastToken = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw := fixture(state)
		raw = strings.Replace(raw, AccountID("owned-provider"), AccountID(provider), 1)
		raw = strings.Replace(raw, "2026-11-24T00:00:00Z", time.Now().UTC().Add(30*24*time.Hour).Truncate(time.Second).Format(time.RFC3339), 1)
		raw = strings.Replace(raw, "GPA.1111-2222-3333-44444", order, 1)
		raw = strings.Replace(raw, `"autoRenewEnabled":true`, `"autoRenewEnabled":true,"recurringPrice":{"currencyCode":"NGN","units":"500","nanos":0}`, 1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}, func(context.Context) (string, error) { return "synthetic-access", nil })
	vault, _ := NewVault(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	return &Store{DB: db, Client: client, Vault: vault}, provider, &state, &order
}
func TestPlayDurableEntitlementsRenewalExpiryAndReferral(t *testing.T) {
	store, provider, state, order := isolatedStore(t)
	ctx := context.Background()
	token := "synthetic-recoverable-purchase-token"
	for i := 0; i < 3; i++ {
		result, err := store.Process(ctx, provider, token, "atl_basic", "monthly")
		if err != nil || !result.Active || result.NeedsAcknowledgement {
			t.Fatalf("Activation/retry %d: %+v %v", i, result, err)
		}
	}
	var subscriptions, receipts, xp int
	store.DB.QueryRow(ctx, `SELECT count(*) FROM provider_subscriptions`).Scan(&subscriptions)
	store.DB.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_payments`).Scan(&receipts)
	store.DB.QueryRow(ctx, `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if subscriptions != 1 || receipts != 1 || xp != 500 {
		t.Fatalf("Duplicate grant or referral mismatch: %d %d %d", subscriptions, receipts, xp)
	}
	var raw []byte
	store.DB.QueryRow(ctx, `SELECT encrypted_token FROM play_provider_purchases`).Scan(&raw)
	if strings.Contains(string(raw), token) {
		t.Fatal("Plain token stored")
	}
	if _, err := store.Process(ctx, uuid.NewString(), token, "atl_basic", "monthly"); err == nil {
		t.Fatal("Cross-provider token accepted")
	}
	*order = "GPA.1111-2222-3333-44444..0"
	if _, err := store.Process(ctx, provider, token, "atl_basic", "monthly"); err != nil {
		t.Fatal(err)
	}
	store.DB.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_payments`).Scan(&receipts)
	store.DB.QueryRow(ctx, `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if receipts != 2 || xp != 500 {
		t.Fatal("Renewal replayed referral or lost receipt")
	}
	*state = "SUBSCRIPTION_STATE_ON_HOLD"
	result, err := store.Process(ctx, provider, token, "atl_basic", "monthly")
	if err != nil || result.Active {
		t.Fatal("Held purchase retained paid access", err)
	}
	var status string
	store.DB.QueryRow(ctx, `SELECT status FROM provider_subscriptions`).Scan(&status)
	if status != "expired" {
		t.Fatal("Inactive state not enforced")
	}
	*state = "SUBSCRIPTION_STATE_EXPIRED"
	result, err = store.Process(ctx, provider, token, "atl_basic", "monthly")
	if err != nil || result.AutoRenews {
		t.Fatal("Expired purchase kept renewal lock", err)
	}
}
func TestPlayLicensePurchaseCannotAwardCashXP(t *testing.T) {
	store, provider, _, _ := isolatedStore(t)
	store.AllowTest = true
	prior := store.Client.http.Transport
	store.Client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		res, err := prior.RoundTrip(r)
		if err == nil && r.Method == "GET" {
			var value map[string]any
			_ = json.NewDecoder(res.Body).Decode(&value)
			res.Body.Close()
			value["testPurchase"] = map[string]any{}
			raw, _ := json.Marshal(value)
			res.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		return res, err
	})
	if _, err := store.Process(context.Background(), provider, "synthetic-license-purchase-token", "atl_basic", "monthly"); err != nil {
		t.Fatal(err)
	}
	var xp int
	store.DB.QueryRow(context.Background(), `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if xp != 5 {
		t.Fatalf("Test purchase awarded cash-equivalent provider XP: %d", xp)
	}
}
func TestPlayConcurrentRetriesMintOnePeriod(t *testing.T) {
	store, provider, _, _ := isolatedStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Process(context.Background(), provider, "synthetic-concurrent-purchase-token", "atl_basic", "monthly")
			if err != nil && err != ErrBusy {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var receipts int
	store.DB.QueryRow(context.Background(), `SELECT count(*) FROM provider_subscription_payments`).Scan(&receipts)
	if receipts != 1 {
		t.Fatal("Concurrent receipt duplication", receipts)
	}
}

func TestPlayPendingRecoveryReservationAndMappingProtection(t *testing.T) {
	store, provider, state, _ := isolatedStore(t)
	ctx := context.Background()
	if err := store.Prepare(ctx, provider, "atl_basic", "monthly"); err != nil {
		t.Fatal(err)
	}
	if err := store.Prepare(ctx, provider, "atl_basic", "monthly"); err == nil {
		t.Fatal("Two simultaneous checkouts reserved")
	}
	*state = "SUBSCRIPTION_STATE_PENDING"
	result, err := store.Process(ctx, provider, "synthetic-pending-purchase-token", "atl_basic", "monthly")
	if err != nil || !result.Pending || result.Active {
		t.Fatal("Pending payment was lost or activated", err)
	}
	var receipts, xp int
	store.DB.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_payments`).Scan(&receipts)
	store.DB.QueryRow(ctx, `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if receipts != 0 || xp != 5 {
		t.Fatal("Pending payment received paid receipt/XP")
	}
	if _, err = store.DB.Exec(ctx, `UPDATE play_provider_products SET amount_minor=60000`); err == nil {
		t.Fatal("Purchased product price mapping mutated")
	}
	// Removing a product from sale must not stop existing payment recovery.
	if _, err = store.DB.Exec(ctx, `UPDATE play_provider_products SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	*state = "SUBSCRIPTION_STATE_ACTIVE"
	result, err = store.Process(ctx, provider, "synthetic-pending-purchase-token", "atl_basic", "monthly")
	if err != nil || !result.Active {
		t.Fatal("Pending payment could not recover", err)
	}
	items, err := store.Catalog(ctx, provider)
	if err != nil || len(items) != 1 || items[0].Purchasable {
		t.Fatal("Retired mapping was lost for restore or remained purchasable", err)
	}
}

func TestPlayUnconfirmedAcknowledgementDefersReferralCredit(t *testing.T) {
	store, provider, _, _ := isolatedStore(t)
	ctx := context.Background()
	prior := store.Client.http.Transport
	fail := true
	store.Client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && fail {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return prior.RoundTrip(r)
	})
	result, err := store.Process(ctx, provider, "synthetic-unconfirmed-ack-token", "atl_basic", "monthly")
	if err != nil || !result.Active || !result.NeedsAcknowledgement {
		t.Fatal("Uncertain acknowledgement not preserved", err)
	}
	var xp int
	store.DB.QueryRow(ctx, `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if xp != 5 {
		t.Fatal("Unacknowledged purchase awarded cash-equivalent XP")
	}
	fail = false
	result, err = store.Process(ctx, provider, "synthetic-unconfirmed-ack-token", "atl_basic", "monthly")
	if err != nil || result.NeedsAcknowledgement {
		t.Fatal("Acknowledgement retry failed", err)
	}
	store.DB.QueryRow(ctx, `SELECT sum(amount) FROM xp_transactions`).Scan(&xp)
	if xp != 500 {
		t.Fatal("Successful acknowledgement failed to reconcile referral XP", xp)
	}
}

func TestPlayUnfulfilledDuplicateGetsDurableRefundWithoutReplacingAccess(t *testing.T) {
	store, provider, _, _ := isolatedStore(t)
	ctx := context.Background()
	_, err := store.DB.Exec(ctx, `INSERT INTO provider_subscriptions(provider_id,plan_id,status,tx_ref,customer_email,expected_amount_ngn,billing_mode,duration_months,current_period_end) SELECT $1::uuid,plan_id,'active','legacy-paid-period','owner@example.invalid',500,'one_time',1,now()+interval '30 days' FROM play_provider_products`, provider)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Process(ctx, provider, "synthetic-unfulfilled-duplicate-token", "atl_basic", "monthly")
	if err != nil || result.Active || result.RefundState != "requested" {
		t.Fatal("Duplicate purchase was granted or recovery was lost", err)
	}
	var state string
	store.DB.QueryRow(ctx, `SELECT status FROM play_provider_refund_jobs`).Scan(&state)
	if state != "confirmed" {
		t.Fatal("Verified unfulfilled purchase refund was not processed")
	}
	var purchases, receipts int
	store.DB.QueryRow(ctx, `SELECT count(*) FROM play_provider_purchases`).Scan(&purchases)
	store.DB.QueryRow(ctx, `SELECT count(*) FROM provider_subscription_payments`).Scan(&receipts)
	if purchases != 0 || receipts != 0 {
		t.Fatal("Refunded duplicate minted entitlement or receipt")
	}
	result, err = store.Process(ctx, provider, "synthetic-unfulfilled-duplicate-token", "atl_basic", "monthly")
	if err != nil || result.RefundState != "confirmed" || result.Active {
		t.Fatal("Restoring refunded token reactivated it", err)
	}
}

func TestPlayPrepaidYearPreservesOneOffBilling(t *testing.T) {
	store, provider, _, _ := isolatedStore(t)
	ctx := context.Background()
	_, err := store.DB.Exec(ctx, `UPDATE play_provider_products SET base_plan_id='yearly',duration_months=12,amount_minor=540000,prepaid=true`)
	if err != nil {
		t.Fatal(err)
	}
	prior := store.Client.http.Transport
	store.Client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		response, e := prior.RoundTrip(r)
		if e != nil || r.Method != "GET" {
			return response, e
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		text := strings.ReplaceAll(string(raw), "monthly", "yearly")
		text = strings.ReplaceAll(text, `"units":"500"`, `"units":"5400"`)
		text = strings.Replace(text, `"autoRenewingPlan":{"autoRenewEnabled":true,"recurringPrice":{"currencyCode":"NGN","units":"5400","nanos":0}}`, `"prepaidPlan":{}`, 1)
		response.Body = io.NopCloser(strings.NewReader(text))
		return response, nil
	})
	result, err := store.Process(ctx, provider, "synthetic-yearly-prepaid-token", "atl_basic", "yearly")
	if err != nil || !result.Active || result.AutoRenews || result.NeedsAcknowledgement {
		t.Fatal("Prepaid purchase changed renewal rules or failed verification", err)
	}
	var months int
	var mode string
	store.DB.QueryRow(ctx, `SELECT duration_months,billing_mode FROM provider_subscriptions`).Scan(&months, &mode)
	if months != 12 || mode != "one_time" {
		t.Fatal("Prepaid entitlement metadata changed", months, mode)
	}
}
