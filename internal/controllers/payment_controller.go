package controllers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"across/backend/internal/config"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentController struct {
	db         *pgxpool.Pool
	cfg        config.Config
	httpClient *http.Client
	rewards    *RewardService
}

func NewPaymentController(db *pgxpool.Pool, cfg config.Config) *PaymentController {
	return &PaymentController{
		db:      db,
		cfg:     cfg,
		rewards: NewRewardService(db),
		httpClient: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

type tokenizedChargeRequest struct {
	OrderID  string `json:"order_id"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type flutterwaveCheckoutRequest struct {
	OrderID     string `json:"order_id"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	RedirectURL string `json:"redirect_url"`
}

type flutterwaveVerifyRequest struct {
	OrderID       string `json:"order_id"`
	TransactionID string `json:"transaction_id"`
	TxRef         string `json:"tx_ref"`
}

func (p *PaymentController) TokenizedCharge(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req tokenizedChargeRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	var email, token string
	err := p.db.QueryRow(c.Context(), `
		SELECT email, flutterwave_token
		FROM users
		WHERE id = $1 AND is_active = true
	`, userID).Scan(&email, &token)
	if err != nil || token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "saved payment token unavailable")
	}

	var orderAmount float64
	var orderCurrency, orderStatus string
	err = p.db.QueryRow(c.Context(), `SELECT total_amount, currency_code, order_status::text FROM orders WHERE id = $1 AND user_id = $2`, req.OrderID, userID).Scan(&orderAmount, &orderCurrency, &orderStatus)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payable order not found")
	}
	if orderStatus != "Pending" {
		return fiber.NewError(fiber.StatusConflict, "order is not payable")
	}

	txRef := newPaymentReference(req.OrderID)
	if p.mockPaymentsEnabled() {
		if err := p.settleOrderPayment(c.Context(), req.OrderID, txRef, "local-dev", orderAmount, orderCurrency); err != nil {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"tx_ref":  txRef,
			"gateway": "flutterwave",
			"mocked":  true,
		})
	}
	payload := map[string]any{
		"token":    token,
		"currency": orderCurrency,
		"amount":   orderAmount,
		"email":    email,
		"tx_ref":   txRef,
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, "https://api.flutterwave.com/v3/tokenized-charges", bytes.NewReader(body))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "charge request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment gateway unavailable")
	}
	defer resp.Body.Close()

	var gatewayResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gatewayResp); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid payment gateway response")
	}
	if resp.StatusCode >= 300 {
		return c.Status(fiber.StatusBadGateway).JSON(gatewayResp)
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"tx_ref":   txRef,
		"gateway":  "flutterwave",
		"response": gatewayResp,
	})
}

