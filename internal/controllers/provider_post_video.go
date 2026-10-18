package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var videoVerificationSlots = make(chan struct{}, 2)

const maxPostVideoBytes = 50 * 1024 * 1024

type videoProbeOutput struct{ buffer bytes.Buffer }

func (b *videoProbeOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1024*1024 {
		return 0, fmt.Errorf("video metadata exceeds safety limit")
	}
	return b.buffer.Write(p)
}
func probePostVideo(ctx context.Context, filename string) (float64, error) {
	var output videoProbeOutput
	command := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-f", "mov", "-show_entries", "format=duration:stream=codec_type,codec_name,duration", "-of", "json", filename)
	command.Stdout = &output
	command.Stderr = io.Discard
	e := command.Run()
	if e != nil {
		return 0, fmt.Errorf("Could not verify this video. Upload a valid H.264 MP4 of up to 60 seconds.")
	}
	return verifiedVideoDuration(output.buffer.Bytes())
}

func verifiedVideoDuration(raw []byte) (float64, error) {
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return 0, fmt.Errorf("Could not inspect this video. Choose a valid MP4 file.")
	}
	d, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || d > 60 {
		return 0, fmt.Errorf("Each video must be no longer than 60 seconds.")
	}
	found := false
	for _, s := range probe.Streams {
		if s.Type == "video" {
			if s.Codec != "h264" {
				return 0, fmt.Errorf("Choose an MP4 video encoded as H.264 for mobile playback.")
			}
			found = true
		}
		if duration, e := strconv.ParseFloat(s.Duration, 64); e == nil && (math.IsNaN(duration) || math.IsInf(duration, 0) || duration > 60) {
			return 0, fmt.Errorf("Each video must be no longer than 60 seconds.")
		}
	}
	if !found {
		return 0, fmt.Errorf("This file does not contain a playable video.")
	}
	return d, nil
}
func (m *ProviderMarketplaceController) CompletePostVideo(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.Context(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	policy, err := m.postPolicy(c.Context(), provider)
	if err != nil {
		return fiber.NewError(503, "Your plan could not be checked. Please retry.")
	}
	if policy.Videos == 0 {
		return fiber.NewError(403, "Upgrade to Gold or Diamond to upload videos.")
	}
	if _, err = exec.LookPath("ffprobe"); err != nil {
		return fiber.NewError(503, "Video verification is temporarily unavailable. Please try again later.")
	}
	var payload struct {
		Key string `json:"key"`
	}
	if c.BodyParser(&payload) != nil {
		return fiber.ErrBadRequest
	}
	key, e := url.PathUnescape(payload.Key)
	if e != nil || !strings.HasPrefix(key, "private/providers/"+provider+"/video-pending/") || strings.Contains(key, "..") {
		return fiber.NewError(422, "Choose a video uploaded through your own provider account.")
	}
	if m.s3 == nil || !m.s3.Configured() {
		return fiber.NewError(503, "Video storage is unavailable.")
	}
	select {
	case videoVerificationSlots <- struct{}{}:
		defer func() { <-videoVerificationSlots }()
	default:
		c.Set("Retry-After", "10")
		return fiber.NewError(429, "Videos are being checked. Please retry in 10 seconds.")
	}
	ctx, done := context.WithTimeout(c.Context(), 75*time.Second)
	defer done()
	signed, e := m.s3.ObjectGetURL(key, time.Minute)
	if e != nil {
		return fiber.ErrInternalServerError
	}
	client := &http.Client{Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	res, e := client.Do(request)
	if e != nil {
		return fiber.NewError(503, "Could not read the uploaded video. Please retry.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength > maxPostVideoBytes {
		return fiber.NewError(422, "Upload an MP4 video no larger than 50 MB.")
	}
	file, e := os.CreateTemp("", "atl-post-*.mp4")
	if e != nil {
		return fiber.ErrInternalServerError
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, e := io.Copy(file, io.LimitReader(res.Body, maxPostVideoBytes+1))
	if e != nil || size <= 0 || size > maxPostVideoBytes {
		return fiber.NewError(422, "Upload a complete MP4 video no larger than 50 MB.")
	}
	probeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	duration, e := probePostVideo(probeCtx, file.Name())
	if e != nil {
		return fiber.NewError(422, e.Error())
	}
	// Publish a fresh object whose PUT URL is never given to the uploader. The
	// temporary upload cannot be overwritten to bypass an earlier verification.
	verifiedKey := "user-uploads/providers/" + provider + "/video/" + uuid.NewString() + ".mp4"
	put, e := m.s3.PresignPut(verifiedKey, "video/mp4", time.Minute)
	if e != nil {
		return fiber.ErrInternalServerError
	}
	if _, e = file.Seek(0, 0); e != nil {
		return fiber.ErrInternalServerError
	}
	upload, _ := http.NewRequestWithContext(ctx, http.MethodPut, put, file)
	upload.ContentLength = size
	upload.Header.Set("Content-Type", "video/mp4")
	published, e := client.Do(upload)
	if e != nil {
		return fiber.NewError(503, "The verified video could not be saved. Please retry.")
	}
	published.Body.Close()
	if published.StatusCode < 200 || published.StatusCode >= 300 {
		return fiber.NewError(503, "The verified video could not be saved. Please retry.")
	}
	public := m.s3.ObjectURL(verifiedKey)
	if _, e = m.db.Exec(ctx, `INSERT INTO provider_post_video_assets(url,provider_id,duration_seconds,size_bytes) VALUES($1,$2::uuid,$3,$4)`, public, provider, duration, size); e != nil {
		return fiber.NewError(503, "The video was checked but could not be registered. Please retry.")
	}
	// The original candidate is no longer needed. Its stale PUT link cannot
	// overwrite the verified public object, even if cleanup must be retried.
	if deleteURL, err := m.s3.ObjectDeleteURL(key, time.Minute); err == nil {
		cleanupCtx, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		request, _ := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, deleteURL, nil)
		if response, err := client.Do(request); err == nil {
			response.Body.Close()
		}
	}
	return c.JSON(fiber.Map{"url": public, "duration_seconds": duration})
}
