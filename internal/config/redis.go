package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"strings"

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
	if c.RedisTLSCAFile != "" && c.RedisTLSCABase64 != "" {
		return nil, errors.New("Redis TLS CA must use one configured source")
	}
	if options.TLSConfig == nil {
		if c.RedisTLSCAFile != "" || c.RedisTLSCABase64 != "" {
			return nil, errors.New("Redis TLS CA requires a rediss URL")
		}
		return options, nil
	}
	if options.TLSConfig.InsecureSkipVerify {
		return nil, errors.New("Redis certificate verification cannot be disabled")
	}
	options.TLSConfig.MinVersion = tls.VersionTLS12
	const maximum = 1024 * 1024
	var certificate []byte
	if c.RedisTLSCAFile != "" {
		file, err := os.Open(c.RedisTLSCAFile)
		if err != nil {
			return nil, errors.New("Redis TLS CA file cannot be read")
		}
		defer file.Close()
		certificate, err = io.ReadAll(io.LimitReader(file, maximum+1))
		if err != nil || len(certificate) > maximum {
			return nil, errors.New("Redis TLS CA file must contain a bounded PEM certificate bundle")
		}
	} else if c.RedisTLSCABase64 != "" {
		if len(c.RedisTLSCABase64) > (maximum+2)/3*4 || strings.ContainsAny(c.RedisTLSCABase64, "\r\n") {
			return nil, errors.New("Redis TLS public CA encoding is invalid or oversized")
		}
		var err error
		certificate, err = base64.StdEncoding.Strict().DecodeString(c.RedisTLSCABase64)
		if err != nil || len(certificate) > maximum {
			return nil, errors.New("Redis TLS public CA encoding is invalid or oversized")
		}
	}
	if c.RedisTLSCAFile != "" || c.RedisTLSCABase64 != "" {
		// Only certificates belong in this setting, never a signing/private key.
		rest := certificate
		count := 0
		for len(strings.TrimSpace(string(rest))) > 0 {
			block, remaining := pem.Decode(rest)
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, errors.New("Redis TLS CA must contain only PEM certificates")
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return nil, errors.New("Redis TLS CA certificate is invalid")
			}
			count++
			rest = remaining
		}
		roots := x509.NewCertPool()
		if count == 0 || !roots.AppendCertsFromPEM(certificate) {
			return nil, errors.New("Redis TLS CA must contain PEM certificates")
		}
		// A configured private CA is the explicit trust set; do not broaden it
		// with system roots. ServerName remains the hostname from REDIS_URL.
		options.TLSConfig.RootCAs = roots
	}
	return options, nil
}