func (p *PaymentController) FlutterwaveCheckout(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req flutterwaveCheckoutRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if strings.TrimSpace(req.OrderID) == "" || strings.TrimSpace(req.Amount) == "" || strings.TrimSpace(req.Currency) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "order_id, amount, and currency are required")
	}

	var email, fullName, phone string
	err := p.db.QueryRow(c.Context(), `
		SELECT COALESCE(email, ''), COALESCE(full_name, ''), COALESCE(phone, '')
		FROM users
		WHERE id = $1 AND is_active = true
	`, userID).Scan(&email, &fullName, &phone)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "user not found")
	}
	if missing := missingPurchasingProfileFields(email, fullName, phone); len(missing) > 0 {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"code":           "PROFILE_INCOMPLETE",
			"message":        "Complete your profile before payment: " + strings.Join(missing, ", "),
			"missing_fields": missing,
		})
	}

	var orderAmount float64
	var orderCurrency, orderStatus string
	err = p.db.QueryRow(c.Context(), `SELECT total_amount, currency_code, order_status::text FROM orders WHERE id = $1 AND user_id = $2`, req.OrderID, userID).Scan(&orderAmount, &orderCurrency, &orderStatus)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payable order not found")
	}
	if orderStatus != "Pending" {
		return fiber.NewError(fiber.StatusConflict, "order is not payable")
	}

	txRef := newPaymentReference(req.OrderID)
	redirectURL := strings.TrimSpace(req.RedirectURL)
	if redirectURL == "" {
		redirectURL = "across://payments/flutterwave"
	}
	if p.mockPaymentsEnabled() {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"tx_ref":        txRef,
			"gateway":       "flutterwave",
			"mocked":        true,
			"redirect_url":  redirectURL,
			"checkout_link": "https://www.flutterwave.com",
		})
	}

	payload := map[string]any{
		"tx_ref":          txRef,
		"amount":          orderAmount,
		"currency":        strings.ToUpper(orderCurrency),
		"redirect_url":    redirectURL,
		"payment_options": "card,banktransfer,ussd",
		"customer":        buildFlutterwaveCustomer(email, fullName, phone),
		"configurations": map[string]any{
			"session_duration":  30,
			"max_retry_attempt": 5,
		},
		"customizations": map[string]any{
			"title":       "Atlantic Express Checkout",
			"description": "Pay securely with Flutterwave",
		},
		"meta": map[string]any{
			"order_id": req.OrderID,
		},
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(c.Context(), http.MethodPost, "https://api.flutterwave.com/v3/payments", bytes.NewReader(body))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "checkout request failed")
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment gateway unavailable")
	}
	defer resp.Body.Close()

	var gatewayResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gatewayResp); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid payment gateway response")
	}
	if resp.StatusCode >= 300 {
		// Extract actual Flutterwave error message
		errMsg := "payment declined by gateway"
		if msg, ok := gatewayResp["message"].(string); ok && msg != "" {
			errMsg = msg
		} else if data, ok := gatewayResp["data"].(map[string]any); ok {
			if msg, ok := data["message"].(string); ok && msg != "" {
				errMsg = msg
			}
		}
		return fiber.NewError(fiber.StatusBadGateway, errMsg)
	}

	link := ""
	if data, ok := gatewayResp["data"].(map[string]any); ok {
		if rawLink, ok := data["link"].(string); ok {
			link = rawLink
		}
	}
	if _, err := p.db.Exec(c.Context(), `
		UPDATE orders
		SET flutterwave_tx_ref = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND order_status = 'Pending'
	`, req.OrderID, userID, txRef); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not record payment attempt")
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"tx_ref":        txRef,
		"gateway":       "flutterwave",
		"checkout_link": link,
		"redirect_url":  redirectURL,
		"response":      gatewayResp,
	})
}

func buildFlutterwaveCustomer(email, fullName, phone string) map[string]any {
	customer := map[string]any{
		"email": strings.TrimSpace(email),
		"name":  strings.TrimSpace(fullName),
	}
	if normalizedPhone := strings.TrimSpace(phone); normalizedPhone != "" {
		customer["phonenumber"] = normalizedPhone
	}
	return customer
}

func (p *PaymentController) mockPaymentsEnabled() bool {
	// Mock payments only when we have NO live key configured
	// Live keys start with "FLWSECK-" followed by the actual hash, not "FLWSECK_TEST_xxx"
	hasLiveKey := p.cfg.FlutterwaveSecretKey != "" &&
		!strings.HasPrefix(p.cfg.FlutterwaveSecretKey, "FLWSECK_TEST") &&
		p.cfg.FlutterwaveSecretKey != "your-key"
	return !hasLiveKey
}

type flutterwaveWebhook struct {
	Event string `json:"event"`
	Type  string `json:"type"`
	Data  struct {
		ID        any    `json:"id"`
		TxRef     string `json:"tx_ref"`
		Reference string `json:"reference"`
		Status    string `json:"status"`
		Amount    any    `json:"amount"`
		Currency  string `json:"currency"`
	} `json:"data"`
}

type flutterwaveVerifyResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID        any    `json:"id"`
		TxRef     string `json:"tx_ref"`
		Reference string `json:"reference"`
		Status    string `json:"status"`
		Amount    any    `json:"amount"`
		Currency  string `json:"currency"`
	} `json:"data"`
}

func (p *PaymentController) FlutterwaveWebhook(c *fiber.Ctx) error {
	raw := c.BodyRaw()
	if !p.validWebhook(raw, c.Get("flutterwave-signature"), c.Get("verif-hash")) {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid webhook signature")
	}

	var event flutterwaveWebhook
	if err := json.Unmarshal(raw, &event); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid webhook")
	}
	eventType := strings.TrimSpace(event.Type)
	if eventType == "" {
		eventType = strings.TrimSpace(event.Event)
	}
	if eventType != "charge.completed" || !successfulFlutterwaveStatus(event.Data.Status) {
		log.Printf("flutterwave payment not settled event=%q status=%q tx_ref=%q transaction_id=%s",
			eventType, event.Data.Status, firstNonEmpty(event.Data.TxRef, event.Data.Reference), stringify(event.Data.ID))
		return c.SendStatus(fiber.StatusAccepted)
	}

	verified, err := p.verifyFlutterwaveTransaction(c.Context(), gatewayID(event.Data.ID), firstNonEmpty(event.Data.TxRef, event.Data.Reference))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "could not verify transaction")
	}
	if !successfulFlutterwaveStatus(verified.Data.Status) {
		return c.SendStatus(fiber.StatusAccepted)
	}
	txRef := firstNonEmpty(verified.Data.TxRef, verified.Data.Reference)
	if strings.HasPrefix(txRef, "PROVIDER-") {
		paidAmount, err := amountValue(verified.Data.Amount)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, "invalid verified payment amount")
		}
		if err := settleProviderSubscription(c.Context(), p.db, txRef, gatewayID(verified.Data.ID), paidAmount, verified.Data.Currency); err != nil {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return c.SendStatus(fiber.StatusOK)
	}
	orderID, err := parseOrderID(txRef)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid tx_ref")
	}
	paidAmount, err := amountValue(verified.Data.Amount)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid verified payment amount")
	}
	if err := p.settleAndNotify(c.Context(), orderID, txRef, gatewayID(verified.Data.ID), paidAmount, verified.Data.Currency); err != nil {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}
	return c.SendStatus(fiber.StatusOK)
}

func (p *PaymentController) VerifyFlutterwavePayment(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	var req flutterwaveVerifyRequest
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.OrderID) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "order_id is required")
	}

	var storedTxRef string
	if err := p.db.QueryRow(c.Context(), `
		SELECT COALESCE(flutterwave_tx_ref, '')
		FROM orders
		WHERE id = $1 AND user_id = $2
	`, req.OrderID, userID).Scan(&storedTxRef); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payment order not found")
	}
	txRef := firstNonEmpty(strings.TrimSpace(req.TxRef), storedTxRef)
	verified, err := p.verifyFlutterwaveTransaction(c.Context(), strings.TrimSpace(req.TransactionID), txRef)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment verification unavailable")
	}
	if !successfulFlutterwaveStatus(verified.Data.Status) {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"payment_state": "pending", "gateway_status": verified.Data.Status})
	}

	verifiedRef := firstNonEmpty(verified.Data.TxRef, verified.Data.Reference)
	orderID, err := parseOrderID(verifiedRef)
	if err != nil || orderID != req.OrderID {
		return fiber.NewError(fiber.StatusConflict, "verified transaction does not match order")
	}
	paidAmount, err := amountValue(verified.Data.Amount)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid verified payment amount")
	}
	if err := p.settleAndNotify(c.Context(), orderID, verifiedRef, gatewayID(verified.Data.ID), paidAmount, verified.Data.Currency); err != nil {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}
	return c.JSON(fiber.Map{"payment_state": "settled", "order_id": orderID})
}

