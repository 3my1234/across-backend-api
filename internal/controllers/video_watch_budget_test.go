package controllers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"

	"github.com/gofiber/fiber/v2"
)

func TestTierVideoAllowancesAndWatchBudget(t *testing.T) {
	db := durableVideoDB(t)
	ctx := context.Background()
	raw, err := os.ReadFile("../../migrations/129_tier_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	// Per-tier allowances (fixture: provider-diamond plan code = gold, provider-business = diamond).
	for _, tc := range []struct {
		tier                                    string
		perListing, perProvider, seconds, watch int
	}{{"gold", 3, 20, 180, 2000}, {"diamond", 2, 10, 120, 1000}, {"silver", 0, 5, 180, 0}} {
		var v, p, s, w int
		if err = db.QueryRow(ctx, `SELECT (b->>'max_videos')::int,(b->>'max_provider_videos')::int,(b->>'max_video_seconds')::int,(b->>'video_watch_minutes')::int FROM provider_tier_benefits($1) b`, tc.tier).Scan(&v, &p, &s, &w); err != nil {
			t.Fatal(err)
		}
		if tc.tier == "diamond" {
			var enabled bool
			_ = db.QueryRow(ctx, `SELECT diamond_enabled FROM provider_video_launch_policy`).Scan(&enabled)
			if !enabled {
				tc.perListing = 0
			}
		}
		if v != tc.perListing || p != tc.perProvider || s != tc.seconds || w != tc.watch {
			t.Fatalf("%s benefits: videos %d/%d seconds %d watch %d", tc.tier, v, p, s, w)
		}
	}

	_, provider, _ := tierProvider(t, db, "provider-diamond")
	d := NewDiscoveryController(db, config.Config{JWTSecret: "isolated-watch-budget-test-key"})
	app := fiber.New()
	app.Post("/events", d.Events)
	items := []discoveryItem{{Provider: provider, Videos: []string{"https://customer-x.cloudflarestream.com/a/manifest/video.m3u8"}}}
	if d.videoBudgetExceeded(ctx, items)[provider] {
		t.Fatal("no watch time yet must keep autoplay")
	}
	// Reports are cumulative per session/card: the largest report wins, duplicates do not add up.
	session := strings.Repeat("ab", 32)
	receipt := d.signReceipt(discoveryReceipt{"product", "00000000-0000-0000-0000-000000000001", provider, session, "promoted", time.Now().Unix()})
	for _, ms := range []int{40000, 90000, 60000} {
		communityCall(t, app, "POST", "/events", "", `{"events":[{"receipt":"`+receipt+`","type":"video_watch","watched_ms":`+itoa(ms)+`}]}`, 204)
	}
	var stored int64
	if err = db.QueryRow(ctx, `SELECT sum(watched_ms) FROM discovery_video_watch WHERE provider_id=$1::uuid`, provider).Scan(&stored); err != nil || stored != 90000 {
		t.Fatal("cumulative watch report", err, stored)
	}
	// Reaching the plan allowance switches this provider to tap-to-play.
	if _, err = db.Exec(ctx, `UPDATE provider_video_launch_policy SET gold_watch_minutes=1`); err != nil {
		t.Fatal(err)
	}
	if !d.videoBudgetExceeded(ctx, items)[provider] {
		t.Fatal("provider over monthly allowance must stop autoplay")
	}
	// Last month's watching does not count against this month.
	if _, err = db.Exec(ctx, `UPDATE discovery_video_watch SET day=date_trunc('month',now())::date-1`); err != nil {
		t.Fatal(err)
	}
	if d.videoBudgetExceeded(ctx, items)[provider] {
		t.Fatal("budget resets monthly")
	}
	// Tiny or absurd reports are ignored or capped.
	communityCall(t, app, "POST", "/events", "", `{"events":[{"receipt":"`+receipt+`","type":"video_watch","watched_ms":500}]}`, 204)
	communityCall(t, app, "POST", "/events", "", `{"events":[{"receipt":"`+receipt+`","type":"video_watch","watched_ms":99999999}]}`, 204)
	var capped int64
	_ = db.QueryRow(ctx, `SELECT max(watched_ms) FROM discovery_video_watch WHERE day=date_trunc('month',now())::date OR day=CURRENT_DATE`).Scan(&capped)
	if capped > 30*60*1000 {
		t.Fatal("watch report not capped", capped)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
