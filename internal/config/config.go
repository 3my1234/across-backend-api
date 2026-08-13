package config

import (
	"bufio"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppEnv                   string
	HTTPAddr                 string
	AllowedOrigins           string
	DatabaseURL              string
	RedisURL                 string
	RedisAddr                string
	RedisPassword            string
	RedisDB                  int
	RedisOptional            bool
	JWTSecret                string
	FlutterwaveSecretKey     string
	FlutterwaveWebhookSecret string
	PrivyAppID               string
	PrivyAppSecret           string
	PrivyVerificationKey     string
	PrivyVerificationMode    string
	DefaultCountry           string
	AllowedCountries         string
	AdminBootstrapToken      string
	AWSRegion                string
	AWSAccessKeyID           string
	AWSSecretAccessKey       string
	S3BucketName             string
	AssetsCDNBase            string
	PublicBaseURL            string
	SMTPHost                 string
	SMTPPort                 string
	SMTPUsername             string
	SMTPPassword             string
	SMTPFromEmail            string
	SMTPFromName             string
	SMTPReplyTo              string
	SESSNSTopicARN           string
	BrandLogoURL             string
	WebsiteURL               string
}

func Load() Config {
	loadDotEnv(".env")
	publicBaseURL := strings.TrimRight(env("PUBLIC_BASE_URL", ""), "/")
	brandLogoURL := strings.TrimSpace(env("BRAND_LOGO_URL", ""))
	if brandLogoURL == "" || strings.Contains(brandLogoURL, "raw.githubusercontent.com/3my1234/across-mobile-app") {
		if publicBaseURL == "" {
			publicBaseURL = "https://atlanticexpress-api.sportbanter.online"
		}
		brandLogoURL = publicBaseURL + "/api/v1/public/brand/logo.png"
	}

	return Config{
		AppEnv:                   env("APP_ENV", "development"),
		HTTPAddr:                 env("HTTP_ADDR", ":8080"),
		AllowedOrigins:           env("ALLOWED_ORIGINS", "*"),
		DatabaseURL:              databaseURL(),
		RedisURL:                 env("REDIS_URL", ""),
		RedisAddr:                env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:            env("REDIS_PASSWORD", ""),
		RedisDB:                  envInt("REDIS_DB", 0),
		RedisOptional:            envBool("REDIS_OPTIONAL", true),
		JWTSecret:                env("JWT_SECRET", "dev-only"),
		FlutterwaveSecretKey:     env("FLUTTERWAVE_SECRET_KEY", ""),
		FlutterwaveWebhookSecret: env("FLUTTERWAVE_WEBHOOK_SECRET", ""),
		PrivyAppID:               env("PRIVY_APP_ID", ""),
		PrivyAppSecret:           env("PRIVY_APP_SECRET", ""),
		PrivyVerificationKey:     env("PRIVY_VERIFICATION_KEY", ""),
		PrivyVerificationMode:    env("PRIVY_VERIFICATION_MODE", "auto"),
		DefaultCountry:           env("DEFAULT_COUNTRY", "NG"),
		AllowedCountries:         env("ALLOWED_COUNTRIES", "NG"),
		AdminBootstrapToken:      env("ADMIN_BOOTSTRAP_TOKEN", ""),
		AWSRegion:                env("AWS_REGION", "eu-north-1"),
		AWSAccessKeyID:           env("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey:       env("AWS_SECRET_ACCESS_KEY", ""),
		S3BucketName:             firstEnv("S3_BUCKET_NAME", "AWS_S3_BUCKET_NAME"),
		AssetsCDNBase:            env("ASSETS_CDN_BASE", ""),
		PublicBaseURL:            publicBaseURL,
		SMTPHost:                 env("SMTP_HOST", ""),
		SMTPPort:                 env("SMTP_PORT", "587"),
		SMTPUsername:             env("SMTP_USERNAME", ""),
		SMTPPassword:             env("SMTP_PASSWORD", ""),
		SMTPFromEmail:            env("SMTP_FROM_EMAIL", "welcome@sportbanter.online"),
		SMTPFromName:             env("SMTP_FROM_NAME", "Atlantic Express"),
		SMTPReplyTo:              env("SMTP_REPLY_TO", ""),
		SESSNSTopicARN:           env("SES_SNS_TOPIC_ARN", ""),
		BrandLogoURL:             brandLogoURL,
		WebsiteURL:               env("WEBSITE_URL", "https://atlanticexpress-web.sportbanter.online"),
	}
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