// AdminReconcileFlutterwavePayment recovers a successful charge whose webhook
// could not be settled. Flutterwave remains the source of truth; request values
// can identify a payment but can never mark it paid.
func (p *PaymentController) AdminReconcileFlutterwavePayment(c *fiber.Ctx) error {
	var req flutterwaveVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.OrderID = strings.TrimSpace(req.OrderID)
	req.TxRef = strings.TrimSpace(req.TxRef)
	req.TransactionID = strings.TrimSpace(req.TransactionID)
	if req.OrderID == "" || (req.TxRef == "" && req.TransactionID == "") {
		return fiber.NewError(fiber.StatusBadRequest, "order_id and a transaction reference or ID are required")
	}

	var storedTxRef string
	if err := p.db.QueryRow(c.Context(), `
		SELECT COALESCE(flutterwave_tx_ref, '') FROM orders WHERE id = $1
	`, req.OrderID).Scan(&storedTxRef); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "payment order not found")
	}
	if storedTxRef != "" && req.TxRef != "" && storedTxRef != req.TxRef {
		return fiber.NewError(fiber.StatusConflict, "transaction reference does not match the order checkout")
	}

	verified, err := p.verifyFlutterwaveTransaction(c.Context(), req.TransactionID, firstNonEmpty(req.TxRef, storedTxRef))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment verification unavailable")
	}
	if !successfulFlutterwaveStatus(verified.Data.Status) {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"payment_state": "pending", "gateway_status": verified.Data.Status, "order_id": req.OrderID,
		})
	}

	verifiedRef := firstNonEmpty(verified.Data.TxRef, verified.Data.Reference)
	verifiedOrderID, err := parseOrderID(verifiedRef)
	if err != nil || verifiedOrderID != req.OrderID {
		return fiber.NewError(fiber.StatusConflict, "verified transaction does not match order")
	}
	paidAmount, err := amountValue(verified.Data.Amount)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid verified payment amount")
	}
	transactionID := gatewayID(verified.Data.ID)
	if err := p.settleAndNotify(c.Context(), verifiedOrderID, verifiedRef, transactionID, paidAmount, verified.Data.Currency); err != nil {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}

	actorID, _ := c.Locals("admin_id").(string)
	if _, err := p.db.Exec(c.Context(), `
		INSERT INTO admin_audit_logs(actor_id, action, entity_type, entity_id, priority, metadata)
		VALUES (NULLIF($1, '')::uuid, 'flutterwave_payment_reconciled', 'order', $2::uuid, 'high',
			jsonb_build_object('tx_ref', $3::text, 'transaction_id', $4::text, 'amount', $5::numeric, 'currency', $6::text))
	`, actorID, verifiedOrderID, verifiedRef, transactionID, paidAmount, verified.Data.Currency); err != nil {
		log.Printf("payment reconciliation audit failed order_id=%s: %v", verifiedOrderID, err)
	}

	return c.JSON(fiber.Map{"payment_state": "settled", "order_id": verifiedOrderID, "transaction_id": transactionID})
}

