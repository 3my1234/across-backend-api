package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func selectPaymentMethods(allowed []string, requested string) ([]string, error) {
	clean := make([]string, 0, len(allowed))
	for _, method := range allowed {
		method = strings.ToLower(strings.TrimSpace(method))
		if method != "" {
			clean = append(clean, method)
		}
	}
	if len(clean) == 0 {
		clean = []string{"card"}
	}
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return clean, nil
	}
	for _, method := range clean {
		if method == requested {
			return []string{requested}, nil
		}
	}
	return nil, fmt.Errorf("payment method %q is not available for this country and currency", requested)
}

func recordOrderPaymentAttempt(ctx context.Context, db *pgxpool.Pool, provider, orderID, userID, countryCode string, amount float64, currency, reference, method string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO payments(
			provider,purpose,order_id,user_id,country_code,amount,currency_code,
			provider_reference,idempotency_key,payment_method
		)
		VALUES($1,'order',$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9)
	`, provider, orderID, userID, strings.ToUpper(countryCode), amount, strings.ToUpper(currency), reference, "checkout:"+provider+":"+reference, method)
	return err
}

func recordSubscriptionPaymentAttempt(ctx context.Context, db *pgxpool.Pool, provider, subscriptionID, userID, countryCode string, amount float64, currency, reference, method string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO payments(
			provider,purpose,provider_subscription_id,user_id,country_code,amount,currency_code,
			provider_reference,idempotency_key,payment_method
		)
		VALUES($1,'provider_subscription',$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9)
	`, provider, subscriptionID, userID, strings.ToUpper(countryCode), amount, strings.ToUpper(currency), reference, "checkout:"+provider+":"+reference, method)
	return err
}

func markPaymentCheckoutReady(ctx context.Context, db *pgxpool.Pool, provider, reference, checkoutURL string) error {
	_, err := db.Exec(ctx, `
		UPDATE payments
		SET payment_status='processing',checkout_url=$3,failure_code='',failure_message='',updated_at=now()
		WHERE provider=$1 AND provider_reference=$2
	`, provider, reference, checkoutURL)
	return err
}

func markPaymentInitializationFailed(ctx context.Context, db *pgxpool.Pool, provider, reference string, failure error) error {
	var code string
	var providerErr *paymentProviderError
	if errors.As(failure, &providerErr) {
		code = fmt.Sprintf("http_%d", providerErr.StatusCode)
	}
	_, err := db.Exec(ctx, `
		UPDATE payments
		SET payment_status='failed',settlement_status='failed',failure_code=$3,failure_message=$4,updated_at=now()
		WHERE provider=$1 AND provider_reference=$2
	`, provider, reference, code, failure.Error())
	return err
}

func settlePaymentLedger(ctx context.Context, tx pgx.Tx, provider, reference, transactionID, providerStatus string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET provider_transaction_id=COALESCE(provider_transaction_id,NULLIF($3,'')),
			payment_status='succeeded',provider_status=$4,
			paid_at=COALESCE(paid_at,now()),
			failure_code='',failure_message='',updated_at=now()
		WHERE provider=$1 AND provider_reference=$2
		  AND purpose='order'
		  AND (provider_transaction_id IS NULL OR provider_transaction_id=$3)
	`, provider, reference, transactionID, providerStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("verified payment attempt was not recorded or transaction ID conflicts")
	}
	return nil
}

func settleSubscriptionPaymentLedger(
	ctx context.Context,
	tx pgx.Tx,
	subscriptionID, userID, countryCode, reference, transactionID string,
	amount float64,
	currency, providerStatus string,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET provider_transaction_id=COALESCE(provider_transaction_id,NULLIF($4,'')),
			payment_status='succeeded',provider_status=$5,
			paid_at=COALESCE(paid_at,now()),failure_code='',failure_message='',updated_at=now()
		WHERE provider=$1 AND purpose='provider_subscription'
		  AND provider_subscription_id=$2::uuid AND provider_reference=$3
		  AND (provider_transaction_id IS NULL OR provider_transaction_id=$4)
	`, flutterwaveProviderName, subscriptionID, reference, transactionID, providerStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	tag, err = tx.Exec(ctx, `
		INSERT INTO payments(
			provider,purpose,provider_subscription_id,user_id,country_code,
			amount,currency_code,provider_reference,provider_transaction_id,
			idempotency_key,payment_method,payment_status,provider_status,paid_at
		)
		VALUES(
			$1,'provider_subscription',$2::uuid,$3::uuid,$4,$5,$6,$7,$8,
			'webhook:' || $1 || ':' || $8,'recurring','succeeded',$9,now()
		)
		ON CONFLICT DO NOTHING
	`, flutterwaveProviderName, subscriptionID, userID, strings.ToUpper(countryCode),
		amount, strings.ToUpper(currency), reference, transactionID, providerStatus)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var existingSubscriptionID, existingReference string
	if err := tx.QueryRow(ctx, `
		SELECT provider_subscription_id::text,provider_reference
		FROM payments
		WHERE provider=$1 AND provider_transaction_id=$2
	`, flutterwaveProviderName, transactionID).Scan(&existingSubscriptionID, &existingReference); err != nil {
		return err
	}
	if existingSubscriptionID != subscriptionID || existingReference != reference {
		return errors.New("provider transaction is already assigned to another payment")
	}
	return nil
}

