package db

import (
	"context"
	"time"

	"across/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	PG     *pgxpool.Pool
	ReadPG *pgxpool.Pool
	Redis  *redis.Client
}

func New(ctx context.Context, cfg config.Config) (*Store, error) {
	pool, err := newPostgresPool(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConns, cfg.DatabaseMinConns)
	if err != nil {
		return nil, err
	}
	readPool := pool
	if cfg.DatabaseReadURL != "" && cfg.DatabaseReadURL != cfg.DatabaseURL {
		readPool, err = newPostgresPool(ctx, cfg.DatabaseReadURL, cfg.DatabaseReadMaxConns, cfg.DatabaseReadMinConns)
		if err != nil {
			pool.Close()
			return nil, err
		}
	}

	redisOptions, err := redisOptions(cfg)
	if err != nil {
		if readPool != pool {
			readPool.Close()
		}
		pool.Close()
		return nil, err
	}
	rdb := redis.NewClient(redisOptions)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		if !cfg.RedisOptional {
			if readPool != pool {
				readPool.Close()
			}
			pool.Close()
			return nil, err
		}
		return &Store{PG: pool, ReadPG: readPool}, nil
	}

	return &Store{PG: pool, ReadPG: readPool, Redis: rdb}, nil
}

func newPostgresPool(ctx context.Context, databaseURL string, configuredMax, configuredMin int) (*pgxpool.Pool, error) {
	pgxCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	maxConns := configuredMax
	if maxConns < 2 {
		maxConns = 2
	}
	minConns := configuredMin
	if minConns < 0 {
		minConns = 0
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	pgxCfg.MaxConns = int32(maxConns)
	pgxCfg.MinConns = int32(minConns)
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

	return pool, nil
}

func redisOptions(cfg config.Config) (*redis.Options, error) {
	var (
		options *redis.Options
		err     error
	)

	if cfg.RedisURL != "" {
		options, err = redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, err
		}
	} else {
		options = &redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		}
	}

	options.MinIdleConns = cfg.RedisMinIdleConns
	options.PoolSize = cfg.RedisPoolSize
	options.ReadTimeout = 2 * time.Second
	options.WriteTimeout = 2 * time.Second
	return options, nil
}

func (s *Store) Close() {
	if s == nil {
		return
	}
	if s.PG != nil {
		s.PG.Close()
	}
	if s.ReadPG != nil && s.ReadPG != s.PG {
		s.ReadPG.Close()
	}
	if s.Redis != nil {
		_ = s.Redis.Close()
	}
}
