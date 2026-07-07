package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"across/backend/internal/config"
)

type S3 struct {
	region    string
	accessKey string
	secretKey string
	bucket    string
	cdnBase   string
	publicURL string
}

func NewS3(cfg config.Config) *S3 {
	return &S3{
		region:    cfg.AWSRegion,
		accessKey: cfg.AWSAccessKeyID,
		secretKey: cfg.AWSSecretAccessKey,
		bucket:    cfg.S3BucketName,
		cdnBase:   strings.TrimRight(cfg.AssetsCDNBase, "/"),
		publicURL: strings.TrimRight(cfg.PublicBaseURL, "/"),
	}
}

func (s *S3) Configured() bool {
	return s.region != "" && s.accessKey != "" && s.secretKey != "" && s.bucket != ""
}

func (s *S3) PresignPut(key, contentType string, expires time.Duration) (string, error) {
	if !s.Configured() {
		return "", errors.New("s3 is not configured")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	credential := fmt.Sprintf("%s/%s", s.accessKey, credentialScope)

	u := &url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.s3.%s.amazonaws.com", s.bucket, s.region),
		Path:   "/" + encodeKeyPath(key),
	}
	q := u.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "content-type;host")
	u.RawQuery = canonicalQuery(q)

	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\n", contentType, u.Host)
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		u.EscapedPath(),
		u.RawQuery,
		canonicalHeaders,
		"content-type;host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hexSHA256(canonicalRequest),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(s.secretKey, dateStamp, s.region, "s3"), stringToSign))
	q.Set("X-Amz-Signature", signature)
	u.RawQuery = canonicalQuery(q)
	return u.String(), nil
}

func (s *S3) ObjectGetURL(key string, expires time.Duration) (string, error) {
	if !s.Configured() {
		return "", errors.New("s3 is not configured")
	}
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	credential := fmt.Sprintf("%s/%s", s.accessKey, credentialScope)
	u := &url.URL{Scheme: "https", Host: fmt.Sprintf("%s.s3.%s.amazonaws.com", s.bucket, s.region), Path: "/" + encodeKeyPath(key)}
	q := u.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", fmt.Sprintf("%d", int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	u.RawQuery = canonicalQuery(q)
	canonicalRequest := strings.Join([]string{http.MethodGet, u.EscapedPath(), u.RawQuery, "host:" + u.Host + "\n", "host", "UNSIGNED-PAYLOAD"}, "\n")
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, credentialScope, hexSHA256(canonicalRequest)}, "\n")
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(signingKey(s.secretKey, dateStamp, s.region, "s3"), stringToSign)))
	u.RawQuery = canonicalQuery(q)
	return u.String(), nil
}

func (s *S3) ObjectURL(key string) string {
	normalized := strings.TrimLeft(key, "/")
	if s.cdnBase != "" {
		return s.cdnBase + "/" + encodeKeyPath(normalized)
	}
	if s.publicURL != "" {
		return s.publicURL + "/api/v1/public/images/view/" + encodeKeyPath(normalized)
	}
	if s.bucket != "" && s.region != "" {
		return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, encodeKeyPath(normalized))
	}
	return normalized
}

func (s *S3) DeleteObject(key string) error {
	if !s.Configured() {
		return errors.New("s3 is not configured")
	}
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" {
		return nil
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	credential := fmt.Sprintf("%s/%s", s.accessKey, credentialScope)
	u := &url.URL{
		Scheme: "https",
		Host:   fmt.Sprintf("%s.s3.%s.amazonaws.com", s.bucket, s.region),
		Path:   "/" + encodeKeyPath(key),
	}
	q := u.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", "60")
	q.Set("X-Amz-SignedHeaders", "host")
	u.RawQuery = canonicalQuery(q)
	canonicalRequest := strings.Join([]string{
		http.MethodDelete,
		u.EscapedPath(),
		u.RawQuery,
		"host:" + u.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hexSHA256(canonicalRequest),
	}, "\n")
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(signingKey(s.secretKey, dateStamp, s.region, "s3"), stringToSign)))
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequest(http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("s3 delete failed: %s", resp.Status)
	}
	return nil
}

func (s *S3) KeyFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if s.cdnBase != "" && strings.HasPrefix(rawURL, s.cdnBase+"/") {
		return strings.TrimPrefix(rawURL, s.cdnBase+"/")
	}
	if s.publicURL != "" {
		prefix := s.publicURL + "/api/v1/public/images/view/"
		if strings.HasPrefix(rawURL, prefix) {
			return strings.TrimPrefix(rawURL, prefix)
		}
	}
	if s.bucket != "" && s.region != "" {
		direct := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/", s.bucket, s.region)
		if strings.HasPrefix(rawURL, direct) {
			return strings.TrimPrefix(rawURL, direct)
		}
	}
	return ""
}

func (s *S3) DeleteObjectsForURLs(urls []string) []string {
	failures := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range urls {
		key := s.KeyFromURL(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := s.DeleteObject(key); err != nil {
			failures = append(failures, key)
		}
	}
	return failures
}

func SafeKey(prefix, filename string) string {
	cleanName := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, filename)
	if cleanName == "" {
		cleanName = "upload"
	}
	return path.Join(strings.Trim(prefix, "/"), fmt.Sprintf("%d_%s", time.Now().UnixMilli(), cleanName))
}

func encodeKeyPath(key string) string {
	parts := strings.Split(strings.TrimLeft(key, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		vals := values[key]
		sort.Strings(vals)
		for _, value := range vals {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.ReplaceAll(strings.Join(parts, "&"), "+", "%20")
}

func hexSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
