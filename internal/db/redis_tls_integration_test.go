package db

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"across/backend/internal/config"
	"github.com/redis/go-redis/v9"
)

// Opt-in, disposable Linux CI lab. No production URL, env file, archive or
// worker runner is loaded. API and worker share db.New and RedisOptions.
func TestRedisTLSCompatibility(t *testing.T) {
	if os.Getenv("REDIS_TLS_LAB") != "1" {
		t.Skip("isolated Redis TLS lab only")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("lab requires an isolated Linux CI runner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	caPath, tlsAddr, plainAddr := startRedisTLSLab(t, false)
	// Rehearse additive listener activation while a legacy client remains up.
	bootstrap := redis.NewClient(&redis.Options{Addr: plainAddr, Password: "disposable-ci-only", MaxRetries: -1})
	defer bootstrap.Close()
	if bootstrap.ConfigSet(ctx, "tls-port", "6379").Err() != nil || bootstrap.Ping(ctx).Err() != nil {
		t.Fatal("additive TLS activation disrupted the legacy listener")
	}
	base := config.Config{AppEnv: "test", RedisURL: "rediss://default:disposable-ci-only@" + tlsAddr, RedisTLSCAFile: caPath, RedisPoolSize: 4, RedisMinIdleConns: 0, RedisOptional: false,
		DatabaseURL: "postgres://postgres:disposable-ci-only@127.0.0.1:5432/redis_tls_fixture?sslmode=disable", DatabaseMaxConns: 2}
	client := func(c config.Config) *redis.Client {
		opts, err := redisOptions(c)
		if err != nil {
			t.Fatal("client options rejected")
		}
		opts.MaxRetries = -1
		opts.DialTimeout = time.Second
		r := redis.NewClient(opts)
		t.Cleanup(func() { _ = r.Close() })
		return r
	}
	r := client(base)
	if err := r.Ping(ctx).Err(); err != nil {
		t.Fatal("verified TLS/password connection failed")
	}
	t.Run("certificate_and_auth_rejection", func(t *testing.T) {
		for _, kind := range []string{"untrusted", "wrong-password", "missing-password", "wrong-hostname"} {
			t.Run(kind, func(t *testing.T) {
				c := base
				switch kind {
				case "untrusted":
					c.RedisTLSCAFile = ""
				case "wrong-password":
					c.RedisURL = "rediss://default:incorrect@" + tlsAddr
				case "missing-password":
					c.RedisURL = "rediss://" + tlsAddr
				}
				opts, err := redisOptions(c)
				if err != nil {
					t.Fatal("fixture options failed")
				}
				if kind == "wrong-hostname" {
					opts.TLSConfig.ServerName = "unrelated.invalid"
				}
				opts.MaxRetries = -1
				opts.DialTimeout = time.Second
				bad := redis.NewClient(opts)
				defer bad.Close()
				if bad.Ping(ctx).Err() == nil {
					t.Fatal("unsafe connection was accepted")
				}
			})
		}
	})
	t.Run("api_and_worker_shared_store", func(t *testing.T) {
		// The exact shared startup connection constructor, with independent pools.
		// This qualifies transport, not full API/worker process or job semantics.
		api, err := New(ctx, base)
		if err != nil {
			t.Fatal("API store connection failed")
		}
		defer api.Close()
		worker, err := New(ctx, base)
		if err != nil {
			t.Fatal("worker store connection failed")
		}
		defer worker.Close()
		if err := api.Redis.Set(ctx, "tls-lab:api", "synthetic", time.Minute).Err(); err != nil {
			t.Fatal("API write failed")
		}
		if value, err := worker.Redis.Get(ctx, "tls-lab:api").Result(); err != nil || value != "synthetic" {
			t.Fatal("worker read failed")
		}
		sub := worker.Redis.Subscribe(ctx, "tls-lab:events")
		defer sub.Close()
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatal("worker subscribe failed")
		}
		if err := api.Redis.Publish(ctx, "tls-lab:events", "synthetic").Err(); err != nil {
			t.Fatal("API publish failed")
		}
		messageCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		if msg, err := sub.ReceiveMessage(messageCtx); err != nil || msg.Payload != "synthetic" {
			t.Fatal("worker receive failed")
		}
	})
	t.Run("listener_cutover_and_rollback", func(t *testing.T) {
		legacy := base
		legacy.RedisURL = "redis://default:disposable-ci-only@" + plainAddr
		legacy.RedisTLSCAFile = ""
		if client(legacy).Ping(ctx).Err() != nil {
			t.Fatal("initial legacy listener unavailable")
		}
		if r.Set(ctx, "tls-lab:rollback", "preserved", time.Minute).Err() != nil {
			t.Fatal("sentinel write failed")
		}
		if r.ConfigSet(ctx, "port", "0").Err() != nil {
			t.Fatal("TLS-only cutover failed")
		}
		if client(legacy).Ping(ctx).Err() == nil {
			t.Fatal("plaintext listener remained open")
		}
		if r.Ping(ctx).Err() != nil {
			t.Fatal("TLS failed after cutover")
		}
		if r.ConfigSet(ctx, "port", "6380").Err() != nil {
			t.Fatal("listener rollback failed")
		}
		old := client(legacy)
		if v, err := old.Get(ctx, "tls-lab:rollback").Result(); err != nil || v != "preserved" {
			t.Fatal("rollback lost connectivity or data")
		}
		// The new production/staging candidate must still reject plaintext
		// off loopback; rollback is for the retained old release, not a bypass.
		legacy.AppEnv = "production"
		legacy.JWTSecret = strings.Repeat("disposable-ci-only-", 3)
		legacy.DatabaseURL = "postgres://fixture:disposable@db.invalid/fixture?sslmode=verify-full"
		if err := legacy.Validate("worker"); err != nil {
			t.Fatal("loopback control configuration rejected")
		}
		legacy.RedisURL = "redis://default:disposable-ci-only@10.42.0.3:6380"
		if legacy.Validate("worker") == nil {
			t.Fatal("candidate allowed an unsafe rollback config")
		}
	})
	t.Run("expired_certificate", func(t *testing.T) {
		expiredCA, addr, _ := startRedisTLSLab(t, true)
		bad := base
		bad.RedisTLSCAFile, bad.RedisURL = expiredCA, "rediss://default:disposable-ci-only@"+addr
		if client(bad).Ping(ctx).Err() == nil {
			t.Fatal("expired certificate accepted")
		}
	})
}

func startRedisTLSLab(t *testing.T, expired bool) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	// Docker's redis user needs traversal/read access to these synthetic files.
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable CI Redis CA"}, NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	if expired {
		leaf.NotBefore, leaf.NotAfter = now.Add(-24*time.Hour), now.Add(-time.Hour)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{"ca.pem": {Type: "CERTIFICATE", Bytes: caDER}, "server.pem": {Type: "CERTIFICATE", Bytes: leafDER}, "server.key": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tlsPort := "0"
	if expired {
		tlsPort = "6379"
	}
	contents := "bind 0.0.0.0\nprotected-mode yes\nport 6380\ntls-port " + tlsPort + "\ntls-cert-file /fixture/server.pem\ntls-key-file /fixture/server.key\ntls-ca-cert-file /fixture/ca.pem\ntls-auth-clients no\ntls-protocols \"TLSv1.2 TLSv1.3\"\nrequirepass disposable-ci-only\nsave \"\"\nappendonly no\ndir /data\n"
	if err := os.WriteFile(filepath.Join(dir, "redis.conf"), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "atl-redis-tls-lab-" + hex.EncodeToString(nonce[:])
	docker := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("disposable Docker operation %s failed", args[0])
		}
		return strings.TrimSpace(string(output))
	}
	docker("run", "-d", "--pull=never", "--name", name, "--label", "atl.redis-tls-lab="+name,
		"--user", "redis", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--memory", "128m", "--cpus", "0.5", "--pids-limit", "64",
		"--mount", "type=bind,src="+dir+",dst=/fixture,readonly", "--tmpfs", "/data:rw,mode=1777,size=16777216",
		"-p", "127.0.0.1::6379", "-p", "127.0.0.1::6380", "redis:7.2.16", "redis-server", "/fixture/redis.conf")
	t.Cleanup(func() {
		if docker("inspect", "--format", "{{index .Config.Labels \"atl.redis-tls-lab\"}}", name) != name {
			t.Error("cleanup ownership check failed")
			return
		}
		docker("rm", "-f", "-v", name)
	})
	tlsAddr, plainAddr := docker("port", name, "6379/tcp"), docker("port", name, "6380/tcp")
	// Wait for the listener, including the intentionally expired-certificate case.
	for attempt := 0; attempt < 50; attempt++ {
		conn, err := net.DialTimeout("tcp", plainAddr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return filepath.Join(dir, "ca.pem"), tlsAddr, plainAddr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("disposable Redis listener did not start")
	return "", "", ""
}
