package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This opt-in suite creates and deletes only its own randomly named schema on
// the configured loopback database. It never migrates or changes public tables.
func settlementTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SETTLEMENT_TEST_LOCAL") != "true" {
		t.Skip("set SETTLEMENT_TEST_LOCAL=true to run isolated PostgreSQL settlement tests")
	}
	taskCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	if err = os.Chdir(taskCWD); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") {
		t.Fatal("integration tests require a loopback database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal("local database unavailable")
	}
	schema := "settlement_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, poolCfg)
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
	_, err = db.Exec(ctx, `CREATE TABLE orders(id uuid PRIMARY KEY,provider_id uuid,currency_code text,flutterwave_tx_ref text);
 CREATE TABLE provider_payout_accounts(provider_id uuid,flutterwave_subaccount_id text,created_at timestamptz DEFAULT now());
 CREATE TABLE payments(id uuid DEFAULT gen_random_uuid() PRIMARY KEY,order_id uuid,provider text DEFAULT 'flutterwave',purpose text DEFAULT 'order',
  provider_reference text,provider_transaction_id text,currency_code text DEFAULT 'NGN',payment_status text DEFAULT 'succeeded',
  paid_at timestamptz DEFAULT now(),settlement_status text DEFAULT 'pending',settled_at timestamptz,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now());
 CREATE TABLE merchant_ledger(id uuid DEFAULT gen_random_uuid() PRIMARY KEY,order_id uuid,provider_id uuid,event_key text UNIQUE,
  currency_code text DEFAULT 'NGN',net_amount numeric NOT NULL DEFAULT 20,status text DEFAULT 'available',paid_at timestamptz,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"049_payment_settlement_reconciliation.sql", "052_durable_seller_settlement_reconciliation.sql"} {
		sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	return db
}

func addSettlementFixture(t *testing.T, db *pgxpool.Pool, ref, account string) string {
	t.Helper()
	ctx := context.Background()
	orderID := uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO orders(id,provider_id,currency_code,flutterwave_tx_ref) VALUES($1::uuid,$2::uuid,'NGN',$3)`, orderID, uuid.NewString(), ref)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO payments(order_id,provider_reference,provider_transaction_id,seller_subaccount_id) VALUES($1::uuid,$2,'2099189232',$3)`, orderID, ref, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO merchant_ledger(order_id,event_key,expected_net_amount) VALUES($1::uuid,'order-paid:'||$1,20)`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	return orderID
}

func settlementDetail(t *testing.T, state string, amount any) flutterwaveSettlementDetail {
	t.Helper()
	body := map[string]any{"status": "success", "data": map[string]any{
		"id": 7016533, "status": state, "currency": "NGN", "transaction_count": 1, "destination": "account", "flag_message": "Settlement amount less than threshold amount",
		"processed_date": "2026-10-03T12:00:00Z", "disburse_ref": "payout-ref",
		"transactions": []map[string]any{{"id": 2099189232, "tx_ref": "test-payment", "currency": "NGN", "charged_amount": 20.2, "app_fee": 0.41, "merchant_fee": 0, "settlement_amount": amount, "subaccount_settlement": 1}},
	}}
	encoded, _ := json.Marshal(body)
	var result flutterwaveSettlementDetail
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSettlementPostgresAmountsAndReplay(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	orderID := addSettlementFixture(t, db, "test-payment", "RS-test")
	held := settlementDetail(t, "on-hold", 19.56)
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", held); err != nil || count != 1 {
		t.Fatalf("held import: %d %v", count, err)
	}
	var net, actual, deductions float64
	var state, status, note string
	var paid *time.Time
	if err := db.QueryRow(ctx, `SELECT net_amount,settlement_amount,gateway_deductions,settlement_status,status,paid_at,settlement_note FROM merchant_ledger WHERE order_id=$1`, orderID).Scan(&net, &actual, &deductions, &state, &status, &paid, &note); err != nil {
		t.Fatal(err)
	}
	if net != 20 || actual != 19.56 || deductions != 0.44 || state != "on_hold" || status != "available" || paid != nil || note == "" {
		t.Fatalf("held payout corrupts entitlement or claims release: %v %v %v %v %v %v", net, actual, deductions, state, status, paid)
	}
	completed := settlementDetail(t, "completed", 19.56)
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", completed); err != nil || count != 1 {
		t.Fatalf("release: %d %v", count, err)
	}
	if _, err := applyFlutterwaveSettlement(ctx, db, "RS-test", completed); err != nil {
		t.Fatal(err)
	}
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", held); err != nil || count != 0 {
		t.Fatalf("stale hold must not downgrade payout: %d %v", count, err)
	}
	if err := db.QueryRow(ctx, `SELECT net_amount,settlement_status,status,paid_at FROM merchant_ledger WHERE order_id=$1`, orderID).Scan(&net, &state, &status, &paid); err != nil {
		t.Fatal(err)
	}
	if net != 19.56 || state != "settled" || status != "paid" || paid == nil {
		t.Fatal("actual payout not recorded")
	}
	reversed := settlementDetail(t, "reversed", nil)
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", reversed); err != nil || count != 1 {
		t.Fatalf("reversal: %d %v", count, err)
	}
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", completed); err != nil || count != 0 {
		t.Fatalf("stale release must not erase reversal: %d %v", count, err)
	}
}

