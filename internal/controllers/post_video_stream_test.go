package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/gofiber/fiber/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostVideoStreamAndPrivateDocumentDenial(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	calls := 0
	http.DefaultTransport = paymentRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Range") != "bytes=0-3" {
			t.Fatal("range was not forwarded")
		}
		return &http.Response{StatusCode: 206, ContentLength: 4, Header: http.Header{"Content-Type": []string{"video/mp4"}, "Content-Range": []string{"bytes 0-3/10"}, "Accept-Ranges": []string{"bytes"}}, Body: io.NopCloser(strings.NewReader("test"))}, nil
	})
	controller := &UploadController{s3: storage.NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic"})}
	app := fiber.New()
	app.Get("/media/*", controller.PublicImageView)
	for _, key := range []string{"private/providers/a/verification/evidence/x.jpg", "user-uploads/providers/a/verification/x.jpg"} {
		res, e := app.Test(httptest.NewRequest("GET", "/media/"+key, nil))
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 404 || calls != 0 {
			t.Fatal("verification evidence was exposed anonymously")
		}
	}
	request := httptest.NewRequest("GET", "/media/user-uploads/providers/a/video/test.mp4", nil)
	request.Header.Set("Range", "bytes=0-3")
	res, e := app.Test(request)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	body, e := io.ReadAll(res.Body)
	if e != nil || string(body) != "test" || res.StatusCode != 206 || res.Header.Get("Content-Range") != "bytes 0-3/10" {
		t.Fatalf("video stream failed: %d %s %v", res.StatusCode, body, e)
	}
}
