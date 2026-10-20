package controllers

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/gen2brain/avif"
	"github.com/gofiber/fiber/v2"
)

const maxImageProxyBytes = 20 << 20

var publicMediaClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	return &http.Client{Transport: transport}
}()

type UploadController struct {
	s3          *storage.S3
	mediaClient *http.Client
}

// Streaming video owns the cancellation until Fiber finishes or closes it.
type publicMediaBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *publicMediaBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

func NewUploadController(cfg config.Config) *UploadController {
	return &UploadController{s3: storage.NewS3(cfg)}
}

func (u *UploadController) AdminPresign(c *fiber.Ctx) error {
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mimeType"`
		Kind     string `json:"kind"`
		Scope    string `json:"scope"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Filename = strings.TrimSpace(req.Filename)
	mime := strings.ToLower(strings.TrimSpace(req.MimeType))
	if req.Filename == "" || mime == "" {
		return fiber.NewError(fiber.StatusBadRequest, "filename and mimeType are required")
	}
	isImage := strings.HasPrefix(mime, "image/")
	isVideo := strings.HasPrefix(mime, "video/")
	if !isImage && !isVideo {
		return fiber.NewError(fiber.StatusBadRequest, "mimeType must be image/* or video/*")
	}
	if isImage && !supportedPortableImageMime(mime) {
		return fiber.NewError(fiber.StatusBadRequest, "product images must be JPG, PNG, or WebP")
	}
	if !u.s3.Configured() {
		log.Printf("admin presign rejected: s3 not configured region=%q bucket=%q access_key_set=%t secret_set=%t", u.s3.Region(), u.s3.Bucket(), u.s3.AccessKeySet(), u.s3.SecretKeySet())
		return fiber.NewError(fiber.StatusServiceUnavailable, "s3 upload is not configured")
	}
	kind := "image"
	if req.Kind == "video" || (!isImage && isVideo) {
		kind = "video"
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "products"
	}
	key := storage.SafeKey("user-uploads/admin/"+scope+"/"+kind, req.Filename)
	uploadURL, err := u.s3.PresignPut(key, mime, 15*time.Minute)
	if err != nil {
		log.Printf("admin presign failed: key=%s mime=%s err=%v", key, mime, err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to generate upload URL")
	}
	viewURL := u.s3.ObjectURL(key)
	return c.JSON(fiber.Map{
		"success":   true,
		"uploadUrl": uploadURL,
		"key":       key,
		"viewUrl":   viewURL,
		"publicUrl": viewURL,
	})
}

func supportedPortableImageMime(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func (u *UploadController) UserPresign(c *fiber.Ctx) error {
	var req struct {
		Filename string `json:"filename"`
		MimeType string `json:"mimeType"`
		Kind     string `json:"kind"`
		Scope    string `json:"scope"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Filename = strings.TrimSpace(req.Filename)
	mime := strings.ToLower(strings.TrimSpace(req.MimeType))
	if req.Filename == "" || mime == "" {
		return fiber.NewError(fiber.StatusBadRequest, "filename and mimeType are required")
	}
	if !strings.HasPrefix(mime, "image/") {
		return fiber.NewError(fiber.StatusBadRequest, "mimeType must be image/*")
	}
	if !u.s3.Configured() {
		log.Printf("user presign rejected: s3 not configured region=%q bucket=%q access_key_set=%t secret_set=%t", u.s3.Region(), u.s3.Bucket(), u.s3.AccessKeySet(), u.s3.SecretKeySet())
		return fiber.NewError(fiber.StatusServiceUnavailable, "s3 upload is not configured")
	}
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "reviews"
	}
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "user session unavailable")
	}
	key := storage.SafeKey("user-uploads/buyer/"+userID+"/"+scope+"/image", req.Filename)
	uploadURL, err := u.s3.PresignPut(key, mime, 15*time.Minute)
	if err != nil {
		log.Printf("user presign failed: key=%s mime=%s err=%v", key, mime, err)
		return fiber.NewError(fiber.StatusInternalServerError, "failed to generate upload URL")
	}
	viewURL := u.s3.ObjectURL(key)
	return c.JSON(fiber.Map{
		"success":   true,
		"uploadUrl": uploadURL,
		"key":       key,
		"viewUrl":   viewURL,
		"publicUrl": viewURL,
	})
}

