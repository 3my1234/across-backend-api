package playbilling

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Vault encrypts the recoverable purchase token for server reconciliation. The
// separate 256-bit key is never a JWT/signing key or a client-supplied value.
type Vault struct{ aead cipher.AEAD }

func NewVault(encoded string) (*Vault, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("Play token encryption requires a base64 256-bit key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("Play token encryption is unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("Play token encryption is unavailable")
	}
	return &Vault{aead: aead}, nil
}
func TokenHash(token string) string {
	value := sha256.Sum256([]byte(token))
	return hex.EncodeToString(value[:])
}
func validToken(token string) bool {
	return len(token) >= 16 && len(token) <= 4096 && strings.TrimSpace(token) == token && !strings.ContainsAny(token, "\r\n\x00")
}
func (v *Vault) Seal(provider, token string) ([]byte, error) {
	if v == nil || !validToken(token) || provider == "" {
		return nil, errors.New("Play token storage input is invalid")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("Play token storage is unavailable")
	}
	return v.aead.Seal(nonce, nonce, []byte(token), []byte("atl-play-v1:"+provider)), nil
}
func (v *Vault) Open(provider string, encrypted []byte) (string, error) {
	if v == nil || len(encrypted) < v.aead.NonceSize()+v.aead.Overhead() || len(encrypted) > 8192 {
		return "", errors.New("Play stored token is invalid")
	}
	nonce := encrypted[:v.aead.NonceSize()]
	plain, err := v.aead.Open(nil, nonce, encrypted[v.aead.NonceSize():], []byte("atl-play-v1:"+provider))
	if err != nil || !validToken(string(plain)) {
		return "", errors.New("Play stored token is invalid")
	}
	return string(plain), nil
}
