package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const labMediaHost = "capacity-lab-media.s3.us-east-1.amazonaws.com"

func requireLabMediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != labMediaHost || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("non-lab object origin rejected")
	}
	return nil
}

// Generate valid portable images, not dummy bytes labelled image/jpeg. Dimensions
// and hashes are synthetic; these are never real customer media.
func syntheticJPEG(width int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, width))
	rng := rand.New(rand.NewSource(42))
	for y := 0; y < width; y++ {
		for x := 0; x < width; x++ {
			offset := img.PixOffset(x, y)
			img.Pix[offset] = byte(rng.Intn(256))
			img.Pix[offset+1] = byte(rng.Intn(256))
			img.Pix[offset+2] = byte(rng.Intn(256))
			img.Pix[offset+3] = 255
		}
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

func sessionMediaCheck(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if err := requireSessionLab(ctx, db); err != nil {
		return err
	}
	var runID, buyer, provider, conversation string
	if err := db.QueryRow(ctx, `SELECT r.run_id::text,a.buyer::text,a.provider_user::text,a.conversation::text FROM capacity_lab_session_run r CROSS JOIN capacity_lab_actors a ORDER BY a.slot LIMIT 1`).Scan(&runID, &buyer, &provider, &conversation); err != nil {
		return err
	}
	c := client()
	defer c.CloseIdleConnections()
	call := func(method, path, token string, body any, want int) (map[string]any, time.Duration, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		req, err := http.NewRequestWithContext(ctx, method, origin+"/api/v1"+path, bytes.NewReader(raw))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		start := time.Now()
		res, err := c.Do(req)
		if err != nil {
			return nil, time.Since(start), fmt.Errorf("media API %s transport failed", method)
		}
		defer res.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil {
			return nil, time.Since(start), err
		}
		if res.StatusCode != want {
			return nil, time.Since(start), fmt.Errorf("media API %s %s status=%d expected=%d", method, path, res.StatusCode, want)
		}
		result := map[string]any{}
		if want < 400 && json.Unmarshal(payload, &result) != nil {
			return nil, time.Since(start), fmt.Errorf("media API response is not JSON")
		}
		return result, time.Since(start), nil
	}
	// Only this fixed synthetic bucket can be created. The actual production
	// signer is used; the in-network object server independently verifies SigV4.
	s3 := storage.NewS3(config.Config{AWSRegion: "us-east-1", AWSAccessKeyID: "lab-access-key-only", AWSSecretAccessKey: "lab-secret-key-only-never-production", S3BucketName: "capacity-lab-media"})
	create, err := s3.PresignPut("", "application/octet-stream", time.Minute)
	if err != nil {
		return err
	}
	if err = requireLabMediaURL(create); err != nil {
		return err
	}
	request, _ := http.NewRequestWithContext(ctx, "PUT", create, nil)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := c.Do(request)
	if err != nil {
		return fmt.Errorf("isolated bucket creation transport failed")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("isolated bucket creation status=%d", response.StatusCode)
	}
	delivered := 0
	totalBytes := 0
	for _, width := range []int{256, 1024, 2048} {
		imageBytes, err := syntheticJPEG(width)
		if err != nil {
			return err
		}
		if len(imageBytes) > 5<<20 {
			return fmt.Errorf("synthetic photo exceeded client limit")
		}
		for _, direction := range []struct{ sender, peer, sendPath, readPath, label string }{{buyer, provider, "/marketplace/conversations/", "/providers/me/conversations/", "customer_to_provider"}, {provider, buyer, "/providers/me/conversations/", "/marketplace/conversations/", "provider_to_customer"}} {
			token, _, err := auth.Sign(direction.sender, secret, time.Hour)
			if err != nil {
				return err
			}
			peerToken, _, err := auth.Sign(direction.peer, secret, time.Hour)
			if err != nil {
				return err
			}
			signed, presignTime, err := call("POST", "/marketplace/chat-images/presign", token, map[string]any{"filename": "synthetic.jpg", "mime_type": "image/jpeg", "size": len(imageBytes)}, 200)
			if err != nil {
				return err
			}
			uploadURL, _ := signed["upload_url"].(string)
			key, _ := signed["key"].(string)
			if err = requireLabMediaURL(uploadURL); err != nil {
				return err
			}
			if !strings.HasPrefix(key, "user-uploads/private-chat/"+direction.sender+"/") {
				return fmt.Errorf("unexpected private image key")
			}
			// API attachment validation must reject an object not yet transferred.
			message := map[string]any{"message": "media-" + runID + ":" + uuid.NewString(), "media_keys": []string{key}, "client_message_id": uuid.NewString()}
			if _, _, err = call("POST", direction.sendPath+conversation+"/messages", token, message, 422); err != nil {
				return fmt.Errorf("incomplete upload guard: %w", err)
			}
			put, _ := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(imageBytes))
			put.Header.Set("Content-Type", "image/jpeg")
			began := time.Now()
			res, err := c.Do(put)
			if err != nil {
				return fmt.Errorf("isolated photo PUT transport failed")
			}
			res.Body.Close()
			putTime := time.Since(began)
			if res.StatusCode != 200 {
				return fmt.Errorf("isolated photo PUT status=%d", res.StatusCode)
			}
			saved, attachTime, err := call("POST", direction.sendPath+conversation+"/messages", token, message, 201)
			if err != nil {
				return err
			}
			replay, _, err := call("POST", direction.sendPath+conversation+"/messages", token, message, 201)
			if err != nil || saved["id"] != replay["id"] {
				return fmt.Errorf("photo message replay duplicated or failed")
			}
			history, _, err := call("GET", direction.readPath+conversation+"/messages?limit=40&mark_read=false", peerToken, nil, 200)
			if err != nil {
				return err
			}
			var view string
			items, _ := history["items"].([]any)
			for _, entry := range items {
				item, _ := entry.(map[string]any)
				if item["id"] == saved["id"] {
					media, _ := item["media_urls"].([]any)
					if len(media) == 1 {
						view, _ = media[0].(string)
					}
				}
			}
			if err = requireLabMediaURL(view); err != nil {
				return fmt.Errorf("intended peer did not receive a safe photo URL")
			}
			get, _ := http.NewRequestWithContext(ctx, "GET", view, nil)
			began = time.Now()
			res, err = c.Do(get)
			if err != nil {
				return fmt.Errorf("isolated recipient download transport failed")
			}
			received, readErr := io.ReadAll(io.LimitReader(res.Body, 6<<20))
			res.Body.Close()
			getTime := time.Since(began)
			if readErr != nil || res.StatusCode != 200 || sha256.Sum256(received) != sha256.Sum256(imageBytes) {
				return fmt.Errorf("photo recipient bytes differ from upload")
			}
			unsigned, _ := url.Parse(view)
			unsigned.RawQuery = ""
			req, _ := http.NewRequestWithContext(ctx, "GET", unsigned.String(), nil)
			res, err = c.Do(req)
			if err != nil {
				return fmt.Errorf("unsigned object access probe failed")
			}
			res.Body.Close()
			if res.StatusCode != 403 {
				return fmt.Errorf("private object allowed unsigned access")
			}
			wrongOwner := map[string]any{"message": "not-owned", "media_keys": []string{key}, "client_message_id": uuid.NewString()}
			if _, _, err = call("POST", direction.readPath+conversation+"/messages", peerToken, wrongOwner, 422); err != nil {
				return fmt.Errorf("attachment ownership guard: %w", err)
			}
			delivered++
			totalBytes += len(imageBytes)
			if err = json.NewEncoder(out).Encode(map[string]any{"photo_case": direction.label, "width": width, "bytes": len(imageBytes), "presign_ms": float64(presignTime.Microseconds()) / 1000, "put_ms": float64(putTime.Microseconds()) / 1000, "attach_with_head_ms": float64(attachTime.Microseconds()) / 1000, "recipient_get_ms": float64(getTime.Microseconds()) / 1000, "sha256_equal": true, "idempotent_replay": true, "incomplete_and_wrong_owner_rejected": true, "unsigned_get_forbidden": true}); err != nil {
				return err
			}
		}
	}
	var stored int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM provider_conversation_messages WHERE body LIKE $1`, "media-"+runID+":%").Scan(&stored); err != nil {
		return err
	}
	if stored != delivered {
		return fmt.Errorf("photo confirmations do not match stored messages")
	}
	return json.NewEncoder(out).Encode(map[string]any{"media_roundtrip_passed": true, "photo_messages": delivered, "stored_messages": stored, "uploaded_bytes": totalBytes, "scope": "isolated TLS S3-compatible server; no AWS/mobile/browser capacity claim"})
}
