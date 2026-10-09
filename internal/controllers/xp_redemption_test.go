package controllers

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestXPCheckoutSendsExplicitZeroMarketplaceAllocation(t *testing.T) {
	client := &http.Client{Transport: paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		split := body["subaccounts"].([]any)[0].(map[string]any)
		if split["id"] != "RS-seller" || split["transaction_charge_type"] != "flat" {
			t.Fatal(split)
		}
		charge, exists := split["transaction_charge"]
		if !exists || charge != float64(0) {
			t.Fatal("fully waived fee must override configured default", split)
		}
		if body["amount"] != float64(100) {
			t.Fatal("seller amount changed", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"link":"https://checkout.flutterwave.com/test"}}`))}, nil
	})}
	provider := newFlutterwaveProvider("test-only-key", client)
	_, err := provider.InitializeCheckout(context.Background(), paymentCheckoutInput{Reference: "test-order", Amount: 100, Currency: "NGN", Subaccounts: []paymentSubaccount{{ID: "RS-seller", TransactionChargeType: "flat", TransactionCharge: 0}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestXPDiscountProtectsSellerAndCurrency(t *testing.T) {
	for _, tc := range []struct {
		points   int
		fee      float64
		currency string
		want     int
	}{
		{650, 1, "NGN", 1}, {650, 500, "NGN", 500}, {650, 1000, "NGN", 650}, {650, 0.2, "NGN", 0},
		{10, 10.99, "NGN", 10}, {-1, 10, "NGN", 0}, {650, 10, "USD", 0}, {650, math.NaN(), "NGN", 0}, {650, math.Inf(1), "NGN", 0},
	} {
		if got := xpDiscount(tc.points, tc.fee, tc.currency); got != tc.want {
			t.Fatalf("%+v got %d", tc, got)
		}
	}
	// Discounts change only the fee: seller gross remains exactly item + delivery.
	for _, fee := range []float64{1, 10.2, 1000} {
		discount := xpDiscount(650, fee, "NGN")
		netFee := roundMoney(fee - float64(discount))
		total := roundMoney(1000 + 500 + netFee)
		if roundMoney(total-netFee) != 1500 {
			t.Fatal("seller amount changed")
		}
	}
}

func TestXPReservationsConcurrencyExpiryConsumptionAndOwnership(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY); ALTER TABLE orders ADD COLUMN user_id uuid,ADD COLUMN order_status text DEFAULT 'Pending';
 CREATE TABLE xp_transactions(user_id uuid,amount integer NOT NULL CHECK(amount>0),reason text,reference_id text,UNIQUE(user_id,reason,reference_id));
 CREATE TABLE notifications(type text,body text); ALTER TABLE payments ADD COLUMN user_id uuid,ADD COLUMN country_code text,ADD COLUMN amount numeric,ADD COLUMN idempotency_key text,ADD COLUMN payment_method text;`)
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
	repair, err := os.ReadFile(filepath.Join("..", "..", "migrations", "061_xp_signed_ledger.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(repair)); err != nil {
		t.Fatal(err)
	}
	user, other := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(ctx, `INSERT INTO users VALUES($1),($2);`, user, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO xp_transactions VALUES($1,650,'welcome','welcome')`, user); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	orders := []string{uuid.NewString(), uuid.NewString()}
	for _, order := range orders {
		if _, err = db.Exec(ctx, `INSERT INTO orders(id,user_id) VALUES($1,$2)`, order, user); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(order string) {
			defer wg.Done()
			tx, err := db.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)
			if err = lockXPUser(ctx, tx, user); err != nil {
				errs <- err
				return
			}
			available, _, err := availableXP(ctx, tx, user)
			if err != nil {
				errs <- err
				return
			}
			points := xpDiscount(available, 500, "NGN")
			if _, err = tx.Exec(ctx, `INSERT INTO xp_redemptions(order_id,user_id,points) VALUES($1,$2,$3);`, order, user, points); err != nil {
				errs <- err
				return
			}
			if _, err = tx.Exec(ctx, `UPDATE orders SET xp_discount=$2 WHERE id=$1`, order, points); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(ctx)
		}(order)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	available, reserved, err := availableXP(ctx, tx, user)
	tx.Rollback(ctx)
	if err != nil || available != 0 || reserved != 650 {
		t.Fatal(available, reserved, err)
	}
	// Once an attempt exists, expiry/replacement must never recycle its points.
	if err = recordOrderPaymentAttempt(ctx, db, "flutterwave", orders[0], user, "NG", 100, "NGN", "xp-test-attempt", "card", "RS-test"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE xp_redemptions SET expires_at=now()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = lockXPUser(ctx, tx, user); err != nil {
		t.Fatal(err)
	}
	if err = releaseUnusedXP(ctx, tx, user, false); err != nil {
		t.Fatal(err)
	}
	if err = validateXPReservation(ctx, tx, orders[0]); err != nil {
		t.Fatal("attempt lost reservation", err)
	}
	if err = validateXPReservation(ctx, tx, orders[1]); err == nil {
		t.Fatal("expired quote accepted")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Deduction and state change are atomic; rollback preserves both.
	tx, _ = db.Begin(ctx)
	if err = consumeXP(ctx, tx, orders[0]); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	tx, _ = db.Begin(ctx)
	if err = consumeXP(ctx, tx, orders[0]); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin(ctx)
	if err = consumeXP(ctx, tx, orders[0]); err == nil {
		t.Fatal("reservation consumed twice")
	}
	tx.Rollback(ctx)
	var debits int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM xp_transactions WHERE amount<0`).Scan(&debits); err != nil || debits != 1 {
		t.Fatal(debits, err)
	}
	controller := NewOrderController(db, true, func() bool { return false })
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-User")); return c.Next() })
	app.Post("/release/:order_id", controller.ReleaseXPQuote)
	for _, tc := range []struct {
		user, order string
		want        int
	}{{other, orders[1], 404}, {user, orders[0], 409}, {user, orders[1], 200}} {
		req := httptest.NewRequest("POST", "/release/"+tc.order, nil)
		req.Header.Set("X-User", tc.user)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("release got %d want %d: %s", res.StatusCode, tc.want, body)
		}
	}
	balanceApp := fiber.New()
	balanceApp.Use(func(c *fiber.Ctx) error { c.Locals("user_id", user); return c.Next() })
	balanceApp.Get("/xp", NewXPController(db).GetBalance)
	res, err := balanceApp.Test(httptest.NewRequest("GET", "/xp", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var balance map[string]any
	if err = json.NewDecoder(res.Body).Decode(&balance); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || balance["reserved_xp"] != float64(0) || balance["redemption_enabled"] != false {
		t.Fatal(balance)
	}
}
