package playbilling

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Product struct {
	ProductID   string `json:"product_id"`
	BasePlanID  string `json:"base_plan_id"`
	PlanID      string `json:"plan_id"`
	Name        string `json:"name"`
	Months      int    `json:"duration_months"`
	AmountMinor int64  `json:"amount_minor"`
	Purchasable bool   `json:"purchasable"`
	Prepaid     bool   `json:"prepaid"`
}
type Result struct {
	State                string    `json:"state"`
	Active               bool      `json:"active"`
	Pending              bool      `json:"pending"`
	AutoRenews           bool      `json:"auto_renews"`
	NeedsAcknowledgement bool      `json:"needs_acknowledgement"`
	ExpiresAt            time.Time `json:"expires_at"`
	RefundState          string    `json:"refund_state,omitempty"`
}
type Store struct {
	DB        *pgxpool.Pool
	Client    *Client
	Vault     *Vault
	AllowTest bool
	Push      *PushVerifier
}

var ErrBusy = errors.New("A billing operation is already in progress")
var ErrIneligible = errors.New("This provider or product is not eligible for checkout")

func (s *Store) Catalog(ctx context.Context, provider string) ([]Product, error) {
	rows, err := s.DB.Query(ctx, `SELECT m.product_id,m.base_plan_id,m.plan_id::text,p.name,m.duration_months,m.amount_minor,(m.enabled AND p.is_active),m.prepaid
 FROM play_provider_products m JOIN provider_subscription_plans p ON p.id=m.plan_id
 WHERE (m.enabled AND p.is_active) OR EXISTS(SELECT 1 FROM play_provider_purchases owned WHERE owned.provider_id=$1::uuid AND owned.product_id=m.product_id AND owned.base_plan_id=m.base_plan_id)
 ORDER BY p.amount_ngn,m.duration_months LIMIT 100`, provider)
	if err != nil {
		return nil, errors.New("Play catalog is unavailable")
	}
	defer rows.Close()
	items := []Product{}
	for rows.Next() {
		var p Product
		if err = rows.Scan(&p.ProductID, &p.BasePlanID, &p.PlanID, &p.Name, &p.Months, &p.AmountMinor, &p.Purchasable, &p.Prepaid); err != nil {
			return nil, errors.New("Play catalog is unavailable")
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// Process serializes verification and entitlement application per provider.
// Latest Google expiry is assigned, never added to now, so retries/renewals do
// not duplicate paid periods. Token and order constraints are global ownership
// boundaries. Credentials and response details never appear in returned errors.
func (s *Store) Process(ctx context.Context, provider, token, product, base string) (Result, error) {
	if _, err := uuid.Parse(provider); err != nil || !validToken(token) || !skuPattern.MatchString(product) || !skuPattern.MatchString(base) {
		return Result{}, ErrIneligible
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Result{}, errors.New("Billing storage is unavailable")
	}
	defer tx.Rollback(context.Background())
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "provider-renewal:"+provider).Scan(&locked); err != nil {
		return Result{}, errors.New("Billing storage is unavailable")
	}
	if !locked {
		return Result{}, ErrBusy
	}
	var refundState string
	if err = tx.QueryRow(ctx, `SELECT status FROM play_provider_refund_jobs WHERE token_hash=$1 AND provider_id=$2::uuid`, TokenHash(token), provider).Scan(&refundState); err == nil {
		return Result{RefundState: refundState, State: "REFUND"}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, errors.New("Billing storage is unavailable")
	}
	var plan Product
	var email string
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT m.plan_id::text,m.duration_months,m.amount_minor,m.prepaid,u.email,(p.is_active AND o.is_active AND o.verification_status='approved' AND u.is_active AND u.email_verified)
 FROM play_provider_products m JOIN provider_subscription_plans p ON p.id=m.plan_id
 JOIN provider_organizations o ON o.id=$3::uuid JOIN users u ON u.id=o.owner_user_id
 WHERE m.product_id=$1 AND m.base_plan_id=$2`, product, base, provider).Scan(&plan.PlanID, &plan.Months, &plan.AmountMinor, &plan.Prepaid, &email, &eligible)
	if err != nil {
		return Result{}, ErrIneligible
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM play_provider_products WHERE product_id=$1 AND base_plan_id=$2`, product, base).Scan(&enabled); err != nil {
		return Result{}, ErrIneligible
	}
	hash := TokenHash(token)
	var subscription, owner, oldProduct, oldBase string
	err = tx.QueryRow(ctx, `SELECT subscription_id::text,provider_id::text,product_id,base_plan_id FROM play_provider_purchases WHERE token_hash=$1 FOR UPDATE`, hash).Scan(&subscription, &owner, &oldProduct, &oldBase)
	existing := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, errors.New("Billing storage is unavailable")
	}
	if existing && (owner != provider || product != oldProduct || base != oldBase) {
		return Result{}, ErrIneligible
	}
	if !existing && (!enabled || !eligible) {
		return Result{}, ErrIneligible
	}
	if _, err = tx.Exec(ctx, `INSERT INTO play_provider_accounts(provider_id,account_id) VALUES($1::uuid,$2) ON CONFLICT(provider_id) DO NOTHING`, provider, AccountID(provider)); err != nil {
		return Result{}, errors.New("Play account binding is unavailable")
	}
	expected := Expected{ProductID: product, BasePlanID: base, AccountID: AccountID(provider), AllowTest: s.AllowTest, AmountMinor: plan.AmountMinor, Prepaid: plan.Prepaid, VerifyOrder: true}
	now := time.Now().UTC()
	receipt, err := s.Client.Verify(ctx, token, expected, now)
	if err != nil {
		return Result{}, err
	}
	result := Result{State: receipt.State, Active: receipt.Active, Pending: receipt.Pending, AutoRenews: receipt.AutoRenews, NeedsAcknowledgement: receipt.NeedsAcknowledgement, ExpiresAt: receipt.ExpiresAt}
	// Pending purchases are durably tracked for recovery but receive no receipt,
	// paid status, acknowledgement or XP until Google confirms payment.
	if !existing {
		if !receipt.Active && !receipt.Pending {
			return result, nil
		}
		var conflict bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_subscriptions WHERE provider_id=$1::uuid AND ((status='active' AND current_period_end>now()) OR (billing_provider='flutterwave' AND billing_mode='recurring' AND status IN('pending','active','past_due','grace') AND NOT EXISTS(SELECT 1 FROM provider_renewal_preferences r WHERE r.provider_id=provider_subscriptions.provider_id AND r.renewal_status='stopped'))))
   OR EXISTS(SELECT 1 FROM play_provider_purchases WHERE provider_id=$1::uuid AND auto_renews)
   OR EXISTS(SELECT 1 FROM provider_renewal_preferences WHERE provider_id=$1::uuid AND renewal_status='uncertain')`, provider).Scan(&conflict); err != nil {
			return Result{}, errors.New("Billing storage is unavailable")
		}
		if conflict {
			if receipt.Pending {
				return result, nil
			}
			encrypted, e := s.Vault.Seal(provider, token)
			if e != nil {
				return Result{}, e
			}
			_, e = tx.Exec(ctx, `INSERT INTO play_provider_refund_jobs(token_hash,provider_id,product_id,base_plan_id,encrypted_token) VALUES($1,$2::uuid,$3,$4,$5) ON CONFLICT(token_hash) DO NOTHING`, hash, provider, product, base, encrypted)
			if e != nil {
				return Result{}, errors.New("Duplicate purchase recovery could not be recorded")
			}
			if e = tx.Commit(ctx); e != nil {
				return Result{}, errors.New("Duplicate purchase recovery is uncertain")
			}
			// The existing subscription stays intact. Only the newly verified,
			// unfulfilled purchase enters the durable refund path.
			_ = s.reconcileRefunds(ctx)
			return Result{State: "REFUND", RefundState: "requested"}, nil
		}
		subscription = uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,tx_ref,customer_email,expected_amount_ngn,billing_mode,duration_months,billing_provider)
   VALUES($1::uuid,$2::uuid,$3::uuid,'pending',$4,$5,$6::numeric/100,'one_time',$7,'google_play')`, subscription, provider, plan.PlanID, "PLAY-"+subscription, email, plan.AmountMinor, plan.Months)
		if err != nil {
			return Result{}, errors.New("Billing storage is unavailable")
		}
	}
	encrypted, err := s.Vault.Seal(provider, token)
	if err != nil {
		return Result{}, err
	}
	fallback := 5 * time.Minute
	if s.Push != nil {
		fallback = time.Hour
	}
	if receipt.Pending {
		fallback = 5 * time.Minute
	}
	if receipt.NeedsAcknowledgement {
		fallback = time.Minute
	}
	nextCheck := now.Add(fallback)
	if receipt.Active && receipt.ExpiresAt.Add(-5*time.Minute).Before(nextCheck) {
		nextCheck = receipt.ExpiresAt.Add(-5 * time.Minute)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO play_provider_purchases(token_hash,provider_id,subscription_id,product_id,base_plan_id,encrypted_token,state,auto_renews,test_purchase,needs_acknowledgement,next_check_at)
 VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,CASE WHEN $7 IN('SUBSCRIPTION_STATE_EXPIRED','SUBSCRIPTION_STATE_PENDING_PURCHASE_CANCELED') THEN 'infinity'::timestamptz ELSE $11::timestamptz END)
 ON CONFLICT(token_hash) DO UPDATE SET encrypted_token=EXCLUDED.encrypted_token,state=EXCLUDED.state,auto_renews=EXCLUDED.auto_renews,needs_acknowledgement=EXCLUDED.needs_acknowledgement,checked_at=now(),next_check_at=EXCLUDED.next_check_at
 WHERE play_provider_purchases.provider_id=EXCLUDED.provider_id AND play_provider_purchases.subscription_id=EXCLUDED.subscription_id`, hash, provider, subscription, product, base, encrypted, receipt.State, receipt.AutoRenews, receipt.Test, receipt.NeedsAcknowledgement, nextCheck)
	if err != nil || tag.RowsAffected() != 1 {
		return Result{}, errors.New("Billing token ownership could not be committed")
	}
	if receipt.Active {
		var receiptSubscription string
		err = tx.QueryRow(ctx, `INSERT INTO provider_subscription_payments(subscription_id,tx_ref,amount,currency_code,billing_provider,play_order_id,test_purchase,referral_eligible)
   VALUES($1::uuid,$2,$3::numeric/100,'NGN','google_play',$4,$5,$6)
   ON CONFLICT(play_order_id) DO UPDATE SET play_order_id=EXCLUDED.play_order_id
   RETURNING subscription_id::text`, subscription, "PLAY-"+subscription, receipt.AmountMinor, receipt.OrderID, receipt.Test, !receipt.NeedsAcknowledgement).Scan(&receiptSubscription)
		if err != nil || receiptSubscription != subscription {
			return Result{}, errors.New("Billing order ownership could not be committed")
		}
		if !receipt.Test {
			_, err = tx.Exec(ctx, `INSERT INTO payments(provider,purpose,provider_subscription_id,user_id,country_code,amount,currency_code,provider_reference,provider_transaction_id,idempotency_key,payment_method,payment_status,provider_status,paid_at)
 SELECT 'google_play','provider_subscription',s.id,o.owner_user_id,o.country_code,$2::numeric/100,'NGN',s.tx_ref,$3,'play:'||$3,'play_store','succeeded',$4,now()
 FROM provider_subscriptions s JOIN provider_organizations o ON o.id=s.provider_id WHERE s.id=$1::uuid ON CONFLICT DO NOTHING`, subscription, receipt.AmountMinor, receipt.OrderID, receipt.State)
			if err != nil {
				return Result{}, errors.New("Billing financial receipt could not be committed")
			}
			var financialOwner string
			if err = tx.QueryRow(ctx, `SELECT provider_subscription_id::text FROM payments WHERE provider='google_play' AND provider_transaction_id=$1`, receipt.OrderID).Scan(&financialOwner); err != nil || financialOwner != subscription {
				return Result{}, errors.New("Billing financial receipt ownership conflict")
			}
		}
	}
	status := "expired"
	if receipt.Active {
		status = "active"
	}
	if receipt.Pending {
		status = "pending"
	}
	_, err = tx.Exec(ctx, `UPDATE provider_subscriptions SET status=$2,starts_at=COALESCE(starts_at,now()),current_period_end=$3,last_payment_at=CASE WHEN $4 THEN now() ELSE last_payment_at END,updated_at=now(),version=version+1 WHERE id=$1::uuid AND billing_provider='google_play'`, subscription, status, receipt.ExpiresAt, receipt.Active)
	if err != nil {
		return Result{}, errors.New("Billing entitlement could not be committed")
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, errors.New("Billing confirmation is uncertain; restore purchases before paying again")
	}
	_, _ = s.DB.Exec(ctx, `DELETE FROM play_provider_checkout_intents WHERE provider_id=$1::uuid`, provider)
	if receipt.NeedsAcknowledgement {
		// No money/access transaction is repeated when acknowledgement is retried.
		_, ackErr := s.Client.Acknowledge(ctx, token, expected, time.Now().UTC())
		if ackErr == nil {
			result.NeedsAcknowledgement = false
		}
	}
	if receipt.Active && !result.NeedsAcknowledgement {
		// Acknowledgement and referral eligibility converge after an uncertain
		// response; a paid receipt is never inserted twice to retry an XP reward.
		ackTx, e := s.DB.Begin(ctx)
		if e == nil {
			defer ackTx.Rollback(context.Background())
			_, e = ackTx.Exec(ctx, `UPDATE play_provider_purchases SET needs_acknowledgement=false WHERE token_hash=$1 AND provider_id=$2::uuid`, hash, provider)
			if e == nil {
				_, e = ackTx.Exec(ctx, `UPDATE provider_subscription_payments SET referral_eligible=true WHERE play_order_id=$1 AND subscription_id=$2::uuid AND NOT test_purchase`, receipt.OrderID, subscription)
			}
			if e == nil {
				_, e = ackTx.Exec(ctx, `SELECT reconcile_referral_reward(owner_user_id) FROM provider_organizations WHERE id=$1::uuid`, provider)
			}
			if e == nil {
				e = ackTx.Commit(ctx)
			}
		}
		if e != nil {
			result.NeedsAcknowledgement = true
		}
	}
	return result, nil
}

// Prepare reserves checkout under the same provider lock used by Flutterwave,
// preventing two workspaces/devices from opening simultaneous paid checkouts.
func (s *Store) Prepare(ctx context.Context, provider, product, base string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return errors.New("Billing storage is unavailable")
	}
	defer tx.Rollback(context.Background())
	var locked, eligible bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "provider-renewal:"+provider).Scan(&locked); err != nil {
		return errors.New("Billing storage is unavailable")
	}
	if !locked {
		return ErrBusy
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_organizations o JOIN users u ON u.id=o.owner_user_id
 JOIN play_provider_products m ON m.product_id=$2 AND m.base_plan_id=$3 JOIN provider_subscription_plans p ON p.id=m.plan_id
 WHERE o.id=$1::uuid AND o.is_active AND o.verification_status='approved' AND u.is_active AND u.email_verified AND m.enabled AND p.is_active)
 AND NOT EXISTS(SELECT 1 FROM provider_subscriptions s WHERE s.provider_id=$1::uuid AND (s.status='pending' OR (s.status='active' AND s.current_period_end>now()) OR (s.billing_provider='flutterwave' AND s.billing_mode='recurring' AND s.status IN('active','past_due','grace') AND NOT EXISTS(SELECT 1 FROM provider_renewal_preferences r WHERE r.provider_id=s.provider_id AND r.renewal_status='stopped'))))
 AND NOT EXISTS(SELECT 1 FROM play_provider_purchases WHERE provider_id=$1::uuid AND auto_renews)
 AND NOT EXISTS(SELECT 1 FROM provider_renewal_preferences WHERE provider_id=$1::uuid AND renewal_status='uncertain')
 AND NOT EXISTS(SELECT 1 FROM play_provider_checkout_intents WHERE provider_id=$1::uuid AND expires_at>now())`, provider, product, base).Scan(&eligible)
	if err != nil {
		return errors.New("Billing storage is unavailable")
	}
	if !eligible {
		return ErrIneligible
	}
	if _, err = tx.Exec(ctx, `INSERT INTO play_provider_accounts(provider_id,account_id) VALUES($1::uuid,$2) ON CONFLICT(provider_id) DO NOTHING`, provider, AccountID(provider)); err != nil {
		return errors.New("Play account binding is unavailable")
	}
	_, err = tx.Exec(ctx, `INSERT INTO play_provider_checkout_intents VALUES($1::uuid,$2,$3,now()+interval '15 minutes') ON CONFLICT(provider_id) DO UPDATE SET product_id=EXCLUDED.product_id,base_plan_id=EXCLUDED.base_plan_id,expires_at=EXCLUDED.expires_at`, provider, product, base)
	if err != nil {
		return errors.New("Billing storage is unavailable")
	}
	return tx.Commit(ctx)
}

