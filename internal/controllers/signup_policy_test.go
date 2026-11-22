package controllers

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSignupMigrationDefaultsOpenAndPreservesWaitlist(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE launch_waitlist(email text PRIMARY KEY); INSERT INTO launch_waitlist VALUES('existing@example.invalid')`); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../migrations/126_signup_mode.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var mode string
	var approved bool
	if err := db.QueryRow(ctx, `SELECT (SELECT mode FROM signup_policy WHERE singleton),approved FROM launch_waitlist WHERE email='existing@example.invalid'`).Scan(&mode, &approved); err != nil {
		t.Fatal(err)
	}
	if mode != "open" || approved {
		t.Fatalf("mode=%s approved=%v", mode, approved)
	}
}

func TestSignupGateDoesNotStoreBlockedSignups(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE signup_approved_emails(email text PRIMARY KEY); CREATE TABLE signup_policy(singleton boolean primary key,mode text);INSERT INTO signup_policy VALUES(true,'approved_only');CREATE TABLE launch_waitlist(email text PRIMARY KEY,full_name text,source text,consent_version text,approved boolean DEFAULT false)`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, e := db.Begin(ctx)
			if e != nil {
				errors <- e
				return
			}
			defer tx.Rollback(ctx)
			allowed, e := signupAllowedTx(ctx, tx, "person@example.invalid", "Person")
			if e == nil && allowed {
				e = fmt.Errorf("unapproved accepted")
			}
			if e == nil {
				e = tx.Commit(ctx)
			}
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM launch_waitlist`).Scan(&count); err != nil || count != 0 {
		t.Fatal("blocked signups must not create waitlist entries", count, err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO launch_waitlist(email,approved) VALUES('person@example.invalid',true)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := signupAllowedTx(ctx, tx, "person@example.invalid", "Person")
	tx.Rollback(ctx)
	if err != nil || !allowed {
		t.Fatal("approved rejected", err)
	}
	if _, err = db.Exec(ctx, `UPDATE signup_policy SET mode='closed'`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err = signupAllowedTx(ctx, tx, "person@example.invalid", "Person")
	tx.Rollback(ctx)
	if !stderrors.Is(err, errSignupClosed) || allowed {
		t.Fatal("closed accepted", err)
	}
}

func TestSignupPolicyModeChangeWaitsForInFlightCreation(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE signup_approved_emails(email text PRIMARY KEY); CREATE TABLE signup_policy(singleton boolean primary key,mode text); INSERT INTO signup_policy VALUES(true,'open');CREATE TABLE launch_waitlist(email text PRIMARY KEY,full_name text,source text,consent_version text,approved boolean DEFAULT false)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	allowed, err := signupAllowedTx(ctx, tx, "test@example.invalid", "Test")
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	done := make(chan error, 1)
	go func() { _, e := db.Exec(ctx, `UPDATE signup_policy SET mode='closed'`); done <- e }()
	select {
	case err := <-done:
		t.Fatalf("mode changed before account transaction ended: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mode update did not complete")
	}
}