// AdminReconcileProviderSubscription recovers provider payments when a
// Flutterwave webhook was delayed or missed. The gateway response remains the
// only source of truth and settlement is idempotent by transaction ID.
func (p *PaymentController) AdminReconcileProviderSubscription(c *fiber.Ctx) error {
	var req struct {
		TxRef         string `json:"tx_ref"`
		TransactionID string `json:"transaction_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	req.TxRef = strings.TrimSpace(req.TxRef)
	req.TransactionID = strings.TrimSpace(req.TransactionID)
	if !strings.HasPrefix(req.TxRef, "PROVIDER-") || (req.TxRef == "" && req.TransactionID == "") {
		return fiber.NewError(fiber.StatusBadRequest, "a provider tx_ref and transaction_id are required")
	}
	verified, err := p.verifyFlutterwaveTransaction(c.Context(), req.TransactionID, req.TxRef)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "payment verification unavailable")
	}
	if !successfulFlutterwaveStatus(verified.Data.Status) {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"payment_state": "pending", "gateway_status": verified.Data.Status})
	}
	verifiedRef := firstNonEmpty(verified.Data.TxRef, verified.Data.Reference)
	if verifiedRef != req.TxRef || !strings.HasPrefix(verifiedRef, "PROVIDER-") {
		return fiber.NewError(fiber.StatusConflict, "verified transaction does not match the provider subscription")
	}
	paidAmount, err := amountValue(verified.Data.Amount)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid verified payment amount")
	}
	if err := settleProviderSubscription(c.Context(), p.db, verifiedRef, gatewayID(verified.Data.ID), paidAmount, verified.Data.Currency); err != nil {
		return fiber.NewError(fiber.StatusConflict, err.Error())
	}
	return c.JSON(fiber.Map{"payment_state": "settled", "tx_ref": verifiedRef})
}

func (p *PaymentController) settleAndNotify(ctx context.Context, orderID, txRef, transactionID string, paidAmount float64, currency string) error {
	if err := p.settleOrderPayment(ctx, orderID, txRef, transactionID, paidAmount, currency); err != nil {
		return err
	}
	var userID string
	var total float64
	if err := p.db.QueryRow(ctx, `SELECT user_id, total_amount FROM orders WHERE id = $1`, orderID).Scan(&userID, &total); err != nil {
		return errors.New("payment settled but post-payment processing failed")
	}
	_ = CreateNotificationOnce(ctx, p.db, userID, orderID, nil, "payment_received",
		"Payment confirmed", fmt.Sprintf("Your payment of NGN %.2f has been received. Your order is being processed.", total),
		map[string]any{"amount": total, "currency": "NGN"}, "payment-received:"+orderID)
	if _, _, err := p.rewards.AwardPurchase(ctx, userID, orderID, total); err != nil {
		log.Printf("purchase reward failed order_id=%s: %v", orderID, err)
	}
	return nil
}

func (p *PaymentController) validWebhook(raw []byte, signature, legacySignature string) bool {
	if p.cfg.FlutterwaveWebhookSecret == "" {
		return false
	}
	if legacySignature != "" && hmac.Equal([]byte(legacySignature), []byte(p.cfg.FlutterwaveWebhookSecret)) {
		return true
	}
	mac := hmac.New(sha256.New, []byte(p.cfg.FlutterwaveWebhookSecret))
	_, _ = mac.Write(raw)
	digest := mac.Sum(nil)
	if signature != "" && hmac.Equal([]byte(base64.StdEncoding.EncodeToString(digest)), []byte(signature)) {
		return true
	}
	return legacySignature != "" && hmac.Equal([]byte(hex.EncodeToString(digest)), []byte(legacySignature))
}

func (p *PaymentController) verifyFlutterwaveTransaction(ctx context.Context, transactionID, txRef string) (flutterwaveVerifyResponse, error) {
	var result flutterwaveVerifyResponse
	endpoint := ""
	if transactionID != "" {
		if _, err := strconv.ParseInt(transactionID, 10, 64); err == nil {
			endpoint = "https://api.flutterwave.com/v3/transactions/" + transactionID + "/verify"
		}
	}
	if endpoint == "" && txRef != "" {
		endpoint = "https://api.flutterwave.com/v3/transactions/verify_by_reference?tx_ref=" + url.QueryEscape(txRef)
	}
	if endpoint == "" {
		return result, errors.New("transaction id or reference is required")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.FlutterwaveSecretKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, err
	}
	if resp.StatusCode >= 300 {
		return result, fmt.Errorf("flutterwave verification returned %d", resp.StatusCode)
	}
	return result, nil
}

func (p *PaymentController) settleOrderPayment(ctx context.Context, orderID, txRef, gatewayID string, paidAmount float64, paidCurrency string) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var countryID, currencyCode, orderStatus, existingTxRef, fulfillmentMode string
	var providerID *string
	var orderAmount float64
	var promisedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT country_id, currency_code, total_amount,
			COALESCE(delivery_promised_at, now() + interval '14 days'),
			order_status::text, COALESCE(flutterwave_tx_ref, ''), fulfillment_mode, provider_id::text
		FROM orders
		WHERE id = $1
		FOR UPDATE
	`, orderID).Scan(&countryID, &currencyCode, &orderAmount, &promisedAt, &orderStatus, &existingTxRef, &fulfillmentMode, &providerID); err != nil {
		return err
	}
	if !strings.EqualFold(currencyCode, strings.TrimSpace(paidCurrency)) || paidAmount+0.001 < orderAmount {
		return errors.New("payment amount or currency does not match order")
	}
	if orderStatus != "Pending" {
		if existingTxRef == txRef && (orderStatus == "Paid" || orderStatus == "Shipped" || orderStatus == "Delivered" || orderStatus == "Completed") {
			return tx.Commit(ctx)
		}
		return errors.New("order is not payable")
	}

	var batchID any
	packageLabel := "LOCAL-" + shortOrderLabel(orderID)
	if fulfillmentMode == "atlantic_import" {
		importBatchID, batchCode, err := p.ensureDailyBatch(ctx, tx, countryID, promisedAt, orderAmount)
		if err != nil {
			return err
		}
		batchID = importBatchID
		packageLabel = fmt.Sprintf("%s-%s", batchCode, shortOrderLabel(orderID))
	}

	tag, err := tx.Exec(ctx, `
		UPDATE orders
		SET order_status = 'Paid',
			batch_id = $2,
			package_label = COALESCE(NULLIF(package_label, ''), $3),
			flutterwave_tx_ref = $4,
			flutterwave_transaction_id = $5,
			paid_at = COALESCE(paid_at, now()),
			updated_at = now()
		WHERE id = $1 AND order_status = 'Pending'
	`, orderID, batchID, packageLabel, txRef, gatewayID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("order is not payable")
	}
	if fulfillmentMode != "atlantic_import" && providerID != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO merchant_ledger(provider_id,order_id,event_key,currency_code,gross_amount,platform_fee,net_amount,status,available_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,0,$5,'pending',now()+interval '7 days') ON CONFLICT(event_key) DO NOTHING`, *providerID, orderID, "order-paid:"+orderID, currencyCode, orderAmount); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
func (p *PaymentController) ensureDailyBatch(ctx context.Context, tx pgx.Tx, countryID string, promisedAt time.Time, orderAmount float64) (string, string, error) {
	var countryCode, operationalTimezone, batchDate string
	if err := tx.QueryRow(ctx, `
		SELECT c.country_code, tz.effective_timezone,
			(now() AT TIME ZONE tz.effective_timezone)::date::text
		FROM countries_config c
		CROSS JOIN LATERAL (
			SELECT CASE
				WHEN c.country_code = 'NG' AND c.operational_timezone = 'UTC'
					THEN 'Africa/Lagos'
				ELSE c.operational_timezone
			END AS effective_timezone
		) tz
		WHERE c.id = $1 AND c.is_active = true
	`, countryID).Scan(&countryCode, &operationalTimezone, &batchDate); err != nil {
		return "", "", err
	}

	routeKey := strings.ToUpper(strings.TrimSpace(countryCode))
	if routeKey == "NG" {
		routeKey = "LOS"
	}
	transportMode := "air"
	lockKey := fmt.Sprintf("daily-batch:%s:%s:%s:%s", countryID, batchDate, routeKey, transportMode)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return "", "", err
	}

	var batchID, batchCode string
	err := tx.QueryRow(ctx, `
		SELECT id, batch_code
		FROM order_batches
		WHERE country_id = $1
		  AND batch_date = $2::date
		  AND route_key = $3
		  AND transport_mode = $4
		  AND status = 'collecting_funds'::batch_status
		  AND membership_locked = false
		ORDER BY batch_sequence DESC
		LIMIT 1
		FOR UPDATE
	`, countryID, batchDate, routeKey, transportMode).Scan(&batchID, &batchCode)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	if errors.Is(err, pgx.ErrNoRows) {
		var sequence int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(batch_sequence), 0) + 1
			FROM order_batches
			WHERE country_id = $1
			  AND batch_date = $2::date
			  AND route_key = $3
			  AND transport_mode = $4
		`, countryID, batchDate, routeKey, transportMode).Scan(&sequence); err != nil {
			return "", "", err
		}
		batchCode = fmt.Sprintf("%s-%s-%s-%s-%02d",
			strings.ToUpper(countryCode),
			routeKey,
			strings.ToUpper(transportMode),
			strings.ReplaceAll(batchDate, "-", ""),
			sequence,
		)
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_batches(
				batch_code, country_id, batch_date, status, transport_mode, route_key,
				batch_sequence, total_ngn_collected, current_location, notes, opened_at,
				membership_locked
			)
			VALUES (
				$1, $2, $3::date, 'collecting_funds', $4, $5,
				$6, $7, 'Payment settlement', '', now(), false
			)
			RETURNING id
		`, batchCode, countryID, batchDate, transportMode, routeKey, sequence, orderAmount).Scan(&batchID); err != nil {
			return "", "", err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE order_batches
			SET total_ngn_collected = total_ngn_collected + $2,
				updated_at = now(),
				version = version + 1
			WHERE id = $1
			  AND status = 'collecting_funds'::batch_status
			  AND membership_locked = false
		`, batchID, orderAmount); err != nil {
			return "", "", err
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO batch_events(batch_id, event_type, status, location, notes, metadata)
		VALUES (
			$1, 'payment_confirmed', 'collecting_funds', 'Payment settlement', $2,
			jsonb_build_object(
				'operational_timezone', $3::text,
				'business_date', $4::text,
				'amount', $5::numeric
			)
		)
	`, batchID, promisedAt.Format(time.RFC3339), operationalTimezone, batchDate, orderAmount)
	if err != nil {
		return "", "", err
	}

	return batchID, batchCode, nil
}

func shortOrderLabel(orderID string) string {
	clean := strings.ReplaceAll(strings.ToUpper(orderID), "-", "")
	if len(clean) > 8 {
		return clean[:8]
	}
	return clean
}

func newPaymentReference(orderID string) string {
	return "ACROSS-" + orderID + "-" + uuid.NewString()
}

func successfulFlutterwaveStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "successful" || status == "succeeded"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func gatewayID(value any) string {
	switch id := value.(type) {
	case string:
		return id
	case float64:
		return strconv.FormatInt(int64(id), 10)
	default:
		return strings.Trim(fmt.Sprint(id), `"`)
	}
}

func parseOrderID(txRef string) (string, error) {
	const prefix = "ACROSS-"
	if !strings.HasPrefix(txRef, prefix) {
		return "", errors.New("missing prefix")
	}
	rest := strings.TrimPrefix(txRef, prefix)
	if len(rest) < 37 || rest[36] != '-' {
		return "", errors.New("invalid order reference")
	}
	orderID := rest[:36]
	if _, err := uuid.Parse(orderID); err != nil {
		return "", errors.New("invalid order id")
	}
	return orderID, nil
}
func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseAmount(amount string) (float64, error) {
	return strconv.ParseFloat(amount, 64)
}

func amountValue(amount any) (float64, error) {
	switch value := amount.(type) {
	case float64:
		return value, nil
	case string:
		return parseAmount(value)
	default:
		return parseAmount(fmt.Sprint(value))
	}
}
