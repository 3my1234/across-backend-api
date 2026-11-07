package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (m *ProviderMarketplaceController) queueProviderVideo(c *fiber.Ctx, provider, key string) error {
	tx, err := m.db.Begin(c.UserContext())
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(c.UserContext())
	var state string
	var expired bool
	err = tx.QueryRow(c.UserContext(), `SELECT state,expires_at<=clock_timestamp() FROM provider_video_uploads WHERE key=$1 AND provider_id=$2::uuid FOR UPDATE`, key, provider).Scan(&state, &expired)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	if state == "complete" || state == "queued" || state == "processing" {
		tx.Rollback(c.UserContext())
		return m.videoStatus(c, provider, key)
	}
	if state == "failed" {
		return fiber.NewError(422, "This video could not be processed. Please upload it again.")
	}
	if expired {
		return fiber.NewError(422, "The upload authorization expired. Please upload again.")
	}
	var paused bool
	var limit int
	if err = tx.QueryRow(c.UserContext(), `SELECT uploads_paused,queue_limit FROM provider_video_launch_policy WHERE singleton FOR UPDATE`).Scan(&paused, &limit); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if paused {
		return fiber.NewError(503, "New video processing is temporarily paused.")
	}
	var allowed bool
	if err = tx.QueryRow(c.UserContext(), `SELECT COALESCE((provider_current_benefits($1::uuid)->>'max_videos')::int>0,false)`, provider).Scan(&allowed); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if !allowed {
		return fiber.NewError(403, "New videos require your current plan to allow video uploads.")
	}

	var count int
	if err = tx.QueryRow(c.UserContext(), `SELECT count(*) FROM provider_video_uploads WHERE processing_mode='background' AND state IN('queued','processing')`).Scan(&count); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if count >= limit {
		c.Set("Retry-After", "30")
		return fiber.NewError(503, "Video processing is busy. Please retry later.")
	}
	if _, err = tx.Exec(c.UserContext(), `UPDATE provider_video_uploads SET state='queued',processing_mode='background',available_at=clock_timestamp() WHERE key=$1`, key); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if err = tx.Commit(c.UserContext()); err != nil {
		return fiber.ErrServiceUnavailable
	}
	return m.videoStatus(c, provider, key)
}
func (m *ProviderMarketplaceController) ProviderVideoStatus(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.UserContext(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	return m.videoStatus(c, provider, c.Query("key"))
}
func (m *ProviderMarketplaceController) videoStatus(c *fiber.Ctx, provider, key string) error {
	var state string
	var url, thumbnail *string
	var duration *float64
	err := m.db.QueryRow(c.UserContext(), `SELECT state,url,thumbnail_url,duration_seconds FROM provider_video_uploads WHERE key=$1 AND provider_id=$2::uuid`, key, provider).Scan(&state, &url, &thumbnail, &duration)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	if state == "failed" {
		return c.Status(422).JSON(fiber.Map{"status": "failed", "message": "This video could not be processed. Please upload it again."})
	}
	result := fiber.Map{"status": state, "key": key}
	if state == "complete" {
		result["url"] = url
		result["thumbnail_url"] = thumbnail
		result["duration_seconds"] = duration
		return c.JSON(result)
	}
	c.Set("Retry-After", "2")
	return c.Status(202).JSON(result)
}

type videoMediaJob struct {
	key, provider, token string
	size                 int64
	attempts             int
}

func claimVideoMediaJob(ctx context.Context, db *pgxpool.Pool) (videoMediaJob, bool, error) {
	var job videoMediaJob
	job.token = uuid.NewString()
	tx, err := db.Begin(ctx)
	if err != nil {
		return job, false, err
	}
	defer tx.Rollback(ctx)
	// A database lease permits only ONE encoder across replicas, without retaining
	// a connection while downloading, encoding or uploading.
	var singleton bool
	err = tx.QueryRow(ctx, `UPDATE provider_video_launch_policy SET worker_token=$1::uuid,worker_lease_until=clock_timestamp()+interval '4 minutes' WHERE singleton AND NOT uploads_paused AND (worker_lease_until IS NULL OR worker_lease_until<=clock_timestamp()) RETURNING singleton`, job.token).Scan(&singleton)
	if err == pgx.ErrNoRows {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_video_uploads SET state='failed',error_code='retry_exhausted',lease_until=NULL WHERE processing_mode='background' AND state='processing' AND lease_until<=clock_timestamp() AND attempts>=3`); err != nil {
		return job, false, err
	}
	err = tx.QueryRow(ctx, `WITH next AS(SELECT key FROM provider_video_uploads WHERE processing_mode='background' AND attempts<3 AND ((state='queued' AND available_at<=clock_timestamp()) OR (state='processing' AND lease_until<=clock_timestamp())) ORDER BY created_at,key FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE provider_video_uploads u SET state='processing',claim_token=$1::uuid,lease_until=clock_timestamp()+interval '4 minutes',attempts=attempts+1 FROM next WHERE u.key=next.key RETURNING u.key,u.provider_id::text,u.size_bytes,u.attempts`, job.token).Scan(&job.key, &job.provider, &job.size, &job.attempts)
	if err == pgx.ErrNoRows {
		if _, e := tx.Exec(ctx, `UPDATE provider_video_launch_policy SET worker_token=NULL,worker_lease_until=NULL WHERE singleton AND worker_token=$1::uuid`, job.token); e != nil {
			return job, false, e
		}
		return job, false, tx.Commit(ctx)
	}
	if err != nil {
		return job, false, err
	}
	return job, true, tx.Commit(ctx)
}

// RunVideoMediaJob is used ONLY by the explicit dedicated media worker role.
func RunVideoMediaJob(parent context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) (result error) {
	if !cfg.AsyncVideoProcessing {
		return nil
	}
	job, found, err := claimVideoMediaJob(parent, db)
	if err != nil || !found {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if result != nil {
			_, _ = db.Exec(ctx, `UPDATE provider_video_uploads SET state=CASE WHEN attempts>=3 THEN 'failed' ELSE 'queued' END,error_code='processing_failed',lease_until=NULL,available_at=clock_timestamp()+attempts*interval '10 seconds' WHERE key=$1 AND claim_token=$2::uuid AND state='processing'`, job.key, job.token)
		}
		_, _ = db.Exec(ctx, `UPDATE provider_video_launch_policy SET worker_token=NULL,worker_lease_until=NULL WHERE singleton AND worker_token=$1::uuid`, job.token)
	}()
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	m := &ProviderMarketplaceController{db: db, cfg: cfg, s3: storage.NewS3(cfg)}
	policy, err := m.postPolicy(ctx, job.provider)
	if err != nil {
		return errors.New("policy unavailable")
	}
	if policy.Videos == 0 || policy.VideoUploadsPaused {
		return errors.New("video allowance changed")
	}
	directory, err := os.MkdirTemp("", "atl-video-job-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	input := filepath.Join(directory, "input.mp4")
	output := filepath.Join(directory, "output.mp4")
	poster := filepath.Join(directory, "poster.jpg")
	signed, err := m.s3.ObjectGetURL(job.key, time.Minute)
	if err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("input unavailable")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return errors.New("input unavailable")
	}
	file, err := os.Create(input)
	if err != nil {
		response.Body.Close()
		return err
	}
	size, copyErr := io.Copy(file, io.LimitReader(response.Body, maxPostVideoBytes+1))
	file.Close()
	response.Body.Close()
	if copyErr != nil || size != job.size || size > maxPostVideoBytes {
		return errors.New("input size mismatch")
	}
	if err = processLaunchVideo(ctx, input, output, poster, policy.VideoSeconds); err != nil {
		return err
	}
	outputInfo, err := os.Stat(output)
	if err != nil || outputInfo.Size() > policy.VideoBytes {
		return errors.New("processed video exceeds plan size")
	}
	outputDuration, err := probePostVideo(ctx, output)
	if err != nil || outputDuration > float64(policy.VideoSeconds) {
		return errors.New("processed video exceeds plan duration")
	}
	videoKey := providerVerifiedVideoKey(job.provider, providerVideoTicket{assetID: job.token})
	posterKey := "user-uploads/providers/" + job.provider + "/video-thumbnails/" + job.token + ".jpg"
	for _, asset := range []struct{ path, key, mime string }{{output, videoKey, "video/mp4"}, {poster, posterKey, "image/jpeg"}} {
		if err = putProcessedMedia(ctx, client, m.s3, asset.path, asset.key, asset.mime); err != nil {
			return err
		}
	}
	info, err := os.Stat(output)
	if err != nil {
		return err
	}
	duration, err := probePostVideo(ctx, output)
	if err != nil {
		return err
	}
	public, err := m.s3.VideoObjectURL(videoKey)
	if err != nil {
		return err
	}
	thumbnail := m.s3.ObjectURL(posterKey)
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, job.provider); err != nil {
		return err
	}
	var permitted bool
	err = tx.QueryRow(ctx, `SELECT COALESCE((provider_current_benefits($1::uuid)->>'max_videos')::int>0,false) AND NOT uploads_paused AND $2::double precision<=max_seconds FROM provider_video_launch_policy WHERE singleton`, job.provider, duration).Scan(&permitted)
	if err != nil {
		return err
	}
	if !permitted {
		return errors.New("allowance changed before publication")
	}
	tag, err := tx.Exec(ctx, `UPDATE provider_video_uploads SET state='complete',url=$3,thumbnail_url=$4,duration_seconds=$5,lease_until=NULL,error_code=NULL WHERE key=$1 AND claim_token=$2::uuid AND state='processing'`, job.key, job.token, public, thumbnail, duration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("stale media claim")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_post_video_assets(url,provider_id,duration_seconds,size_bytes) VALUES($1,$2::uuid,$3,$4)`, public, job.provider, duration, info.Size()); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Delete only the exact source object AFTER durable publication. Failed cleanup
	// is covered by the narrowly scoped pending-object lifecycle proposal.
	if target, e := m.s3.ObjectDeleteURL(job.key, time.Minute); e == nil {
		r, _ := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
		if response, e := client.Do(r); e == nil {
			response.Body.Close()
		}
	}
	return nil
}

func putProcessedMedia(ctx context.Context, client *http.Client, s3 *storage.S3, path, key, mime string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 || info.Size() > 16*1024*1024 {
		return errors.New("processed media exceeds output bound")
	}
	target, err := s3.PresignPut(key, mime, time.Minute)
	if err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, target, file)
	request.ContentLength = info.Size()
	request.Header.Set("Content-Type", mime)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("processed media upload failed")
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("processed media upload rejected")
	}
	return nil
}

