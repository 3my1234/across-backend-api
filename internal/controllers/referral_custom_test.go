package controllers

import (
	appdb "across/backend/internal/db"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func referralRequest(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	// Fiber errors may be plain text; success responses must be valid JSON.
	err = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode == 200 && err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, result
}
func TestReferralCodeNormalization(t *testing.T) {
	for _, raw := range []string{" sabinus001 ", "Sabiboi", "ABCDEF123456", "abcd", strings.Repeat("A", 20)} {
		got, err := normalizedReferralCode(raw)
		if err != nil || got != strings.ToUpper(strings.TrimSpace(raw)) {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"abc", strings.Repeat("A", 21), "name with spaces", "foo-bar", "user@example.test", "\uFF33\uFF21\uFF22\uFF29", "USER_1"} {
		if _, err := normalizedReferralCode(raw); err == nil {
			t.Fatal("accepted invalid name", raw)
		}
	}
}
func TestReferralCustomMigrationOldAndNewCodes(t *testing.T) {
	db, inviter, user := legacyReferralFixture(t)
	if err := appdb.VerifyReferralCodeSchema(context.Background(), db); err == nil {
		t.Fatal("old schema passed startup gate")
	}
	applyCustomReferralMigration(t, db)
	if err := appdb.VerifyReferralCodeSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM referral_code_names WHERE user_id=$1 AND code='ABCDEF123456' AND kind='original'`, inviter).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// Simulate an older API issuing an original after the additive migration.
	if _, err := db.Exec(ctx, `INSERT INTO referral_codes(user_id,code) VALUES($1,'000000000001')`, user); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM referral_code_names WHERE user_id=$1 AND code='000000000001'`, user).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO referral_code_names(code,user_id,kind)VALUES('123456789012',$1,'custom')`, inviter); err != nil {
		t.Fatal(err)
	}
	third := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id) VALUES($1)`, third); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO referral_codes(user_id,code) VALUES($1,'123456789012')`, third); err == nil {
		t.Fatal("new original stole an existing custom name")
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM referral_codes WHERE user_id=$1`, third).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed issuance left an inconsistent original", n, err)
	}
}
func TestReferralCustomOneTimeAndOriginalAttribution(t *testing.T) {
	db, inviter, user := referralFixture(t)
	controller := NewReferralController(db)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", inviter); return c.Next() })
	app.Get("/me", controller.Mine)
	app.Post("/custom", controller.Customize)
	app.Get("/validate", controller.Validate)
	status, result := referralRequest(t, app, "GET", "/me", "")
	if status != 200 || result["code"] != "ABCDEF123456" || result["can_customize"] != true {
		t.Fatal(status, result)
	}
	status, result = referralRequest(t, app, "POST", "/custom", `{"code":" sabiboi "}`)
	if status != 200 || result["custom_code"] != "SABIBOI" {
		t.Fatal(status, result)
	}
	for _, raw := range []string{"SABIBOI", "sabiboi"} {
		status, _ = referralRequest(t, app, "POST", "/custom", fmt.Sprintf(`{"code":%q}`, raw))
		if status != 200 {
			t.Fatal("idempotent acknowledgement", status)
		}
	}
	status, _ = referralRequest(t, app, "POST", "/custom", `{"code":"ANOTHER"}`)
	if status != 409 {
		t.Fatal("second choice allowed", status)
	}
	status, result = referralRequest(t, app, "GET", "/me", "")
	if status != 200 || result["code"] != "ABCDEF123456" || result["custom_code"] != "SABIBOI" || result["can_customize"] != false {
		t.Fatal(status, result)
	}
	for _, code := range []string{"ABCDEF123456", "SABIBOI"} {
		status, result = referralRequest(t, app, "GET", "/validate?code="+code, "")
		if status != 200 || result["valid"] != true || len(result) != 1 {
			t.Fatal(status, result)
		}
	}
	// Each invitation resolves to the same user. Neither choosing nor validating awards XP.
	if referralBalance(t, db, inviter) != 0 {
		t.Fatal("code choice awarded XP")
	}
	for i, code := range []string{"SABIBOI", "ABCDEF123456"} {
		referred := user
		if i > 0 {
			referred = uuid.NewString()
			if _, err := db.Exec(context.Background(), `INSERT INTO users(id)VALUES($1)`, referred); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := db.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = attachReferralTx(context.Background(), tx, referred, code); err != nil {
			tx.Rollback(context.Background())
			t.Fatal(err)
		}
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(context.Background(), `UPDATE users SET is_active=true,email_verified=true WHERE id=$1`, referred); err != nil {
			t.Fatal(err)
		}
	}
	if referralBalance(t, db, inviter) != 10 {
		t.Fatal("original/custom referral rewards changed")
	}
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = attachReferralTx(context.Background(), tx, inviter, "SABIBOI"); err == nil {
		t.Fatal("custom code allowed self-referral")
	}
}
func TestReferralCustomConcurrentChoicesAndCollisions(t *testing.T) {
	for _, sameAccount := range []bool{true, false} {
		t.Run(fmt.Sprint(sameAccount), func(t *testing.T) {
			db, inviter, user := referralFixture(t)
			ctx := context.Background()
			if _, err := db.Exec(ctx, `UPDATE users SET is_active=true,email_verified=true WHERE id=$1`, user); err != nil {
				t.Fatal(err)
			}
			controller := NewReferralController(db)
			app := fiber.New()
			app.Post("/custom", func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-Test-User")); return controller.Customize(c) })
			statuses := make(chan int, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					owner, name := inviter, fmt.Sprintf("CHOICE%d", i)
					if !sameAccount {
						name = "SHAREDNAME"
						if i%2 == 1 {
							owner = user
						}
					}
					req := httptest.NewRequest("POST", "/custom", strings.NewReader(fmt.Sprintf(`{"code":%q}`, name)))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Test-User", owner)
					resp, err := app.Test(req, -1)
					if err != nil {
						statuses <- 0
						return
					}
					resp.Body.Close()
					statuses <- resp.StatusCode
				}(i)
			}
			wg.Wait()
			close(statuses)
			success := 0
			for status := range statuses {
				if status == 200 {
					success++
				} else if status != 409 {
					t.Fatal("unexpected concurrent status", status)
				}
			}
			var count int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM referral_code_names WHERE kind='custom'`).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if sameAccount && success != 1 {
				t.Fatal("multiple names accepted", success)
			}
			if !sameAccount && success != 4 {
				t.Fatal("winner should acknowledge its same-code retries", success)
			}
		})
	}
}
func TestReferralCustomGuards(t *testing.T) {
	db, inviter, user := referralFixture(t)
	controller := NewReferralController(db)
	app := fiber.New()
	owner := inviter
	app.Post("/custom", func(c *fiber.Ctx) error { c.Locals("user_id", owner); return controller.Customize(c) })
	for _, raw := range []string{`{"code":"ABCDEF123456"}`, `{"code":"x"}`, `{"code":"A B C"}`} {
		status, _ := referralRequest(t, app, "POST", "/custom", raw)
		if status != 409 && status != 422 {
			t.Fatal(status)
		}
	}
	owner = user
	status, _ := referralRequest(t, app, "POST", "/custom", `{"code":"OTHERUSER"}`)
	if status != 403 {
		t.Fatal("unverified user customized", status)
	}
	owner = inviter
	if _, err := db.Exec(context.Background(), `UPDATE referral_program_settings SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	status, _ = referralRequest(t, app, "POST", "/custom", `{"code":"PAUSEDUSER"}`)
	if status != 503 {
		t.Fatal(status)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM referral_code_names WHERE kind='custom'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
