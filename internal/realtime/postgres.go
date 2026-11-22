package realtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Every API instance can LISTEN. The Redis script atomically deduplicates the
// same committed event across listeners before publishing it to all replicas.
var forwardScript = redis.NewScript(`if redis.call('SET',KEYS[1],'1','EX',60,'NX') then return redis.call('PUBLISH',KEYS[2],ARGV[1]) else return 0 end`)

type PostgresRelay struct {
	DB              *pgxpool.Pool
	Redis           *redis.Client
	ready           atomic.Bool
	forwardFailures atomic.Uint64
}

func (p *PostgresRelay) Ready() bool             { return p.ready.Load() }
func (p *PostgresRelay) ForwardFailures() uint64 { return p.forwardFailures.Load() }

func (p *PostgresRelay) forward(ctx context.Context, payload string) error {
	parts := strings.Split(payload, ":")
	if len(parts) != 2 {
		return errors.New("invalid real-time signal")
	}
	event, err := uuid.Parse(parts[0])
	if err != nil {
		return errors.New("invalid event")
	}
	recipient := parts[1]
	if recipient != "hall" {
		user, err := uuid.Parse(recipient)
		if err != nil {
			return errors.New("invalid recipient")
		}
		recipient = user.String()
	}
	if p.Redis == nil {
		return errors.New("Redis unavailable")
	}
	return forwardScript.Run(ctx, p.Redis, []string{"atl:realtime:event:" + event.String(), redisChannel}, recipient).Err()
}

// A database connection is held only while the Redis operational switch is on.
// Lost notifications are recovered by client catch-up and bounded polling.
func (p *PostgresRelay) Run(ctx context.Context) {
	if p.DB == nil || p.Redis == nil {
		return
	}
	for ctx.Err() == nil {
		check, cancel := context.WithTimeout(ctx, time.Second)
		enabled, err := p.Redis.Get(check, EnabledKey).Result()
		cancel()
		if err == nil && enabled == "on" {
			p.listen(ctx)
		}
		p.ready.Store(false)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *PostgresRelay) listen(ctx context.Context) {
	acquire, cancel := context.WithTimeout(ctx, 2*time.Second)
	conn, err := p.DB.Acquire(acquire)
	cancel()
	if err != nil {
		return
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "LISTEN atl_realtime_wake"); err != nil {
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanup, "UNLISTEN atl_realtime_wake")
	}()
	var policyEnabled bool
	if err = conn.QueryRow(ctx, "SELECT enabled FROM realtime_signal_policy WHERE singleton").Scan(&policyEnabled); err != nil || !policyEnabled {
		return
	}
	p.ready.Store(true)
	defer p.ready.Store(false)
	for ctx.Err() == nil {
		wait, stop := context.WithTimeout(ctx, 25*time.Second)
		notification, err := conn.Conn().WaitForNotification(wait)
		stop()
		if errors.Is(err, context.DeadlineExceeded) {
			check, cancel := context.WithTimeout(ctx, time.Second)
			enabled, e := p.Redis.Get(check, EnabledKey).Result()
			cancel()
			if e != nil || enabled != "on" {
				return
			}
			checkPolicy, stopPolicy := context.WithTimeout(ctx, time.Second)
			e = conn.QueryRow(checkPolicy, "SELECT enabled FROM realtime_signal_policy WHERE singleton").Scan(&policyEnabled)
			stopPolicy()
			if e != nil || !policyEnabled {
				return
			}
			continue
		}
		if err != nil {
			return
		}
		publish, cancel := context.WithTimeout(ctx, time.Second)
		err = p.forward(publish, notification.Payload)
		cancel()
		if err != nil {
			p.forwardFailures.Add(1)
			return // Close sockets so clients catch up and use fallback until recovery.
		}
	}
}
