package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"bytes"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func durableVideoDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := tierEntitlementDB(t, nil)
	for _, name := range []string{"111_launch_video_allowances.sql", "112_durable_video_processing.sql"} {
		raw, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(context.Background(), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func TestDurableVideoClaimsGlobalBoundRecoveryAndExhaustion(t *testing.T) {
	db := durableVideoDB(t)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "provider-diamond")
	m := &ProviderMarketplaceController{db: db}
	for _, key := range []string{"one", "two"} {
		if err := m.reserveProviderVideo(ctx, provider, key, 100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE provider_video_uploads SET state='queued',processing_mode='background'`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan videoMediaJob, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			job, ok, err := claimVideoMediaJob(ctx, db)
			if err != nil {
				t.Error(err)
			}
			if ok {
				results <- job
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	count := 0
	var first videoMediaJob
	for job := range results {
		first = job
		count++
	}
	if count != 1 {
		t.Fatalf("global encoders admitted %d", count)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_video_launch_policy SET worker_lease_until=now()-interval '1 second';UPDATE provider_video_uploads SET lease_until=now()-interval '1 second',attempts=3 WHERE state='processing';UPDATE provider_video_uploads SET state='failed' WHERE state='queued'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := claimVideoMediaJob(ctx, db); err != nil || ok {
		t.Fatal("exhausted claim resumed", err, ok)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT state FROM provider_video_uploads WHERE key=$1`, first.key).Scan(&state); err != nil || state != "failed" {
		t.Fatal("exhausted job not terminal", err, state)
	}
	var lease bool
	if err := db.QueryRow(ctx, `SELECT worker_token IS NOT NULL FROM provider_video_launch_policy`).Scan(&lease); err != nil || lease {
		t.Fatal("empty cycle leaked global lease", err)
	}
}
func TestDurableVideoEnqueueBoundAndOwnerStatus(t *testing.T) {
	db := durableVideoDB(t)
	ctx := context.Background()
	user, provider, _ := tierProvider(t, db, "provider-diamond")
	otherUser, _, _ := tierProvider(t, db, "provider-diamond")
	cfg := config.Config{AsyncVideoProcessing: true}
	m := &ProviderMarketplaceController{db: db, cfg: cfg}
	key := "private/video-pending/" + provider + "/fixture.mp4"
	if err := m.reserveProviderVideo(ctx, provider, key, 100); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Test-User")); return c.Next() })
	app.Post("/complete", m.CompletePostVideo)
	app.Get("/status", m.ProviderVideoStatus)
	request := httptest.NewRequest("POST", "/complete", bytes.NewBufferString(`{"key":"`+key+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Test-User", user)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != 202 {
		t.Fatal("enqueue failed", err, response.StatusCode)
	}
	response.Body.Close()
	request = httptest.NewRequest("GET", "/status?key="+key, nil)
	request.Header.Set("Test-User", otherUser)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != 404 {
		t.Fatal("other owner read processing state", err)
	}
	response.Body.Close()
	if _, err = db.Exec(ctx, `UPDATE provider_video_launch_policy SET queue_limit=1`); err != nil {
		t.Fatal(err)
	}
	second := key + "2"
	if err = m.reserveProviderVideo(ctx, provider, second, 100); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("POST", "/complete", bytes.NewBufferString(`{"key":"`+second+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Test-User", user)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != 503 {
		t.Fatal("queue bound not enforced", err, response.StatusCode)
	}
	response.Body.Close()
}
func TestDurableVideoActualWorkerTranscodesAndPublishesOnce(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("CI requires actual FFmpeg worker test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mp4")
	if output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=1920x1080:rate=12", "-t", "1", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-an", input).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	video, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	db := durableVideoDB(t)
	_, provider, _ := tierProvider(t, db, "provider-diamond")
	cfg := config.Config{AsyncVideoProcessing: true, PublicBaseURL: "https://api.example.test", AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic", S3BucketName: "synthetic"}
	m := &ProviderMarketplaceController{db: db, cfg: cfg, s3: storage.NewS3(cfg)}
	key := "private/video-pending/" + provider + "/fixture.mp4"
	if err = m.reserveProviderVideo(ctx, provider, key, int64(len(video))); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE provider_video_uploads SET state='queued',processing_mode='background' WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var processed, poster []byte
	client := &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.Method {
		case "GET":
			return &http.Response{StatusCode: 200, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video)), Header: make(http.Header)}, nil
		case "PUT":
			data, e := io.ReadAll(r.Body)
			if e != nil {
				t.Fatal(e)
			}
			if r.Header.Get("Content-Type") == "video/mp4" {
				processed = data
			} else {
				poster = data
			}
		case "DELETE":
		default:
			t.Fatal("unexpected S3 method")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}
	if err = RunVideoMediaJob(ctx, db, cfg, client); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output.mp4")
	if err = os.WriteFile(output, processed, 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := inspectProcessingVideo(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range probe.Streams {
		if stream.Type == "video" && (stream.Width > 1280 || stream.Height > 720 || stream.Codec != "h264" || rationalFPS(stream.FPS) > 30.01) {
			t.Fatal("unbounded processed output", stream)
		}
	}
	if bytes.Index(processed, []byte("moov")) > bytes.Index(processed, []byte("mdat")) {
		t.Fatal("MP4 is not fast-start")
	}
	metadata, _, err := image.DecodeConfig(bytes.NewReader(poster))
	if err != nil || metadata.Width > 400 || metadata.Height > 400 {
		t.Fatal("invalid thumbnail", err, metadata)
	}
	var state string
	var stored, attempts int
	if err = db.QueryRow(ctx, `SELECT state,attempts,(SELECT count(*) FROM provider_post_video_assets WHERE provider_id=$2::uuid) FROM provider_video_uploads WHERE key=$1`, key, provider).Scan(&state, &attempts, &stored); err != nil || state != "complete" || attempts != 1 || stored != 1 {
		t.Fatal("publication not atomic", err, state, attempts, stored)
	}
	if err = RunVideoMediaJob(ctx, db, cfg, client); err != nil || calls != 4 {
		t.Fatal("completed job did object work twice", err, calls)
	}
}

func TestDurableVideoFailureRetriesAreBounded(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("CI requires real invalid-file verification")
	}
	ctx := context.Background()
	db := durableVideoDB(t)
	_, provider, _ := tierProvider(t, db, "provider-diamond")
	cfg := config.Config{AsyncVideoProcessing: true, PublicBaseURL: "https://api.example.test", AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic", S3BucketName: "synthetic"}
	m := &ProviderMarketplaceController{db: db}
	key := "private/video-pending/" + provider + "/bad.mp4"
	if err := m.reserveProviderVideo(ctx, provider, key, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_video_uploads SET state='queued',processing_mode='background' WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("invalid source published or prematurely deleted")
		}
		return &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(bytes.NewBufferString("junk")), Header: make(http.Header)}, nil
	})}
	for i := 0; i < 3; i++ {
		if err := RunVideoMediaJob(ctx, db, cfg, client); err == nil {
			t.Fatal("invalid video succeeded")
		}
		if _, err := db.Exec(ctx, `UPDATE provider_video_uploads SET available_at=now()-interval '1 second' WHERE key=$1`, key); err != nil {
			t.Fatal(err)
		}
	}
	var state, phase string
	var attempts, assets int
	if err := db.QueryRow(ctx, `SELECT state,attempts,error_code,(SELECT count(*) FROM provider_post_video_assets) FROM provider_video_uploads WHERE key=$1`, key).Scan(&state, &attempts, &phase, &assets); err != nil || state != "failed" || attempts != 3 || phase != "verification_and_encoding" || assets != 0 {
		t.Fatal("retry/rollback failure", err, state, attempts, phase, assets)
	}
	if err := RunVideoMediaJob(ctx, db, cfg, client); err != nil {
		t.Fatal("terminal failure retried", err)
	}
}
