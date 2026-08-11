package controllers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"log"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"across/backend/internal/services"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var errPrivyNotConfigured = errors.New("Privy server credentials are incomplete")

// --- JWKS Types ---

type jwksKey struct {
	KeyType   string `json:"kty"`
	Curve     string `json:"crv"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	X         string `json:"x"`
	Y         string `json:"y"`
	Use       string `json:"use"`
}

type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

type jwksCache struct {
	parsedKeys map[string]*ecdsa.PublicKey
	fetchedAt  time.Time
}

// --- AuthController ---

type AuthController struct {
	db                   *pgxpool.Pool
	cfg                  config.Config
	httpClient           *http.Client
	privyKeyMu           sync.RWMutex
	privyVerificationKey string
	privyJWKSCache       jwksCache
	privyJWKSMu          sync.RWMutex
	identities           *IdentityService
	rewards              *RewardService
}

func NewAuthController(db *pgxpool.Pool, cfg config.Config) *AuthController {
	return &AuthController{
		db:         db,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		identities: NewIdentityService(db),
		rewards:    NewRewardService(db),
	}
}

func (a *AuthController) Signup(c *fiber.Ctx) error {
	var req struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	var validEmail bool
	req.Email, validEmail = normalizeEmail(req.Email)
	req.FullName = strings.TrimSpace(req.FullName)
	if req.FullName == "" || !validEmail || len(req.Password) < 8 {
		return fiber.NewError(fiber.StatusBadRequest, "name, email, and 8+ character password are required")
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	verificationToken, err := newVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(24 * time.Hour)

	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not create account")
	}
	defer tx.Rollback(c.Context())
	var userID string
	err = tx.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, phone, password_hash, full_name, is_active, email_verified, verification_token, verification_token_expires_at, verification_sent_at, verification_resend_count)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, false, false, $6, $7, now(), 0)
		RETURNING id
	`, countryID, req.Email, strings.TrimSpace(req.Phone), string(hash), req.FullName, verificationToken, expiresAt).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_phone_key" {
				return fiber.NewError(fiber.StatusConflict, "phone number is already linked to another account")
			}
			return fiber.NewError(fiber.StatusConflict, "email account already exists; sign in or resend verification")
		}
		log.Printf("signup insert failed: %v", err)
		return fiber.NewError(fiber.StatusInternalServerError, "could not create account")
	}
	if err := services.QueueVerificationEmail(c.Context(), tx, userID, req.Email, req.FullName, verificationToken, a.cfg.PublicBaseURL); err != nil {
		if errors.Is(err, services.ErrRecipientSuppressed) {
			return fiber.NewError(fiber.StatusBadRequest, "this email address cannot receive account messages; use another email")
		}
		log.Printf("verification email queue failed user_id=%s: %v", userID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "could not queue account verification")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not create account")
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"message": "Account created. Check your email shortly to activate it.", "requires_email_verification": true,
		"verification_delivery_pending": true, "user_id": userID,
	})
}

func (a *AuthController) Login(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email, validEmail := normalizeEmail(req.Email)
	if !validEmail {
		return fiber.NewError(fiber.StatusBadRequest, "valid email required")
	}

	var userID, hash string
	var emailVerified, isActive bool
	err := a.db.QueryRow(c.Context(), `
		SELECT id, password_hash, email_verified, is_active
		FROM users
		WHERE email = $1
	`, email).Scan(&userID, &hash, &emailVerified, &isActive)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid email or password")
	}
	if !isActive || !emailVerified {
		return fiber.NewError(fiber.StatusForbidden, "please verify your email before signing in")
	}
	return a.respondSession(c, userID)
}

