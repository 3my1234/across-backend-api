package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func SharedPublicCache(client *redis.Client, ttl time.Duration) fiber.Handler {
	return sharedPublicCache(client, ttl, 2*time.Second)
}

// One fill per key per API process, not one per waiting request. Each Fiber
// context stays on its own request goroutine; only completion signals are shared.
// Different replicas may each fill once. Redis remains the shared cache and
// catalogue revisions remain authoritative. There is no cross-server lock.
func sharedPublicCache(client *redis.Client, ttl, waitLimit time.Duration) fiber.Handler {
	var mu sync.Mutex
	flights := map[string]chan struct{}{}
	return func(c *fiber.Ctx) error {
		if client == nil || ttl <= 0 || c.Method() != fiber.MethodGet || c.Get(fiber.HeaderAuthorization) != "" || !publicCacheablePath(c.Path()) {
			return c.Next()
		}
		revision, _ := c.Locals("catalog_revision").(string)
		if publicCacheBypass(c) && revision == "" {
			return c.Next()
		}
		keyInput := canonicalPublicURL(c.OriginalURL()) + "|" + c.Get(fiber.HeaderAcceptEncoding) + "|" + c.Get("CF-IPCountry") + "|" + c.Get("X-Client-Country-Code") + "|" + revision
		digest := sha256.Sum256([]byte(keyInput))
		key := "cache:public:" + hex.EncodeToString(digest[:])
		deadline := time.Now().Add(waitLimit)
		for {
			entry, err := readPublicCache(c, client, key)
			if err == nil {
				return sendPublicCache(c, entry)
			}
			if !errors.Is(err, redis.Nil) {
				return c.Next()
			} // Existing Redis-outage fail-open behavior.
			mu.Lock()
			done, waiting := flights[key]
			if !waiting {
				done = make(chan struct{})
				flights[key] = done
			}
			mu.Unlock()
			if !waiting {
				return func() error {
					defer func() { mu.Lock(); delete(flights, key); close(done); mu.Unlock() }()
					// A previous fill may have finished between GET and registration.
					if entry, err := readPublicCache(c, client, key); err == nil {
						return sendPublicCache(c, entry)
					}
					return fillPublicCache(c, client, key, ttl)
				}()
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return cacheFillBusy(c)
			}
			timer := time.NewTimer(remaining)
			select {
			case <-done:
				timer.Stop()
			case <-timer.C:
				return cacheFillBusy(c)
			}
		}
	}
}

func readPublicCache(c *fiber.Ctx, client *redis.Client, key string) (cachedResponse, error) {
	var entry cachedResponse
	raw, err := client.Get(c.Context(), key).Bytes()
	if err != nil {
		return entry, err
	}
	if json.Unmarshal(raw, &entry) != nil {
		return entry, redis.Nil
	}
	return entry, nil
}

func sendPublicCache(c *fiber.Ctx, entry cachedResponse) error {
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

func fillPublicCache(c *fiber.Ctx, client *redis.Client, key string, ttl time.Duration) error {
	if err := c.Next(); err != nil {
		return err
	}
	if c.Response().StatusCode() != fiber.StatusOK {
		return nil
	}
	entry := cachedResponse{Body: append([]byte(nil), c.Response().Body()...), ContentType: string(c.Response().Header.ContentType()), Encoding: string(c.Response().Header.Peek(fiber.HeaderContentEncoding)), CacheControl: string(c.Response().Header.Peek(fiber.HeaderCacheControl))}
	if raw, err := json.Marshal(entry); err == nil {
		if ttl > 5*time.Second {
			ttl = 5 * time.Second
		}
		_ = client.Set(c.Context(), key, raw, ttl).Err()
	}
	c.Set("X-Cache", "MISS")
	return nil
}

func cacheFillBusy(c *fiber.Ctx) error {
	c.Set("Retry-After", "2")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Catalogue is refreshing. Please retry shortly."})
}
