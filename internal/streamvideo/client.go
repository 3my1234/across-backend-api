// Package streamvideo is a minimal Cloudflare Stream client for provider videos.
// Phones upload directly to Stream through one-time tus URLs; this package never
// receives video bytes. The API token is sent only to api.cloudflare.com and is
// never logged or returned.
package streamvideo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound    = errors.New("stream video not found")
	ErrUnavailable = errors.New("video service unavailable")
	uidPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type Client struct {
	AccountID string
	Token     string
	BaseURL   string // https://api.cloudflare.com/client/v4 unless overridden in tests
	HTTP      *http.Client

	mu           sync.Mutex
	usage        Usage
	usageFetched time.Time
}

type Usage struct {
	StoredMinutes float64
	LimitMinutes  float64
}

type Video struct {
	UID          string
	Ready        bool
	State        string // pendingupload, downloading, queued, inprogress, ready, error
	ErrorReason  string
	Duration     float64
	Size         int64
	HLS          string
	Thumbnail    string
	Creator      string
}

func New(accountID, token string) *Client {
	return &Client{AccountID: accountID, Token: token, BaseURL: "https://api.cloudflare.com/client/v4", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func ValidUID(uid string) bool { return uidPattern.MatchString(uid) }

func (c *Client) request(ctx context.Context, method, path string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+"/accounts/"+c.AccountID+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	return res, nil
}

func b64(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }

// CreateUpload reserves a one-time resumable (tus) upload URL. Stream rejects any
// upload longer than maxSeconds, so plan duration is enforced before encoding.
func (c *Client) CreateUpload(ctx context.Context, uploadLength int64, maxSeconds int, creator, name string, expires time.Time) (uploadURL, uid string, err error) {
	meta := []string{
		"maxDurationSeconds " + b64(strconv.Itoa(maxSeconds)),
		"expiry " + b64(expires.UTC().Format(time.RFC3339)),
		"name " + b64(name),
	}
	res, err := c.request(ctx, http.MethodPost, "/stream?direct_user=true", map[string]string{
		"Tus-Resumable":   "1.0.0",
		"Upload-Length":   strconv.FormatInt(uploadLength, 10),
		"Upload-Metadata": strings.Join(meta, ","),
		"Upload-Creator":  creator,
	})
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%w: create upload status %d", ErrUnavailable, res.StatusCode)
	}
	uploadURL, uid = res.Header.Get("Location"), res.Header.Get("Stream-Media-Id")
	if !strings.HasPrefix(uploadURL, "https://") || !ValidUID(uid) {
		return "", "", fmt.Errorf("%w: incomplete upload response", ErrUnavailable)
	}
	return uploadURL, uid, nil
}

type envelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
}

func (c *Client) Video(ctx context.Context, uid string) (Video, error) {
	if !ValidUID(uid) {
		return Video{}, ErrNotFound
	}
	res, err := c.request(ctx, http.MethodGet, "/stream/"+uid, nil)
	if err != nil {
		return Video{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Video{}, ErrNotFound
	}
	var body envelope
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&body) != nil || !body.Success {
		return Video{}, fmt.Errorf("%w: video status %d", ErrUnavailable, res.StatusCode)
	}
	var raw struct {
		UID           string  `json:"uid"`
		ReadyToStream bool    `json:"readyToStream"`
		Duration      float64 `json:"duration"`
		Size          int64   `json:"size"`
		Thumbnail     string  `json:"thumbnail"`
		Creator       string  `json:"creator"`
		Status        struct {
			State           string `json:"state"`
			ErrorReasonText string `json:"errorReasonText"`
		} `json:"status"`
		Playback struct {
			HLS string `json:"hls"`
		} `json:"playback"`
	}
	if json.Unmarshal(body.Result, &raw) != nil || raw.UID != uid {
		return Video{}, fmt.Errorf("%w: malformed video", ErrUnavailable)
	}
	return Video{UID: raw.UID, Ready: raw.ReadyToStream, State: raw.Status.State, ErrorReason: raw.Status.ErrorReasonText, Duration: raw.Duration, Size: raw.Size, HLS: raw.Playback.HLS, Thumbnail: raw.Thumbnail, Creator: raw.Creator}, nil
}

// Delete is idempotent: a video that no longer exists counts as deleted.
func (c *Client) Delete(ctx context.Context, uid string) error {
	if !ValidUID(uid) {
		return ErrNotFound
	}
	res, err := c.request(ctx, http.MethodDelete, "/stream/"+uid, nil)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("%w: delete status %d", ErrUnavailable, res.StatusCode)
}

// StorageUsage is cached for a minute; every upload checks it, Cloudflare need not.
func (c *Client) StorageUsage(ctx context.Context) (Usage, error) {
	c.mu.Lock()
	if !c.usageFetched.IsZero() && time.Since(c.usageFetched) < time.Minute {
		u := c.usage
		c.mu.Unlock()
		return u, nil
	}
	c.mu.Unlock()
	res, err := c.request(ctx, http.MethodGet, "/stream/storage-usage", nil)
	if err != nil {
		return Usage{}, err
	}
	defer res.Body.Close()
	var body envelope
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&body) != nil || !body.Success {
		return Usage{}, fmt.Errorf("%w: storage status %d", ErrUnavailable, res.StatusCode)
	}
	var raw struct {
		Total float64 `json:"totalStorageMinutes"`
		Limit float64 `json:"totalStorageMinutesLimit"`
	}
	if json.Unmarshal(body.Result, &raw) != nil {
		return Usage{}, fmt.Errorf("%w: malformed storage usage", ErrUnavailable)
	}
	u := Usage{StoredMinutes: raw.Total, LimitMinutes: raw.Limit}
	c.mu.Lock()
	c.usage, c.usageFetched = u, time.Now()
	c.mu.Unlock()
	return u, nil
}

// Forget drops the cached usage after an upload is reserved or a video deleted.
func (c *Client) Forget() { c.mu.Lock(); c.usageFetched = time.Time{}; c.mu.Unlock() }
