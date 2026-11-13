package storage

import (
	"across/backend/internal/config"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPrivateEvidenceNeverUsesPublicCDNURL(t *testing.T) {
	s := NewS3(config.Config{AWSRegion: "us-east-1", S3BucketName: "synthetic", AssetsCDNBase: "https://media.example.test", PublicBaseURL: "https://api.example.test"})
	private := "private/providers/owner/verification/evidence/face.jpg"
	raw := s.ObjectURL(private)
	if strings.Contains(raw, "media.example.test") || strings.Contains(raw, "/public/images/view/") || s.KeyFromURL(raw) != private {
		t.Fatal("private evidence became a public cache URL")
	}
	if s.ObjectURL("user-uploads/public.jpg") != "https://media.example.test/user-uploads/public.jpg" {
		t.Fatal("public image CDN contract changed")
	}
}

func TestSizedVideoUploadSignatureBindsExactLength(t *testing.T) {
	s := NewS3(config.Config{AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic-key", AWSSecretAccessKey: "synthetic-secret", S3BucketName: "synthetic-bucket"})
	if _, err := s.PresignPutSized("private/video.mp4", "video/mp4", time.Minute, 0); err == nil {
		t.Fatal("unbounded upload allowed")
	}
	first, err := s.PresignPutSized("private/video.mp4", "video/mp4", time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.PresignPutSized("private/video.mp4", "video/mp4", time.Minute, 101)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := url.Parse(first)
	b, _ := url.Parse(second)
	if a.Query().Get("X-Amz-SignedHeaders") != "content-length;content-type;host" {
		t.Fatal("size not signed")
	}
	if a.Query().Get("X-Amz-Signature") == b.Query().Get("X-Amz-Signature") {
		t.Fatal("changing content length did not change signature")
	}
	legacy, err := s.PresignPut("public/image.jpg", "image/jpeg", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(legacy)
	if u.Query().Get("X-Amz-SignedHeaders") != "content-type;host" {
		t.Fatal("existing image upload contract changed")
	}
}