func (u *UploadController) PublicImageView(c *fiber.Ctx) error {
	rawKey := c.Params("*")
	key, err := normalizeS3ViewKey(rawKey)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid media key")
	}
	if strings.HasPrefix(key, "private/") || strings.Contains(key, "/verification/") {
		return fiber.ErrNotFound
	}
	avatar := c.Query("avatar") == "1"
	if avatar {
		if cached := cachedNotificationAvatar(key); cached != nil {
			return sendNotificationAvatar(c, key, cached)
		}
		select {
		case avatarFetchSlots <- struct{}{}:
			defer func() { <-avatarFetchSlots }()
		default:
			c.Set("Retry-After", "2")
			return fiber.NewError(503, "Avatar service is busy")
		}
	}
	isAVIF := strings.EqualFold(filepath.Ext(key), ".avif")
	getURL, err := u.s3.ObjectGetURL(key, 10*time.Minute)
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "media storage is not configured")
	}
	ctx, cancel := context.WithCancel(c.UserContext())
	streamed := false
	defer func() {
		if !streamed {
			cancel()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return err
	}
	// AVIF objects must be fetched whole before transcoding. Forwarding a client
	// range would leave the decoder with an incomplete image payload.
	if rangeHeader := c.Get("Range"); rangeHeader != "" && !isAVIF && !avatar {
		req.Header.Set("Range", rangeHeader)
	}
	client := u.mediaClient
	if client == nil {
		client = publicMediaClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// An http error may contain the signed S3 URL; never log that URL.
		log.Printf("public image view failed: request_id=%s", c.GetRespHeader("X-Request-ID"))
		return fiber.NewError(fiber.StatusBadGateway, "media unavailable")
	}
	defer func() {
		if !streamed {
			resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		return fiber.NewError(resp.StatusCode, "media unavailable")
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "video/") {
		if avatar {
			return fiber.ErrBadRequest
		}
		for _, header := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified", "Accept-Ranges", "Content-Range"} {
			if value := resp.Header.Get(header); value != "" {
				c.Set(header, value)
			}
		}
		c.Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=604800")
		c.Status(resp.StatusCode)
		streamed = true
		body := &publicMediaBody{ReadCloser: resp.Body, cancel: cancel}
		if resp.ContentLength >= 0 {
			return c.SendStream(body, int(resp.ContentLength))
		}
		return c.SendStream(body)
	}
	// Limit both the wait and memory consumed by slow or unexpectedly large
	// objects. Do not send successful headers for an incomplete upstream image.
	data, readErr := readPublicImage(resp, cancel, 15*time.Second)
	if readErr != nil {
		log.Printf("public image read incomplete or oversized: request_id=%s", c.GetRespHeader("X-Request-ID"))
		return readErr
	}
	if isAVIF || strings.EqualFold(resp.Header.Get("Content-Type"), "image/avif") {
		if avatar {
			meta, err := avif.DecodeConfig(bytes.NewReader(data))
			if err != nil || meta.Width <= 0 || meta.Height <= 0 || int64(meta.Width)*int64(meta.Height) > 16<<20 {
				return fiber.NewError(502, "Avatar format or dimensions unavailable")
			}
		}
		converted, err := transcodeAVIFToJPEG(bytes.NewReader(data))
		if err != nil {
			log.Printf("public AVIF conversion failed: key=%s err=%v", key, err)
			return fiber.NewError(fiber.StatusBadGateway, "media conversion failed")
		}
		if avatar {
			cropped, err := circularNotificationAvatar(converted)
			if err != nil {
				return err
			}
			return sendNotificationAvatar(c, key, cropped)
		}
		c.Set("Content-Type", "image/jpeg")
		c.Set("Content-Length", fmt.Sprintf("%d", len(converted)))
		c.Set("Cache-Control", "public, max-age=2592000, immutable")
		return c.Send(converted)
	}
	if avatar {
		cropped, err := circularNotificationAvatar(data)
		if err != nil {
			return err
		}
		return sendNotificationAvatar(c, key, cropped)
	}
	for _, header := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified", "Accept-Ranges", "Content-Range"} {
		if value := resp.Header.Get(header); value != "" {
			c.Set(header, value)
		}
	}
	c.Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=604800")
	c.Status(resp.StatusCode)
	return c.Send(data)
}

func readPublicImage(resp *http.Response, cancel context.CancelFunc, timeout time.Duration) ([]byte, error) {
	timer := time.AfterFunc(timeout, cancel)
	defer timer.Stop()
	if resp.ContentLength > maxImageProxyBytes {
		return nil, fiber.NewError(fiber.StatusBadGateway, "media exceeds image size limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageProxyBytes+1))
	if err != nil || len(data) > maxImageProxyBytes || (resp.ContentLength >= 0 && int64(len(data)) != resp.ContentLength) {
		return nil, fiber.NewError(fiber.StatusBadGateway, "media unavailable or incomplete")
	}
	return data, nil
}

func transcodeAVIFToJPEG(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageProxyBytes {
		return nil, fiber.ErrRequestEntityTooLarge
	}
	decoded, err := avif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, decoded, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func normalizeS3ViewKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	key = strings.TrimLeft(key, "/")
	key = strings.ReplaceAll(key, ",", "/")
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "user-uploads/private-chat/") {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid media key")
	}
	return key, nil
}
