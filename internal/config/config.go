package config

import (
	"bufio"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAllowedOrigins = "https://atlxpres.com,https://admin.atlxpres.com,https://provider.atlxpres.com"
	defaultAPIBaseURL     = "https://api.atlxpres.com"
	defaultAssetsCDNBase  = "https://media.atlxpres.com"
	defaultWebsiteURL     = "https://atlxpres.com"
	defaultProviderURL    = "https://provider.atlxpres.com"
)

type Config struct {
	AppEnv                        string
	HTTPAddr                      string
	WorkerHealthAddr              string
	AllowedOrigins                string
	DatabaseURL                   string
	DatabaseReadURL               string
	MigrationDatabaseURL          string
	RedisURL                      string
	RedisAddr                     string
	RedisPassword                 string
	RedisDB                       int
	RedisOptional                 bool
	DatabaseMaxConns              int
	DatabaseMinConns              int
	DatabaseReadMaxConns          int
	DatabaseReadMinConns          int
	RedisPoolSize                 int
	RedisMinIdleConns             int
	RunInlineWorkers              bool
	RunMigrations                 bool
	RateLimitPerMinute            int
	PublicCacheTTLSeconds         int
	JWTSecret                     string
	FlutterwaveSecretKey          string
	FlutterwaveWebhookSecret      string
	FlutterwaveCustomerPaysFees   bool
	PrivyAppID                    string
	PrivyAppSecret                string
	PrivyVerificationKey          string
	PrivyVerificationMode         string
	DefaultCountry                string
	AllowedCountries              string
	AdminBootstrapToken           string
	AWSRegion                     string
	AWSAccessKeyID                string
	AWSSecretAccessKey            string
	S3BucketName                  string
	AssetsCDNBase                 string
	PublicBaseURL                 string
	SMTPHost                      string
	SMTPPort                      string
	SMTPUsername                  string
	SMTPPassword                  string
	SMTPFromEmail                 string
	SMTPFromName                  string
	SMTPReplyTo                   string
	SESSNSTopicARN                string
	BrandLogoURL                  string
	WebsiteURL                    string
	ProviderPortalURL             string
	ProviderSubscriptionsEnforced bool
	ProviderSubscriptionsStartAt  *time.Time
	ProviderSubscriptionPolicy    *ProviderSubscriptionPolicy
}