func TestSettlementPostgresRejectsBadIdentityAndAmounts(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	addSettlementFixture(t, db, "test-payment", "RS-test")
	for _, test := range []struct {
		name      string
		mutate    func(*flutterwaveSettlementDetail)
		account   string
		wantError bool
	}{
		{"missing released amount", func(d *flutterwaveSettlementDetail) { d.Data.Transactions[0].SettlementAmount = nil }, "RS-test", true},
		{"negative amount", func(d *flutterwaveSettlementDetail) { d.Data.Transactions[0].SettlementAmount = -1 }, "RS-test", true},
		{"nonfinite amount", func(d *flutterwaveSettlementDetail) { d.Data.Transactions[0].SettlementAmount = "NaN" }, "RS-test", true},
		{"zero released amount", func(d *flutterwaveSettlementDetail) { d.Data.Transactions[0].SettlementAmount = 0 }, "RS-test", true},
		{"another seller", func(d *flutterwaveSettlementDetail) {}, "RS-other", false},
		{"another transaction", func(d *flutterwaveSettlementDetail) { d.Data.Transactions[0].ID = 123 }, "RS-test", false},
		{"another currency", func(d *flutterwaveSettlementDetail) { d.Data.Currency = "USD"; d.Data.Transactions[0].Currency = "USD" }, "RS-test", false},
		{"parent settlement", func(d *flutterwaveSettlementDetail) { zero := 0; d.Data.Transactions[0].SubaccountSettlement = &zero }, "RS-test", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := settlementDetail(t, "completed", 19.56)
			test.mutate(&detail)
			count, err := applyFlutterwaveSettlement(ctx, db, test.account, detail)
			if count != 0 || (err != nil) != test.wantError {
				t.Fatalf("unexpected import: %d %v", count, err)
			}
		})
	}
	held := settlementDetail(t, "on-hold", nil)
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", held); err != nil || count != 1 {
		t.Fatalf("missing pending amount should preserve entitlement: %d %v", count, err)
	}
	if _, err := db.Exec(ctx, `UPDATE payments SET seller_subaccount_id='RS-other'`); err == nil {
		t.Fatal("checkout split recipient is mutable")
	}
}

