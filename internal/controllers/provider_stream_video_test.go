package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"across/backend/internal/config"
	"across/backend/internal/streamvideo"

	"github.com/gofiber/fiber/v2"
)

// fakeStream imitates the Cloudflare Stream API surface the backend uses.
type fakeStream struct {
	mu       sync.Mutex
	videos   map[string]map[string]any
	creates  []http.Header
	deletes  []string
	stored   float64
	limit    float64
	sequence int
}

func newFakeStream() *fakeStream {
	return &fakeStream{videos: map[string]map[string]any{}, limit: 1000}
}

func (f *fakeStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer synthetic-stream-token-0123456789" {
		w.WriteHeader(403)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/accounts/0123456789abcdef0123456789abcdef")
	switch {
	case r.Method == http.MethodPost && path == "/stream":
		f.sequence++
		uid := fmt.Sprintf("%032x", f.sequence)
		f.creates = append(f.creates, r.Header.Clone())
		f.videos[uid] = map[string]any{"uid": uid, "readyToStream": false, "duration": -1, "size": 0, "status": map[string]any{"state": "pendingupload"}, "playback": map[string]any{"hls": "https://customer-x.cloudflarestream.com/" + uid + "/manifest/video.m3u8"}, "thumbnail": "https://customer-x.cloudflarestream.com/" + uid + "/thumbnails/thumbnail.jpg"}
		w.Header().Set("Location", "https://upload.videodelivery.net/tus/"+uid)
		w.Header().Set("Stream-Media-Id", uid)
		w.WriteHeader(201)
	case r.Method == http.MethodGet && path == "/stream/storage-usage":
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"totalStorageMinutes": f.stored, "totalStorageMinutesLimit": f.limit}})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/stream/"):
		video, ok := f.videos[strings.TrimPrefix(path, "/stream/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": video})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/stream/"):
		uid := strings.TrimPrefix(path, "/stream/")
		f.deletes = append(f.deletes, uid)
		delete(f.videos, uid)
		w.WriteHeader(200)
	default:
		w.WriteHeader(404)
	}
}

func (f *fakeStream) finish(uid string, duration float64, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.videos[uid]
	v["status"] = map[string]any{"state": state, "errorReasonText": map[bool]string{true: "Max duration exceeded", false: ""}[state == "error"]}
	v["readyToStream"] = state == "ready"
	v["duration"] = duration
	v["size"] = 123456789
}

func streamTestApp(t *testing.T) (*ProviderMarketplaceController, *fiber.App, *fakeStream) {
	t.Helper()
	db := durableVideoDB(t)
	fake := newFakeStream()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := streamvideo.New("0123456789abcdef0123456789abcdef", "synthetic-stream-token-0123456789")
	client.BaseURL = server.URL
	m := &ProviderMarketplaceController{db: db, cfg: config.Config{StreamVideoEnabled: true}, stream: client}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Test-User")); return c.Next() })
	app.Get("/policy", m.ProviderPostPolicy)
	app.Post("/stream-upload", m.StreamVideoUpload)
	app.Get("/status", m.ProviderVideoStatus)
	app.Get("/videos", m.ListProviderVideos)
	return m, app, fake
}

func call(t *testing.T, app *fiber.App, method, path, user, body string, headers ...string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Test-User", user)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := app.Test(request, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return response.StatusCode, result
}