type paymentWebhookReceipt struct {
	EventKey string
	Done     bool
}

func beginPaymentWebhook(ctx context.Context, db *pgxpool.Pool, provider, eventType, reference, transactionID string, payload []byte) (paymentWebhookReceipt, error) {
	digest := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(digest[:])
	keyParts := strings.Join([]string{eventType, transactionID, reference}, ":")
	if strings.Trim(keyParts, ":") == "" {
		keyParts = payloadHash
	}
	eventDigest := sha256.Sum256([]byte(keyParts))
	eventKey := hex.EncodeToString(eventDigest[:])
	encoded := json.RawMessage(payload)
	var status string
	err := db.QueryRow(ctx, `
		INSERT INTO payment_webhook_events(
			provider,event_key,event_type,provider_reference,provider_transaction_id,payload_sha256,payload
		)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)
		ON CONFLICT(provider,event_key) DO UPDATE SET
			attempt_count=payment_webhook_events.attempt_count+1,
			last_received_at=now(),
			processing_status=CASE
				WHEN payment_webhook_events.processing_status='failed' THEN 'processing'
				ELSE payment_webhook_events.processing_status
			END
		RETURNING processing_status
	`, provider, eventKey, eventType, reference, transactionID, payloadHash, encoded).Scan(&status)
	if err != nil {
		return paymentWebhookReceipt{}, err
	}
	return paymentWebhookReceipt{EventKey: eventKey, Done: status == "processed" || status == "ignored"}, nil
}

func finishPaymentWebhook(ctx context.Context, db *pgxpool.Pool, provider string, receipt paymentWebhookReceipt, status string, failure error) {
	message := ""
	if failure != nil {
		message = failure.Error()
	}
	_, _ = db.Exec(ctx, `
		UPDATE payment_webhook_events
		SET processing_status=$3,last_error=$4,processed_at=CASE WHEN $3 IN ('processed','ignored') THEN now() ELSE NULL END,last_received_at=now()
		WHERE provider=$1 AND event_key=$2
	`, provider, receipt.EventKey, status, message)
}

func (p *PaymentController) PaymentOptions(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	countryCode := strings.ToUpper(strings.TrimSpace(c.Query("country_code")))
	rows, err := p.db.Query(c.Context(), `
		SELECT policy.country_code,policy.currency_code,policy.provider,policy.payment_methods
		FROM payment_method_policies policy
		JOIN countries_config country
		  ON country.country_code=policy.country_code
		 AND country.currency_code=policy.currency_code
		 AND policy.provider=ANY(country.active_payment_gateways)
		WHERE policy.is_active=true AND country.is_active=true
		  AND (
		    ($2<>'' AND policy.country_code=$2)
		    OR ($2='' AND country.id=(SELECT country_id FROM users WHERE id=$1::uuid))
		  )
		ORDER BY policy.provider
	`, userID, countryCode)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "payment options unavailable")
	}
	defer rows.Close()
	options := make([]fiber.Map, 0)
	for rows.Next() {
		var country, currency, provider string
		var methods []string
		if err := rows.Scan(&country, &currency, &provider, &methods); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "payment options unavailable")
		}
		options = append(options, fiber.Map{
			"country_code": strings.TrimSpace(country), "currency": strings.TrimSpace(currency),
			"provider": provider, "payment_methods": methods,
		})
	}
	if len(options) == 0 {
		return fiber.NewError(fiber.StatusNotFound, "no payment options are configured for this country")
	}
	return c.JSON(fiber.Map{"items": options})
}
