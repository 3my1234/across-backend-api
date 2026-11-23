package controllers

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/streamvideo"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cloudflare Stream provider videos. The phone uploads the (lightly prepared)
// original straight to Stream through a one-time resumable URL; Stream enforces
// the plan duration, encodes and serves adaptive HLS. This server only reserves,
// verifies and registers — it never receives or encodes video bytes.

const (
	streamUploadWindow   = 4 * time.Hour
	streamStatusInterval = 3 * time.Second
	streamOrphanAge      = 7 * 24 * time.Hour
	streamVideoHeader    = "X-Video-Upload-Mode"
)

func newStreamClient(cfg config.Config) *streamvideo.Client {
	if !cfg.StreamVideoEnabled || cfg.CloudflareAccountID == "" || cfg.CloudflareStreamToken == "" {
		return nil
	}
	return streamvideo.New(cfg.CloudflareAccountID, cfg.CloudflareStreamToken)
}

// streamRequested reports whether this client can upload through Stream. Older
// app builds keep the S3/FFmpeg route and its 180-second limit.
func (m *ProviderMarketplaceController) streamRequested(c *fiber.Ctx) bool {
	return m.stream != nil && strings.EqualFold(c.Get(streamVideoHeader), "stream")
}

func (m *ProviderMarketplaceController) StreamVideoUpload(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.UserContext(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	if m.stream == nil {
		return fiber.NewError(404, "Direct video upload is not enabled. Update the app or try again later.")
	}
	var payload struct {
		UploadLength int64  `json:"upload_length"`
		Filename     string `json:"filename"`
	}
	if c.BodyParser(&payload) != nil {
		return fiber.ErrBadRequest
	}
	policy, err := m.postPolicy(c.UserContext(), provider)
	if err != nil {
		return c.Status(503).JSON(fiber.Map{"message": "Your plan could not be checked. Please retry."})
	}
	if policy.VideoUploadsPaused {
		c.Set("Retry-After", "60")
		return c.Status(503).JSON(fiber.Map{"message": "Video uploads are temporarily paused. Your published videos remain available."})
	}
	if policy.Videos == 0 {
		return fiber.NewError(403, "New videos require your current plan to allow video uploads.")
	}
	if payload.UploadLength <= 0 || policy.VideoBytes <= 0 || payload.UploadLength > policy.VideoBytes {
		return fiber.NewError(422, "This video is larger than your upload allowance. Trim it or record at a lower resolution.")
	}
	if err = m.reserveStreamVideo(c.UserContext(), provider); err != nil {
		return err
	}
	usage, err := m.stream.StorageUsage(c.UserContext())
	if err != nil {
		c.Set("Retry-After", "30")
		return c.Status(503).JSON(fiber.Map{"message": "The video service is unavailable. Please retry shortly."})
	}
	var reserve int
	_ = m.db.QueryRow(c.UserContext(), `SELECT stream_storage_reserve_minutes FROM provider_video_launch_policy WHERE singleton`).Scan(&reserve)
	if usage.LimitMinutes > 0 && usage.StoredMinutes+float64(reserve)+float64(policy.VideoSeconds)/60 > usage.LimitMinutes {
		log.Printf("stream storage near limit: stored_minutes=%.0f limit_minutes=%.0f; raise the Cloudflare Stream storage plan", usage.StoredMinutes, usage.LimitMinutes)
		c.Set("Retry-After", "600")
		return c.Status(503).JSON(fiber.Map{"message": "Video storage is temporarily full. Your listing can be published without a video; please add it later."})
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 15*time.Second)
	defer cancel()
	uploadURL, uid, err := m.stream.CreateUpload(ctx, payload.UploadLength, policy.VideoSeconds, provider, "provider "+provider, time.Now().Add(streamUploadWindow))
	if err != nil {
		c.Set("Retry-After", "15")
		return c.Status(503).JSON(fiber.Map{"message": "The video service could not start this upload. Please retry."})
	}
	m.stream.Forget()
	key := "stream/" + provider + "/" + uid
	if _, err = m.db.Exec(c.UserContext(), `INSERT INTO provider_video_uploads(key,provider_id,asset_id,size_bytes,state,processing_mode,stream_uid,expires_at) VALUES($1,$2::uuid,$3::uuid,$4,'processing','stream',$5,clock_timestamp()+$6::interval)`, key, provider, uuid.NewString(), payload.UploadLength, uid, streamUploadWindow.String()); err != nil {
		// Never strand the reserved Stream upload without a local record.
		_ = m.stream.Delete(context.Background(), uid)
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	return c.Status(201).JSON(fiber.Map{"key": key, "upload_url": uploadURL, "max_duration_seconds": policy.VideoSeconds, "expires_in_seconds": int(streamUploadWindow.Seconds())})
}

// Short provider-row lock for daily/pending counts; the Cloudflare call happens after commit.
func (m *ProviderMarketplaceController) reserveStreamVideo(ctx context.Context, provider string) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, provider); err != nil {
		return fiber.ErrServiceUnavailable
	}
	var dailyLimit, pendingLimit, dailyUsed, pendingUsed int
	err = tx.QueryRow(ctx, `SELECT p.daily_upload_limit,p.pending_upload_limit,
		(SELECT count(*) FROM provider_video_uploads WHERE provider_id=$1::uuid AND created_at>=clock_timestamp()-interval '24 hours'),
		(SELECT count(*) FROM provider_video_uploads WHERE provider_id=$1::uuid AND state NOT IN('complete','failed') AND expires_at>clock_timestamp())
		FROM provider_video_launch_policy p WHERE singleton`, provider).Scan(&dailyLimit, &pendingLimit, &dailyUsed, &pendingUsed)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	if dailyUsed >= dailyLimit || pendingUsed >= pendingLimit {
		return fiber.NewError(429, "You have reached today's video upload limit, or other videos are still uploading. Finish or cancel those, then retry.")
	}
	return tx.Commit(ctx)
}

