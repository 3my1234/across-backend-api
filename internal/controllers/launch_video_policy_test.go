package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLaunchVideoPolicyPreservesPublishedAndRejectsNewDiamond(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-business")
	id := uuid.NewString()
	url := "https://fixture.test/existing.mp4"
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO provider_post_video_assets VALUES($1,$2,60,100,now())`, url, provider)
	must(`INSERT INTO products(id,provider_id,moderation_status,factory_details) VALUES($1,$2,'approved',jsonb_build_object('video_urls',jsonb_build_array($3::text)))`, id, provider, url)
	migration, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	must(string(migration))
	must(`UPDATE products SET moderation_status='approved' WHERE id=$1`, id)
	must(`UPDATE products SET image_urls=ARRAY['https://fixture.test/new.jpg'] WHERE id=$1`, id)
	var retained string
	if err = db.QueryRow(ctx, `SELECT factory_details->'video_urls'->>0 FROM products WHERE id=$1`, id).Scan(&retained); err != nil || retained != url {
		t.Fatal("existing media changed", err, retained)
	}
	m := &ProviderMarketplaceController{db: db}
	if err = m.checkPostUpload(ctx, provider, "", "", "post_video"); err == nil {
		t.Fatal("new Diamond upload allowed")
	}
	newURL := "https://fixture.test/new.mp4"
	must(`INSERT INTO provider_post_video_assets VALUES($1,$2,30,100,now())`, newURL, provider)
	if _, err = db.Exec(ctx, `UPDATE products SET factory_details=jsonb_build_object('video_urls',jsonb_build_array($2::text)) WHERE id=$1`, id, newURL); err == nil {
		t.Fatal("new Diamond video attached")
	}
	must(`UPDATE products SET factory_details='{"video_urls":[]}' WHERE id=$1`, id)
}

func TestLaunchVideoPolicyAtomicMixedProviderQuota(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-diamond")
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	must(string(migration))
	for i := 0; i < 6; i++ {
		must(`INSERT INTO provider_post_video_assets VALUES($1,$2,30,100,now())`, fmt.Sprintf("https://fixture.test/%d.mp4", i), provider)
	}
	for i := 0; i < 4; i++ {
		must(`INSERT INTO products(id,provider_id,moderation_status,factory_details) VALUES($1,$2,'draft',jsonb_build_object('video_urls',jsonb_build_array($3::text)))`, uuid.NewString(), provider, fmt.Sprintf("https://fixture.test/%d.mp4", i))
	}
	start := make(chan struct{})
	result := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 4; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, e := db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,status,attributes) VALUES($1,$2,'draft',jsonb_build_object('video_urls',jsonb_build_array($3::text)))`, uuid.NewString(), provider, fmt.Sprintf("https://fixture.test/%d.mp4", i))
			result <- e
		}(i)
	}
	close(start)
	wg.Wait()
	close(result)
	success := 0
	for err := range result {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("provider quota race: %d writes accepted; want 1", success)
	}
	// 31-second and foreign assets are rejected even if the per-listing count fits.
	long := "https://fixture.test/long.mp4"
	must(`INSERT INTO provider_post_video_assets VALUES($1,$2,31,100,now())`, long, provider)
	if _, err = db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,factory_details) VALUES($1,$2,'draft',jsonb_build_object('video_urls',jsonb_build_array($3::text)))`, uuid.NewString(), provider, long); err == nil {
		t.Fatal("31-second asset attached")
	}
	must(`UPDATE provider_video_launch_policy SET uploads_paused=true`)
	if err = (&ProviderMarketplaceController{db: db}).checkPostUpload(ctx, provider, "", "", "post_video"); err == nil {
		t.Fatal("pause ignored")
	}
}

func TestLaunchVideoPolicyMatrixAndConfigurableDiamond(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	raw, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{"", "bronze", "silver", "diamond", "gold"} {
		var raw []byte
		if err = db.QueryRow(ctx, `SELECT provider_tier_benefits($1)`, tier).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var policy providerPostPolicy
		if err = json.Unmarshal(raw, &policy); err != nil {
			t.Fatal(err)
		}
		expected := 0
		if tier == "gold" {
			expected = 1
		}
		if policy.Videos != expected || policy.VideoSeconds != 30 || policy.ProviderVideos != 5 || policy.VideoBytes != 52428800 {
			t.Fatalf("wrong %s policy: %+v", tier, policy)
		}
		legacy := postPolicy(tier)
		if legacy.Posts != policy.Posts || legacy.Images != policy.Images || legacy.Rooms != policy.Rooms {
			t.Fatal("unrelated entitlements changed")
		}
	}
	if _, err = db.Exec(ctx, `UPDATE provider_video_launch_policy SET diamond_enabled=true`); err != nil {
		t.Fatal(err)
	}
	var videos int
	if err = db.QueryRow(ctx, `SELECT (provider_tier_benefits('diamond')->>'max_videos')::int`).Scan(&videos); err != nil || videos != 1 {
		t.Fatal("future policy config ignored", err)
	}
}

func TestLaunchVideoReservationLimitsRecoveryAndIdempotentCompletion(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	raw, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	_, provider, sub := tierProvider(t, db, "provider-diamond")
	_, other, _ := tierProvider(t, db, "provider-diamond")
	m := &ProviderMarketplaceController{db: db}
	key := "private/providers/" + provider + "/video-pending/test.mp4"
	if err = m.reserveProviderVideo(ctx, provider, key, 100); err != nil {
		t.Fatal(err)
	}
	if err = m.reserveProviderVideo(ctx, provider, key+"-oversize", 52428801); err == nil {
		t.Fatal("oversized upload reserved")
	}
	if _, err = m.claimProviderVideo(ctx, other, key); err == nil {
		t.Fatal("other provider claimed ticket")
	}
	ticket, err := m.claimProviderVideo(ctx, provider, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.claimProviderVideo(ctx, provider, key); err == nil {
		t.Fatal("concurrent finalizer claimed same ticket")
	}
	url := providerVerifiedVideoKey(provider, ticket)
	// A failure must leave both ticket and asset registration uncommitted.
	if err = m.finishProviderVideo(ctx, provider, key, url, 31, 100); err == nil {
		t.Fatal("overlong video completed")
	}
	if err = m.finishProviderVideo(ctx, provider, key, url, 30, 99); err == nil {
		t.Fatal("different size completed")
	}
	if err = m.finishProviderVideo(ctx, provider, key, url, 30, 100); err != nil {
		t.Fatal(err)
	}
	replay, err := m.claimProviderVideo(ctx, provider, key)
	if err != nil || !replay.complete || replay.url != url {
		t.Fatal("completion retry not idempotent", err)
	}
	// Already completed media remains recoverable after the plan expires.
	if _, err = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	replay, err = m.claimProviderVideo(ctx, provider, key)
	if err != nil || !replay.complete {
		t.Fatal("prior completion unavailable after expiry", err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM provider_post_video_assets WHERE url=$1`, url).Scan(&count); err != nil || count != 1 {
		t.Fatal("asset duplicated", err, count)
	}
	// Reserve concurrently with a single pending slot. Only one may obtain it.
	if _, err = db.Exec(ctx, `UPDATE provider_video_launch_policy SET pending_upload_limit=1,daily_upload_limit=2`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) { <-start; result <- m.reserveProviderVideo(ctx, other, fmt.Sprintf("pending-%d", i), 100) }(i)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-result == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("pending slot race admitted %d", success)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_video_uploads SET expires_at=now()-interval '1 second' WHERE provider_id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if err = m.reserveProviderVideo(ctx, other, "replacement", 100); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_video_uploads SET expires_at=now()-interval '1 second' WHERE provider_id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if err = m.reserveProviderVideo(ctx, other, "daily-excess", 100); err == nil {
		t.Fatal("daily limit bypassed via expired tickets")
	}
}