func TestStreamVideoUploadLifecycle(t *testing.T) {
	m, app, fake := streamTestApp(t)
	ctx := context.Background()
	user, provider, _ := tierProvider(t, m.db, "provider-diamond")

	// Old app builds: S3 route limits. New app (header): Stream, 10 minutes.
	status, legacy := call(t, app, "GET", "/policy", user, "")
	if status != 200 || legacy["max_video_seconds"].(float64) != 180 || legacy["video_upload_mode"] != "storage" || legacy["max_video_bytes"].(float64) != maxPostVideoBytes {
		t.Fatal("legacy policy", status, legacy)
	}
	status, modern := call(t, app, "GET", "/policy", user, "", streamVideoHeader, "stream")
	if status != 200 || modern["max_video_seconds"].(float64) != 600 || modern["video_upload_mode"] != "stream" || modern["max_video_bytes"].(float64) != 1073741824 {
		t.Fatal("stream policy", status, modern)
	}

	// Reservation: Cloudflare enforces the plan duration on the upload itself.
	status, upload := call(t, app, "POST", "/stream-upload", user, `{"upload_length":200000000,"filename":"shop.mp4"}`)
	if status != 201 || !strings.HasPrefix(upload["upload_url"].(string), "https://upload.videodelivery.net/tus/") || upload["max_duration_seconds"].(float64) != 600 {
		t.Fatal("upload", status, upload)
	}
	key := upload["key"].(string)
	uid := strings.TrimPrefix(key, "stream/"+provider+"/")
	header := fake.creates[0]
	if header.Get("Upload-Length") != "200000000" || header.Get("Tus-Resumable") != "1.0.0" || !strings.Contains(header.Get("Upload-Metadata"), "maxDurationSeconds "+base64.StdEncoding.EncodeToString([]byte("600"))) || header.Get("Upload-Creator") != provider {
		t.Fatal("tus reservation headers", header)
	}

	// Still uploading: 202, nothing registered.
	if status, body := call(t, app, "GET", "/status?key="+key, user, ""); status != 202 || body["status"] != "processing" {
		t.Fatal("pending", status, body)
	}
	other, _, _ := tierProvider(t, m.db, "provider-diamond")
	if status, _ := call(t, app, "GET", "/status?key="+key, other, ""); status != 404 {
		t.Fatal("another provider read this upload", status)
	}

	// Ready: registered exactly once as a verified asset with the HLS URL.
	fake.finish(uid, 540.4, "ready")
	if _, err := m.db.Exec(ctx, `UPDATE provider_video_uploads SET stream_checked_at=NULL WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	status, done := call(t, app, "GET", "/status?key="+key, user, "")
	if status != 200 || done["status"] != "complete" || !strings.HasSuffix(done["url"].(string), uid+"/manifest/video.m3u8") {
		t.Fatal("complete", status, done)
	}
	var assets int
	var duration float64
	if err := m.db.QueryRow(ctx, `SELECT count(*),max(duration_seconds) FROM provider_post_video_assets WHERE provider_id=$1::uuid AND stream_uid=$2`, provider, uid).Scan(&assets, &duration); err != nil || assets != 1 || duration != 540.4 {
		t.Fatal("asset registration", err, assets, duration)
	}
	if status, again := call(t, app, "GET", "/status?key="+key, user, ""); status != 200 || again["url"] != done["url"] {
		t.Fatal("repeat status", status, again)
	}
	if _, list := call(t, app, "GET", "/videos", user, ""); len(list["items"].([]any)) != 1 {
		t.Fatal("list", list)
	}

	// Longer than the plan: rejected and removed from Stream storage.
	_, second := call(t, app, "POST", "/stream-upload", user, `{"upload_length":1000,"filename":"long.mp4"}`)
	secondKey := second["key"].(string)
	secondUID := strings.TrimPrefix(secondKey, "stream/"+provider+"/")
	fake.finish(secondUID, 900, "ready")
	status, failed := call(t, app, "GET", "/status?key="+secondKey, user, "")
	if status != 422 || !strings.Contains(failed["message"].(string), "longer than your plan") || len(fake.deletes) != 1 || fake.deletes[0] != secondUID {
		t.Fatal("too long", status, failed, fake.deletes)
	}

	// Oversized file and plans without video are refused before Cloudflare is called.
	creates := len(fake.creates)
	if status, _ := call(t, app, "POST", "/stream-upload", user, `{"upload_length":2000000000}`); status != 422 {
		t.Fatal("oversize", status)
	}
	bronze, _, _ := tierProvider(t, m.db, "provider-monthly")
	if status, _ := call(t, app, "POST", "/stream-upload", bronze, `{"upload_length":1000}`); status != 403 {
		t.Fatal("bronze video", status)
	}
	// Storage nearly full: refuse rather than let Cloudflare reject mid-upload.
	fake.mu.Lock()
	fake.stored = 990
	fake.mu.Unlock()
	m.stream.Forget()
	if status, body := call(t, app, "POST", "/stream-upload", user, `{"upload_length":1000}`); status != 503 || !strings.Contains(body["message"].(string), "storage") {
		t.Fatal("storage guard", status, body)
	}
	if len(fake.creates) != creates {
		t.Fatal("refused uploads reached Cloudflare")
	}
}

func TestStreamVideoCleanupReleasesOnlyUnusedVideos(t *testing.T) {
	m, app, fake := streamTestApp(t)
	ctx := context.Background()
	user, provider, _ := tierProvider(t, m.db, "provider-diamond")
	if _, err := m.db.Exec(ctx, `UPDATE provider_video_launch_policy SET pending_upload_limit=5`); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for i := 0; i < 3; i++ {
		_, upload := call(t, app, "POST", "/stream-upload", user, `{"upload_length":1000}`)
		keys = append(keys, upload["key"].(string))
	}
	uid := func(key string) string { return strings.TrimPrefix(key, "stream/"+provider+"/") }
	for _, key := range keys[:2] {
		fake.finish(uid(key), 30, "ready")
		call(t, app, "GET", "/status?key="+key, user, "")
	}
	var attachedURL string
	if err := m.db.QueryRow(ctx, `SELECT url FROM provider_video_uploads WHERE key=$1`, keys[0]).Scan(&attachedURL); err != nil {
		t.Fatal(err)
	}
	// keys[0] attached to an (archived) listing; keys[1] never attached; keys[2] never uploaded.
	if _, err := m.db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id,title,status,attributes) VALUES(gen_random_uuid(),$1::uuid,'Archived service','archived',jsonb_build_object('video_urls',jsonb_build_array($2::text)))`, provider, attachedURL); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(ctx, `UPDATE provider_video_uploads SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()-interval '2 hours' WHERE processing_mode='stream'`); err != nil {
		t.Fatal(err)
	}
	deleted, err := RunStreamVideoCleanup(ctx, m.db, m.cfg, m.stream)
	if err != nil || deleted != 2 {
		t.Fatal("cleanup", err, deleted, fake.deletes)
	}
	for _, d := range fake.deletes {
		if d == uid(keys[0]) {
			t.Fatal("deleted a video used by a listing")
		}
	}
	var assets int
	_ = m.db.QueryRow(ctx, `SELECT count(*) FROM provider_post_video_assets WHERE provider_id=$1::uuid`, provider).Scan(&assets)
	if assets != 1 {
		t.Fatal("asset rows after cleanup", assets)
	}
	// Bring rows back inside the 7-day list window: only the kept video is offered.
	if _, err := m.db.Exec(ctx, `UPDATE provider_video_uploads SET created_at=clock_timestamp() WHERE processing_mode='stream'`); err != nil {
		t.Fatal(err)
	}
	if _, list := call(t, app, "GET", "/videos", user, ""); len(list["items"].([]any)) != 1 || list["items"].([]any)[0].(map[string]any)["url"] != attachedURL {
		t.Fatal("deleted videos still offered", list)
	}
	if again, _ := RunStreamVideoCleanup(ctx, m.db, m.cfg, m.stream); again != 0 {
		t.Fatal("cleanup not idempotent", again)
	}
}