type streamRow struct {
	key, provider, uid, state string
	checked                   *time.Time
	created                   time.Time
}

// refreshStreamVideo asks Cloudflare about one unfinished Stream upload and, once
// it is ready and within the plan, registers it exactly once as a verified asset.
func (m *ProviderMarketplaceController) refreshStreamVideo(ctx context.Context, provider, key string) error {
	if m.stream == nil {
		return nil
	}
	var row streamRow
	err := m.db.QueryRow(ctx, `SELECT key,provider_id::text,stream_uid,state,stream_checked_at,created_at FROM provider_video_uploads WHERE key=$1 AND provider_id=$2::uuid AND processing_mode='stream'`, key, provider).Scan(&row.key, &row.provider, &row.uid, &row.state, &row.checked, &row.created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if row.state == "complete" || row.state == "failed" || (row.checked != nil && time.Since(*row.checked) < streamStatusInterval) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	video, err := m.stream.Video(ctx, row.uid)
	if errors.Is(err, streamvideo.ErrNotFound) {
		if time.Since(row.created) > streamUploadWindow {
			return m.failStream(ctx, row, "stream_missing", false)
		}
		_, err = m.db.Exec(ctx, `UPDATE provider_video_uploads SET stream_checked_at=clock_timestamp() WHERE key=$1`, row.key)
		return err
	}
	if err != nil {
		return nil // transient: the phone keeps polling
	}
	if video.State == "error" {
		code := "stream_error"
		if strings.Contains(strings.ToLower(video.ErrorReason), "duration") {
			code = "stream_too_long"
		}
		return m.failStream(ctx, row, code, true)
	}
	if !video.Ready || video.Duration <= 0 || !strings.HasPrefix(video.HLS, "https://") {
		_, err = m.db.Exec(ctx, `UPDATE provider_video_uploads SET stream_checked_at=clock_timestamp() WHERE key=$1`, row.key)
		return err
	}
	policy, err := m.postPolicy(ctx, provider)
	if err != nil {
		return nil
	}
	if video.Duration > float64(policy.VideoSeconds)+1 {
		return m.failStream(ctx, row, "stream_too_long", true)
	}
	duration := math.Min(video.Duration, float64(policy.VideoSeconds))
	size := max(video.Size, 1)
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_video_uploads SET state='complete',url=$2,thumbnail_url=$3,duration_seconds=$4,size_bytes=$5,stream_checked_at=clock_timestamp(),error_code=NULL WHERE key=$1 AND state NOT IN('complete','failed')`, row.key, video.HLS, video.Thumbnail, duration, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err = tx.Exec(ctx, `INSERT INTO provider_post_video_assets(url,provider_id,duration_seconds,size_bytes,stream_uid) VALUES($1,$2::uuid,$3,$4,$5) ON CONFLICT (url) DO NOTHING`, video.HLS, provider, duration, size, row.uid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *ProviderMarketplaceController) failStream(ctx context.Context, row streamRow, code string, remove bool) error {
	if remove {
		if err := m.stream.Delete(ctx, row.uid); err == nil {
			m.stream.Forget()
			_, _ = m.db.Exec(ctx, `UPDATE provider_video_uploads SET stream_deleted_at=clock_timestamp() WHERE key=$1`, row.key)
		}
	}
	_, err := m.db.Exec(ctx, `UPDATE provider_video_uploads SET state='failed',error_code=$2,stream_checked_at=clock_timestamp() WHERE key=$1 AND state<>'complete'`, row.key, code)
	return err
}

func streamFailureMessage(code string, maxSeconds int) string {
	switch code {
	case "stream_too_long":
		return "This video is longer than your plan allows. Trim it and upload again."
	case "stream_missing":
		return "The upload did not finish in time. Please upload the video again."
	case "stream_error":
		return "This video could not be processed. Try another recording or export it again from your gallery."
	}
	return "This video could not be processed. Please upload it again."
}

// refreshRecentStreamVideos keeps "Check uploaded videos" current without a webhook.
func (m *ProviderMarketplaceController) refreshRecentStreamVideos(ctx context.Context, provider string) {
	if m.stream == nil {
		return
	}
	rows, err := m.db.Query(ctx, `SELECT key FROM provider_video_uploads WHERE provider_id=$1::uuid AND processing_mode='stream' AND state NOT IN('complete','failed') AND created_at>=clock_timestamp()-interval '7 days' ORDER BY created_at DESC LIMIT 3`, provider)
	if err != nil {
		return
	}
	var keys []string
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			keys = append(keys, key)
		}
	}
	rows.Close()
	for _, key := range keys {
		_ = m.refreshStreamVideo(ctx, provider, key)
	}
}

// RunStreamVideoCleanup deletes Stream videos that never finished uploading or
// were never attached to any product or service (including archived ones), so
// the prepaid Stream storage is spent only on videos customers can see.
// Runs in the dedicated media worker.
func RunStreamVideoCleanup(ctx context.Context, db *pgxpool.Pool, cfg config.Config, client *streamvideo.Client) (int, error) {
	if client == nil {
		client = newStreamClient(cfg)
	}
	if client == nil {
		return 0, nil
	}
	rows, err := db.Query(ctx, `SELECT u.key,u.stream_uid FROM provider_video_uploads u
		WHERE u.processing_mode='stream' AND u.stream_deleted_at IS NULL AND (
		 (u.state<>'complete' AND u.expires_at<clock_timestamp()-interval '1 hour')
		 OR (u.state='complete' AND u.created_at<clock_timestamp()-$1::interval
		     AND NOT EXISTS(SELECT 1 FROM products p WHERE p.provider_id=u.provider_id AND COALESCE(to_jsonb(p)#>'{factory_details,video_urls}','[]'::jsonb) @> jsonb_build_array(u.url))
		     AND NOT EXISTS(SELECT 1 FROM provider_listings l WHERE l.provider_id=u.provider_id AND COALESCE(to_jsonb(l)#>'{attributes,video_urls}','[]'::jsonb) @> jsonb_build_array(u.url))))
		ORDER BY u.created_at LIMIT 50`, streamOrphanAge.String())
	if err != nil {
		return 0, err
	}
	type target struct{ key, uid string }
	var targets []target
	for rows.Next() {
		var t target
		if rows.Scan(&t.key, &t.uid) == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()
	deleted := 0
	for _, t := range targets {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := client.Delete(call, t.uid)
		cancel()
		if err != nil {
			continue
		}
		if _, err = db.Exec(ctx, `UPDATE provider_video_uploads SET stream_deleted_at=clock_timestamp(),state=CASE WHEN state='complete' THEN state ELSE 'failed' END,error_code=CASE WHEN state='complete' THEN error_code ELSE COALESCE(error_code,'stream_missing') END WHERE key=$1`, t.key); err == nil {
			_, _ = db.Exec(ctx, `DELETE FROM provider_post_video_assets WHERE stream_uid=$1`, t.uid)
			deleted++
		}
	}
	if deleted > 0 {
		client.Forget()
	}
	return deleted, nil
}
