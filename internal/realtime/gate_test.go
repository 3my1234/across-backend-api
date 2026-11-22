package realtime

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"sync"
	"sync/atomic"
	"testing"
)

func TestReconnectAdmissionBudgetIsShared(t *testing.T) {
	m := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: m.Addr(), PoolSize: 32})
	defer cache.Close()
	ctx := context.Background()
	if err := cache.Set(ctx, EnabledKey, "on", 0).Err(); err != nil {
		t.Fatal(err)
	}
	var accepted, failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := gateScript.Run(ctx, cache, []string{EnabledKey, "test:single-second"}).Int64()
			if err != nil {
				failed.Add(1)
				return
			}
			if n <= 20 {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 || accepted.Load() != 20 {
		t.Fatalf("accepted %d failed %d; wanted 20 accepted and no errors", accepted.Load(), failed.Load())
	}
}