func (a *AuthController) Gmail(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	if req.Email == "" || !strings.Contains(req.Email, "@gmail.") && !strings.HasSuffix(req.Email, "@gmail.com") {
		return fiber.NewError(fiber.StatusBadRequest, "valid gmail address required")
	}
	if req.FullName == "" {
		req.FullName = "Across Buyer"
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	var userID string
	err = a.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, password_hash, full_name, is_active, email_verified)
		VALUES ($1, $2, 'gmail-oauth-placeholder', $3, true, true)
		ON CONFLICT (email) DO UPDATE SET full_name = EXCLUDED.full_name, is_active = true, email_verified = true, updated_at = now()
		RETURNING id
	`, countryID, req.Email, req.FullName).Scan(&userID)
	if err != nil {
		return err
	}
	if err := services.QueueWelcomeEmail(c.Context(), a.db, userID, req.Email, req.FullName); err != nil && !errors.Is(err, services.ErrRecipientSuppressed) {
		log.Printf("welcome email queue failed user_id=%s: %v", userID, err)
	}
	_ = CreateNotification(c.Context(), a.db, userID, "", nil, "order_confirmed", "Welcome to Atlantic Express!",
		"Thank you for joining ATLANTIC SHANSU LOGISTICS LIMITED. Start shopping for quality products from China!", nil)
	return a.respondSession(c, userID)
}

func (a *AuthController) VerifyPrivy(c *fiber.Ctx) error {
	var req struct {
		PrivyToken    string `json:"privy_token"`
		PrivyTokenAlt string `json:"privyToken"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	token := strings.TrimSpace(req.PrivyToken)
	if token == "" {
		token = strings.TrimSpace(req.PrivyTokenAlt)
	}
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(c.Get("Authorization"), "Bearer "))
	}
	if token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "privy token required")
	}

	email, name, privyUserID, err := a.verifyPrivyToken(c.Context(), token)
	if err != nil {
		log.Printf("privy verification failed: %v", err)
		if errors.Is(err, errPrivyNotConfigured) {
			return fiber.NewError(fiber.StatusServiceUnavailable, "Google sign-in is not configured on the server")
		}
		code := privyFailureCode(err)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Google session verification failed; please sign in again", "code": code, "request_id": c.Locals("requestid")})
	}

	countryID, err := ensureCountry(c, a.db)
	if err != nil {
		return err
	}
	userID, _, err := a.identities.ResolvePrivy(c.Context(), countryID, privyUserID, email, name)
	if err != nil {
		log.Printf("privy identity resolution failed subject=%s: %v", privyUserID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "Google account could not be linked")
	}
	awarded, rewardErr := a.rewards.AwardWelcome(c.Context(), userID)
	if rewardErr != nil {
		log.Printf("welcome reward failed user_id=%s: %v", userID, rewardErr)
	}
	if err := services.QueueWelcomeEmail(c.Context(), a.db, userID, email, name); err != nil && !errors.Is(err, services.ErrRecipientSuppressed) {
		log.Printf("welcome email queue failed user_id=%s: %v", userID, err)
	}
	if awarded {
		log.Printf("welcome reward awarded user_id=%s", userID)
	}
	return a.respondSession(c, userID)
}

// --- JWKS Support ---

// getPrivyVerificationKey returns a parsed ECDSA P-256 public key for verifying
// Privy access tokens. It supports three modes controlled by PRIVY_VERIFICATION_MODE:
//
//   - "local":   Use the static PRIVY_VERIFICATION_KEY from env config only.
//   - "jwks":    Fetch the key from the Privy JWKS endpoint only.
//   - "auto" (default): Try JWKS first, fall back to static key if JWKS is unavailable.
//
// In "auto" mode, JWKS keys are cached for up to 1 hour. The static key is also
// cached once fetched (it never changes at runtime).
func (a *AuthController) getPrivyVerificationKey(ctx context.Context) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(a.cfg.PrivyVerificationMode))

	// In "local" mode, bypass JWKS entirely and use the static key.
	if mode == "local" {
		return a.getStaticVerificationKey()
	}

	// In "jwks" or "auto" mode, try JWKS first.
	if mode == "jwks" || mode == "auto" || mode == "" {
		jwksPEM, jwksErr := a.fetchAndCacheJWKS(ctx)
		if jwksErr == nil {
			return jwksPEM, nil
		}
		// In "jwks" mode, JWKS failure is fatal.
		if mode == "jwks" {
			return "", fmt.Errorf("jwks: %w", jwksErr)
		}
		// In "auto" mode, log and try the static key.
		log.Printf("privy jwks fetch failed, falling back to static key: %v", jwksErr)
	}

	return a.getStaticVerificationKey()
}

// getStaticVerificationKey returns the cached or configured static verification key.
func (a *AuthController) getStaticVerificationKey() (string, error) {
	a.privyKeyMu.RLock()
	cached := a.privyVerificationKey
	a.privyKeyMu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	// Try fetching from the Privy apps API (original method).
	apiKey, err := a.fetchPrivyAppsAPIKey()
	if err == nil {
		a.privyKeyMu.Lock()
		a.privyVerificationKey = apiKey
		a.privyKeyMu.Unlock()
		return apiKey, nil
	}
	log.Printf("privy apps api fetch failed: %v", err)

	// Fall back to the env-configured PRIVY_VERIFICATION_KEY.
	fallback := strings.TrimSpace(a.cfg.PrivyVerificationKey)
	if fallback != "" {
		if _, parseErr := parsePrivyVerificationKey(fallback); parseErr != nil {
			return "", fmt.Errorf("static key from env is invalid: %w", parseErr)
		}
		a.privyKeyMu.Lock()
		a.privyVerificationKey = fallback
		a.privyKeyMu.Unlock()
		return fallback, nil
	}

	return "", fmt.Errorf("no privy verification key available: %w", err)
}

