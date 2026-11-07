package controllers

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Short provider-row transaction reserves both pending bytes and a daily upload.
// No network, file download or video inspection happens while this lock is held.
func (m *ProviderMarketplaceController) reserveProviderVideo(ctx context.Context, provider, key string, size int64) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, provider); err != nil {
		return fiber.ErrServiceUnavailable
	}
	var allowed int
	var paused bool
	var maxFile, maxStored int64
	var daily, pending int
	err = tx.QueryRow(ctx, `SELECT (provider_current_benefits($1::uuid)->>'max_videos')::int,p.uploads_paused,p.max_upload_bytes,p.max_provider_stored_bytes,p.daily_upload_limit,p.pending_upload_limit FROM provider_video_launch_policy p WHERE singleton`, provider).Scan(&allowed, &paused, &maxFile, &maxStored, &daily, &pending)
	if err != nil {
		return fiber.NewError(503, "Video limits could not be checked.")
	}
	if paused {
		return fiber.NewError(503, "Video uploads are temporarily paused.")
	}
	if allowed == 0 {
		return fiber.NewError(403, "New video uploads require Gold.")
	}
	if size <= 0 || size > maxFile {
		return fiber.NewError(422, "Supply a valid video file_size within your current upload allowance.")
	}
	var dailyUsed, pendingUsed int
	var occupied int64
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_video_uploads WHERE provider_id=$1::uuid AND created_at>=clock_timestamp()-interval '24 hours'),(SELECT count(*) FROM provider_video_uploads WHERE provider_id=$1::uuid AND state<>'complete' AND (expires_at>clock_timestamp() OR lease_until>clock_timestamp())),COALESCE((SELECT sum(size_bytes) FROM provider_post_video_assets WHERE provider_id=$1::uuid),0)+COALESCE((SELECT sum(size_bytes) FROM provider_video_uploads WHERE provider_id=$1::uuid AND state<>'complete' AND (expires_at>clock_timestamp() OR lease_until>clock_timestamp())),0)`, provider).Scan(&dailyUsed, &pendingUsed, &occupied)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	if dailyUsed >= daily || pendingUsed >= pending || occupied+size > maxStored {
		return fiber.NewError(429, "Your video upload or storage allowance is full. Retry later or remove unused media through support.")
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_video_uploads(key,provider_id,asset_id,size_bytes) VALUES($1,$2::uuid,$3::uuid,$4)`, key, provider, uuid.NewString(), size)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	return tx.Commit(ctx)
}

type providerVideoTicket struct {
	background   bool
	assetID, url string
	size         int64
	duration     float64
	complete     bool
}

func (m *ProviderMarketplaceController) claimProviderVideo(ctx context.Context, provider, key string) (providerVideoTicket, error) {
	var ticket providerVideoTicket
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return ticket, fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(ctx)
	var state string
	var expired, leased bool
	err = tx.QueryRow(ctx, `SELECT asset_id::text,size_bytes,state,COALESCE(url,''),COALESCE(duration_seconds,0),expires_at<=clock_timestamp(),COALESCE(lease_until>clock_timestamp(),false),COALESCE(to_jsonb(provider_video_uploads)->>'processing_mode','inline')='background' FROM provider_video_uploads WHERE key=$1 AND provider_id=$2::uuid FOR UPDATE`, key, provider).Scan(&ticket.assetID, &ticket.size, &state, &ticket.url, &ticket.duration, &expired, &leased, &ticket.background)
	if err == pgx.ErrNoRows {
		return ticket, fiber.NewError(422, "Request a new video upload authorization.")
	}
	if err != nil {
		return ticket, fiber.ErrServiceUnavailable
	}
	if state == "complete" {
		ticket.complete = true
		return ticket, nil
	}
	if ticket.background {
		return ticket, nil
	}
	if leased {
		return ticket, fiber.NewError(503, "This video is already being checked. Retry later.")
	}
	if expired {
		return ticket, fiber.NewError(422, "The video upload authorization expired. Upload again.")
	}
	_, err = tx.Exec(ctx, `UPDATE provider_video_uploads SET state='processing',lease_until=clock_timestamp()+interval '3 minutes' WHERE key=$1`, key)
	if err != nil {
		return ticket, fiber.ErrServiceUnavailable
	}
	return ticket, tx.Commit(ctx)
}
func (m *ProviderMarketplaceController) releaseProviderVideo(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = m.db.Exec(ctx, `UPDATE provider_video_uploads SET state='pending',lease_until=NULL WHERE key=$1 AND state='processing'`, key)
}
func (m *ProviderMarketplaceController) finishProviderVideo(ctx context.Context, provider, key, url string, duration float64, size int64) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock in the same order as reservation creation, then recheck the live plan.
	if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, provider); err != nil {
		return err
	}
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT COALESCE((provider_current_benefits($1::uuid)->>'max_videos')::int>0,false) AND NOT uploads_paused AND $2::double precision<=max_seconds AND $3<=max_upload_bytes FROM provider_video_launch_policy WHERE singleton`, provider, duration, size).Scan(&authorized)
	if err != nil {
		return err
	}
	if !authorized {
		return fiber.NewError(409, "Your video allowance changed. This video was not published.")
	}
	tag, err := tx.Exec(ctx, `UPDATE provider_video_uploads SET state='complete',url=$3,duration_seconds=$4,lease_until=NULL WHERE key=$1 AND provider_id=$2::uuid AND state='processing' AND size_bytes=$5`, key, provider, url, duration, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fiber.NewError(409, "Video completion state changed. Retry.")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_post_video_assets(url,provider_id,duration_seconds,size_bytes) VALUES($1,$2::uuid,$3,$4)`, url, provider, duration, size); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Generated object key is stable for retries and is never signed for client PUT.
func providerVerifiedVideoKey(provider string, ticket providerVideoTicket) string {
	return "user-uploads/providers/" + provider + "/video/" + ticket.assetID + ".mp4"
}
