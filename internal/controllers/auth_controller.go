package controllers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var errPrivyNotConfigured = errors.New("Privy server credentials are incomplete")

type AuthController struct {
	db                   *pgxpool.Pool
	cfg                  config.Config
	email                *services.EmailService
	httpClient           *http.Client
	privyKeyMu           sync.RWMutex
	privyVerificationKey string
	identities           *IdentityService
	rewards              *RewardService
}

func NewAuthController(db *pgxpool.Pool, cfg config.Config) *AuthController {
	return &AuthController{
		db:         db,
		cfg:        cfg,
		email:      services.NewEmailService(cfg),
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

	var userID string
	err = a.db.QueryRow(c.Context(), `
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

	deliveryPending := false
	if err := a.sendVerificationEmail(c, userID, req.Email, req.FullName, verificationToken); err != nil {
		deliveryPending = true
		log.Printf("verification email delivery failed user_id=%s: %v", userID, err)
	}

	message := "Account created. Check your email to activate it."
	if deliveryPending {
		message = "Account created, but email delivery is delayed. Use Resend verification shortly."
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"message": message, "requires_email_verification": true,
		"verification_delivery_pending": deliveryPending, "user_id": userID,
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
	_ = a.email.SendWelcomeEmail(req.Email, req.FullName)
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
	userID, created, err := a.identities.ResolvePrivy(c.Context(), countryID, privyUserID, email, name)
	if err != nil {
		log.Printf("privy identity resolution failed subject=%s: %v", privyUserID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "Google account could not be linked")
	}
	awarded, rewardErr := a.rewards.AwardWelcome(c.Context(), userID)
	if rewardErr != nil {
		log.Printf("welcome reward failed user_id=%s: %v", userID, rewardErr)
	}
	if created {
		_ = a.email.SendWelcomeEmail(email, name)
	}
	if awarded {
		log.Printf("welcome reward awarded user_id=%s", userID)
	}
	return a.respondSession(c, userID)
}
func (a *AuthController) getPrivyVerificationKey(ctx context.Context) (string, error) {
	a.privyKeyMu.RLock()
	cached := a.privyVerificationKey
	a.privyKeyMu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.privy.io/api/v1/apps/"+url.PathEscape(a.cfg.PrivyAppID), nil)
	if err != nil {
		return "", err
	}
	request.SetBasicAuth(a.cfg.PrivyAppID, a.cfg.PrivyAppSecret)
	request.Header.Set("privy-app-id", a.cfg.PrivyAppID)
	request.Header.Set("privy-client", "across-backend")

	response, requestErr := a.httpClient.Do(request)
	var remoteErr error
	if requestErr != nil {
		remoteErr = fmt.Errorf("fetch Privy verification key: %w", requestErr)
	} else {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			remoteErr = fmt.Errorf("fetch Privy verification key returned HTTP %d", response.StatusCode)
		} else {
			var settings struct {
				VerificationKey string `json:"verification_key"`
			}
			if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
				remoteErr = fmt.Errorf("decode Privy app settings: %w", err)
			} else if strings.TrimSpace(settings.VerificationKey) == "" {
				remoteErr = errors.New("Privy app settings did not include a verification key")
			} else {
				key := strings.TrimSpace(settings.VerificationKey)
				a.privyKeyMu.Lock()
				a.privyVerificationKey = key
				a.privyKeyMu.Unlock()
				return key, nil
			}
		}
	}

	fallback := strings.TrimSpace(a.cfg.PrivyVerificationKey)
	if fallback != "" {
		a.privyKeyMu.Lock()
		a.privyVerificationKey = fallback
		a.privyKeyMu.Unlock()
		return fallback, nil
	}
	return "", remoteErr
}

func (a *AuthController) PrivyReady(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.PrivyAppID) == "" || strings.TrimSpace(a.cfg.PrivyAppSecret) == "" {
		return errPrivyNotConfigured
	}
	_, err := a.getPrivyVerificationKey(ctx)
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

	verificationKey, err := a.getPrivyVerificationKey(ctx)
	if err != nil {
		return "", "", "", err
	}
	privyUserID, err := verifyPrivyAccessToken(token, verificationKey, a.cfg.PrivyAppID)
	if err != nil {
		return "", "", "", err
	}

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

	var userID, email, fullName string
	err := a.db.QueryRow(c.Context(), `
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

	_ = a.email.SendWelcomeEmail(email, fullName)
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

	var userID, fullName string
	var emailVerified bool
	var sentAt *time.Time
	var resendCount int
	err := a.db.QueryRow(c.Context(), `
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
	_, err = a.db.Exec(c.Context(), `
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
	if err := a.sendVerificationEmail(c, userID, email, fullName, verificationToken); err != nil {
		return err
	}

	return c.JSON(fiber.Map{"message": "verification email sent"})
}

func (a *AuthController) respondSession(c *fiber.Ctx, userID string) error {
	token, expiresAt, err := auth.Sign(userID, a.cfg.JWTSecret, 24*30*time.Hour)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"access_token": token,
		"expires_at":   expiresAt,
		"user_id":      userID,
	})
}

func (a *AuthController) sendVerificationEmail(c *fiber.Ctx, userID, toEmail, toName, token string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(a.cfg.PublicBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	link := baseURL + "/api/v1/auth/verify-email?token=" + token
	if err := a.email.SendVerificationEmail(toEmail, toName, link); err != nil {
		return err
	}

	return nil
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