// fetchPrivyAppsAPIKey fetches the app settings from Privy's apps API and
// extracts the verification_key field. Returns the PEM string.
func (a *AuthController) fetchPrivyAppsAPIKey() (string, error) {
	request, err := http.NewRequest(http.MethodGet, "https://auth.privy.io/api/v1/apps/"+url.PathEscape(a.cfg.PrivyAppID), nil)
	if err != nil {
		return "", err
	}
	request.SetBasicAuth(a.cfg.PrivyAppID, a.cfg.PrivyAppSecret)
	request.Header.Set("privy-app-id", a.cfg.PrivyAppID)
	request.Header.Set("privy-client", "across-backend")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch Privy apps API: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch Privy apps API returned HTTP %d", response.StatusCode)
	}

	var settings struct {
		VerificationKey string `json:"verification_key"`
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("decode Privy app settings: %w", err)
	}
	key := strings.TrimSpace(settings.VerificationKey)
	if key == "" {
		return "", errors.New("Privy app settings did not include a verification_key field")
	}
	if _, parseErr := parsePrivyVerificationKey(key); parseErr != nil {
		return "", fmt.Errorf("verification key from Privy API is invalid: %w", parseErr)
	}
	return key, nil
}

// --- JWKS Implementation ---

// fetchAndCacheJWKS fetches the JWKS from the Privy JWKS endpoint and
// returns a single PEM-encoded public key for use with the existing
// verifyPrivyAccessToken function (which validates ES256-signed JWTs).
//
// When multiple keys exist in the JWKS set, each is cached. The returned
// PEM string is the first valid P-256 key found, so that the existing
// token verification logic works. The cached JWKS is used for subsequent
// calls, with a 1-hour TTL before re-fetching.
func (a *AuthController) fetchAndCacheJWKS(ctx context.Context) (string, error) {
	// Check if we have a fresh cached set of parsed keys.
	if pemKey := a.getCachedJWKSPEM(); pemKey != "" {
		return pemKey, nil
	}

	// Fetch the JWKS from Privy.
	jwksURL := "https://auth.privy.io/api/v1/apps/" + url.PathEscape(a.cfg.PrivyAppID) + "/jwks.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("privy-app-id", a.cfg.PrivyAppID)
	request.Header.Set("privy-client", "across-backend")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch Privy JWKS: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch Privy JWKS returned HTTP %d", response.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(response.Body).Decode(&jwks); err != nil {
		return "", fmt.Errorf("decode Privy JWKS: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return "", errors.New("Privy JWKS returned an empty key set")
	}

	parsed := make(map[string]*ecdsa.PublicKey, len(jwks.Keys))
	var firstPEM string

	// Parse each JWK into an ECDSA P-256 public key.
	for _, key := range jwks.Keys {
		if key.KeyType != "EC" || key.Curve != "P-256" {
			continue
		}
		xBytes, xErr := base64.RawURLEncoding.DecodeString(key.X)
		yBytes, yErr := base64.RawURLEncoding.DecodeString(key.Y)
		if xErr != nil || yErr != nil {
			continue
		}
		publicKey := &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}
		if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
			continue
		}

		kid := key.KeyID
		if kid == "" {
			kid = "default"
		}
		parsed[kid] = publicKey

		// Encode the first valid key as PEM for use with the existing verifier.
		if firstPEM == "" {
			derBytes, derErr := x509.MarshalPKIXPublicKey(publicKey)
			if derErr != nil {
				continue
			}
			pemBlock := &pem.Block{
				Type:  "PUBLIC KEY",
				Bytes: derBytes,
			}
			firstPEM = string(pem.EncodeToMemory(pemBlock))
		}
	}

	if len(parsed) == 0 {
		return "", errors.New("no valid EC P-256 keys found in Privy JWKS")
	}

	// Update the cache.
	a.privyJWKSMu.Lock()
	a.privyJWKSCache = jwksCache{
		parsedKeys: parsed,
		fetchedAt:  time.Now(),
	}
	a.privyJWKSMu.Unlock()

	if firstPEM == "" {
		return "", errors.New("could not encode any JWKS key as PEM")
	}
	return firstPEM, nil
}

