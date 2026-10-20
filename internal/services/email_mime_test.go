package services

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"across/backend/internal/branding"
	"across/backend/internal/config"
)

func TestBrandedEmailCarriesDecodableInlineLogoAndShortLines(t *testing.T) {
	html := NewEmailService(config.Config{BrandLogoURL: "https://broken.invalid/logo.png"}).layout("Verify email", "Verification", strings.Repeat("<p>Hello &amp; welcome</p>", 100))
	body, contentType, err := emailMIMEBody("Plain text verification instructions", html)
	if err != nil {
		t.Fatal(err)
	}
	kind, params, err := mime.ParseMediaType(contentType)
	if err != nil || kind != "multipart/related" {
		t.Fatal(kind, err)
	}
	reader := multipart.NewReader(strings.NewReader(body), params["boundary"])
	alternative, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	_, nested, _ := mime.ParseMediaType(alternative.Header.Get("Content-Type"))
	nestedReader := multipart.NewReader(alternative, nested["boundary"])
	plain, _ := nestedReader.NextPart()
	text, _ := io.ReadAll(plain)
	if !strings.Contains(string(text), "verification instructions") {
		t.Fatal("missing plain text")
	}
	markup, _ := nestedReader.NextPart()
	decoded, _ := io.ReadAll(markup)
	if string(decoded) != html || strings.Contains(string(decoded), "broken.invalid") {
		t.Fatal("HTML damaged or still depends on remote logo")
	}
	logo, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if logo.Header.Get("Content-ID") != "<"+branding.EmailLogoCID+">" {
		t.Fatal("logo CID not linked")
	}
	image, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, logo))
	if err != nil || !bytes.Equal(image, branding.Logo) {
		t.Fatal("inline logo damaged", err)
	}
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("MIME line exceeds SMTP limit: %d", len(line))
		}
	}
}

func TestNotificationAvatarRejectsPrivateAndUntrustedImages(t *testing.T) {
	for _, raw := range []string{"https://evil.test/image.png", "http://api.atlxpres.com/user-uploads/avatar.png", "user-uploads/private-chat/user/photo.jpg", "private/providers/id.png", "user-uploads/providers/a/verification/id.png", "https://bucket.s3.eu-north-1.amazonaws.com/user-uploads/photo.png?X-Amz-Signature=secret"} {
		if notificationAvatar(raw) != "" {
			t.Fatal("unsafe avatar accepted", raw)
		}
	}
	for _, raw := range []string{"user-uploads/providers/a/logo/photo.png", "https://bucket.s3.eu-north-1.amazonaws.com/user-uploads/providers/a/logo/photo.png", "https://api.atlxpres.com/api/v1/public/images/view/user-uploads/providers/a/logo/photo.png"} {
		if !strings.HasSuffix(notificationAvatar(raw), "?avatar=1") {
			t.Fatal("public avatar not converted", raw)
		}
	}
}
