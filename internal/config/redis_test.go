package config

import (
	"crypto/tls"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedisTLSOptions(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{RedisURL: "rediss://default:disposable@redis.example.invalid:6379", RedisTLSCAFile: ca}
	for _, role := range []string{"api", "worker"} {
		preflight := safeConfig()
		preflight.RedisTLSCAFile = ca
		if err := preflight.Validate(role); err != nil {
			t.Fatal("valid private CA rejected by role preflight")
		}
		preflight.RedisTLSCAFile = ca + ".missing"
		if preflight.Validate(role) == nil {
			t.Fatal("role preflight accepted missing private CA")
		}
	}
	opts, err := c.RedisOptions()
	if err != nil || opts.TLSConfig == nil || opts.TLSConfig.RootCAs == nil || opts.TLSConfig.InsecureSkipVerify || opts.TLSConfig.MinVersion != tls.VersionTLS12 || opts.TLSConfig.ServerName != "redis.example.invalid" {
		t.Fatal("verified TLS options were not preserved")
	}
	c.RedisTLSCAFile = ""
	opts, err = c.RedisOptions()
	if err != nil || opts.TLSConfig.RootCAs != nil {
		t.Fatal("system trust should remain available without a custom CA")
	}
	for _, name := range []string{"missing", "malformed", "oversized", "plaintext"} {
		t.Run(name, func(t *testing.T) {
			bad := c
			bad.RedisTLSCAFile = filepath.Join(t.TempDir(), "private-path.pem")
			data := []byte("not a certificate")
			if name == "oversized" {
				data = []byte(strings.Repeat("x", 1024*1024+1))
			}
			if name != "missing" {
				if err := os.WriteFile(bad.RedisTLSCAFile, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "plaintext" {
				bad.RedisURL = "redis://default:disposable@localhost:6379"
				bad.RedisTLSCAFile = ca
			}
			_, err := bad.RedisOptions()
			if err == nil || strings.Contains(err.Error(), "disposable") || strings.Contains(err.Error(), bad.RedisTLSCAFile) {
				t.Fatal("bad TLS configuration must fail without disclosing values")
			}
		})
	}
}