// getCachedJWKSPEM checks the in-memory JWKS cache and returns a PEM-encoded
// public key if the cache is fresh (less than 1 hour old). Returns "" if
// the cache is empty or stale.
func (a *AuthController) getCachedJWKSPEM() string {
	a.privyJWKSMu.RLock()
	defer a.privyJWKSMu.RUnlock()

	if a.privyJWKSCache.fetchedAt.IsZero() || time.Since(a.privyJWKSCache.fetchedAt) > time.Hour {
		return ""
	}
	// Return the first key from the cache as PEM.
	for _, pubKey := range a.privyJWKSCache.parsedKeys {
		derBytes, err := x509.MarshalPKIXPublicKey(pubKey)
		if err != nil {
			continue
		}
		pemBlock := &pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: derBytes,
		}
		return string(pem.EncodeToMemory(pemBlock))
	}
	return ""
}

// parseJWKSTokenHeader extracts the key ID (kid) from an unverified JWT header.
// This is used to look up the correct JWKS key when the token specifies one.
func parseJWKSTokenHeader(tokenString string) (string, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) < 2 {
		return "", errors.New("invalid JWT format")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode JWT header: %w", err)
	}
	var header struct {
		KeyID string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return "", fmt.Errorf("parse JWT header: %w", err)
	}
	return header.KeyID, nil
}

// verifyPrivyAccessTokenWithJWKS verifies a Privy access token using the
// JWKS key set. It extracts the kid from the JWT header, looks up the
// matching key in the JWKS cache, and verifies the ES256 signature.
func (a *AuthController) verifyPrivyAccessTokenWithJWKS(token, appID string) (string, error) {
	// Parse the kid from the token header.
	kid, err := parseJWKSTokenHeader(token)
	if err != nil {
		return "", fmt.Errorf("parse token header for kid: %w", err)
	}

	// Look up the key in the JWKS cache.
	a.privyJWKSMu.RLock()
	cache := a.privyJWKSCache
	a.privyJWKSMu.RUnlock()

	if cache.fetchedAt.IsZero() || time.Since(cache.fetchedAt) > time.Hour {
		return "", errors.New("JWKS cache is empty or stale")
	}

	publicKey, ok := cache.parsedKeys[kid]
	if !ok {
		// Try "default" if no kid matched.
		publicKey, ok = cache.parsedKeys["default"]
		if !ok {
			return "", fmt.Errorf("no JWK found for kid=%s", kid)
		}
	}

	// Parse and verify the JWT using the matched key.
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(parsedToken *jwt.Token) (any, error) {
		return publicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("privy.io"), jwt.WithAudience(appID), jwt.WithExpirationRequired())
	if err != nil {
		return "", fmt.Errorf("invalid Privy access token: %w", err)
	}
	if !parsed.Valid || strings.TrimSpace(claims.Subject) == "" {
		return "", errors.New("invalid Privy access token claims")
	}
	return strings.TrimSpace(claims.Subject), nil
}

// PrivyReady checks that the Privy integration is configured and the
// verification key (either from JWKS or static config) is valid.
func (a *AuthController) PrivyReady(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.PrivyAppID) == "" || strings.TrimSpace(a.cfg.PrivyAppSecret) == "" {
		return errPrivyNotConfigured
	}

	mode := strings.ToLower(strings.TrimSpace(a.cfg.PrivyVerificationMode))

	// Validate JWKS endpoint if mode is "jwks" or "auto".
	if mode == "jwks" || mode == "auto" || mode == "" {
		jwksURL := "https://auth.privy.io/api/v1/apps/" + url.PathEscape(a.cfg.PrivyAppID) + "/jwks.json"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
		if err != nil {
			if mode == "jwks" {
				return fmt.Errorf("jwks readiness: %w", err)
			}
		} else {
			request.Header.Set("privy-app-id", a.cfg.PrivyAppID)
			request.Header.Set("privy-client", "across-backend")
			response, reqErr := a.httpClient.Do(request)
			if reqErr == nil {
				defer response.Body.Close()
				var jwks jwksResponse
				if decodeErr := json.NewDecoder(response.Body).Decode(&jwks); decodeErr == nil && len(jwks.Keys) > 0 {
					// JWKS is available and valid.
					return nil
				}
			}
			// If JWKS fails and mode is "jwks", return error.
			if mode == "jwks" {
				return fmt.Errorf("jwks readiness: %w", reqErr)
			}
		}
		// In "auto" mode, fall through to validate the static key.
	}

	// Fall back to validating the static key.
	verificationKey, err := a.getStaticVerificationKey()
	if err != nil {
		return err
	}
	_, err = parsePrivyVerificationKey(verificationKey)
	return err
}

