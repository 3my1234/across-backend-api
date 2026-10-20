package controllers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/gofiber/fiber/v2"
)

type mediaRoundTrip func(*http.Request) (*http.Response, error)

func (f mediaRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type stalledImage struct{ ctx context.Context }

func (b stalledImage) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b stalledImage) Close() error             { return nil }

func TestPublicImageReadBoundsAndDeadline(t *testing.T) {
	for _, length := range []int64{maxImageProxyBytes + 1, -1} {
		resp := &http.Response{ContentLength: length, Body: io.NopCloser(bytes.NewReader(make([]byte, maxImageProxyBytes+1)))}
		if data, err := readPublicImage(resp, func() {}, time.Second); err == nil || data != nil {
			t.Fatal("oversized upstream image accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	if data, err := readPublicImage(&http.Response{ContentLength: -1, Body: stalledImage{ctx}}, cancel, 10*time.Millisecond); err == nil || data != nil {
		t.Fatal("stalled image accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("image deadline did not bound wait")
	}
	if data, err := readPublicImage(&http.Response{ContentLength: 10, Body: io.NopCloser(strings.NewReader("short"))}, func() {}, time.Second); err == nil || data != nil {
		t.Fatal("truncated image accepted")
	}
}

func TestPublicImageProxyDoesNotCacheIncompleteResponses(t *testing.T) {
	u := &UploadController{s3: storage.NewS3(config.Config{AWSRegion: "us-east-1", AWSAccessKeyID: "test", AWSSecretAccessKey: "test", S3BucketName: "fixture"})}
	fail := true
	calls := 0
	u.mediaClient = &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Path, "private") {
			t.Fatal("private object fetched")
		}
		body := io.NopCloser(strings.NewReader("image"))
		length := int64(5)
		if fail {
			body = io.NopCloser(io.MultiReader(strings.NewReader("part"), errorMediaReader{}))
			length = 10
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: body, ContentLength: length}, nil
	})}
	app := fiber.New()
	app.Get("/images/*", u.PublicImageView)
	response, err := app.Test(mustMediaRequest("/images/photo.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 502 || response.Header.Get("Cache-Control") != "" {
		t.Fatal("partial image was successful or cacheable", response.StatusCode)
	}
	response.Body.Close()
	fail = false
	response, err = app.Test(mustMediaRequest("/images/photo.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(data) != "image" || !strings.Contains(response.Header.Get("Cache-Control"), "public") {
		t.Fatal("valid image failed", response.StatusCode, string(data))
	}
	response, err = app.Test(mustMediaRequest("/images/user-uploads/private-chat/person/photo.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 200 || calls != 2 {
		t.Fatal("private chat photo passed public proxy")
	}
}

type errorMediaReader struct{}

func (errorMediaReader) Read([]byte) (int, error) { return 0, errors.New("upstream reset") }
func mustMediaRequest(path string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://test"+path, nil)
	return r
}