func TestSettlementPostgresDurablePaginationAndLeases(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	addSettlementFixture(t, db, "test-payment", "RS-test")
	_, err := db.Exec(ctx, `INSERT INTO seller_settlement_jobs(subaccount_id,from_date) VALUES('RS-test','2025-01-01'),('RS-other','2025-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	first, err := claimSettlementJob(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := claimSettlementJob(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first.Subaccount == second.Subaccount {
		t.Fatal("workers claimed the same account")
	}
	if _, err = db.Exec(ctx, `UPDATE seller_settlement_jobs SET lease_token=NULL,locked_until=NULL WHERE subaccount_id='RS-test'`); err != nil {
		t.Fatal(err)
	}
	job, err := claimSettlementJob(ctx, db)
	if err != nil || job == nil {
		t.Fatalf("claim failed: %v", err)
	}
	listPages, detailPages := map[int]bool{}, map[int]bool{}
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		page, _ := strconv.Atoi(req.URL.Query().Get("page"))
		body := ""
		if strings.HasSuffix(req.URL.Path, "/settlements") {
			listPages[page] = true
			if page < 22 {
				body = fmt.Sprintf(`{"status":"success","meta":{"page_info":{"total_pages":22}},"data":[{"id":%d}]}`, 1000+page)
			} else {
				body = `{"status":"success","meta":{"page_info":{"total_pages":22}},"data":[{"id":7016533}]}`
			}
		} else if strings.HasSuffix(req.URL.Path, "/7016533") {
			detailPages[page] = true
			d := settlementDetail(t, "on-hold", 19.56)
			d.Data.TransactionCount = 12
			d.Meta.PageInfo.TotalPages = 12
			if page < 12 {
				d.Data.Transactions[0].TxRef = fmt.Sprintf("unrelated-%d", page)
			}
			encoded, _ := json.Marshal(d)
			body = string(encoded)
		} else {
			id, _ := strconv.Atoi(filepath.Base(req.URL.Path))
			body = fmt.Sprintf(`{"status":"success","data":{"id":%d,"status":"completed","currency":"NGN","transactions":[]}}`, id)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	imported := 0
	for attempt := 0; attempt < 10; attempt++ {
		count, err := processSettlementJob(ctx, db, "test", client, job)
		if err != nil {
			t.Fatal(err)
		}
		imported += count
		var checked *time.Time
		if err = db.QueryRow(ctx, `SELECT checked_at FROM seller_settlement_jobs WHERE subaccount_id='RS-test'`).Scan(&checked); err != nil {
			t.Fatal(err)
		}
		if checked != nil {
			break
		}
		if _, err = db.Exec(ctx, `UPDATE seller_settlement_jobs SET next_check_at=now() WHERE subaccount_id='RS-test'`); err != nil {
			t.Fatal(err)
		}
		job, err = claimSettlementJob(ctx, db)
		if err != nil || job == nil {
			t.Fatalf("resume failed: %v", err)
		}
	}
	if !listPages[22] || !detailPages[12] || imported != 1 {
		t.Fatalf("lost continuation: list=%v detail=%v imported=%d", listPages, detailPages, imported)
	}
}

func TestSettlementPostgresHistoricalBackfill(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	orderID := addSettlementFixture(t, db, "test-payment", "RS-test")
	providerID := uuid.NewString()
	_, err := db.Exec(ctx, `UPDATE orders SET provider_id=$1::uuid WHERE id=$2::uuid`, providerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_payout_accounts(provider_id,flutterwave_subaccount_id,created_at) VALUES($1::uuid,'RS-backfill','2025-01-01')`, providerID)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the protection trigger only inside this private test schema to
	// reproduce a pre-052 payment with no recipient snapshot.
	_, err = db.Exec(ctx, `DROP TRIGGER trg_protect_payment_seller_split ON payments; UPDATE payments SET seller_subaccount_id=''; UPDATE merchant_ledger SET net_amount=0,settlement_status='on_hold'`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "052_durable_seller_settlement_reconciliation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var account string
	var net float64
	var state string
	if err = db.QueryRow(ctx, `SELECT p.seller_subaccount_id,ml.net_amount,ml.settlement_status FROM payments p JOIN merchant_ledger ml ON ml.order_id=p.order_id`).Scan(&account, &net, &state); err != nil {
		t.Fatal(err)
	}
	if account != "RS-backfill" || net != 20 || state != "on_hold" {
		t.Fatal("unsafe historical backfill")
	}
	var jobs int
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM seller_settlement_jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("historical job missing: %v", err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("migration replay failed: %v", err)
	}
}

func TestSettlementPostgresFailureDoesNotBlockOtherSellers(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO seller_settlement_jobs(subaccount_id,from_date) VALUES('RS-bad','2025-01-01'),('RS-good','2025-01-01')`); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		status, body := 200, `{"status":"success","data":[]}`
		if req.URL.Query().Get("subaccount_id") == "RS-bad" {
			status = 503
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := ReconcileFlutterwaveSettlements(ctx, db, config.Config{FlutterwaveSecretKey: "test"}, client); err == nil {
		t.Fatal("failed account was not reported")
	}
	var failures int
	var checked *time.Time
	if err := db.QueryRow(ctx, `SELECT failure_count FROM seller_settlement_jobs WHERE subaccount_id='RS-bad'`).Scan(&failures); err != nil || failures != 1 {
		t.Fatal("failed seller retry not persisted")
	}
	if err := db.QueryRow(ctx, `SELECT checked_at FROM seller_settlement_jobs WHERE subaccount_id='RS-good'`).Scan(&checked); err != nil || checked == nil {
		t.Fatal("healthy seller was blocked by failing account")
	}
}

func TestSettlementPostgresMissingLedgerRollsBack(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	addSettlementFixture(t, db, "test-payment", "RS-test")
	if _, err := db.Exec(ctx, `DELETE FROM merchant_ledger`); err != nil {
		t.Fatal(err)
	}
	if count, err := applyFlutterwaveSettlement(ctx, db, "RS-test", settlementDetail(t, "completed", 19.56)); err == nil || count != 0 {
		t.Fatal("missing ledger was silently skipped")
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT settlement_status FROM payments`).Scan(&state); err != nil || state != "pending" {
		t.Fatal("partial payout update was committed")
	}
}

