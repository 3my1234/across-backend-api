package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestXPWithdrawalsReserveRetryRejectAndPay(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,email text); CREATE TABLE admins(id uuid PRIMARY KEY);
 CREATE TABLE xp_transactions(user_id uuid,amount int CHECK(amount<>0),reason text,reference_id text,UNIQUE(user_id,reason,reference_id));
 CREATE TABLE xp_redemptions(user_id uuid,order_id uuid,status text,expires_at timestamptz,points int,updated_at timestamptz);
 CREATE TABLE notifications(user_id uuid,order_id uuid,batch_id uuid,type text,title text,body text,data jsonb,event_key text UNIQUE);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "067_xp_withdrawals.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	user, other, admin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, entry := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users VALUES($1,'buyer@example.test'),($2,'other@example.test')`, []any{user, other}},
		{`INSERT INTO admins VALUES($1)`, []any{admin}},
		{`INSERT INTO xp_transactions VALUES($1,1500,'legacy','welcome')`, []any{user}},
	} {
		if _, err = db.Exec(ctx, entry.query, entry.args...); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", c.Get("X-User"))
		c.Locals("admin_id", admin)
		return c.Next()
	})
	controller := NewXPController(db)
	app.Post("/withdraw", controller.RequestWithdrawal)
	app.Get("/withdraw", controller.ListWithdrawals)
	app.Patch("/review/:withdrawal_id", controller.AdminReviewWithdrawal)
	call := func(method, path, actor string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-User", actor)
		response, e := app.Test(r, 10000)
		if e != nil {
			t.Error(e)
			return 0, nil
		}
		defer response.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(response.Body).Decode(&result)
		return response.StatusCode, result
	}
	payload := func(key string, points int) map[string]any {
		return map[string]any{"request_key": key, "points": points, "bank_name": "Test bank", "account_name": "Buyer", "account_number": "1234567890"}
	}
	if status, _ := call("POST", "/withdraw", user, payload(uuid.NewString(), 999)); status != 400 {
		t.Fatal(status)
	}
	key := uuid.NewString()
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, result := call("POST", "/withdraw", user, payload(key, 1000))
			if status != 200 && status != 201 {
				t.Error(status, result)
				return
			}
			ids <- result["id"].(string)
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("retry duplicated withdrawal")
		}
		id = got
	}
	balance := func(want int) {
		var got int
		if err = db.QueryRow(ctx, `SELECT SUM(amount)::int FROM xp_transactions WHERE user_id=$1`, user).Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	balance(500)
	if status, _ := call("POST", "/withdraw", user, payload(uuid.NewString(), 1000)); status != 409 {
		t.Fatal(status)
	}
	if status, result := call("GET", "/withdraw", other, nil); status != 200 || len(result["items"].([]any)) != 0 {
		t.Fatal("history exposed another user's bank details", status, result)
	}
	if status, _ := call("PATCH", "/review/"+id, user, map[string]any{"status": "paid", "payout_reference": "bank-1"}); status != 409 {
		t.Fatal("paid without review", status)
	}
	for i := 0; i < 2; i++ {
		if status, result := call("PATCH", "/review/"+id, user, map[string]any{"status": "rejected", "note": "Bank details need correction"}); status != 200 {
			t.Fatal(status, result)
		}
	}
	balance(1500)
	status, result := call("POST", "/withdraw", user, payload(uuid.NewString(), 1000))
	if status != 201 {
		t.Fatal(status, result)
	}
	id = result["id"].(string)
	for _, action := range []map[string]any{{"status": "processing"}, {"status": "paid", "payout_reference": "bank-2"}, {"status": "paid", "payout_reference": "bank-2"}} {
		status, result = call("PATCH", "/review/"+id, user, action)
		if status != 200 {
			t.Fatal(status, result)
		}
	}
	balance(500)
	if status, _ = call("PATCH", "/review/"+id, user, map[string]any{"status": "rejected", "note": "reverse"}); status != 409 {
		t.Fatal("paid withdrawal refunded", status)
	}
}