type processingVideoProbe struct {
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Type        string `json:"codec_type"`
		Codec       string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		FPS         string `json:"avg_frame_rate"`
		PixelFormat string `json:"pix_fmt"`
	} `json:"streams"`
}

func inspectProcessingVideo(ctx context.Context, path string) (processingVideoProbe, error) {
	var probe processingVideoProbe
	var output videoProbeOutput
	command := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-f", "mov", "-show_entries", "format=duration,bit_rate:stream=codec_type,codec_name,width,height,avg_frame_rate,pix_fmt", "-of", "json", path)
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return probe, errors.New("invalid video input")
	}
	if err := json.Unmarshal(output.buffer.Bytes(), &probe); err != nil {
		return probe, errors.New("invalid video metadata")
	}
	return probe, nil
}
func rationalFPS(value string) float64 {
	parts := strings.Split(value, "/")
	n, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return math.Inf(1)
	}
	if len(parts) == 2 {
		d, e := strconv.ParseFloat(parts[1], 64)
		if e != nil || d <= 0 {
			return math.Inf(1)
		}
		n /= d
	}
	if !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 {
		return n
	}
	return math.Inf(1)
}
func processLaunchVideo(ctx context.Context, input, output, poster string, maxSeconds int) error {
	probe, err := inspectProcessingVideo(ctx, input)
	if err != nil {
		return err
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > float64(maxSeconds) {
		return errors.New("video duration exceeds plan")
	}
	compliant := true
	found := false
	for _, stream := range probe.Streams {
		if stream.Type == "video" {
			if found {
				compliant = false
			}
			found = true
			if stream.Width < 1 || stream.Height < 1 || int64(stream.Width)*int64(stream.Height) > 35000000 {
				return errors.New("video dimensions exceed decode bound")
			}
			if stream.Codec != "h264" || stream.PixelFormat != "yuv420p" || min(stream.Width, stream.Height) > 720 || max(stream.Width, stream.Height) > 1280 || rationalFPS(stream.FPS) > 30 {
				compliant = false
			}
		} else if stream.Type == "audio" && stream.Codec != "aac" {
			compliant = false
		}
	}
	if !found {
		return errors.New("video stream missing")
	}
	bitrate, _ := strconv.ParseInt(probe.Format.BitRate, 10, 64)
	if bitrate <= 0 || bitrate > 1800000 {
		compliant = false
	}
	args := []string{"-nostdin", "-v", "error", "-y", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file", "-f", "mov", "-i", input, "-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1"}
	if compliant {
		args = append(args, "-c", "copy")
	} else {
		filter := "scale=w='if(gte(iw,ih),min(1280,iw),min(720,iw))':h='if(gte(iw,ih),min(720,ih),min(1280,ih))':force_original_aspect_ratio=decrease:force_divisible_by=2,setsar=1,fps=30"
		args = append(args, "-vf", filter, "-c:v", "libx264", "-threads", "1", "-preset", "veryfast", "-crf", "28", "-maxrate", "1600k", "-bufsize", "3200k", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "96k", "-ac", "2")
	}
	args = append(args, "-movflags", "+faststart", output)
	command := exec.CommandContext(ctx, "ffmpeg", args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = 5 * time.Second
	if err = command.Run(); err != nil {
		return errors.New("video encoding failed")
	}
	encoded, err := inspectProcessingVideo(ctx, output)
	if err != nil {
		return err
	}
	for _, stream := range encoded.Streams {
		if stream.Type == "video" && (stream.Codec != "h264" || min(stream.Width, stream.Height) > 720 || max(stream.Width, stream.Height) > 1280 || rationalFPS(stream.FPS) > 30.01) {
			return errors.New("invalid encoded video")
		}
	}
	if _, err = probePostVideo(ctx, output); err != nil {
		return err
	}
	// Decode the generated output before publication, rather than trusting a successful encoder exit.
	command = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-threads", "1", "-i", output, "-f", "null", "-")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = 5 * time.Second
	if err = command.Run(); err != nil {
		return errors.New("encoded video is not playable")
	}
	command = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-threads", "1", "-filter_threads", "1", "-i", output, "-frames:v", "1", "-vf", "scale=w='min(400,iw)':h='min(400,ih)':force_original_aspect_ratio=decrease", "-map_metadata", "-1", poster)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = 5 * time.Second
	if err = command.Run(); err != nil {
		return fmt.Errorf("video thumbnail failed")
	}
	return nil
}

func (m *ProviderMarketplaceController) ListProviderVideos(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.UserContext(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	rows, err := m.db.Query(c.UserContext(), `SELECT key,state,COALESCE(url,''),COALESCE(thumbnail_url,''),COALESCE(duration_seconds,0) FROM provider_video_uploads WHERE provider_id=$1::uuid AND created_at>=clock_timestamp()-interval '7 days' ORDER BY created_at DESC,key LIMIT 10`, provider)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var key, state, url, thumbnail string
		var duration float64
		if err = rows.Scan(&key, &state, &url, &thumbnail, &duration); err != nil {
			return fiber.ErrServiceUnavailable
		}
		items = append(items, fiber.Map{"key": key, "status": state, "url": url, "thumbnail_url": thumbnail, "duration_seconds": duration})
	}
	if err = rows.Err(); err != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items})
}

func (o *OpsController) VideoQueueHealth(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
	defer cancel()
	var queued, processing, failed, completed int64
	var oldest float64
	var active, paused bool
	err := o.db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state='processing'),count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='complete'),COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-MIN(created_at) FILTER(WHERE state IN('queued','processing'))),0) FROM provider_video_uploads WHERE processing_mode='background'`).Scan(&queued, &processing, &failed, &completed, &oldest)
	if err != nil {
		return fiber.NewError(503, "Video queue health unavailable.")
	}
	if err = o.db.QueryRow(ctx, `SELECT COALESCE(worker_lease_until>clock_timestamp(),false),uploads_paused FROM provider_video_launch_policy WHERE singleton`).Scan(&active, &paused); err != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"queued": queued, "processing": processing, "failed": failed, "completed": completed, "oldest_seconds": oldest, "encoder_lease_active": active, "uploads_paused": paused, "attention_required": failed > 0 || oldest > 120})
}