// Reconcile claims bounded work without parallel duplicate claims. Failure
// schedules a later retry; a stale or unreachable store never extends expiry.
func (s *Store) Reconcile(ctx context.Context) error {
	if err := s.recoverNotificationPurchases(ctx); err != nil {
		return err
	}
	// Events can precede mobile verification. Retain unmatched hashes until a
	// purchase is restored, and always read current Google state out of order.
	_, err := s.DB.Exec(ctx, `WITH ready AS(SELECT e.message_id FROM play_provider_events e JOIN play_provider_purchases p ON p.token_hash=e.token_hash WHERE e.processed_at IS NULL ORDER BY e.received_at LIMIT 100 FOR UPDATE OF e SKIP LOCKED), claimed AS(UPDATE play_provider_events e SET processed_at=now() FROM ready WHERE ready.message_id=e.message_id RETURNING e.token_hash) UPDATE play_provider_purchases p SET next_check_at='-infinity'::timestamptz FROM claimed WHERE claimed.token_hash=p.token_hash`)
	if err != nil {
		return errors.New("Play notification reconciliation is unavailable")
	}
	_, _ = s.DB.Exec(ctx, `DELETE FROM play_provider_events WHERE message_id IN(SELECT message_id FROM play_provider_events WHERE received_at<now()-interval '14 days' LIMIT 1000)`)
	if err := s.reconcileRefunds(ctx); err != nil {
		return err
	}
	rows, err := s.DB.Query(ctx, `WITH due AS (SELECT token_hash FROM play_provider_purchases WHERE next_check_at<=now() ORDER BY next_check_at LIMIT 50 FOR UPDATE SKIP LOCKED)
 UPDATE play_provider_purchases p SET next_check_at=now()+interval '5 minutes' FROM due WHERE p.token_hash=due.token_hash
 RETURNING p.provider_id::text,p.encrypted_token,p.product_id,p.base_plan_id`)
	if err != nil {
		return errors.New("Play reconciliation storage is unavailable")
	}
	type work struct {
		provider, product, base string
		encrypted               []byte
	}
	items := []work{}
	for rows.Next() {
		var w work
		if err = rows.Scan(&w.provider, &w.encrypted, &w.product, &w.base); err != nil {
			rows.Close()
			return errors.New("Play reconciliation storage is unavailable")
		}
		items = append(items, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("Play reconciliation storage is unavailable")
	}
	pending := make(chan work, len(items))
	results := make(chan error, len(items))
	for _, w := range items {
		pending <- w
	}
	close(pending)
	var group sync.WaitGroup
	for i := 0; i < min(4, len(items)); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for w := range pending {
				token, e := s.Vault.Open(w.provider, w.encrypted)
				if e == nil {
					_, e = s.Process(ctx, w.provider, token, w.product, w.base)
				}
				results <- e
			}
		}()
	}
	group.Wait()
	close(results)
	failures := 0
	for e := range results {
		if e != nil {
			failures++
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failures > 0 {
		return fmt.Errorf("Play reconciliation: %d checks deferred", failures)
	}
	return nil
}