func privyFailureCode(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return "privy_token_expired"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "privy_token_audience_mismatch"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "privy_token_issuer_mismatch"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return "privy_token_signature_invalid"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "verification key returned http 401"), strings.Contains(message, "verification key returned http 403"):
		return "privy_server_credentials_rejected"
	case strings.Contains(message, "parse privy verification key"):
		return "privy_verification_key_invalid"
	case strings.Contains(message, "fetch privy user returned http"):
		return "privy_user_lookup_failed"
	case strings.Contains(message, "no verified email"):
		return "privy_verified_email_missing"
	case strings.Contains(message, "jwks"):
		return "privy_jwks_error"
	default:
		return "privy_token_invalid"
	}
}

func parsePrivyVerificationKey(value string) (*ecdsa.PublicKey, error) {
	normalized := strings.TrimSpace(value)
	for attempt := 0; attempt < 3; attempt++ {
		if strings.HasPrefix(normalized, `"`) && strings.HasSuffix(normalized, `"`) {
			if unquoted, err := strconv.Unquote(normalized); err == nil {
				normalized = strings.TrimSpace(unquoted)
			}
		}
		next := strings.ReplaceAll(normalized, `\\n`, "\n")
		next = strings.ReplaceAll(next, `\n`, "\n")
		next = strings.ReplaceAll(next, `\\r`, "")
		next = strings.ReplaceAll(next, `\r`, "")
		if next == normalized {
			break
		}
		normalized = strings.TrimSpace(next)
	}

	if publicKey, err := parsePrivyPEMOrDER([]byte(normalized)); err == nil {
		return publicKey, nil
	}

	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(normalized)
		if err != nil {
			continue
		}
		if publicKey, err := parsePrivyPEMOrDER(decoded); err == nil {
			return publicKey, nil
		}
	}

	var jwk struct {
		KeyType string `json:"kty"`
		Curve   string `json:"crv"`
		X       string `json:"x"`
		Y       string `json:"y"`
	}
	if err := json.Unmarshal([]byte(normalized), &jwk); err == nil && jwk.KeyType == "EC" && jwk.Curve == "P-256" {
		xBytes, xErr := base64.RawURLEncoding.DecodeString(jwk.X)
		yBytes, yErr := base64.RawURLEncoding.DecodeString(jwk.Y)
		if xErr == nil && yErr == nil {
			publicKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}
			if publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
				return publicKey, nil
			}
		}
	}

	return nil, fmt.Errorf("unsupported verification key format (%s, %d bytes)", privyKeyFormat(normalized), len(normalized))
}

func parsePrivyPEMOrDER(value []byte) (*ecdsa.PublicKey, error) {
	der := value
	if block, _ := pem.Decode(value); block != nil {
		der = block.Bytes
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return nil, errors.New("verification key is not an ECDSA P-256 public key")
	}
	return publicKey, nil
}

func privyKeyFormat(value string) string {
	switch {
	case strings.HasPrefix(value, "-----BEGIN"):
		return "pem"
	case strings.HasPrefix(value, "{"):
		return "json"
	case strings.Contains(value, `\n`):
		return "escaped-pem"
	default:
		return "encoded"
	}
}

