package playbilling

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
)

// Environment is opt-in and server-only. No mobile/public configuration contains
// the Publisher credential, token key, or test-purchase permission.
func FromEnvironment(db *pgxpool.Pool) (*Store, error) {
	if os.Getenv("PLAY_BILLING_ENABLED") != "true" {
		return nil, nil
	}
	allowTest := os.Getenv("PLAY_BILLING_ALLOW_TEST") == "true"
	if allowTest && os.Getenv("APP_ENV") != "test" && os.Getenv("APP_ENV") != "staging" && os.Getenv("APP_ENV") != "development" {
		return nil, errors.New("Play license testing requires an isolated environment")
	}
	file, err := os.Open(os.Getenv("PLAY_BILLING_SERVICE_ACCOUNT_FILE"))
	if err != nil {
		return nil, errors.New("Play server credential is unavailable")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 65536 {
		return nil, errors.New("Play server credential is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("Play server credential is invalid")
	}
	source, err := ServiceAccountSource(raw)
	if err != nil {
		return nil, err
	}
	client, err := New(nil, source)
	if err != nil {
		return nil, err
	}
	vault, err := NewVault(os.Getenv("PLAY_BILLING_TOKEN_KEY"))
	if err != nil {
		return nil, err
	}
	return &Store{DB: db, Client: client, Vault: vault, AllowTest: allowTest}, nil
}
