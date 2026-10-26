package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type brandingHeadTransport func(*http.Request) (*http.Response, error)

func (f brandingHeadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderBrandKeyOwnership(t *testing.T) {
	id := uuid.NewString()
	for _, sample := range []struct {
		key, purpose string
		allowed      bool
	}{
		{"user-uploads/providers/" + id + "/logo/photo.jpg", "logo", true},
		{"user-uploads/providers/" + id + "/banner/banner.webp", "banner", true},
		{"user-uploads/providers/another/logo/photo.jpg", "logo", false},
		{"user-uploads/providers/" + id + "/verification/id.jpg", "logo", false},
		{"user-uploads/providers/" + id + "/logo/../verification/id.jpg", "logo", false},
		{"user-uploads/providers/" + id + "/banner/photo.jpg", "logo", false},
		{"user-uploads/providers/" + id + "/logo/", "logo", false},
	} {
		if got := providerBrandKeyAllowed(sample.key, id, sample.purpose); got != sample.allowed {
			t.Fatalf("%s: got %v", sample.key, got)
		}
	}
}

func TestProviderMediaRemovalPreservesOtherFieldsAndRequiresManager(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE users(id uuid PRIMARY KEY,avatar_url text,updated_at timestamptz DEFAULT now()); CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,is_active boolean DEFAULT true,logo_url text,banner_url text,business_name text,updated_at timestamptz DEFAULT now());CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);`)
	if err != nil {
		t.Fatal(err)
	}
	provider, owner, viewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,owner_user_id,logo_url,banner_url,business_name)VALUES($1,$2,'photo','banner','Business')`, provider, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO users(id,avatar_url) VALUES($1,'photo'),($2,'staff-photo')`, owner, viewer); err != nil {
		t.Fatal(err)
	}
	for user, role := range map[string]string{owner: "owner", viewer: "member"} {
		if _, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,$3,true)`, provider, user, role); err != nil {
			t.Fatal(err)
		}
	}
	market := &ProviderMarketplaceController{db: db, s3: storage.NewS3(config.Config{AWSRegion: "eu-north-1", AWSAccessKeyID: "test", AWSSecretAccessKey: "test", S3BucketName: "branding-test"})}
	previous := http.DefaultClient
	requests := 0
	http.DefaultClient = &http.Client{Transport: brandingHeadTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "HEAD" || r.URL.Host != "branding-test.s3.eu-north-1.amazonaws.com" {
			t.Fatalf("unexpected storage request %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, ContentLength: 128, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Put("/media", market.UpdateProviderMedia)
	for _, sample := range []struct {
		user, body string
		status     int
	}{{viewer, `{"photo_url":""}`, 403}, {owner, `{}`, 400}, {owner, `{"photo_url":""}`, 204}} {
		request := httptest.NewRequest("PUT", "/media", strings.NewReader(sample.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Actor", sample.user)
		response, e := app.Test(request)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != sample.status {
			t.Fatalf("got %d wanted %d", response.StatusCode, sample.status)
		}
	}
	var photo, banner, name string
	if err = db.QueryRow(ctx, `SELECT logo_url,banner_url,business_name FROM provider_organizations WHERE id=$1`, provider).Scan(&photo, &banner, &name); err != nil {
		t.Fatal(err)
	}
	var removedAvatar string
	_ = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&removedAvatar)
	if removedAvatar != "" {
		t.Fatal("linked personal photo was not removed", removedAvatar)
	}
	if photo != "" || banner != "banner" || name != "Business" {
		t.Fatalf("unexpected profile %q/%q/%q", photo, banner, name)
	}
	for _, sample := range []struct {
		url    string
		status int
	}{
		{"https://example.com/photo.jpg", 422},
		{"https://branding-test.s3.eu-north-1.amazonaws.com/user-uploads/providers/another/logo/photo.jpg", 422},
		{"https://branding-test.s3.eu-north-1.amazonaws.com/user-uploads/providers/" + provider + "/verification/id.jpg", 422},
		{"https://branding-test.s3.eu-north-1.amazonaws.com/user-uploads/providers/" + provider + "/logo/photo.jpg", 204},
	} {
		request := httptest.NewRequest("PUT", "/media", strings.NewReader(`{"photo_url":"`+sample.url+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Actor", owner)
		response, e := app.Test(request)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != sample.status {
			t.Fatalf("media %s got %d want %d", sample.url, response.StatusCode, sample.status)
		}
	}
	var avatar string
	if err = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&avatar); err != nil || !strings.Contains(avatar, "/logo/photo.jpg") {
		t.Fatal("owner photo not synced", avatar, err)
	}
	call := func(actor, body string) {
		t.Helper()
		req := httptest.NewRequest("PUT", "/media", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Actor", actor)
		res, e := app.Test(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatal("media update failed", res.StatusCode)
		}
	}
	// Banner edits and delegated staff branding never replace personal photos.
	call(owner, `{"banner_url":""}`)
	if err = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&avatar); err != nil || !strings.Contains(avatar, "/logo/photo.jpg") {
		t.Fatal("banner overwrote owner", avatar, err)
	}
	_, _ = db.Exec(ctx, `UPDATE provider_members SET role='manager' WHERE user_id=$1`, viewer)
	call(viewer, `{"photo_url":"https://branding-test.s3.eu-north-1.amazonaws.com/user-uploads/providers/`+provider+`/logo/manager.jpg"}`)
	var staffAvatar string
	_ = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, viewer).Scan(&staffAvatar)
	_ = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&avatar)
	if staffAvatar != "staff-photo" || !strings.Contains(avatar, "/logo/photo.jpg") {
		t.Fatal("staff branding changed personal identity", staffAvatar, avatar)
	}
	// A separately changed personal photo survives removal of a business logo.
	_, _ = db.Exec(ctx, `UPDATE users SET avatar_url='personal-new' WHERE id=$1`, owner)
	call(owner, `{"photo_url":""}`)
	_ = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&avatar)
	if avatar != "personal-new" {
		t.Fatal("removal erased independent personal image", avatar)
	}
	// If personal synchronization fails, the business update must roll back too.
	if _, err = db.Exec(ctx, `ALTER TABLE users ADD CONSTRAINT fixture_avatar_guard CHECK(avatar_url NOT LIKE '%blocked.jpg')`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "/media", strings.NewReader(`{"photo_url":"https://branding-test.s3.eu-north-1.amazonaws.com/user-uploads/providers/`+provider+`/logo/blocked.jpg"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Actor", owner)
	res, e := app.Test(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 500 {
		t.Fatal("sync failure not reported", res.StatusCode)
	}
	_ = db.QueryRow(ctx, `SELECT logo_url FROM provider_organizations WHERE id=$1`, provider).Scan(&photo)
	_ = db.QueryRow(ctx, `SELECT avatar_url FROM users WHERE id=$1`, owner).Scan(&avatar)
	if photo != "" || avatar != "personal-new" {
		t.Fatal("partial photo write", photo, avatar)
	}
	if requests != 3 {
		t.Fatalf("expected three verified storage HEADs, got %d", requests)
	}
}
