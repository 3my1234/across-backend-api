package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// Validate is offline: it never connects, rotates secrets or logs their values.
// Call before opening any dependencies. Development/test retain local fixtures.
func (c Config) Validate(role string) error {
	var problems []string
	add := func(value string) { problems = append(problems, value) }
	env := strings.ToLower(strings.TrimSpace(c.AppEnv))
	switch env {
	case "development", "test", "production", "staging":
	default:
		add("APP_ENV must be development, test, staging or production")
	}
	proxyCount := 0
	for _, raw := range strings.Split(c.TrustedProxyCIDRs, ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix.Bits() == 0 {
				add("TRUSTED_PROXY_CIDRS must contain explicit non-universal CIDRs")
				break
			}
			proxyCount++
		}
	}
	if role != "api" && role != "worker" && role != "migrate" {
		add("service role must be api, worker or migrate")
	}
	if env != "production" && env != "staging" {
		return validationError(problems)
	}
	if role != "migrate" {
		if c.EnableDevLogin {
			add("ENABLE_DEV_LOGIN must be false")
		}
		if len(strings.TrimSpace(c.JWTSecret)) < 32 {
			add("JWT_SECRET must contain at least 32 bytes of a securely generated secret")
		}
		if c.AdminBootstrapToken != "" {
			add("ADMIN_BOOTSTRAP_TOKEN must be unset; use authenticated admin accounts")
		}
		if c.EnableMockPayments {
			add("ENABLE_MOCK_PAYMENTS must be false")
		}
	}
	if role == "api" {
		if proxyCount == 0 {
			add("TRUSTED_PROXY_CIDRS must identify the verified ingress hops before serving proxied traffic")
		}
		if c.RunMigrations {
			add("RUN_MIGRATIONS must be false; use the separate migration command")
		}
		if c.RunInlineWorkers {
			add("RUN_INLINE_WORKERS must be false; use the separate worker service")
		}
		if c.HTTPMaxInFlight < 1 {
			add("HTTP_MAX_IN_FLIGHT must be positive")
		}
		for _, origin := range strings.Split(c.AllowedOrigins, ",") {
			u, err := url.Parse(strings.TrimSpace(origin))
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || strings.Contains(origin, "*") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				add("ALLOWED_ORIGINS must contain exact HTTPS origins")
				break
			}
		}
	}
	if c.DatabaseMaxConns < 2 || c.DatabaseMinConns < 0 || c.DatabaseMinConns > c.DatabaseMaxConns {
		add("DB_MIN_CONNS/DB_MAX_CONNS must define a bounded valid pool")
	}
	if c.DatabaseReadURL != "" && c.DatabaseReadURL != c.DatabaseURL && (c.DatabaseReadMaxConns < 2 || c.DatabaseReadMinConns < 0 || c.DatabaseReadMinConns > c.DatabaseReadMaxConns) {
		add("DB_READ_MIN_CONNS/DB_READ_MAX_CONNS must define a bounded valid read pool")
	}
	urls := []struct{ name, value string }{{"DATABASE_URL", c.DatabaseURL}, {"DATABASE_READ_URL", c.DatabaseReadURL}}
	if role == "migrate" {
		urls = []struct{ name, value string }{{"MIGRATION_DATABASE_URL", c.MigrationDatabaseURL}}
	}
	for _, item := range urls {
		if item.value == "" && item.name == "DATABASE_READ_URL" {
			continue
		}
		if !verifiedPostgresTLS(item.value) {
			add(item.name + " must use certificate-verified TLS without plaintext fallback (sslmode=verify-full)")
		}
	}
	if role != "migrate" {
		var options *redis.Options
		var err error
		if c.RedisURL != "" {
			options, err = redis.ParseURL(c.RedisURL)
		} else {
			options = &redis.Options{Addr: c.RedisAddr, Password: c.RedisPassword}
		}
		if err != nil || options == nil {
			add("REDIS_URL is invalid")
		} else {
			host, _, splitErr := net.SplitHostPort(options.Addr)
			ip, _ := netip.ParseAddr(host)
			loopback := splitErr == nil && (host == "localhost" || ip.IsLoopback())
			if !loopback && (options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify) {
				add("Redis must use certificate-verified TLS except on loopback")
			}
			if options.Password == "" {
				add("Redis authentication must be configured")
			}
		}
	}
	return validationError(problems)
}

func verifiedPostgresTLS(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	c, err := pgconn.ParseConfig(raw)
	if err != nil || c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify || c.TLSConfig.ServerName == "" {
		return false
	}
	for _, fallback := range c.Fallbacks {
		if fallback.TLSConfig == nil || fallback.TLSConfig.InsecureSkipVerify || fallback.TLSConfig.ServerName == "" {
			return false
		}
	}
	return true
}

func validationError(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New("configuration rejected: " + strings.Join(problems, "; "))
}

func (c Config) DevLoginAllowed() bool {
	env := strings.ToLower(strings.TrimSpace(c.AppEnv))
	return c.EnableDevLogin && (env == "development" || env == "test")
}
