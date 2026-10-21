package config

import (
	"strings"
	"testing"
)

func safeConfig() Config {
	return Config{AppEnv: "production", JWTSecret: strings.Repeat("aZ9-", 12), DatabaseURL: "postgres://app:private-value@db.example.invalid/app?sslmode=verify-full", MigrationDatabaseURL: "postgres://migrator:private-value@db.example.invalid/app?sslmode=verify-full", DatabaseMaxConns: 8, DatabaseMinConns: 1, RedisURL: "rediss://default:private-value@cache.example.invalid:6379", AllowedOrigins: "https://app.example.invalid", HTTPMaxInFlight: 64, TrustedProxyCIDRs: "10.42.0.2/32"}
}

func TestProductionConfigRejectsUnsafeSettingsWithoutDisclosingValues(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		change      func(*Config)
	}{
		{"short secret", "JWT_SECRET", func(c *Config) { c.JWTSecret = "private-value" }},
		{"bootstrap", "ADMIN_BOOTSTRAP_TOKEN", func(c *Config) { c.AdminBootstrapToken = "private-value" }},
		{"mock payments", "ENABLE_MOCK_PAYMENTS", func(c *Config) { c.EnableMockPayments = true }},
		{"dev login", "ENABLE_DEV_LOGIN", func(c *Config) { c.EnableDevLogin = true }},
		{"inline migrations", "RUN_MIGRATIONS", func(c *Config) { c.RunMigrations = true }},
		{"inline workers", "RUN_INLINE_WORKERS", func(c *Config) { c.RunInlineWorkers = true }},
		{"db plaintext", "DATABASE_URL", func(c *Config) { c.DatabaseURL = "postgres://app:private-value@db.invalid/app?sslmode=disable" }},
		{"db downgrade", "DATABASE_URL", func(c *Config) { c.DatabaseURL = "postgres://app:private-value@db.invalid/app?sslmode=prefer" }},
		{"db encryption without verification", "DATABASE_URL", func(c *Config) { c.DatabaseURL = "postgres://app:private-value@db.invalid/app?sslmode=require" }},
		{"replica plaintext", "DATABASE_READ_URL", func(c *Config) { c.DatabaseReadURL = "postgres://app:private-value@db.invalid/app?sslmode=disable" }},
		{"bad db string", "DATABASE_URL", func(c *Config) { c.DatabaseURL = "private-value malformed" }},
		{"redis plaintext", "Redis", func(c *Config) { c.RedisURL = "redis://default:private-value@cache.invalid:6379" }},
		{"redis missing password", "Redis", func(c *Config) { c.RedisURL = "rediss://cache.invalid:6379" }},
		{"wildcard origin", "ALLOWED_ORIGINS", func(c *Config) { c.AllowedOrigins = "*" }},
		{"invalid pool", "DB_MIN_CONNS", func(c *Config) { c.DatabaseMinConns = 99 }},
		{"all proxies", "TRUSTED_PROXY_CIDRS", func(c *Config) { c.TrustedProxyCIDRs = "0.0.0.0/0" }},
		{"unknown env", "APP_ENV", func(c *Config) { c.AppEnv = "prodution" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := safeConfig()
			tc.change(&c)
			err := c.Validate("api")
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("expected field %s rejection", tc.field)
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestConfigPreflightRolesAndTransport(t *testing.T) {
	c := safeConfig()
	for _, role := range []string{"api", "worker", "migrate"} {
		if err := c.Validate(role); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
	}
	c.AppEnv = "staging"
	c.JWTSecret = "dev-only"
	if c.Validate("api") == nil {
		t.Fatal("staging must be hardened")
	}
	c.AppEnv = "development"
	if err := c.Validate("api"); err != nil {
		t.Fatal(err)
	}
	c = safeConfig()
	c.RedisURL = "redis://default:private-value@127.0.0.1:6379"
	if err := c.Validate("api"); err != nil {
		t.Fatal(err)
	}
	c = safeConfig()
	c.MigrationDatabaseURL = "postgres://app:private-value@db.invalid/app?sslmode=disable"
	if c.Validate("migrate") == nil {
		t.Fatal("migration transport must be verified")
	}
	c = safeConfig()
	c.RunMigrations = true
	c.RunInlineWorkers = true
	if err := c.Validate("worker"); err != nil {
		t.Fatal("unused API switches must not reject dedicated worker")
	}
}
