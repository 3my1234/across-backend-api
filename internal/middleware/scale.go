package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

var fixedWindowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return {current, redis.call("PTTL", KEYS[1])}
`)

func DistributedRateLimit(client *redis.Client, namespace string, requests int, window time.Duration) fiber.Handler {
	if namespace == "" {
		namespace = "api"
	}
	return func(c *fiber.Ctx) error {
		if client == nil || requests < 1 || window <= 0 || c.Method() == fiber.MethodOptions ||
			c.Path() == "/api/v1/health" || c.Path() == "/api/v1/ready" ||
			strings.HasPrefix(c.Path(), "/api/v1/webhooks/") {
			return c.Next()
		}
		identity := scaleClientIP(c)
		windowID := time.Now().UnixMilli() / window.Milliseconds()
		key := "rate:" + namespace + ":" + identity + ":" + strconv.FormatInt(windowID, 10)
		values, err := fixedWindowScript.Run(c.Context(), client, []string{key}, window.Milliseconds()).Int64Slice()
		if err != nil || len(values) != 2 {
			// Availability wins if Redis is briefly unavailable; the database and
			// endpoint-level validation remain authoritative.
			return c.Next()
		}
		remaining := requests - int(values[0])
		if remaining < 0 {
			remaining = 0
		}
		c.Set("X-RateLimit-Limit", strconv.Itoa(requests))
		c.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if values[0] > int64(requests) {
			retrySeconds := maxInt64(1, (values[1]+999)/1000)
			c.Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Too many requests. Please wait and try again.",
			})
		}
		return c.Next()
	}
}

type cachedResponse struct {
	Body         []byte `json:"body"`
	ContentType  string `json:"content_type"`
	Encoding     string `json:"content_encoding"`
	CacheControl string `json:"cache_control"`
}

func SharedPublicCache(client *redis.Client, ttl time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if client == nil || ttl <= 0 || c.Method() != fiber.MethodGet ||
			c.Get(fiber.HeaderAuthorization) != "" || !publicCacheablePath(c.Path()) {
			return c.Next()
		}
		bypass := strings.Contains(strings.ToLower(c.Get(fiber.HeaderCacheControl)), "no-cache") ||
			c.Query("refresh") != "" || c.Query("_") != ""
		keyInput := c.OriginalURL() + "|" + c.Get(fiber.HeaderAcceptEncoding) + "|" + c.Get("CF-IPCountry") + "|" + c.Get("X-Client-Country-Code")
		digest := sha256.Sum256([]byte(keyInput))
		key := "cache:public:" + hex.EncodeToString(digest[:])
		if !bypass {
			if raw, err := client.Get(c.Context(), key).Bytes(); err == nil {
				var entry cachedResponse
				if json.Unmarshal(raw, &entry) == nil {
					if entry.ContentType != "" {
						c.Set(fiber.HeaderContentType, entry.ContentType)
					}
					if entry.Encoding != "" {
						c.Set(fiber.HeaderContentEncoding, entry.Encoding)
					}
					if entry.CacheControl != "" {
						c.Set(fiber.HeaderCacheControl, entry.CacheControl)
					}
					c.Set("X-Cache", "HIT")
					return c.Send(entry.Body)
				}
			}
		}
		if err := c.Next(); err != nil {
			return err
		}
		if c.Response().StatusCode() != fiber.StatusOK {
			return nil
		}
		entry := cachedResponse{
			Body:         append([]byte(nil), c.Response().Body()...),
			ContentType:  string(c.Response().Header.ContentType()),
			Encoding:     string(c.Response().Header.Peek(fiber.HeaderContentEncoding)),
			CacheControl: string(c.Response().Header.Peek(fiber.HeaderCacheControl)),
		}
		if raw, err := json.Marshal(entry); err == nil {
			_ = client.Set(c.Context(), key, raw, ttl).Err()
		}
		c.Set("X-Cache", "MISS")
		return nil
	}
}

func publicCacheablePath(path string) bool {
	if path == "/api/v1/products" || path == "/api/v1/products/flash-sale" ||
		path == "/api/v1/marketplace/listings" || path == "/api/v1/marketplace/nearby" ||
		path == "/api/v1/marketplace/subscription-plans" {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/products/") {
		return !strings.Contains(path, "/reviews") && !strings.Contains(path, "/mine")
	}
	if strings.HasPrefix(path, "/api/v1/marketplace/listings/") {
		return !strings.Contains(path, "/reviews") && !strings.Contains(path, "/availability")
	}
	return false
}

func scaleClientIP(c *fiber.Ctx) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if value := strings.TrimSpace(c.Get(header)); net.ParseIP(value) != nil {
			return value
		}
	}
	if forwarded := strings.TrimSpace(strings.Split(c.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
		return forwarded
	}
	return c.IP()
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
