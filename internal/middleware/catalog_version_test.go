package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type countedRevision struct {
	calls atomic.Int64
	value atomic.Int64
	fail  atomic.Bool
}

func (r *countedRevision) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	r.calls.Add(1)
	if _, ok := ctx.Deadline(); !ok {
		panic("revision query must have a deadline")
	}
	if r.fail.Load() {
		return revisionRow{err: errors.New("database unavailable")}
	}
	return revisionRow{revision: r.value.Load()}
}

func TestCatalogVersionBurstSharesPrimaryQueryAndRecovers(t *testing.T) {
	reader := &countedRevision{}
	reader.value.Store(7)
	var clock atomic.Int64
	clock.Store(time.Unix(120, 0).UnixNano())
	app := fiber.New()
	app.Get("/version", newCatalogVersion(reader, func() time.Time { return time.Unix(0, clock.Load()) }))
	request := func(status int) string {
		t.Helper()
		response, err := app.Test(httptest.NewRequest("GET", "/version?refresh=random", nil), 5000)
		if err != nil {
			t.Error(err)
			return ""
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Errorf("status=%d want=%d", response.StatusCode, status)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Error("change hint cached outside the process")
		}
		var body struct {
			Token string `json:"change_token"`
		}
		_ = json.NewDecoder(response.Body).Decode(&body)
		return body.Token
	}
	var group sync.WaitGroup
	for i := 0; i < 200; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if token := request(200); token != "7:2" {
				t.Errorf("token=%q", token)
			}
		}()
	}
	group.Wait()
	if reader.calls.Load() != 1 {
		t.Fatalf("200 heartbeat requests used %d queries", reader.calls.Load())
	}
	clock.Add(int64(time.Second))
	reader.value.Store(8)
	if request(200) != "8:2" {
		t.Fatal("new committed revision was not observed after hint expiry")
	}
	clock.Add(int64(time.Second))
	reader.fail.Store(true)
	if request(503) != "" {
		t.Fatal("served stale hint after a failed revalidation")
	}
	reader.fail.Store(false)
	reader.value.Store(9)
	if request(200) != "9:2" {
		t.Fatal("failure was cached instead of allowing recovery")
	}
}