func TestSettlementPostgresManifestEndpointsRejectHeldFunds(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	orderID := addSettlementFixture(t, db, "test-payment", "RS-test")
	userID, providerID := uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `ALTER TABLE orders ADD COLUMN user_id uuid,ADD COLUMN paid_at timestamptz DEFAULT now();
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE order_fulfillments(id uuid DEFAULT gen_random_uuid() PRIMARY KEY,order_id uuid,provider_id uuid,route text DEFAULT 'merchant_cross_border',owner text DEFAULT 'merchant',status text DEFAULT 'processing',version bigint DEFAULT 1);
 CREATE TABLE merchant_manifests(id uuid PRIMARY KEY,provider_id uuid,manifest_code text,origin_country_code text,origin_city text,cutoff_at timestamptz,status text DEFAULT 'open',version bigint DEFAULT 1);
 CREATE TABLE merchant_manifest_orders(manifest_id uuid,order_id uuid UNIQUE);
 CREATE TABLE merchant_manifest_events(manifest_id uuid,idempotency_key text,status text);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_organizations(id) VALUES($1::uuid)`, []any{providerID}},
		{`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1::uuid,$2::uuid,'owner')`, []any{providerID, userID}},
		{`UPDATE orders SET provider_id=$1::uuid,user_id=$2::uuid WHERE id=$3::uuid`, []any{providerID, userID, orderID}},
		{`INSERT INTO order_fulfillments(order_id,provider_id) VALUES($1::uuid,$2::uuid)`, []any{orderID, providerID}},
	} {
		if _, err = db.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	controller := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", userID); return c.Next() })
	app.Post("/manifests", controller.CreateMerchantManifest)
	app.Post("/manifests/:manifest_id/transition", controller.TransitionMerchantManifest)
	create := fmt.Sprintf(`{"order_ids":["%s"],"origin_country_code":"CN","origin_city":"Shenzhen","cutoff_at":"2026-10-04T12:00:00Z"}`, orderID)
	request := func(path, body string) *http.Response {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for _, state := range []string{"pending", "on_hold", "failed", "reversed"} {
		if _, err = db.Exec(ctx, `UPDATE merchant_ledger SET settlement_status=$1`, state); err != nil {
			t.Fatal(err)
		}
		resp := request("/manifests", create)
		_ = resp.Body.Close()
		if resp.StatusCode != 409 {
			t.Fatalf("manifest creation with %s funds returned %d", state, resp.StatusCode)
		}
	}
	var manifests int
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM merchant_manifests`).Scan(&manifests); err != nil || manifests != 0 {
		t.Fatal("rejected manifest was partially committed")
	}
	if _, err = db.Exec(ctx, `UPDATE merchant_ledger SET settlement_status='settled'`); err != nil {
		t.Fatal(err)
	}
	resp := request("/manifests", create)
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("settled manifest failed: %d %s", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE merchant_manifests SET status='closed';UPDATE merchant_ledger SET settlement_status='on_hold'`); err != nil {
		t.Fatal(err)
	}
	resp = request("/manifests/"+created.ID+"/transition", `{"status":"dispatched","expected_version":1,"idempotency_key":"held-dispatch"}`)
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("held manifest dispatch returned %d: %s", resp.StatusCode, body)
	}
	var status string
	if err = db.QueryRow(ctx, `SELECT status FROM order_fulfillments`).Scan(&status); err != nil || status != "processing" {
		t.Fatal("held order was dispatched")
	}
}

