package middleware

import (
	"fmt"
	"io"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func TestSimultaneousPublicCacheMissesShareOneLoad(t *testing.T) {
	r := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer r.Close()
	memory := &memoryRedis{values: map[string]string{}}
	r.AddHook(memory)
	app := fiber.New()
	app.Use(SharedPublicCache(r, time.Second))
	var calls atomic.Int32
	app.Get("/api/v1/products", func(c *fiber.Ctx) error {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		return c.JSON(fiber.Map{"products": []string{"synthetic"}})
	})
	for wave := 0; wave < 2; wave++ {
		memory.mu.Lock()
		memory.values = map[string]string{}
		memory.mu.Unlock()
		var wg sync.WaitGroup
		start := make(chan struct{})
		for n := 0; n < 40; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				res, err := app.Test(httptest.NewRequest("GET", "/api/v1/products", nil), 5000)
				if err != nil {
					t.Error(err)
					return
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				if res.StatusCode != 200 || string(body) != `{"products":["synthetic"]}` {
					t.Errorf("bad response %d %s", res.StatusCode, body)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := calls.Load(); got != int32(wave+1) {
			t.Fatalf("concurrent miss wave %d ran %d catalogue loads; want %d", wave, got, wave+1)
		}
	}
}

func TestPublicCacheSeparatesCountryEncodingAndRevision(t *testing.T) {
	r := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer r.Close()
	r.AddHook(&memoryRedis{values: map[string]string{}})
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("catalog_revision", c.Get("X-Test-Revision")); return c.Next() })
	app.Use(SharedPublicCache(r, time.Second))
	var mu sync.Mutex
	calls := map[string]int{}
	app.Get("/api/v1/products", func(c *fiber.Ctx) error {
		key := c.Get("X-Client-Country-Code") + "|" + c.Get("Accept-Encoding") + "|" + c.Get("X-Test-Revision")
		mu.Lock()
		calls[key]++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return c.SendString(key)
	})
	var wg sync.WaitGroup
	for _, country := range []string{"NG", "GH"} {
		for _, encoding := range []string{"gzip", "identity"} {
			for _, revision := range []string{"1", "2"} {
				for n := 0; n < 5; n++ {
					wg.Add(1)
					go func(country, encoding, revision string) {
						defer wg.Done()
						req := httptest.NewRequest("GET", "/api/v1/products", nil)
						req.Header.Set("X-Client-Country-Code", country)
						req.Header.Set("Accept-Encoding", encoding)
						req.Header.Set("X-Test-Revision", revision)
						res, err := app.Test(req, 5000)
						if err != nil {
							t.Error(err)
							return
						}
						defer res.Body.Close()
						body, _ := io.ReadAll(res.Body)
						if string(body) != country+"|"+encoding+"|"+revision {
							t.Errorf("cache boundary crossed: %s", body)
						}
					}(country, encoding, revision)
				}
			}
		}
	}
	wg.Wait()
	for key, count := range calls {
		if count != 1 {
			t.Errorf("%s loads=%d", key, count)
		}
	}
	if len(calls) != 8 {
		t.Fatal("cache keys were merged")
	}
}

func TestPublicCacheWaitIsBoundedAndPrivateRequestsBypassIt(t *testing.T) {
	r := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer r.Close()
	r.AddHook(&memoryRedis{values: map[string]string{}})
	app := fiber.New()
	app.Use(sharedPublicCache(r, time.Second, 20*time.Millisecond))
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	app.Get("/api/v1/products", func(c *fiber.Ctx) error {
		if c.Get("Authorization") != "" {
			return c.SendString("private")
		}
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return c.SendString("public")
	})
	first := make(chan error, 1)
	go func() {
		res, err := app.Test(httptest.NewRequest("GET", "/api/v1/products", nil), 5000)
		if err == nil {
			res.Body.Close()
		}
		first <- err
	}()
	<-started
	private := httptest.NewRequest("GET", "/api/v1/products", nil)
	private.Header.Set("Authorization", "Bearer synthetic-private")
	res, err := app.Test(private, 500)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "private" || res.Header.Get("X-Cache") != "" {
		t.Fatal("private request joined public cache")
	}
	res, err = app.Test(httptest.NewRequest("GET", "/api/v1/products", nil), 500)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 || res.Header.Get("Retry-After") != "2" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("unbounded cache-fill wait")
	}
	if calls.Load() != 1 {
		t.Fatal("waiter issued a duplicate load")
	}
	once.Do(func() { close(release) })
	if err = <-first; err != nil {
		t.Fatal(err)
	}
	res, err = app.Test(httptest.NewRequest("GET", "/api/v1/products", nil), 500)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("X-Cache") != "HIT" {
		t.Fatal("cache fill did not recover")
	}
}

func TestPublicCacheReleasesFailedLoads(t *testing.T) {
	r := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer r.Close()
	r.AddHook(&memoryRedis{values: map[string]string{}})
	app := fiber.New()
	app.Use(SharedPublicCache(r, time.Second))
	var calls atomic.Int32
	app.Get("/api/v1/products", func(c *fiber.Ctx) error {
		if calls.Add(1) == 1 {
			return fmt.Errorf("synthetic load failure")
		}
		return c.SendString("recovered")
	})
	for _, want := range []int{500, 200, 200} {
		res, err := app.Test(httptest.NewRequest("GET", "/api/v1/products", nil), 1000)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("status %d want %d", res.StatusCode, want)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("failed response cached or successful fill repeated")
	}
}
