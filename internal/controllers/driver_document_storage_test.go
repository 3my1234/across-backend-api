package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"bytes"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriverDocumentStorageIsOwnedAndImmutable(t *testing.T) {
	provider := uuid.NewString()
	var data bytes.Buffer
	if e := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 20))); e != nil {
		t.Fatal(e)
	}
	calls := 0
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	http.DefaultTransport = paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method == http.MethodGet {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: int64(data.Len()), Body: io.NopCloser(bytes.NewReader(data.Bytes()))}, nil
		}
		if req.Method != http.MethodPut || !strings.Contains(req.URL.Path, "/verification/evidence/") {
			t.Fatal("evidence was not copied to a fresh restricted key")
		}
		body, e := io.ReadAll(req.Body)
		if e != nil || !bytes.Equal(body, data.Bytes()) {
			t.Fatal("evidence bytes changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	s3 := storage.NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic"})
	controller := &ProviderMarketplaceController{s3: s3}
	raw := ""
	app := fiber.New()
	app.Post("/document", func(c *fiber.Ctx) error {
		url, e := controller.freezeDriverDocument(c, provider, raw)
		if e != nil {
			return e
		}
		return c.JSON(fiber.Map{"url": url})
	})
	call := func() int {
		res, e := app.Test(httptest.NewRequest("POST", "/document", nil), 5000)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		return res.StatusCode
	}
	raw = s3.ObjectURL("private/providers/" + uuid.NewString() + "/verification/input.png")
	if call() != 422 || calls != 0 {
		t.Fatal("another provider's evidence was accepted")
	}
	raw = s3.ObjectURL("private/providers/" + provider + "/verification/input.png")
	if call() != 200 || calls != 2 {
		t.Fatal("owned evidence was not frozen")
	}
}
