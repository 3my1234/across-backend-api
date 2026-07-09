package controllers

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/gofiber/fiber/v2"
)

type UploadController struct {
	s3 *storage.S3
}

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
	key := storage.SafeKey("user-uploads/buyer/"+scope+"/image", req.Filename)
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
	getURL, err := u.s3.ObjectGetURL(key, 10*time.Minute)
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "media storage is not configured")
	}
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, getURL, nil)
	if err != nil {
		return err
	}
	if rangeHeader := c.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("public image view failed: key=%s err=%v", key, err)
		return fiber.NewError(fiber.StatusBadGateway, "media unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fiber.NewError(resp.StatusCode, "media unavailable")
	}
	for _, header := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified", "Accept-Ranges", "Content-Range"} {
		if value := resp.Header.Get(header); value != "" {
			c.Set(header, value)
		}
	}
	c.Set("Cache-Control", "public, max-age=86400")
	c.Status(resp.StatusCode)
	_, err = io.Copy(c.Response().BodyWriter(), resp.Body)
	return err
}

func normalizeS3ViewKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	key = strings.TrimLeft(key, "/")
	key = strings.ReplaceAll(key, ",", "/")
	if key == "" || strings.Contains(key, "..") {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid media key")
	}
	return key, nil
}
