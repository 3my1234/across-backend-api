package realtime

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// Gate bounds handshake pressure across API replicas before session lookup.
// It deliberately fails closed; existing HTTP/polling remains available.
var gateScript = redis.NewScript(`
if redis.call('GET',KEYS[1])~='on' then return -1 end
local n=redis.call('INCR',KEYS[2]); if n==1 then redis.call('EXPIRE',KEYS[2],2) end
return n
`)

func HandshakeGate(cache *redis.Client, perSecond int) fiber.Handler {
	if perSecond < 1 {
		panic("handshake limit must be positive")
	}
	return func(c *fiber.Ctx) error {
		if cache == nil {
			return fiber.ErrServiceUnavailable
		}
		key := "atl:realtime:handshakes:" + time.Now().UTC().Format("20060102150405")
		count, err := gateScript.Run(c.Context(), cache, []string{EnabledKey, key}).Int64()
		if err != nil || count < 0 {
			return fiber.ErrServiceUnavailable
		}
		if count > int64(perSecond) {
			c.Set("Retry-After", "2")
			return fiber.ErrTooManyRequests
		}
		return c.Next()
	}
}
