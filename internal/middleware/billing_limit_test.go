package middleware

import (
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type billingRedis struct{ counts map[string]int64 }

func (b *billingRedis) DialHook(next redis.DialHook) redis.DialHook { return next }
func (b *billingRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (b *billingRedis) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, command redis.Cmder) error {
		key := command.Args()[3].(string)
		b.counts[key]++
		command.(*redis.Cmd).SetVal([]any{b.counts[key], int64(60000)})
		return nil
	}
}
func TestBillingQuotaIsPerAccountBehindSharedCarrierAddress(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	memory := &billingRedis{counts: map[string]int64{}}
	client.AddHook(memory)
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("X-Test-Account")); return c.Next() })
	app.Post("/billing", DistributedUserRateLimit(client, "play-test", 1, time.Minute), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	for _, test := range []struct {
		user string
		want int
	}{{"provider-a", 200}, {"provider-b", 200}, {"provider-a", 429}} {
		request := httptest.NewRequest("POST", "/billing", nil)
		request.Header.Set("X-Test-Account", test.user)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.want {
			t.Fatalf("Accounts sharing an IP shared quota: %d expected %d", response.StatusCode, test.want)
		}
	}
	if len(memory.counts) != 2 {
		t.Fatal("Quota did not separate accounts")
	}
	for key := range memory.counts {
		if strings.Contains(key, "provider-") {
			t.Fatal("Raw account identity stored in quota key")
		}
	}
}
