package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestSubscriptionRenewalCancellationAndNextPlan(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_subscription_plans(id uuid PRIMARY KEY,name text,amount_ngn numeric,is_active boolean DEFAULT true,flutterwave_plan_id bigint);
 CREATE TABLE provider_subscriptions(id uuid PRIMARY KEY,provider_id uuid,plan_id uuid,status text,billing_mode text,customer_email text,starts_at timestamptz,current_period_end timestamptz,created_at timestamptz DEFAULT now());
 CREATE TABLE provider_subscription_payments(subscription_id uuid,flutterwave_transaction_id text,paid_at timestamptz DEFAULT now());`)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "094_provider_renewal_controls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	leaseSQL, e := os.ReadFile(filepath.Join("..", "..", "migrations", "114_provider_renewal_operation_lease.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, string(leaseSQL)); e != nil {
		t.Fatal(e)
	}
	owner, provider, basic, growth, subscription := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO users VALUES($1)`, owner)
	exec(`INSERT INTO provider_organizations(id) VALUES($1)`, provider)
	exec(`INSERT INTO provider_members(provider_id,user_id,role) VALUES($1,$2,'owner')`, provider, owner)
	exec(`INSERT INTO provider_subscription_plans VALUES($1,'Basic',500,true,123),($2,'Growth',1000,true,456)`, basic, growth)
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,billing_mode,customer_email,starts_at,current_period_end) VALUES($1,$2,$3,'active','recurring','owner@example.test',now()-interval '1 day',now()+interval '29 days')`, subscription, provider, basic)
	exec(`INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id) VALUES($1,'1001')`, subscription)
	exec(`UPDATE provider_subscriptions SET gateway_plan_id=123 WHERE false`) // immutable snapshot is tested below on a new row.
	var gatewayHook func(*http.Request)
	cancelled := map[string]bool{}
	puts := 0
	failVerify := false
	wrongPlan := false
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if gatewayHook != nil {
			gatewayHook(req)
		}
		body := `{"status":"success","data":[]}`
		if req.Method == http.MethodGet {
			if req.URL.Query().Get("transaction_id") != "1001" || req.URL.Query().Get("email") != "owner@example.test" {
				t.Fatalf("not scoped to owned verified transaction: %s", req.URL)
			}
			items := []string{}
			for _, id := range []string{"41", "42"} {
				if !cancelled[id] || failVerify {
					plan := 123
					if wrongPlan {
						plan = 999
					}
					items = append(items, fmt.Sprintf(`{"id":%s,"plan":%d,"status":"active","customer":{"customer_email":"owner@example.test"}}`, id, plan))
				}
			}
			body = `{"status":"success","meta":{"page_info":{"total_pages":1}},"data":[` + strings.Join(items, ",") + `]}`
		} else if req.Method == http.MethodPut {
			puts++
			parts := strings.Split(req.URL.Path, "/")
			cancelled[parts[len(parts)-2]] = true
		} else {
			t.Fatalf("unexpected gateway request %s", req.Method)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: newFlutterwaveProvider("test", client)}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", owner); return c.Next() })
	app.Get("/renewal", controller.ProviderRenewalStatus)
	app.Post("/renewal", controller.ProviderManageRenewal)
	app.Post("/checkout", controller.SubscriptionCheckout)
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, e := app.Test(req, 5000)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var data map[string]any
		raw, _ := io.ReadAll(res.Body)
		if e = json.Unmarshal(raw, &data); e != nil {
			data = map[string]any{"error": string(raw)}
		}
		return res.StatusCode, data
	}
	var originalEnd time.Time
	if err = db.QueryRow(ctx, `SELECT current_period_end FROM provider_subscriptions WHERE id=$1`, subscription).Scan(&originalEnd); err != nil {
		t.Fatal(err)
	}
	// A stalled gateway must hold zero pool connections. Another API replica
	// must still reject the same-provider renewal and replacement checkout.
	entered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	gatewayHook = func(req *http.Request) { once.Do(func() { close(entered); <-proceed }) }
	type reply struct {
		status int
		data   map[string]any
	}
	done := make(chan reply, 1)
	go func() {
		status, data := call("POST", "/renewal", `{"next_plan_id":"`+growth+`"}`)
		done <- reply{status, data}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway not reached")
	}
	if acquired := db.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("gateway retained %d DB connections", acquired)
	}
	var probe int
	if err = db.QueryRow(ctx, `SELECT 1`).Scan(&probe); err != nil {
		t.Error(err)
	}
	if status, _ := call("POST", "/renewal", `{}`); status != 409 {
		t.Errorf("parallel replica renewal accepted: %d", status)
	}
	if status, _ := call("POST", "/checkout", `{}`); status != 409 {
		t.Errorf("checkout bypassed pending cancellation: %d", status)
	}
	// An old API updating preferences must not bypass the durable lease.
	if _, err = db.Exec(ctx, `UPDATE provider_renewal_preferences SET renewal_status='uncertain' WHERE provider_id=$1::uuid`, provider); err == nil {
		t.Error("old API bypassed active lease")
	}
	close(proceed)
	result := <-done
	gatewayHook = nil
	status, data := result.status, result.data
	if status != 200 || data["renewal_status"] != "stopped" || puts != 2 {
		t.Fatalf("schedule: %d %+v puts%d", status, data, puts)
	}
	status, data = call("GET", "/renewal", "")
	if status != 200 || data["next_plan_id"] != growth || data["current_plan_id"] != basic {
		t.Fatalf("status %d %+v", status, data)
	}
	var stillPlan, stillStatus string
	var stillEnd time.Time
	if err = db.QueryRow(ctx, `SELECT plan_id::text,status,current_period_end FROM provider_subscriptions WHERE id=$1`, subscription).Scan(&stillPlan, &stillStatus, &stillEnd); err != nil {
		t.Fatal(err)
	}
	if stillPlan != basic || stillStatus != "active" || !stillEnd.Equal(originalEnd) {
		t.Fatal("cancellation changed paid entitlement")
	}
	// Reproduce the durable state left when the process dies AFTER both
	// gateway cancellations, BEFORE committing local confirmation. No defer from
	// that dead process runs. Recovery must use the ordinary endpoint on a fresh API.
	crashToken := uuid.NewString()
	exec(`UPDATE provider_renewal_preferences SET renewal_status='uncertain',operation_token=$2,operation_expires_at=clock_timestamp()+interval '2 minutes' WHERE provider_id=$1`, provider, crashToken)
	exec(`UPDATE provider_renewal_cancellations SET status='requested' WHERE provider_id=$1`, provider)
	status, data = call("GET", "/renewal", "")
	if status != 200 || data["renewal_status"] != "uncertain" {
		t.Fatalf("crash uncertainty lost: %d %+v", status, data)
	}
	if status, _ = call("POST", "/renewal", `{}`); status != 409 {
		t.Fatal("crash lease did not block overlapping renewal")
	}
	if status, _ = call("POST", "/checkout", `{"plan_id":"`+growth+`"}`); status != 409 {
		t.Fatal("checkout bypassed unresolved crash")
	}
	// Move the DB clock deadline, not the real clock, to simulate lease expiry.
	expire, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = expire.Exec(ctx, `SELECT set_config('app.renewal_operation_token',$1,true)`, crashToken); e != nil {
		t.Fatal(e)
	}
	if _, e = expire.Exec(ctx, `UPDATE provider_renewal_preferences SET operation_expires_at=clock_timestamp()-interval '1 second' WHERE provider_id=$1`, provider); e != nil {
		t.Fatal(e)
	}
	if e = expire.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	controller = &ProviderMarketplaceController{db: db, paymentProvider: newFlutterwaveProvider("test", client)}
	recovery := fiber.New()
	recovery.Use(func(c *fiber.Ctx) error { c.Locals("user_id", owner); return c.Next() })
	recovery.Post("/renewal", controller.ProviderManageRenewal)
	request := httptest.NewRequest("POST", "/renewal", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recovered, e := recovery.Test(request, 5000)
	if e != nil {
		t.Fatal(e)
	}
	recovered.Body.Close()
	if recovered.StatusCode != 200 || puts != 2 {
		t.Fatalf("crash recovery: status%d cancellation PUTs%d", recovered.StatusCode, puts)
	}
	status, data = call("GET", "/renewal", "")
	if status != 200 || data["renewal_status"] != "stopped" {
		t.Fatalf("gateway/local reconciliation failed: %+v", data)
	}
	if err = db.QueryRow(ctx, `SELECT plan_id::text,status,current_period_end FROM provider_subscriptions WHERE id=$1`, subscription).Scan(&stillPlan, &stillStatus, &stillEnd); err != nil || stillPlan != basic || stillStatus != "active" || !stillEnd.Equal(originalEnd) {
		t.Fatal("crash recovery changed paid entitlement")
	}
	var unresolved int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_renewal_cancellations WHERE provider_id=$1 AND status<>'confirmed'`, provider).Scan(&unresolved); err != nil || unresolved != 0 {
		t.Fatal("crash cancellation audit not reconciled")
	}
	status, _ = call("POST", "/checkout", `{"plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("active paid period allowed another checkout")
	}
	// Separate API replicas share the same renewal/checkout lock.
	lockTx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "provider-renewal:"+provider); e != nil {
		t.Fatal(e)
	}
	status, _ = call("POST", "/renewal", `{}`)
	if status != 409 {
		t.Fatal("concurrent renewal change was not blocked")
	}
	status, _ = call("POST", "/checkout", `{}`)
	if status != 409 {
		t.Fatal("concurrent replacement checkout was not blocked")
	}
	if e = lockTx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	status, data = call("POST", "/renewal", `{}`)
	if status != 200 || data["next_plan_id"] != nil || puts != 2 {
		t.Fatalf("idempotent cancellation %d %+v", status, data)
	}
	exec(`UPDATE provider_members SET role='staff' WHERE provider_id=$1`, provider)
	status, _ = call("POST", "/renewal", `{}`)
	if status != 403 {
		t.Fatal("non-owner could cancel billing")
	}
	exec(`UPDATE provider_members SET role='owner' WHERE provider_id=$1`, provider)
	cancelled = map[string]bool{}
	wrongPlan = true
	status, _ = call("POST", "/renewal", `{}`)
	if status != 502 || puts != 2 {
		t.Fatal("unowned gateway plan was cancelled")
	}
	wrongPlan = false
	failVerify = true
	status, _ = call("POST", "/renewal", `{}`)
	if status != 502 {
		t.Fatal("unconfirmed cancellation reported success")
	}
	_, data = call("GET", "/renewal", "")
	if data["renewal_status"] != "uncertain" {
		t.Fatalf("uncertainty lost %+v", data)
	}
	exec(`UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, subscription)
	status, _ = call("POST", "/checkout", `{"plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("uncertain old renewal allowed replacement billing")
	}
	failVerify = false
	status, _ = call("POST", "/renewal", `{}`)
	if status != 200 {
		t.Fatal("could not reconcile cancellation after retry")
	}
	status, _ = call("POST", "/renewal", `{"next_plan_id":"`+growth+`"}`)
	if status != 409 {
		t.Fatal("scheduled an expired entitlement")
	}
	// A new verified purchase clears the old preference from the status response.
	exec(`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,billing_mode,customer_email,starts_at,current_period_end,created_at,gateway_plan_id) VALUES($1,$2,$3,'active','recurring','owner@example.test',now(),now()+interval '1 month',now()+interval '1 second',456)`, uuid.NewString(), provider, growth)
	_, data = call("GET", "/renewal", "")
	if data["renewal_status"] != "" || data["current_plan_id"] != growth {
		t.Fatalf("old preferences leaked into new subscription %+v", data)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET gateway_plan_id=999 WHERE gateway_plan_id=456`); err == nil {
		t.Fatal("gateway ownership snapshot was mutable")
	}
}
