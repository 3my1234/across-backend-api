package controllers

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type payoutTestGateway struct {
	paymentProvider
	calls     atomic.Int32
	started   chan struct{}
	release   chan struct{}
	duplicate bool
}

func (p *payoutTestGateway) CreateCollectionSubaccount(context.Context, collectionSubaccountInput) (collectionSubaccountResult, error) {
	p.calls.Add(1)
	if p.duplicate {
		return collectionSubaccountResult{}, &paymentProviderError{Message: "A subaccount with the account number and bank already exists"}
	}
	close(p.started)
	<-p.release
	return collectionSubaccountResult{NumericID: 42, SubaccountID: "RS-test-bank", AccountName: "Test Seller", BankName: "Test Bank"}, nil
}

func TestProviderPayoutConcurrentAndDuplicate(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,provider_type text,is_active boolean,business_name text,contact_phone text,contact_email text);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean);
 ALTER TABLE provider_payout_accounts ADD COLUMN status text,ADD COLUMN country_code text,ADD COLUMN currency_code text,
 ADD COLUMN account_bank text,ADD COLUMN account_number_last4 text,ADD COLUMN account_name text,ADD COLUMN bank_name text,
 ADD COLUMN flutterwave_subaccount_numeric_id bigint;`)
	if err != nil {
		t.Fatal(err)
	}
	providerID, userID := uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations VALUES($1,'product_merchant',true,'Seller','','')`, providerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,'owner',true)`, providerID, userID)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &payoutTestGateway{started: make(chan struct{}), release: make(chan struct{})}
	controller := &ProviderMarketplaceController{db: db, paymentProvider: gateway}
	app := fiber.New()
	app.Post("/bank", func(c *fiber.Ctx) error { c.Locals("user_id", userID); return controller.ConfigurePayoutAccount(c) })
	request := func() int {
		req := httptest.NewRequest("POST", "/bank", strings.NewReader(`{"country_code":"NG","account_bank":"044","account_number":"0123456789"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	first := make(chan int, 1)
	go func() { first <- request() }()
	select {
	case <-gateway.started:
	case status := <-first:
		t.Fatalf("create failed before gateway: %d", status)
	case <-time.After(10 * time.Second):
		t.Fatal("gateway was never called")
	}
	second := request()
	close(gateway.release)
	initial := <-first
	if second != 409 || initial != 201 || gateway.calls.Load() != 1 {
		t.Fatalf("concurrent creation: initial=%d second=%d calls=%d", initial, second, gateway.calls.Load())
	}
	if status := request(); status != 409 || gateway.calls.Load() != 1 {
		t.Fatalf("existing account was recreated: %d", status)
	}
	if _, err = db.Exec(ctx, `DELETE FROM provider_payout_accounts WHERE provider_id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	gateway.duplicate = true
	if status := request(); status != 409 {
		t.Fatalf("gateway duplicate should be actionable conflict, got %d", status)
	}
	req := httptest.NewRequest("POST", "/bank", strings.NewReader(`{"country_code":"NG","account_bank":"044","account_number":"0123456789"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 409 || !strings.Contains(string(body), "already connected to Flutterwave") || !strings.Contains(string(body), "support@atlxpres.com") {
		t.Fatalf("duplicate response must explain the existing connection: status=%d body=%s err=%v", resp.StatusCode, body, err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_payout_accounts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate must not attach an unverified account: count=%d err=%v", count, err)
	}
}
