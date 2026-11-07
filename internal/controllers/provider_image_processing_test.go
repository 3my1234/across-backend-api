package controllers

import (
	"across/backend/internal/config"
	"bytes"
	"context"
	"encoding/binary"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/image/webp"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func imageMediaDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := durableVideoDB(t)
	if _, err := db.Exec(context.Background(), `ALTER TABLE provider_organizations ADD COLUMN banner_url text DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/113_durable_provider_images.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	return db
}
func exifJPEG(raw []byte, orientation int) []byte {
	tiff := make([]byte, 26)
	copy(tiff, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(tiff[8:], 1)
	binary.LittleEndian.PutUint16(tiff[10:], 274)
	binary.LittleEndian.PutUint16(tiff[12:], 3)
	binary.LittleEndian.PutUint32(tiff[14:], 1)
	binary.LittleEndian.PutUint16(tiff[18:], uint16(orientation))
	payload := append([]byte{'E', 'x', 'i', 'f', 0, 0}, tiff...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out = append(out, payload...)
	return append(out, raw[2:]...)
}
func TestProviderImageSignaturePixelBoundsAndExif(t *testing.T) {
	var source bytes.Buffer
	if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil); err != nil {
		t.Fatal(err)
	}
	for orientation := 1; orientation <= 8; orientation++ {
		if got := providerImageOrientation(exifJPEG(source.Bytes(), orientation)); got != orientation {
			t.Fatalf("orientation %d parsed as %d", orientation, got)
		}
	}
	for _, bad := range [][]byte{nil, []byte("Exif"), exifJPEG(source.Bytes(), 9)} {
		if providerImageOrientation(bad) != 1 {
			t.Fatal("unsafe EXIF default")
		}
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := processProviderImage(context.Background(), t.TempDir(), pngData.Bytes(), "image/jpeg", "listing", 80); err == nil {
		t.Fatal("forged content type accepted")
	}
	bomb := append([]byte(nil), source.Bytes()...)
	for i := 0; i+9 < len(bomb); i++ {
		if bomb[i] == 0xff && bomb[i+1] == 0xc0 {
			bomb[i+5] = 0xff
			bomb[i+6] = 0xff
			bomb[i+7] = 0xff
			bomb[i+8] = 0xff
			break
		}
	}
	if _, _, _, err := processProviderImage(context.Background(), t.TempDir(), bomb, "image/jpeg", "listing", 80); err == nil {
		t.Fatal("decompression dimensions accepted")
	}
}
func TestProviderImageQuotaPreservationAndOwnership(t *testing.T) {
	db := imageMediaDB(t)
	ctx := context.Background()
	_, provider, _ := tierProvider(t, db, "")
	_, other, _ := tierProvider(t, db, "")
	id := uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status,image_urls) VALUES($1,$2,'draft',ARRAY['https://legacy.test/published.jpg'])`, id, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_video_launch_policy SET image_processing_required=true,image_pending_limit=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET title='Preserved legacy media' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET image_urls=ARRAY['https://raw.test/unverified.jpg'] WHERE id=$1`, id); err == nil {
		t.Fatal("unprocessed new image attached")
	}
	owned := "https://assets.test/processed.webp"
	if _, err := db.Exec(ctx, `INSERT INTO provider_image_assets VALUES($1,$2,'thumb','fallback',16,16,100,now())`, owned, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET image_urls=ARRAY[$2] WHERE id=$1`, id, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_organizations SET logo_url=$2 WHERE id=$1`, other, owned); err == nil {
		t.Fatal("other provider used processed branding")
	}
	m := &ProviderMarketplaceController{db: db}
	if err := m.reserveProviderImage(ctx, provider, "one", "listing", "image/png", 100); err != nil {
		t.Fatal(err)
	}
	if err := m.reserveProviderImage(ctx, provider, "two", "listing", "image/png", 100); err == nil {
		t.Fatal("pending image count bypassed")
	}
	if _, _, err := claimMediaJob(ctx, db, "products"); err == nil {
		t.Fatal("arbitrary table accepted for queue claims")
	}
}
func TestProviderImageActualOrientationAlphaAndNoUpscale(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("CI requires actual image encoding")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 3200, 2000)), nil); err != nil {
		t.Fatal(err)
	}
	variants, width, height, err := processProviderImage(ctx, t.TempDir(), exifJPEG(photo.Bytes(), 6), "image/jpeg", "listing", 80)
	if err != nil || width != 1000 || height != 1600 {
		t.Fatal("orientation/resize failed", err, width, height)
	}
	for _, variant := range variants {
		data, e := os.ReadFile(variant.path)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(data, []byte{'E', 'x', 'i', 'f', 0, 0}) {
			t.Fatal("source EXIF retained")
		}
	}
	var transparent bytes.Buffer
	if err = png.Encode(&transparent, image.NewNRGBA(image.Rect(0, 0, 16, 8))); err != nil {
		t.Fatal(err)
	}
	variants, width, height, err = processProviderImage(ctx, t.TempDir(), transparent.Bytes(), "image/png", "logo", 80)
	if err != nil || width != 16 || height != 8 {
		t.Fatal("image upscaled", err, width, height)
	}
	file, e := os.Open(variants[0].path)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := webp.Decode(file)
	file.Close()
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha != 0 {
		t.Fatal("transparency lost")
	}
}
func TestProviderImageActualDurableWorkerAndVariants(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("CI requires actual durable image worker")
	}
	ctx := context.Background()
	db := imageMediaDB(t)
	_, provider, _ := tierProvider(t, db, "")
	photo := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	photo.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, photo); err != nil {
		t.Fatal(err)
	}
	m := &ProviderMarketplaceController{db: db}
	key := "private/image-pending/" + provider + "/image.png"
	if err := m.reserveProviderImage(ctx, provider, key, "logo", "image/png", int64(raw.Len())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_image_uploads SET state='queued' WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == "GET" {
			return &http.Response{StatusCode: 200, ContentLength: int64(raw.Len()), Body: io.NopCloser(bytes.NewReader(raw.Bytes())), Header: make(http.Header)}, nil
		}
		if r.Method == "PUT" {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Fatal(err)
			}
		} else if r.Method != "DELETE" {
			t.Fatal("unexpected object request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})}
	cfg := config.Config{AsyncImageProcessing: true, AWSRegion: "eu-north-1", AWSAccessKeyID: "synthetic", AWSSecretAccessKey: "synthetic", S3BucketName: "synthetic", AssetsCDNBase: "https://assets.example.test"}
	if err := RunImageMediaJob(ctx, db, cfg, client); err != nil {
		t.Fatal(err)
	}
	var state, url string
	var width, height, assets int
	if err := db.QueryRow(ctx, `SELECT u.state,a.url,a.width,a.height,(SELECT count(*) FROM provider_image_assets) FROM provider_image_uploads u JOIN provider_image_assets a ON a.url=u.url WHERE u.key=$1`, key).Scan(&state, &url, &width, &height, &assets); err != nil || state != "complete" || width != 32 || height != 16 || assets != 1 {
		t.Fatal("image publication failed", err, state, width, height, assets)
	}
	if filepath.Ext(url) != ".webp" {
		t.Fatal("display format not portable WebP")
	}
	if err := RunImageMediaJob(ctx, db, cfg, client); err != nil || calls != 5 {
		t.Fatal("image replay performed object work", err, calls)
	}
}