func TestSettlementPostgresSavedTokenPreservesSellerSplit(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	orderID, userID, providerID, countryID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `ALTER TABLE orders ADD COLUMN user_id uuid,ADD COLUMN country_id uuid,
		ADD COLUMN total_amount numeric DEFAULT 20.2,ADD COLUMN platform_fee numeric DEFAULT 0.2,ADD COLUMN order_status text DEFAULT 'Pending',
		ADD COLUMN fulfillment_contact_snapshot jsonb DEFAULT '{"address":"Test street","city":"Abuja","state":"FCT","postal_code":"900001"}';
		ALTER TABLE provider_payout_accounts ADD COLUMN status text DEFAULT 'active',ADD COLUMN payment_provider text DEFAULT 'flutterwave';
		ALTER TABLE payments ADD COLUMN user_id uuid,ADD COLUMN country_code text,ADD COLUMN amount numeric,
		ADD COLUMN idempotency_key text,ADD COLUMN payment_method text,ADD COLUMN checkout_url text,
		ADD COLUMN failure_code text,ADD COLUMN failure_message text;
		CREATE TABLE users(id uuid,email text,flutterwave_token text,is_active boolean DEFAULT true,country_id uuid,
		address text DEFAULT 'Test street',city text DEFAULT 'Abuja',state text DEFAULT 'FCT',postal_code text DEFAULT '900001');
		CREATE TABLE countries_config(id uuid,country_code text,active_payment_gateways text[],currency_code text DEFAULT 'NGN',is_active boolean DEFAULT true);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,email,flutterwave_token,country_id) VALUES($1::uuid,'buyer@example.test','test-card-token',$2::uuid)`, []any{userID, countryID}},
		{`INSERT INTO countries_config(id,country_code,active_payment_gateways) VALUES($1::uuid,'NG',ARRAY['flutterwave'])`, []any{countryID}},
		{`INSERT INTO orders(id,user_id,provider_id,country_id,currency_code) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'NGN')`, []any{orderID, userID, providerID, countryID}},
		{`INSERT INTO provider_payout_accounts(provider_id,flutterwave_subaccount_id) VALUES($1::uuid,'RS-test')`, []any{providerID}},
	} {
		if _, err = db.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		var body struct {
			Country     string              `json:"country"`
			Subaccounts []paymentSubaccount `json:"subaccounts"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Country != "NG" || len(body.Subaccounts) != 1 || body.Subaccounts[0].ID != "RS-test" || body.Subaccounts[0].TransactionChargeType != "flat" || body.Subaccounts[0].TransactionCharge != 0.2 {
			t.Fatalf("saved-token seller split missing: %+v", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"id":123}}`))}, nil
	})}
	controller := &PaymentController{db: db, cfg: config.Config{FlutterwaveSecretKey: "FLWSECK-not-a-real-test-key"}, httpClient: client, provider: newFlutterwaveProvider("test", client)}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", userID); return c.Next() })
	app.Post("/charge", controller.TokenizedCharge)
	req := httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(fmt.Sprintf(`{"order_id":"%s"}`, orderID)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 202 || !called {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("tokenized charge failed: %d %s", resp.StatusCode, body)
	}
	var recipient string
	if err = db.QueryRow(ctx, `SELECT seller_subaccount_id FROM payments`).Scan(&recipient); err != nil || recipient != "RS-test" {
		t.Fatal("saved-token checkout snapshot missing")
	}
	for _, change := range []string{
		`UPDATE users SET address='A different delivery address'`,
		`UPDATE users SET address='Test street'; UPDATE countries_config SET is_active=false`,
	} {
		if _, err = db.Exec(ctx, change); err != nil {
			t.Fatal(err)
		}
		called = false
		req = httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(fmt.Sprintf(`{"order_id":"%s"}`, orderID)))
		req.Header.Set("Content-Type", "application/json")
		blocked, testErr := app.Test(req)
		if testErr != nil {
			t.Fatal(testErr)
		}
		blocked.Body.Close()
		if blocked.StatusCode != 404 || called {
			t.Fatalf("stale address/inactive market reached gateway: %d", blocked.StatusCode)
		}
	}
}
