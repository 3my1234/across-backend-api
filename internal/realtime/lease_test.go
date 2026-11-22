package realtime

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestConnectionCapIsSharedAcrossReplicas(t *testing.T) {
	s := miniredis.RunT(t)
	a := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer a.Close()
	b := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer b.Close()
	ctx := context.Background()
	user := "11111111-1111-4111-8111-111111111111"
	first, err := acquireLease(ctx, a, user, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireLease(ctx, b, user, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquireLease(ctx, a, user, 2); !errors.Is(err, ErrCapacity) {
		t.Fatal("global cap bypassed")
	}
	if err = first.renew(ctx); err != nil {
		t.Fatal(err)
	}
	first.release()
	third, err := acquireLease(ctx, b, user, 2)
	if err != nil {
		t.Fatal("released slot unavailable", err)
	}
	second.release()
	third.release()
}
