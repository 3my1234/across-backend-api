package playbilling

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func credentials(t *testing.T, endpoint string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"type": "service_account", "client_email": "synthetic@synthetic.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})), "token_uri": endpoint})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCredentialOriginAndConcurrentCache(t *testing.T) {
	if _, err := ServiceAccountSource(credentials(t, "https://untrusted.invalid/token")); err == nil {
		t.Fatal("Untrusted OAuth endpoint accepted")
	}
	var calls atomic.Int32
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.String() != oauthEndpoint || req.Method != "POST" {
			t.Fatal("OAuth credentials sent to unexpected endpoint")
		}
		if err := req.ParseForm(); err != nil || !strings.Contains(req.Form.Get("grant_type"), "jwt-bearer") || req.Form.Get("assertion") == "" {
			t.Fatal("Server-signed OAuth assertion missing")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"synthetic-server-access","token_type":"Bearer","expires_in":3600}`))}, nil
	})}
	source, err := serviceAccountSource(credentials(t, oauthEndpoint), client)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			got, err := source(context.Background())
			if err != nil {
				errors <- err
			} else if got != "synthetic-server-access" {
				errors <- io.ErrUnexpectedEOF
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("OAuth refresh stampede: %d calls", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = source(ctx); err == nil {
		t.Fatal("Cancelled caller accepted")
	}
}
