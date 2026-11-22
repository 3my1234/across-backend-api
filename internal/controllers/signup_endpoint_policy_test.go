package controllers

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func signupEndpointFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db, _, _ := referralFixture(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `
 ALTER TABLE users ADD COLUMN verification_token text, ADD COLUMN verification_token_expires_at timestamptz,
 ADD COLUMN verification_sent_at timestamptz, ADD COLUMN verification_resend_count int DEFAULT 0,
 ADD COLUMN registration_context text;
 CREATE TABLE countries_config(id uuid PRIMARY KEY DEFAULT gen_random_uuid(), country_code text, is_active boolean, active_payment_gateways text[]);
 INSERT INTO countries_config(country_code,is_active,active_payment_gateways) VALUES('NG',true,ARRAY['flutterwave']);
 CREATE TABLE signup_approved_emails(email text PRIMARY KEY); CREATE TABLE signup_policy(singleton boolean PRIMARY KEY, mode text);
 INSERT INTO signup_approved_emails VALUES('override@example.invalid');
 INSERT INTO signup_policy VALUES(true,'approved_only');
 CREATE TABLE launch_waitlist(email text PRIMARY KEY, approved boolean DEFAULT false);
 INSERT INTO launch_waitlist VALUES('approved@example.invalid',true);
 CREATE TABLE email_suppressions(email text PRIMARY KEY);
 CREATE TABLE email_outbox(user_id uuid, dedupe_key text UNIQUE, recipient_email text, recipient_name text, template_type text, payload jsonb, status text);
 `)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPasswordSignupPolicyEndpoint(t *testing.T) {
	db := signupEndpointFixture(t)
	ctx := context.Background()
	app := fiber.New()
	app.Post("/signup", NewAuthController(db, config.Config{}).Signup)
	for _, tc := range []struct {
		mode, email              string
		status, accounts, emails int
	}{
		{"approved_only", "blocked@example.invalid", 403, 0, 0},
		{"approved_only", "approved@example.invalid", 202, 1, 1},
		{"approved_only", "override@example.invalid", 202, 1, 1},
		{"closed", "closed@example.invalid", 403, 0, 0},
		{"open", "open@example.invalid", 202, 1, 1},
	} {
		if _, err := db.Exec(ctx, `UPDATE signup_policy SET mode=$1`, tc.mode); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"full_name":"Test User","email":%q,"password":"ValidPass!23"}`, tc.email)
		req := httptest.NewRequest("POST", "/signup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatalf("%s %s: status %d", tc.mode, tc.email, res.StatusCode)
		}
		var accounts, emails, waitlist int
		if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE email=$1),(SELECT count(*) FROM email_outbox WHERE recipient_email=$1),(SELECT count(*) FROM launch_waitlist WHERE email=$1)`, tc.email).Scan(&accounts, &emails, &waitlist); err != nil {
			t.Fatal(err)
		}
		expectedWaitlist := 0
		if tc.email == "approved@example.invalid" {
			expectedWaitlist = 1
		}
		if accounts != tc.accounts || emails != tc.emails || waitlist != expectedWaitlist {
			t.Fatalf("%s: accounts=%d emails=%d waitlist=%d", tc.email, accounts, emails, waitlist)
		}
	}
}