func verifyPrivyAccessToken(token, verificationKey, appID string) (string, error) {
	publicKey, err := parsePrivyVerificationKey(verificationKey)
	if err != nil {
		return "", fmt.Errorf("parse Privy verification key: %w", err)
	}
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(parsedToken *jwt.Token) (any, error) {
		return publicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("privy.io"), jwt.WithAudience(appID), jwt.WithExpirationRequired())
	if err != nil {
		return "", fmt.Errorf("invalid Privy access token: %w", err)
	}
	if !parsed.Valid || strings.TrimSpace(claims.Subject) == "" {
		return "", errors.New("invalid Privy access token claims")
	}
	return strings.TrimSpace(claims.Subject), nil
}
func (a *AuthController) verifyPrivyToken(ctx context.Context, token string) (string, string, string, error) {
	if a.cfg.PrivyAppID == "" || a.cfg.PrivyAppSecret == "" {
		return "", "", "", errPrivyNotConfigured
	}

	// Get the verification key (supports JWKS + static key fallback).
	verificationKey, err := a.getPrivyVerificationKey(ctx)
	if err != nil {
		return "", "", "", err
	}

	// Verify the access token using the PEM key.
	privyUserID, err := verifyPrivyAccessToken(token, verificationKey, a.cfg.PrivyAppID)
	if err != nil {
		return "", "", "", err
	}

	// Fetch the Privy user to get email and name.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.privy.io/api/v1/users/"+url.PathEscape(privyUserID), nil)
	if err != nil {
		return "", "", "", err
	}
	request.SetBasicAuth(a.cfg.PrivyAppID, a.cfg.PrivyAppSecret)
	request.Header.Set("privy-app-id", a.cfg.PrivyAppID)

	response, err := a.httpClient.Do(request)
	if err != nil {
		return "", "", "", fmt.Errorf("fetch Privy user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("fetch Privy user returned HTTP %d", response.StatusCode)
	}

	var privyUser struct {
		ID             string `json:"id"`
		LinkedAccounts []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
			Email   string `json:"email"`
			Name    string `json:"name"`
		} `json:"linked_accounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&privyUser); err != nil {
		return "", "", "", fmt.Errorf("decode Privy user: %w", err)
	}
	if privyUser.ID != privyUserID {
		return "", "", "", errors.New("Privy user identity mismatch")
	}

	email := ""
	name := ""
	for _, account := range privyUser.LinkedAccounts {
		if account.Type == "google_oauth" {
			email = strings.TrimSpace(account.Email)
			name = strings.TrimSpace(account.Name)
			break
		}
	}
	if email == "" {
		for _, account := range privyUser.LinkedAccounts {
			if account.Type == "email" {
				email = strings.TrimSpace(account.Address)
				break
			}
		}
	}
	email, validEmail := normalizeEmail(email)
	if !validEmail {
		return "", "", "", errors.New("Privy user has no verified email account")
	}
	if name == "" {
		name = strings.TrimSpace(strings.Split(email, "@")[0])
	}
	if name == "" {
		name = "Atlantic Express Buyer"
	}
	return email, name, privyUserID, nil
}
func (a *AuthController) Session(c *fiber.Ctx) error {
	userID := c.Locals("user_id")
	return c.JSON(fiber.Map{"authenticated": true, "user_id": userID})
}

func (a *AuthController) VerifyEmail(c *fiber.Ctx) error {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "verification token required")
	}

	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())
	var userID, email, fullName string
	err = tx.QueryRow(c.Context(), `
		UPDATE users
		SET email_verified = true,
			is_active = true,
			verification_token = NULL,
			verification_token_expires_at = NULL,
			verification_sent_at = NULL,
			verification_resend_count = 0,
			updated_at = now()
		WHERE verification_token = $1
		  AND verification_token_expires_at >= now()
		RETURNING id, email, full_name
	`, token).Scan(&userID, &email, &fullName)
	if err != nil {
		c.Status(fiber.StatusBadRequest)
		c.Type("html", "utf-8")
		return c.SendString(a.verificationResultPage(false, "Verification link unavailable", "This link is invalid or has expired. Return to the app and request a new verification email."))
	}
	if err := services.QueueWelcomeEmail(c.Context(), tx, userID, email, fullName); err != nil && !errors.Is(err, services.ErrRecipientSuppressed) {
		return err
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}
	if _, err := a.rewards.AwardWelcome(c.Context(), userID); err != nil {
		log.Printf("welcome reward failed user_id=%s: %v", userID, err)
	}

	c.Type("html", "utf-8")
	return c.SendString(a.verificationResultPage(true, "Email verified successfully", "Your Atlantic Express account is active. Return to the mobile app and sign in to continue."))
}

func (a *AuthController) ResendVerification(c *fiber.Ctx) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email required")
	}

	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not resend verification")
	}
	defer tx.Rollback(c.Context())
	var userID, fullName string
	var emailVerified bool
	var sentAt *time.Time
	var resendCount int
	err = tx.QueryRow(c.Context(), `
		SELECT id, full_name, email_verified, verification_sent_at, verification_resend_count
		FROM users
		WHERE email = $1
	`, email).Scan(&userID, &fullName, &emailVerified, &sentAt, &resendCount)
	if err != nil {
		return c.JSON(fiber.Map{"message": "if the email exists, a verification message will be sent"})
	}
	if emailVerified {
		return c.JSON(fiber.Map{"message": "account is already verified"})
	}
	if sentAt != nil && time.Since(*sentAt) < 5*time.Minute {
		return fiber.NewError(fiber.StatusTooManyRequests, "please wait before requesting another verification email")
	}

	verificationToken, err := newVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err = tx.Exec(c.Context(), `
		UPDATE users
		SET verification_token = $2,
			verification_token_expires_at = $3,
			verification_sent_at = now(),
			verification_resend_count = $4,
			updated_at = now()
		WHERE id = $1
	`, userID, verificationToken, expiresAt, resendCount+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not resend verification")
	}
	if err := services.QueueVerificationEmail(c.Context(), tx, userID, email, fullName, verificationToken, a.cfg.PublicBaseURL); err != nil {
		if errors.Is(err, services.ErrRecipientSuppressed) {
			return c.JSON(fiber.Map{"message": "if the email exists, a verification message will be sent"})
		}
		return fiber.NewError(fiber.StatusInternalServerError, "could not queue verification email")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not resend verification")
	}

	return c.JSON(fiber.Map{"message": "verification email queued"})
}

func (a *AuthController) ForgotPassword(c *fiber.Ctx) error {
	const responseMessage = "If an account exists for that email, a password reset link will be sent shortly."
	var req struct {
		Email string `json:"email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	email, valid := normalizeEmail(req.Email)
	if !valid {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": responseMessage})
	}
	tx, err := a.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	defer tx.Rollback(c.Context())
	var userID, fullName string
	var requestedAt *time.Time
	err = tx.QueryRow(c.Context(), `
		SELECT id, full_name, password_reset_requested_at
		FROM users
		WHERE email = $1
		FOR UPDATE
	`, email).Scan(&userID, &fullName, &requestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": responseMessage})
	}
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	if requestedAt != nil && time.Since(*requestedAt) < 5*time.Minute {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": responseMessage})
	}
	token, err := newVerificationToken()
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	digest := resetTokenDigest(token)
	if _, err := tx.Exec(c.Context(), `
		UPDATE users
		SET password_reset_token_hash = $2,
			password_reset_expires_at = now() + interval '30 minutes',
			password_reset_requested_at = now(), updated_at = now()
		WHERE id = $1
	`, userID, digest); err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	if err := services.QueuePasswordResetEmail(c.Context(), tx, userID, email, fullName, token, a.cfg.PublicBaseURL); err != nil {
		if errors.Is(err, services.ErrRecipientSuppressed) {
			return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": responseMessage})
		}
		log.Printf("password reset email queue failed user_id=%s: %v", userID, err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "password recovery is temporarily unavailable")
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": responseMessage})
}

func (a *AuthController) ResetPasswordPage(c *fiber.Ctx) error {
	token := strings.TrimSpace(c.Query("token"))
	valid := false
	if token != "" {
		_ = a.db.QueryRow(c.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM users
				WHERE password_reset_token_hash = $1 AND password_reset_expires_at >= now()
			)
		`, resetTokenDigest(token)).Scan(&valid)
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.Set("Referrer-Policy", "no-referrer")
	c.Type("html", "utf-8")
	if !valid {
		c.Status(fiber.StatusBadRequest)
		return c.SendString(a.verificationResultPage(false, "Reset link unavailable", "This password reset link is invalid, expired, or has already been used. Return to the app and request a new one."))
	}
	brand := `<div style="font-size:22px;font-weight:800;color:#0F3D35;">Atlantic Express</div>`
	if logoURL := strings.TrimSpace(a.cfg.BrandLogoURL); logoURL != "" {
		brand = `<img src="` + html.EscapeString(logoURL) + `" width="170" alt="Atlantic Express" style="max-width:170px;height:auto;">`
	}
	return c.SendString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Reset your Atlantic Express password</title></head><body style="margin:0;background:#F3F7F6;font-family:Arial,Helvetica,sans-serif;color:#142522;"><main style="min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;"><section style="width:100%;max-width:520px;background:#FFFFFF;border:1px solid #E2EBE8;border-radius:18px;padding:36px;box-sizing:border-box;box-shadow:0 12px 40px rgba(15,61,53,.08);"><div style="text-align:center;">` + brand + `</div><h1 style="font-size:26px;margin:28px 0 10px;">Choose a new password</h1><p style="font-size:15px;line-height:1.6;color:#52625E;margin:0 0 24px;">Use at least eight characters. Completing this reset signs out your existing buyer sessions.</p><form method="post" action="/api/v1/auth/reset-password"><input type="hidden" name="token" value="` + html.EscapeString(token) + `"><label style="display:block;font-size:14px;font-weight:700;margin:0 0 8px;">New password</label><input name="password" type="password" minlength="8" autocomplete="new-password" required style="width:100%;box-sizing:border-box;padding:13px;border:1px solid #C9D7D3;border-radius:8px;font-size:16px;margin-bottom:16px;"><label style="display:block;font-size:14px;font-weight:700;margin:0 0 8px;">Confirm password</label><input name="confirm_password" type="password" minlength="8" autocomplete="new-password" required style="width:100%;box-sizing:border-box;padding:13px;border:1px solid #C9D7D3;border-radius:8px;font-size:16px;margin-bottom:22px;"><button type="submit" style="width:100%;border:0;border-radius:8px;background:#FF4747;color:#FFFFFF;font-size:16px;font-weight:700;padding:14px;cursor:pointer;">Reset password</button></form><p style="margin:24px 0 0;text-align:center;font-size:12px;color:#84918E;">ATLANTIC SHANSU LOGISTICS LIMITED</p></section></main></body></html>`)
}

func (a *AuthController) ResetPassword(c *fiber.Ctx) error {
	var req struct {
		Token           string `json:"token" form:"token"`
		Password        string `json:"password" form:"password"`
		ConfirmPassword string `json:"confirm_password" form:"confirm_password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || len(req.Password) < 8 || req.Password != req.ConfirmPassword {
		return a.resetPasswordFailure(c, "The passwords must match and contain at least eight characters.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not reset password")
	}
	var userID string
	err = a.db.QueryRow(c.Context(), `
		UPDATE users
		SET password_hash = $2,
			password_reset_token_hash = NULL,
			password_reset_expires_at = NULL,
			password_reset_requested_at = NULL,
			session_version = session_version + 1,
			updated_at = now()
		WHERE password_reset_token_hash = $1
		  AND password_reset_expires_at >= now()
		RETURNING id
	`, resetTokenDigest(req.Token), string(hash)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return a.resetPasswordFailure(c, "This password reset link is invalid, expired, or has already been used.")
	}
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not reset password")
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	if strings.Contains(strings.ToLower(c.Get(fiber.HeaderContentType)), "application/x-www-form-urlencoded") {
		c.Type("html", "utf-8")
		return c.SendString(a.verificationResultPage(true, "Password reset complete", "Your password has been changed and existing buyer sessions were signed out. Return to the app and sign in with your new password."))
	}
	return c.JSON(fiber.Map{"message": "password reset complete; sign in with your new password"})
}

func (a *AuthController) resetPasswordFailure(c *fiber.Ctx, message string) error {
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	if strings.Contains(strings.ToLower(c.Get(fiber.HeaderContentType)), "application/x-www-form-urlencoded") {
		c.Status(fiber.StatusBadRequest)
		c.Type("html", "utf-8")
		return c.SendString(a.verificationResultPage(false, "Password reset failed", message))
	}
	return fiber.NewError(fiber.StatusBadRequest, message)
}

func (a *AuthController) respondSession(c *fiber.Ctx, userID string) error {
	var sessionVersion int
	if err := a.db.QueryRow(c.Context(), `SELECT session_version FROM users WHERE id = $1`, userID).Scan(&sessionVersion); err != nil {
		return err
	}
	token, expiresAt, err := auth.SignWithVersion(userID, a.cfg.JWTSecret, 24*30*time.Hour, sessionVersion)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"access_token": token,
		"expires_at":   expiresAt,
		"user_id":      userID,
	})
}

func resetTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (a *AuthController) verificationResultPage(success bool, title, message string) string {
	accent := "#0F3D35"
	icon := "&#10003;"
	if !success {
		accent = "#B42318"
		icon = "!"
	}
	brand := `<div style="font-size:22px;font-weight:800;color:#0F3D35;">Atlantic Express</div>`
	if logoURL := strings.TrimSpace(a.cfg.BrandLogoURL); logoURL != "" {
		brand = `<img src="` + html.EscapeString(logoURL) + `" width="170" alt="Atlantic Express" style="max-width:170px;height:auto;">`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title></head><body style="margin:0;background:#F3F7F6;font-family:Arial,Helvetica,sans-serif;color:#142522;"><main style="min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;"><section style="width:100%;max-width:540px;background:#FFFFFF;border:1px solid #E2EBE8;border-radius:18px;padding:36px;box-sizing:border-box;text-align:center;box-shadow:0 12px 40px rgba(15,61,53,.08);">` + brand + `<div style="width:64px;height:64px;border-radius:50%;background:` + accent + `;color:#FFFFFF;display:flex;align-items:center;justify-content:center;margin:28px auto 20px;font-size:34px;font-weight:800;">` + icon + `</div><h1 style="font-size:26px;margin:0 0 14px;">` + html.EscapeString(title) + `</h1><p style="font-size:16px;line-height:1.6;color:#52625E;margin:0;">` + html.EscapeString(message) + `</p><p style="margin:28px 0 0;font-size:12px;color:#84918E;">ATLANTIC SHANSU LOGISTICS LIMITED</p></section></main></body></html>`
}
func newVerificationToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func normalizeEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.Count(email, "@") != 1 {
		return "", false
	}
	return email, true
}

func ensureCountry(c *fiber.Ctx, db *pgxpool.Pool) (string, error) {
	var countryID string
	err := db.QueryRow(c.Context(), `
		INSERT INTO countries_config(country_code, currency_code, base_escrow_days, active_payment_gateways)
		VALUES ('NG', 'NGN', 14, ARRAY['flutterwave'])
		ON CONFLICT (country_code) DO UPDATE SET updated_at = now()
		RETURNING id
	`).Scan(&countryID)
	return countryID, err
}
