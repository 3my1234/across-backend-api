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
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true,logo_url text,banner_url text,business_name text,updated_at timestamptz DEFAULT now());CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);`)
	if err != nil {
		t.Fatal(err)
	}
	provider, owner, viewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO provider_organizations(id,logo_url,banner_url,business_name)VALUES($1,'photo','banner','Business')`, provider)
	if err != nil {
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
	if requests != 1 {
		t.Fatalf("expected one verified storage HEAD, got %d", requests)
	}
}