func Load() Config {
	loadDotEnv(".env")
	publicBaseURL := strings.TrimRight(env("PUBLIC_BASE_URL", ""), "/")
	brandLogoURL := strings.TrimSpace(env("BRAND_LOGO_URL", ""))
	if brandLogoURL == "" || strings.Contains(brandLogoURL, "raw.githubusercontent.com/3my1234/across-mobile-app") {
		if publicBaseURL == "" {
			publicBaseURL = defaultAPIBaseURL
		}
		brandLogoURL = publicBaseURL + "/api/v1/public/brand/logo.png?v=20260929-atl"
	}
	smtpUsername := strings.TrimSpace(firstEnv("SMTP_USERNAME", "SMTP_USER"))
	smtpPassword := strings.TrimSpace(firstEnv("SMTP_PASSWORD", "SMTP_PASS"))
	smtpFromEmail := strings.TrimSpace(firstEnv("SMTP_FROM_EMAIL", "DEFAULT_FROM_EMAIL"))
	if smtpFromEmail == "" {
		smtpFromEmail = "welcome@atlxpres.com"
	}
	if strings.HasSuffix(strings.ToLower(smtpFromEmail), "@sportbanter.online") {
		smtpFromEmail = "welcome@atlxpres.com"
	}
	smtpFromName := strings.TrimSpace(firstEnv("SMTP_FROM_NAME", "DEFAULT_FROM_NAME"))
	if smtpFromName == "" {
		smtpFromName = "Atlantic Express"
	}
	smtpReplyTo := strings.TrimSpace(env("SMTP_REPLY_TO", "support@atlxpres.com"))
	if strings.HasSuffix(strings.ToLower(smtpReplyTo), "@sportbanter.online") {
		smtpReplyTo = "support@atlxpres.com"
	}

	return Config{
		AppEnv:                        env("APP_ENV", "development"),
		HTTPAddr:                      env("HTTP_ADDR", ":8080"),
		WorkerHealthAddr:              env("WORKER_HEALTH_ADDR", ":8080"),
		AllowedOrigins:                env("ALLOWED_ORIGINS", defaultAllowedOrigins),
		DatabaseURL:                   databaseURL(),
		DatabaseReadURL:               env("DATABASE_READ_URL", ""),
		MigrationDatabaseURL:          env("MIGRATION_DATABASE_URL", databaseURL()),
		RedisURL:                      env("REDIS_URL", ""),
		RedisAddr:                     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:                 env("REDIS_PASSWORD", ""),
		RedisDB:                       envInt("REDIS_DB", 0),
		RedisOptional:                 envBool("REDIS_OPTIONAL", true),
		DatabaseMaxConns:              envInt("DB_MAX_CONNS", 20),
		DatabaseMinConns:              envInt("DB_MIN_CONNS", 2),
		DatabaseReadMaxConns:          envInt("DB_READ_MAX_CONNS", 20),
		DatabaseReadMinConns:          envInt("DB_READ_MIN_CONNS", 2),
		RedisPoolSize:                 envInt("REDIS_POOL_SIZE", 64),
		RedisMinIdleConns:             envInt("REDIS_MIN_IDLE_CONNS", 4),
		RunInlineWorkers:              envBool("RUN_INLINE_WORKERS", true),
		RunMigrations:                 envBool("RUN_MIGRATIONS", true),
		RateLimitPerMinute:            envInt("RATE_LIMIT_PER_MINUTE", 600),
		PublicCacheTTLSeconds:         envInt("PUBLIC_CACHE_TTL_SECONDS", 5),
		JWTSecret:                     env("JWT_SECRET", "dev-only"),
		FlutterwaveSecretKey:          env("FLUTTERWAVE_SECRET_KEY", ""),
		FlutterwaveWebhookSecret:      env("FLUTTERWAVE_WEBHOOK_SECRET", ""),
		FlutterwaveCustomerPaysFees:   envBool("FLUTTERWAVE_CUSTOMER_PAYS_FEES", false),
		PrivyAppID:                    env("PRIVY_APP_ID", ""),
		PrivyAppSecret:                env("PRIVY_APP_SECRET", ""),
		PrivyVerificationKey:          env("PRIVY_VERIFICATION_KEY", ""),
		PrivyVerificationMode:         env("PRIVY_VERIFICATION_MODE", "auto"),
		DefaultCountry:                env("DEFAULT_COUNTRY", "NG"),
		AllowedCountries:              env("ALLOWED_COUNTRIES", "NG"),
		AdminBootstrapToken:           env("ADMIN_BOOTSTRAP_TOKEN", ""),
		AWSRegion:                     env("AWS_REGION", "eu-north-1"),
		AWSAccessKeyID:                env("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey:            env("AWS_SECRET_ACCESS_KEY", ""),
		S3BucketName:                  firstEnv("S3_BUCKET_NAME", "AWS_S3_BUCKET_NAME"),
		AssetsCDNBase:                 env("ASSETS_CDN_BASE", defaultAssetsCDNBase),
		PublicBaseURL:                 publicBaseURL,
		SMTPHost:                      env("SMTP_HOST", ""),
		SMTPPort:                      env("SMTP_PORT", "587"),
		SMTPUsername:                  smtpUsername,
		SMTPPassword:                  smtpPassword,
		SMTPFromEmail:                 smtpFromEmail,
		SMTPFromName:                  smtpFromName,
		SMTPReplyTo:                   smtpReplyTo,
		SESSNSTopicARN:                env("SES_SNS_TOPIC_ARN", ""),
		BrandLogoURL:                  brandLogoURL,
		WebsiteURL:                    env("WEBSITE_URL", defaultWebsiteURL),
		ProviderPortalURL:             env("PROVIDER_PORTAL_URL", defaultProviderURL),
		ProviderSubscriptionsEnforced: envBool("PROVIDER_SUBSCRIPTIONS_ENFORCED", false),
		ProviderSubscriptionsStartAt:  envTime("PROVIDER_SUBSCRIPTIONS_START_AT"),
	}
}

// ProviderSubscriptionsRequired reports whether paid provider access should be
// enforced at the supplied time. A future start date keeps launch access free
// until that instant without requiring a redeploy.
func (c Config) ProviderSubscriptionsRequired(at time.Time) bool {
	if c.ProviderSubscriptionPolicy != nil {
		return c.ProviderSubscriptionPolicy.Required(at)
	}
	if !c.ProviderSubscriptionsEnforced {
		return false
	}
	return c.ProviderSubscriptionsStartAt == nil || !at.Before(*c.ProviderSubscriptionsStartAt)
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}

	host := env("DB_HOST", "localhost")
	port := env("DB_PORT", "5432")
	user := env("DB_USERNAME", "postgres")
	password := env("DB_PASSWORD", "")
	name := env("DB_NAME", "across_db")

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   host + ":" + port,
		Path:   name,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes"
}

func envTime(key string) *time.Time {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		panic(key + " must be an RFC3339 timestamp")
	}
	value = value.UTC()
	return &value
}
