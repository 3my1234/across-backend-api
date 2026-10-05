package middleware

import (
	"context"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// Exercise the real Redis middleware without a network/server dependency.
type memoryRedis struct{ values map[string]string }

func (m *memoryRedis) DialHook(next redis.DialHook) redis.DialHook { return next }
func (m *memoryRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (m *memoryRedis) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		key := cmd.Args()[1].(string)
		switch command := cmd.(type) {
		case *redis.StringCmd:
			value, ok := m.values[key]
			if !ok {
				command.SetErr(redis.Nil)
				return redis.Nil
			}
			command.SetVal(value)
		case *redis.StatusCmd:
			value := cmd.Args()[2].([]byte)
			m.values[key] = string(value)
			command.SetVal("OK")
		default:
			panic("unexpected Redis command")
		}
		return nil
	}
}

func TestApprovalInvalidatesCachedPriceForEveryRefreshVariant(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer client.Close()
	client.AddHook(&memoryRedis{values: map[string]string{}})
	revision := &revisionRow{revision: 1}
	price, calls := 20, 0
	app := fiber.New()
	app.Use(CatalogFreshness(revision))
	app.Use(SharedPublicCache(client, time.Minute))
	app.Get("/api/v1/products", func(c *fiber.Ctx) error { calls++; return c.SendString(strconv.Itoa(price)) })
	read := func(query string) (string, string) {
		t.Helper()
		request := httptest.NewRequest("GET", "/api/v1/products?country_code=NG&"+query, nil)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if query != "" && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("explicit refresh was cacheable at the edge")
		}
		return string(body), response.Header.Get("X-Cache")
	}
	first, cache := read("")
	if first != "20" || cache != "MISS" || calls != 1 {
		t.Fatal("first price was not cached")
	}
	same, cache := read("fresh=123")
	if same != first || cache != "HIT" || calls != 1 {
		t.Fatal("validated refresh unnecessarily queried the catalogue again")
	}
	// Provider edit and admin approval commit a new catalogue revision.
	revision.revision = 2
	price = 100
	fresh, cache := read("refresh=456")
	if fresh != "100" || cache != "MISS" || calls != 2 {
		t.Fatal("approved price edit reused the old cached price")
	}
	same, cache = read("refresh=789")
	if same != fresh || cache != "HIT" || calls != 2 {
		t.Fatal("buyers did not share the current price")
	}
}
