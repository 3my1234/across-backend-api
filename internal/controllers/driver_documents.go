package controllers

import (
	"bytes"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func driverDocument(kind string) bool {
	return kind == "drivers_license" || kind == "driver_vehicle" || kind == "driver_plate"
}

var driverEvidenceSlots = make(chan struct{}, 2)

func (m *ProviderMarketplaceController) freezeDriverDocument(c *fiber.Ctx, provider, raw string) (string, error) {
	if m.s3 == nil || !m.s3.Configured() {
		return "", fiber.NewError(503, "Document storage is unavailable. Please retry.")
	}
	key, e := url.PathUnescape(m.s3.KeyFromURL(raw))
	if e != nil || !strings.HasPrefix(key, "private/providers/"+provider+"/verification/") || strings.Contains(key, "..") {
		return "", fiber.NewError(422, "Upload your own clear licence, car or plate photo using the verification form.")
	}
	select {
	case driverEvidenceSlots <- struct{}{}:
		defer func() { <-driverEvidenceSlots }()
	default:
		c.Set("Retry-After", "10")
		return "", fiber.NewError(429, "Document checks are busy. Please retry in 10 seconds.")
	}
	signed, e := m.s3.ObjectGetURL(key, time.Minute)
	if e != nil {
		return "", fiber.NewError(503, "Could not verify the uploaded document. Please retry.")
	}
	ctx, done := context.WithTimeout(c.Context(), 15*time.Second)
	defer done()
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if e != nil {
		return "", fiber.ErrInternalServerError
	}
	res, e := client.Do(req)
	if e != nil {
		return "", fiber.NewError(503, "The document upload could not be checked. Please retry.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !supportedPortableImageMime(strings.Split(res.Header.Get("Content-Type"), ";")[0]) || res.ContentLength > 10*1024*1024 {
		return "", fiber.NewError(422, "Upload a clear JPG, PNG or WebP photo up to 10 MB. Driver documents cannot be PDFs.")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 10*1024*1024+1))
	if err != nil || len(data) == 0 || len(data) > 10*1024*1024 {
		return "", fiber.NewError(422, "Upload a complete photo no larger than 10 MB.")
	}
	mime := http.DetectContentType(data)
	if !supportedPortableImageMime(mime) {
		return "", fiber.NewError(422, "Driver evidence must be a genuine JPG, PNG or WebP photo.")
	}
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[mime]
	frozen := "private/providers/" + provider + "/verification/evidence/" + uuid.NewString() + ext
	put, err := m.s3.PresignPut(frozen, mime, time.Minute)
	if err != nil {
		return "", fiber.NewError(503, "The document could not be secured. Please retry.")
	}
	upload, _ := http.NewRequestWithContext(ctx, http.MethodPut, put, bytes.NewReader(data))
	upload.Header.Set("Content-Type", mime)
	result, err := client.Do(upload)
	if err != nil {
		return "", fiber.NewError(503, "The document could not be secured. Please retry.")
	}
	result.Body.Close()
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", fiber.NewError(503, "The document could not be secured. Please retry.")
	}
	return m.s3.ObjectURL(frozen), nil
}
