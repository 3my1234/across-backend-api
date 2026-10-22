package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"

	"github.com/redis/go-redis/v9"
)

// RedisOptions is shared by offline preflight and both service connection paths.
// Errors deliberately omit URLs, credentials, file contents and OS error details.
func (c Config) RedisOptions() (*redis.Options, error) {
	options := &redis.Options{Addr: c.RedisAddr, Password: c.RedisPassword, DB: c.RedisDB}
	if c.RedisURL != "" {
		var err error
		options, err = redis.ParseURL(c.RedisURL)
		if err != nil {
			return nil, errors.New("Redis URL is invalid")
		}
	}
	if options.TLSConfig == nil {
		if c.RedisTLSCAFile != "" {
			return nil, errors.New("Redis TLS CA requires a rediss URL")
		}
		return options, nil
	}
	if options.TLSConfig.InsecureSkipVerify {
		return nil, errors.New("Redis certificate verification cannot be disabled")
	}
	options.TLSConfig.MinVersion = tls.VersionTLS12
	if c.RedisTLSCAFile != "" {
		file, err := os.Open(c.RedisTLSCAFile)
		if err != nil {
			return nil, errors.New("Redis TLS CA file cannot be read")
		}
		defer file.Close()
		const maximum = 1024 * 1024
		pem, err := io.ReadAll(io.LimitReader(file, maximum+1))
		roots := x509.NewCertPool()
		if err != nil || len(pem) > maximum || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("Redis TLS CA file must contain a bounded PEM certificate bundle")
		}
		// A configured private CA is the explicit trust set; do not broaden it
		// with system roots. ServerName remains the hostname from REDIS_URL.
		options.TLSConfig.RootCAs = roots
	}
	return options, nil
}
