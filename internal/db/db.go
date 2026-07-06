package db

import (
	"context"
	"time"

	"across/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	PG    *pgxpool.Pool
	Redis *redis.Client
}

func New(ctx context.Context, cfg config.Config) (*Store, error) {
	pgxCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pgxCfg.MaxConns = 80
	pgxCfg.MinConns = 8
	pgxCfg.MaxConnLifetime = 45 * time.Minute
	pgxCfg.MaxConnIdleTime = 5 * time.Minute
	pgxCfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		MinIdleConns: 16,
		PoolSize:     256,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		if !cfg.RedisOptional {
			pool.Close()
			return nil, err
		}
		return &Store{PG: pool}, nil
	}

	return &Store{PG: pool, Redis: rdb}, nil
}

func (s *Store) Close() {
	if s == nil {
		return
	}
	if s.PG != nil {
		s.PG.Close()
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
}