func TestDisconnectedSignupMayStillCreateAccount(t *testing.T) {
	db := signupEndpointFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE signup_policy SET mode='open'`); err != nil {
		t.Fatal(err)
	}
	gate := middleware.NewBoundedPasswordAdmission(1, 1, 3*time.Second)
	app := fiber.New(fiber.Config{BodyLimit: 4096, DisableStartupMessage: true})
	holding, release, arrived, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	app.Post("/hold", gate.Handler, func(c *fiber.Ctx) error { close(holding); <-release; return c.SendStatus(200) })
	controller := NewAuthController(db, config.Config{})
	app.Post("/signup", func(c *fiber.Ctx) error { _ = c.Body(); close(arrived); return c.Next() }, gate.Handler, func(c *fiber.Ctx) error {
		defer close(finished)
		if c.Context().Err() != nil || c.UserContext().Err() != nil {
			return c.SendStatus(499)
		}
		return controller.Signup(c)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(listener) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	go func() {
		response, e := app.Test(httptest.NewRequest("POST", "/hold", nil), -1)
		if e == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-holding:
	case <-time.After(3 * time.Second):
		t.Fatal("hold not admitted")
	}
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	body := `{"full_name":"Disconnected User","email":"disconnected@example.invalid","password":"ValidPass!23"}`
	_, err = fmt.Fprintf(conn, "POST /signup HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	if err != nil {
		conn.Close()
		close(release)
		t.Fatal(err)
	}
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		conn.Close()
		close(release)
		t.Fatal("signup did not arrive")
	}
	time.Sleep(100 * time.Millisecond)
	conn.Close()
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("signup did not finish")
	}
	var accounts, emails int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM users WHERE email='disconnected@example.invalid'),(SELECT count(*) FROM email_outbox WHERE recipient_email='disconnected@example.invalid')`).Scan(&accounts, &emails); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || emails != 1 {
		t.Fatalf("observed accounts=%d emails=%d", accounts, emails)
	}
}

func TestExistingPasswordLoginAcrossSignupModes(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("ValidPass!23"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `CREATE TABLE signup_approved_emails(email text PRIMARY KEY); CREATE TABLE signup_policy(singleton boolean PRIMARY KEY,mode text);
 INSERT INTO signup_policy VALUES(true,'open');
 CREATE TABLE users(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),email text,password_hash text,email_verified boolean,is_active boolean,session_version int DEFAULT 0);`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO users(email,password_hash,email_verified,is_active) VALUES('existing@example.invalid',$1,true,true)`, string(hash))
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/login", NewAuthController(db, config.Config{JWTSecret: "test-only-long-secret-key-for-local-regression"}).Login)
	for _, mode := range []string{"open", "approved_only", "closed"} {
		if _, err := db.Exec(ctx, `UPDATE signup_policy SET mode=$1`, mode); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/login", strings.NewReader(`{"email":"existing@example.invalid","password":"ValidPass!23"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("mode %s: login status %d", mode, res.StatusCode)
		}
	}
}

func TestPasswordResetAndAdminLoginAcrossSignupModes(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("ValidPass!23"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `CREATE TABLE signup_approved_emails(email text PRIMARY KEY); CREATE TABLE signup_policy(singleton boolean PRIMARY KEY,mode text);
 INSERT INTO signup_policy VALUES(true,'open');
 CREATE TABLE users(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),password_hash text,password_reset_token_hash text,password_reset_expires_at timestamptz,password_reset_requested_at timestamptz,session_version int DEFAULT 0,updated_at timestamptz DEFAULT now());
 CREATE TABLE admins(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),email text,full_name text,password_hash text,role text,is_active boolean);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO admins(email,full_name,password_hash,role,is_active) VALUES('admin@example.invalid','Admin',$1,'super_admin',true)`, string(hash)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users(password_hash) VALUES($1)`, string(hash)); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-only-long-secret-key-for-local-regression"}
	app := fiber.New()
	app.Post("/reset", NewAuthController(db, cfg).ResetPassword)
	app.Post("/admin", NewAdminController(db, cfg).Login)
	for _, mode := range []string{"open", "approved_only", "closed"} {
		if _, err := db.Exec(ctx, `UPDATE signup_policy SET mode=$1`, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET password_reset_token_hash=$1,password_reset_expires_at=now()+interval '30 minutes'`, resetTokenDigest("local-test-token")); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ path, body string }{
			{"/reset", `{"token":"local-test-token","password":"ValidPass!23","confirm_password":"ValidPass!23"}`},
			{"/admin", `{"email":"admin@example.invalid","password":"ValidPass!23"}`},
		} {
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req, -1)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("%s %s: status %d", mode, tc.path, res.StatusCode)
			}
		}
	}
}
