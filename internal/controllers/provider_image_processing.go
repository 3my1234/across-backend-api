package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxProviderImageBytes = 10 * 1024 * 1024

func (m *ProviderMarketplaceController) reserveProviderImage(ctx context.Context, provider, key, scope, mime string, size int64) error {
	if size <= 0 || size > maxProviderImageBytes {
		return fiber.NewError(422, "Choose an image up to10MB and provide its file_size.")
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, provider); err != nil {
		return fiber.ErrServiceUnavailable
	}
	var daily, pending int
	var maximum int64
	var paused bool
	if err = tx.QueryRow(ctx, `SELECT image_daily_limit,image_pending_limit,image_stored_limit_bytes,uploads_paused FROM provider_video_launch_policy WHERE singleton`).Scan(&daily, &pending, &maximum, &paused); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if paused {
		return fiber.NewError(503, "New media uploads are temporarily paused.")
	}
	var used, waiting int
	var occupied int64
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM provider_image_uploads WHERE provider_id=$1::uuid AND created_at>=clock_timestamp()-interval '24 hours'),(SELECT count(*) FROM provider_image_uploads WHERE provider_id=$1::uuid AND state<>'complete' AND (expires_at>clock_timestamp() OR lease_until>clock_timestamp())),COALESCE((SELECT sum(size_bytes) FROM provider_image_assets WHERE provider_id=$1::uuid),0)+COALESCE((SELECT count(*)*16777216 FROM provider_image_uploads WHERE provider_id=$1::uuid AND state<>'complete' AND (expires_at>clock_timestamp() OR lease_until>clock_timestamp())),0)`, provider).Scan(&used, &waiting, &occupied)
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	if used >= daily || waiting >= pending || occupied+16777216 > maximum {
		return fiber.NewError(429, "Your image upload or storage allowance is full. Please retry later.")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_image_uploads(key,provider_id,asset_id,scope,mime_type,size_bytes) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6)`, key, provider, uuid.NewString(), scope, mime, size); err != nil {
		return fiber.ErrServiceUnavailable
	}
	return tx.Commit(ctx)
}
func (m *ProviderMarketplaceController) CompleteProviderImage(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.UserContext(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	var payload struct {
		Key string `json:"key"`
	}
	if c.BodyParser(&payload) != nil || !strings.HasPrefix(payload.Key, "private/image-pending/"+provider+"/") {
		return fiber.ErrBadRequest
	}
	return m.queueProviderImage(c, provider, payload.Key)
}
func (m *ProviderMarketplaceController) ProviderImageStatus(c *fiber.Ctx) error {
	user, _ := c.Locals("user_id").(string)
	provider, _, err := m.providerForUser(c.UserContext(), user)
	if err != nil {
		return fiber.ErrForbidden
	}
	return m.imageStatus(c, provider, c.Query("key"))
}
func (m *ProviderMarketplaceController) imageStatus(c *fiber.Ctx, provider, key string) error {
	var state string
	var url, thumbnail, fallback *string
	err := m.db.QueryRow(c.UserContext(), `SELECT state,url,thumbnail_url,fallback_url FROM provider_image_uploads WHERE key=$1 AND provider_id=$2::uuid`, key, provider).Scan(&state, &url, &thumbnail, &fallback)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	c.Set("Cache-Control", "private, no-store")
	if state == "failed" {
		return fiber.NewError(422, "This image could not be prepared. Please choose another image.")
	}
	result := fiber.Map{"key": key, "status": state}
	if state == "complete" {
		result["url"] = url
		result["thumbnail_url"] = thumbnail
		result["fallback_url"] = fallback
		return c.JSON(result)
	}
	c.Set("Retry-After", "2")
	return c.Status(202).JSON(result)
}
func (m *ProviderMarketplaceController) queueProviderImage(c *fiber.Ctx, provider, key string) error {
	tx, err := m.db.Begin(c.UserContext())
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(c.UserContext())
	var state string
	var expired bool
	err = tx.QueryRow(c.UserContext(), `SELECT state,expires_at<=clock_timestamp() FROM provider_image_uploads WHERE key=$1 AND provider_id=$2::uuid FOR UPDATE`, key, provider).Scan(&state, &expired)
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	if state == "complete" || state == "queued" || state == "processing" {
		tx.Rollback(c.UserContext())
		return m.imageStatus(c, provider, key)
	}
	if state == "failed" {
		return fiber.NewError(422, "This image could not be processed. Please upload it again.")
	}
	if expired {
		return fiber.NewError(422, "The upload authorization expired. Please upload again.")
	}
	var paused bool
	var limit int
	if err = tx.QueryRow(c.UserContext(), `SELECT uploads_paused,image_queue_limit FROM provider_video_launch_policy WHERE singleton FOR UPDATE`).Scan(&paused, &limit); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if paused {
		return fiber.NewError(503, "New image processing is temporarily paused.")
	}
	var allowed bool
	if err = tx.QueryRow(c.UserContext(), `SELECT COALESCE((provider_current_benefits($1::uuid)->>'max_images')::int>0,false)`, provider).Scan(&allowed); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if !allowed {
		return fiber.NewError(403, "New images require your current plan to allow image uploads.")
	}

	var count int
	if err = tx.QueryRow(c.UserContext(), `SELECT count(*) FROM provider_image_uploads WHERE processing_mode='background' AND state IN('queued','processing')`).Scan(&count); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if count >= limit {
		c.Set("Retry-After", "30")
		return fiber.NewError(503, "Image processing is busy. Please retry later.")
	}
	if _, err = tx.Exec(c.UserContext(), `UPDATE provider_image_uploads SET state='queued',processing_mode='background',available_at=clock_timestamp() WHERE key=$1`, key); err != nil {
		return fiber.ErrServiceUnavailable
	}
	if err = tx.Commit(c.UserContext()); err != nil {
		return fiber.ErrServiceUnavailable
	}
	return m.imageStatus(c, provider, key)
}

// One media worker slot is shared with video encodes; all claims are short transactions.
func RunImageMediaJob(parent context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) (result error) {
	if !cfg.AsyncImageProcessing {
		return nil
	}
	job, found, err := claimMediaJob(parent, db, "provider_image_uploads")
	if err != nil || !found {
		return err
	}
	phase := "input_download"
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if result != nil {
			_, _ = db.Exec(ctx, `UPDATE provider_image_uploads SET state=CASE WHEN attempts>=3 THEN 'failed' ELSE 'queued' END,error_code=$3,lease_until=NULL,available_at=clock_timestamp()+attempts*interval '10 seconds' WHERE key=$1 AND claim_token=$2::uuid AND state='processing'`, job.key, job.token, phase)
		}
		_, _ = db.Exec(ctx, `UPDATE provider_video_launch_policy SET worker_token=NULL,worker_lease_until=NULL WHERE singleton AND worker_token=$1::uuid`, job.token)
	}()
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	s3 := storage.NewS3(cfg)
	var scope, mime string
	var quality int
	if err = db.QueryRow(ctx, `SELECT u.scope,u.mime_type,p.image_quality FROM provider_image_uploads u CROSS JOIN provider_video_launch_policy p WHERE u.key=$1 AND p.singleton`, job.key).Scan(&scope, &mime, &quality); err != nil {
		return err
	}
	signed, err := s3.ObjectGetURL(job.key, time.Minute)
	if err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("image input unavailable")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return errors.New("image input unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderImageBytes+1))
	response.Body.Close()
	if err != nil || int64(len(data)) != job.size || len(data) > maxProviderImageBytes {
		return errors.New("image input size mismatch")
	}
	directory, err := os.MkdirTemp("", "atl-image-job-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	phase = "verification_and_resizing"
	variants, width, height, err := processProviderImage(ctx, directory, data, mime, scope, quality)
	if err != nil {
		return err
	}
	phase = "output_upload"
	urls := map[string]string{}
	var storedBytes int64
	for _, variant := range variants {
		info, e := os.Stat(variant.path)
		if e != nil {
			return e
		}
		storedBytes += info.Size()
	}
	if storedBytes > 16*1024*1024 {
		return errors.New("image variants exceed storage bound")
	}
	for _, variant := range variants {
		extension := variant.extension
		if variant.kind == "display" {
			extension = variants[2].extension + extension
		}
		key := "user-uploads/providers/" + job.provider + "/" + scope + "/" + job.token + "." + variant.kind + extension
		if err = putProcessedMedia(ctx, client, s3, variant.path, key, variant.mime); err != nil {
			return err
		}
		urls[variant.kind] = s3.ObjectURL(key)
	}
	phase = "atomic_publication"
	var paused bool
	if err = db.QueryRow(ctx, `SELECT uploads_paused FROM provider_video_launch_policy WHERE singleton`).Scan(&paused); err != nil || paused {
		return errors.New("image publication paused")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE provider_image_uploads SET state='complete',url=$3,thumbnail_url=$4,fallback_url=$5,lease_until=NULL,error_code=NULL WHERE key=$1 AND claim_token=$2::uuid AND state='processing'`, job.key, job.token, urls["display"], urls["thumbnail"], urls["fallback"])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("stale image claim")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_image_assets(url,provider_id,thumbnail_url,fallback_url,width,height,size_bytes) VALUES($1,$2::uuid,$3,$4,$5,$6,$7)`, urls["display"], job.provider, urls["thumbnail"], urls["fallback"], width, height, storedBytes); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if target, e := s3.ObjectDeleteURL(job.key, time.Minute); e == nil {
		r, _ := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
		if response, e := client.Do(r); e == nil {
			response.Body.Close()
		}
	}
	return nil
}

type providerImageVariant struct{ path, kind, extension, mime string }

func processProviderImage(ctx context.Context, directory string, data []byte, mime, scope string, quality int) ([]providerImageVariant, int, int, error) {
	if len(data) == 0 || len(data) > maxProviderImageBytes || quality < 50 || quality > 90 {
		return nil, 0, 0, errors.New("image size or quality outside bounds")
	}
	metadata, format, err := image.DecodeConfig(bytes.NewReader(data))
	expected := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}
	if err != nil || expected[format] == "" || expected[format] != mime {
		return nil, 0, 0, errors.New("image signature does not match upload type")
	}
	if metadata.Width < 1 || metadata.Height < 1 || metadata.Width > 16384 || metadata.Height > 16384 || int64(metadata.Width)*int64(metadata.Height) > 40000000 {
		return nil, 0, 0, errors.New("image pixel dimensions exceed decode bound")
	}
	input := filepath.Join(directory, "input."+format)
	if err = os.WriteFile(input, data, 0600); err != nil {
		return nil, 0, 0, err
	}
	maxSide := 1600
	if scope == "logo" {
		maxSide = 512
	}
	fallbackExtension, fallbackMime := ".png", "image/png"
	if format == "jpeg" {
		fallbackExtension = ".jpg"
		fallbackMime = "image/jpeg"
	}
	variants := []providerImageVariant{{filepath.Join(directory, "display.webp"), "display", ".webp", "image/webp"}, {filepath.Join(directory, "thumbnail.webp"), "thumbnail", ".webp", "image/webp"}, {filepath.Join(directory, "fallback"+fallbackExtension), "fallback", fallbackExtension, fallbackMime}}
	orientation := providerImageOrientation(data)
	transforms := map[int]string{2: "hflip,", 3: "hflip,vflip,", 4: "vflip,", 5: "transpose=0,", 6: "transpose=1,", 7: "transpose=3,", 8: "transpose=2,"}
	for _, variant := range variants {
		side := maxSide
		if variant.kind == "thumbnail" {
			side = min(maxSide, 400)
		}
		filter := transforms[orientation] + fmt.Sprintf("scale=w='min(%d,iw)':h='min(%d,ih)':force_original_aspect_ratio=decrease", side, side)
		args := []string{"-nostdin", "-v", "error", "-y", "-threads", "1", "-filter_threads", "1", "-noautorotate", "-protocol_whitelist", "file", "-i", input, "-frames:v", "1", "-map_metadata", "-1", "-vf", filter}
		if variant.extension == ".webp" {
			args = append(args, "-c:v", "libwebp", "-quality", strconv.Itoa(quality), "-compression_level", "4")
		} else if variant.extension == ".jpg" {
			args = append(args, "-q:v", "3")
		}
		args = append(args, variant.path)
		command := exec.CommandContext(ctx, "ffmpeg", args...)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		command.WaitDelay = 5 * time.Second
		if err = command.Run(); err != nil {
			return nil, 0, 0, errors.New("image conversion failed")
		}
	}
	var width, height int
	for _, variant := range variants {
		output, openErr := os.Open(variant.path)
		if openErr != nil {
			return nil, 0, 0, openErr
		}
		processed, _, decodeErr := image.DecodeConfig(output)
		output.Close()
		side := maxSide
		if variant.kind == "thumbnail" {
			side = min(maxSide, 400)
		}
		if decodeErr != nil || processed.Width < 1 || processed.Height < 1 || processed.Width > side || processed.Height > side {
			return nil, 0, 0, errors.New("image output invalid")
		}
		if variant.kind == "display" {
			width, height = processed.Width, processed.Height
		}
	}
	return variants, width, height, nil
}

// Read only the bounded TIFF orientation tag. JPEG APP1, PNG eXIf and WebP EXIF
// are handled without decoding pixels or trusting offsets outside their chunk.
func providerImageOrientation(data []byte) int {
	var tiff []byte
	if len(data) > 4 && data[0] == 0xff && data[1] == 0xd8 {
		for i := 2; i+4 <= len(data); {
			if data[i] != 0xff {
				break
			}
			marker := data[i+1]
			if marker == 0xda || marker == 0xd9 {
				break
			}
			n := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
			if n < 2 || i+2+n > len(data) {
				break
			}
			chunk := data[i+4 : i+2+n]
			if marker == 0xe1 && bytes.HasPrefix(chunk, []byte("Exif\x00\x00")) {
				tiff = chunk[6:]
				break
			}
			i += 2 + n
		}
	} else if len(data) > 8 && bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		for i := 8; i+12 <= len(data); {
			n := int(binary.BigEndian.Uint32(data[i : i+4]))
			if n < 0 || n > len(data)-i-12 {
				break
			}
			if string(data[i+4:i+8]) == "eXIf" {
				tiff = data[i+8 : i+8+n]
				break
			}
			i += n + 12
		}
	} else if len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		for i := 12; i+8 <= len(data); {
			n := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
			if n < 0 || n > len(data)-i-8 {
				break
			}
			if string(data[i:i+4]) == "EXIF" {
				tiff = data[i+8 : i+8+n]
				if bytes.HasPrefix(tiff, []byte("Exif\x00\x00")) {
					tiff = tiff[6:]
				}
				break
			}
			i += 8 + n + n%2
		}
	}
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	if string(tiff[:2]) == "II" {
		order = binary.LittleEndian
	} else if string(tiff[:2]) == "MM" {
		order = binary.BigEndian
	} else {
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset > len(tiff)-2 {
		return 1
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	if count > 4096 {
		return 1
	}
	for i := 0; i < count; i++ {
		pos := offset + 2 + i*12
		if pos+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[pos:pos+2]) == 274 && order.Uint16(tiff[pos+2:pos+4]) == 3 && order.Uint32(tiff[pos+4:pos+8]) == 1 {
			value := int(order.Uint16(tiff[pos+8 : pos+10]))
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}