// Older payment fixtures intentionally contain only billing tables. Add the
// current policy function without inventing any provider/payment success state.
func installLaunchVideoBenefitsTest(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.SplitN(string(raw), "CREATE FUNCTION enforce_provider_video_launch_policy", 2)[0]
	if _, err = db.Exec(context.Background(), prefix); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchVideoDeliveryRedirectNeverProxiesBytes(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-diamond")
	cfg := config.Config{PublicBaseURL: "https://api.example.test", AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic", S3BucketName: "synthetic"}
	m := &ProviderMarketplaceController{db: db, s3: storage.NewS3(cfg)}
	key := providerVerifiedVideoKey(provider, providerVideoTicket{assetID: uuid.NewString()})
	public, _ := m.s3.VideoObjectURL(key)
	if _, err := db.Exec(ctx, `INSERT INTO provider_post_video_assets VALUES($1,$2,30,100,now())`, public, provider); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/video/*", m.PublicProviderVideo)
	for _, path := range []string{key, "private/video-pending/" + provider + "/x.mp4", providerVerifiedVideoKey(provider, providerVideoTicket{assetID: uuid.NewString()})} {
		resp, err := app.Test(httptest.NewRequest("GET", "/video/"+path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if path == key {
			u, e := url.Parse(resp.Header.Get("Location"))
			if e != nil || resp.StatusCode != 302 || u.Host != "synthetic.s3.eu-north-1.amazonaws.com" || u.Query().Get("X-Amz-Expires") != "300" || resp.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal("incorrect redirect", resp.StatusCode, resp.Header)
			}
		} else if resp.StatusCode != 404 {
			t.Fatal("unregistered/private object exposed", resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestLaunchVideoActualVerificationAndCompletionRetry(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("requires actual FFmpeg and ffprobe; CI requires this test to pass")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	file := filepath.Join(t.TempDir(), "fixture.mp4")
	if output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=16x16:rate=1", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", file).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	video, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	db := tierEntitlementDB(t, nil)
	raw, err := os.ReadFile("../../migrations/111_launch_video_allowances.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	user, provider, sub := tierProvider(t, db, "provider-diamond")
	cfg := config.Config{PublicBaseURL: "https://api.example.test", AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic", S3BucketName: "synthetic"}
	calls := 0
	client := &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == "GET" {
			return &http.Response{StatusCode: 200, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video)), Header: make(http.Header)}, nil
		}
		if r.Method == "DELETE" {
			return &http.Response{StatusCode: 204, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		if r.Method != "PUT" || r.ContentLength != int64(len(video)) {
			t.Fatal("unexpected object write", r.Method, r.ContentLength)
		}
		uploaded, e := io.ReadAll(r.Body)
		if e != nil || !bytes.Equal(uploaded, video) {
			t.Fatal("verified bytes changed", e)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}
	m := &ProviderMarketplaceController{db: db, s3: storage.NewS3(cfg), videoClient: client}
	key := "private/video-pending/" + provider + "/fixture.mp4"
	if err = m.reserveProviderVideo(ctx, provider, key, int64(len(video))); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", user); return c.Next() })
	app.Post("/complete", m.CompletePostVideo)
	var first string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/complete", bytes.NewBufferString(`{"key":"`+key+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, e := app.Test(req, 10000)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("completion failed: %d %s", resp.StatusCode, body)
		}
		var result struct {
			URL string `json:"url"`
		}
		if e = json.Unmarshal(body, &result); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			first = result.URL
			if _, e = db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub); e != nil {
				t.Fatal(e)
			}
		} else if result.URL != first {
			t.Fatal("retry changed URL")
		}
	}
	// GET, verified PUT, best-effort pending DELETE; completion retry does none.
	if calls != 3 {
		t.Fatalf("completion performed duplicate object work: %d", calls)
	}
}
