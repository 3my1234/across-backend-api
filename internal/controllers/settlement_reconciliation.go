package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"across/backend/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type settlementPageMeta struct {
	PageInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"page_info"`
}
type flutterwaveSettlementList struct {
	Status string             `json:"status"`
	Meta   settlementPageMeta `json:"meta"`
	Data   []struct {
		ID any `json:"id"`
	} `json:"data"`
}
type flutterwaveSettlementTransaction struct {
	SubaccountSettlement *int   `json:"subaccount_settlement"`
	ID                   any    `json:"id"`
	TxRef                string `json:"tx_ref"`
	Currency             string `json:"currency"`
	ChargedAmount        any    `json:"charged_amount"`
	AppFee               any    `json:"app_fee"`
	LegacyAppFee         any    `json:"appfee"`
	MerchantFee          any    `json:"merchant_fee"`
	LegacyMerchantFee    any    `json:"merchantfee"`
	SettlementAmount     any    `json:"settlement_amount"`
}
type flutterwaveSettlementDetail struct {
	Status string             `json:"status"`
	Meta   settlementPageMeta `json:"meta"`
	Data   struct {
		ID                any                                `json:"id"`
		Status            string                             `json:"status"`
		Currency          string                             `json:"currency"`
		Reference         string                             `json:"reference"`
		DisburseRef       string                             `json:"disburse_ref"`
		ProcessorRef      string                             `json:"processor_ref"`
		Destination       string                             `json:"destination"`
		SettlementAccount any                                `json:"settlement_account"`
		ProcessedAt       string                             `json:"processed_date"`
		TransactionCount  int                                `json:"transaction_count"`
		FlagMessage       string                             `json:"flag_message"`
		Transactions      []flutterwaveSettlementTransaction `json:"transactions"`
	} `json:"data"`
}

type settlementJob struct {
	Subaccount           string
	From, To             time.Time
	ListPage, DetailPage int
	DetailSeen           int
	DetailHash, ListHash string
	IDs                  []string
	ListFinished         bool
	Lease                string
}

// ReconcileFlutterwaveSettlements claims bounded, durable cursors. A worker
// never holds a database connection while waiting for the gateway. Failed
// accounts do not block other sellers; leases allow replica-safe recovery.
func ReconcileFlutterwaveSettlements(ctx context.Context, db *pgxpool.Pool, cfg config.Config, client *http.Client) (int, error) {
	if strings.TrimSpace(cfg.FlutterwaveSecretKey) == "" {
		return 0, nil
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	count := 0
	var failures []error
	// Claim one at a time so leases don't expire while earlier sellers run.
	for n := 0; n < 10; n++ {
		job, err := claimSettlementJob(ctx, db)
		if err != nil {
			return count, errors.Join(append(failures, err)...)
		}
		if job == nil {
			break
		}
		updated, err := processSettlementJob(ctx, db, cfg.FlutterwaveSecretKey, client, job)
		count += updated
		if err != nil {
			failures = append(failures, fmt.Errorf("seller settlement reconciliation: %w", err))
			_, releaseErr := db.Exec(ctx, `UPDATE seller_settlement_jobs SET
    locked_until=NULL,lease_token=NULL,next_check_at=now()+interval '30 minutes',
    failure_count=failure_count+1,last_error=$3,updated_at=now()
    WHERE subaccount_id=$1 AND lease_token=$2::uuid`, job.Subaccount, job.Lease, err.Error())
			if releaseErr != nil {
				failures = append(failures, releaseErr)
			}
		}
	}
	return count, errors.Join(failures...)
}

func claimSettlementJob(ctx context.Context, db *pgxpool.Pool) (*settlementJob, error) {
	job := &settlementJob{Lease: uuid.NewString()}
	rows, err := db.Query(ctx, `WITH candidate AS (
  SELECT subaccount_id FROM seller_settlement_jobs
  WHERE next_check_at<=now() AND (locked_until IS NULL OR locked_until<now())
  ORDER BY next_check_at,subaccount_id FOR UPDATE SKIP LOCKED LIMIT 1
 ) UPDATE seller_settlement_jobs j SET lease_token=$1::uuid,
  locked_until=now()+interval '5 minutes',updated_at=now()
 FROM candidate c WHERE j.subaccount_id=c.subaccount_id
 RETURNING j.subaccount_id,j.from_date,j.to_date,j.list_page,j.detail_page,j.settlement_ids,j.list_finished,j.detail_seen,j.detail_hash,j.list_hash`, job.Lease)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	if err := rows.Scan(&job.Subaccount, &job.From, &job.To, &job.ListPage, &job.DetailPage, &job.IDs, &job.ListFinished, &job.DetailSeen, &job.DetailHash, &job.ListHash); err != nil {
		return nil, err
	}
	return job, nil
}

func processSettlementJob(ctx context.Context, db *pgxpool.Pool, secret string, client *http.Client, job *settlementJob) (int, error) {
	count := 0
	for requests := 0; requests < 10; requests++ {
		if len(job.IDs) == 0 {
			if job.ListFinished {
				// Keep failures/holds eligible indefinitely; also revisit recent releases
				// to observe reversals. There is no 120-day exclusion of unpaid sellers.
				tag, err := db.Exec(ctx, `UPDATE seller_settlement_jobs j SET
     from_date=LEAST((now() AT TIME ZONE 'UTC')::date-7,
      COALESCE((SELECT MIN((p.created_at AT TIME ZONE 'UTC')::date)
       FROM payments p JOIN merchant_ledger ml ON ml.order_id=p.order_id AND ml.event_key='order-paid:'||p.order_id::text
       WHERE p.seller_subaccount_id=j.subaccount_id AND p.payment_status='succeeded' AND ml.settlement_status<>'settled'),(now() AT TIME ZONE 'UTC')::date-7)),
     to_date=(now() AT TIME ZONE 'UTC')::date,list_page=1,detail_page=1,
     list_finished=false,settlement_ids='{}',detail_seen=0,detail_hash='',list_hash='',locked_until=NULL,lease_token=NULL,
     checked_at=now(),failure_count=0,last_error='',next_check_at=now()+interval '30 minutes',updated_at=now()
     WHERE subaccount_id=$1 AND lease_token=$2::uuid AND locked_until>now()`, job.Subaccount, job.Lease)
				if err == nil && tag.RowsAffected() != 1 {
					err = errors.New("settlement cursor lease expired")
				}
				return count, err
			}
			page, err := listFlutterwaveSettlementPage(ctx, secret, job.Subaccount, job.From, job.To, job.ListPage, client)
			if err != nil {
				return count, err
			}
			hash := settlementPageHash(page.Data)
			if len(page.Data) > 0 && hash == job.ListHash {
				return count, errors.New("Flutterwave repeated a settlement list page")
			}
			job.ListHash = hash
			for _, item := range page.Data {
				job.IDs = append(job.IDs, settlementNumericID(item.ID))
			}
			job.ListFinished = len(page.Data) == 0 || (page.Meta.PageInfo.TotalPages > 0 && job.ListPage >= page.Meta.PageInfo.TotalPages)
			job.ListPage++
		} else {
			detail, err := getFlutterwaveSettlementPage(ctx, secret, job.IDs[0], job.DetailPage, client)
			if err != nil {
				return count, err
			}
			hash := settlementPageHash(detail.Data.Transactions)
			if len(detail.Data.Transactions) > 0 && hash == job.DetailHash {
				return count, errors.New("Flutterwave repeated a settlement transaction page")
			}
			updated, err := applyFlutterwaveSettlement(ctx, db, job.Subaccount, detail)
			if err != nil {
				return count, err
			}
			count += updated
			job.DetailHash = hash
			job.DetailSeen += len(detail.Data.Transactions)
			finished := len(detail.Data.Transactions) == 0 || (detail.Meta.PageInfo.TotalPages > 0 && job.DetailPage >= detail.Meta.PageInfo.TotalPages)
			if detail.Meta.PageInfo.TotalPages == 0 && detail.Data.TransactionCount > 0 && job.DetailSeen >= detail.Data.TransactionCount {
				finished = true
			}
			if finished && detail.Data.TransactionCount > job.DetailSeen {
				return count, errors.New("settlement pagination ended before all reported transactions were fetched")
			}
			if finished {
				job.IDs = job.IDs[1:]
				job.DetailPage = 1
				job.DetailSeen = 0
				job.DetailHash = ""
			} else {
				job.DetailPage++
			}
		}
		if err := saveSettlementCursor(ctx, db, job, false); err != nil {
			return count, err
		}
	}
	return count, saveSettlementCursor(ctx, db, job, true)
}

func saveSettlementCursor(ctx context.Context, db *pgxpool.Pool, job *settlementJob, release bool) error {
	tag, err := db.Exec(ctx, `UPDATE seller_settlement_jobs SET list_page=$3,
  detail_page=$4,settlement_ids=$5,list_finished=$6,failure_count=0,last_error='',
  locked_until=CASE WHEN $7 THEN NULL ELSE locked_until END,
  lease_token=CASE WHEN $7 THEN NULL ELSE lease_token END,
  next_check_at=CASE WHEN $7 THEN now()+interval '1 minute' ELSE next_check_at END,
  detail_seen=$8,detail_hash=$9,list_hash=$10,updated_at=now()
  WHERE subaccount_id=$1 AND lease_token=$2::uuid AND locked_until>now()`, job.Subaccount, job.Lease, job.ListPage, job.DetailPage, job.IDs, job.ListFinished, release, job.DetailSeen, job.DetailHash, job.ListHash)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("settlement cursor lease expired")
	}
	return err
}

func settlementPageHash(page any) string {
	data, _ := json.Marshal(page)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func fetchSettlementJSON(ctx context.Context, secret, endpoint string, client *http.Client, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Flutterwave settlement HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("invalid settlement response: %w", err)
	}
	return nil
}

func listFlutterwaveSettlementPage(ctx context.Context, secret, subaccount string, from, to time.Time, page int, client *http.Client) (flutterwaveSettlementList, error) {
	var result flutterwaveSettlementList
	query := url.Values{"subaccount_id": {subaccount}, "from": {from.Format("2006-01-02")}, "to": {to.Format("2006-01-02")}, "page": {strconv.Itoa(page)}}
	if err := fetchSettlementJSON(ctx, secret, "https://api.flutterwave.com/v3/settlements?"+query.Encode(), client, &result); err != nil {
		return result, err
	}
	if result.Status != "success" {
		return result, errors.New("Flutterwave did not return successful settlement listing")
	}
	for _, item := range result.Data {
		if settlementNumericID(item.ID) == "" {
			return result, errors.New("settlement listing contains missing ID")
		}
	}
	return result, nil
}

func getFlutterwaveSettlementPage(ctx context.Context, secret, id string, page int, client *http.Client) (flutterwaveSettlementDetail, error) {
	var result flutterwaveSettlementDetail
	endpoint := "https://api.flutterwave.com/v3/settlements/" + url.PathEscape(id) + "?page=" + strconv.Itoa(page)
	if err := fetchSettlementJSON(ctx, secret, endpoint, client, &result); err != nil {
		return result, err
	}
	if result.Status != "success" || settlementNumericID(result.Data.ID) != id {
		return result, errors.New("invalid settlement identity or response status")
	}
	return result, nil
}

// optionalSettlementAmount distinguishes an omitted value from an actual zero.
func optionalSettlementAmount(values ...any) (*float64, error) {
	for _, v := range values {
		if v == nil {
			continue
		}
		value, err := amountValue(v)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1e12 {
			return nil, errors.New("invalid settlement amount")
		}
		value = roundMoney(value)
		return &value, nil
	}
	return nil, nil
}

func applyFlutterwaveSettlement(ctx context.Context, db *pgxpool.Pool, subaccount string, detail flutterwaveSettlementDetail) (int, error) {
	id := settlementNumericID(detail.Data.ID)
	if detail.Status != "success" || id == "" || strings.TrimSpace(subaccount) == "" {
		return 0, errors.New("invalid settlement response")
	}
	state := normalizeSettlementStatus(detail.Data.Status)
	currency := strings.ToUpper(strings.TrimSpace(detail.Data.Currency))
	if len(currency) != 3 {
		return 0, errors.New("settlement currency missing")
	}
	destination := strings.ToLower(strings.TrimSpace(detail.Data.Destination))
	if destination == "" && detail.Data.SettlementAccount != nil {
		destination = "bank account"
	}
	reference := firstNonEmpty(detail.Data.DisburseRef, detail.Data.Reference, detail.Data.ProcessorRef)
	processed := parseFlutterwaveTime(detail.Data.ProcessedAt)
	count := 0
	for _, item := range detail.Data.Transactions {
		if item.SubaccountSettlement != nil && *item.SubaccountSettlement != 1 {
			continue // Parent-account settlement entries cannot release seller funds.
		}
		ref := strings.TrimSpace(item.TxRef)
		transactionID := settlementNumericID(item.ID)
		if transactionID == "" {
			return count, errors.New("settlement transaction ID missing or invalid")
		}
		if ref == "" {
			return count, errors.New("settlement transaction reference missing")
		}
		if item.Currency != "" && !strings.EqualFold(item.Currency, currency) {
			return count, errors.New("settlement transaction currency mismatch")
		}
		actual, err := optionalSettlementAmount(item.SettlementAmount)
		if err != nil {
			return count, err
		}
		// Don't mark money released without an authoritative payout value.
		if state == "settled" && (actual == nil || *actual <= 0) {
			return count, errors.New("completed settlement transaction amount missing")
		}
		charged, err := optionalSettlementAmount(item.ChargedAmount)
		if err != nil {
			return count, err
		}
		appFee, err := optionalSettlementAmount(item.AppFee, item.LegacyAppFee)
		if err != nil {
			return count, err
		}
		merchantFee, err := optionalSettlementAmount(item.MerchantFee, item.LegacyMerchantFee)
		if err != nil {
			return count, err
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return count, err
		}
		// Return the matched order from the payment UPDATE. This constrains ledger
		// writes to the exact verified payment, seller split, currency and tx ID.
		var orderID string
		err = tx.QueryRow(ctx, `UPDATE payments p SET
   charged_amount=COALESCE($4::numeric,p.charged_amount),
   gateway_fee=COALESCE($5::numeric,p.gateway_fee),merchant_fee=COALESCE($6::numeric,p.merchant_fee),
   provider_settlement_amount=COALESCE($7::numeric,p.provider_settlement_amount),
   provider_settlement_id=$8,settlement_reference=$9,settlement_status=$10,settlement_destination=$11,
   settlement_note=$14,
   settled_at=CASE WHEN $10='settled' THEN COALESCE($12::timestamptz,p.settled_at,now()) ELSE p.settled_at END,updated_at=now()
   FROM orders o WHERE p.order_id=o.id AND p.provider='flutterwave' AND p.purpose='order'
   AND p.payment_status='succeeded' AND p.provider_reference=$1 AND o.flutterwave_tx_ref=$1
   AND p.seller_subaccount_id=$2 AND p.currency_code=$3 AND o.currency_code=$3
   AND ($13='' OR p.provider_transaction_id=$13)
   AND (p.settlement_status NOT IN ('settled','reversed') OR
     (p.provider_settlement_id=$8 AND ($10='reversed' OR (p.settlement_status='settled' AND $10='settled'))) OR
     ($10='settled' AND $12::timestamptz IS NOT NULL AND $12::timestamptz>p.settled_at))
   RETURNING o.id::text`, ref, subaccount, currency, charged, appFee, merchantFee, actual, id, reference, state, destination, processed, transactionID, strings.TrimSpace(detail.Data.FlagMessage)).Scan(&orderID)
		if err == nil {
			tag, ledgerErr := tx.Exec(ctx, `UPDATE merchant_ledger SET
    gateway_deductions=CASE WHEN $2::numeric IS NOT NULL THEN GREATEST(expected_net_amount-$2::numeric,0) ELSE gateway_deductions END,
    settlement_amount=COALESCE($2::numeric,settlement_amount),
    net_amount=CASE WHEN $4='settled' THEN $2::numeric ELSE net_amount END,
    settlement_id=$3,settlement_status=$4,settlement_reference=$5,settlement_destination=$6,
    settlement_checked_at=now(),settlement_note=$9,updated_at=now(),
    status=CASE WHEN $4='reversed' THEN 'reversed' WHEN $4='settled' THEN 'paid' ELSE status END,
    paid_at=CASE WHEN $4='settled' THEN COALESCE($7::timestamptz,paid_at,now()) ELSE paid_at END
    WHERE order_id=$1::uuid AND event_key='order-paid:'||$1 AND currency_code=$8`, orderID, actual, id, state, reference, destination, processed, currency, strings.TrimSpace(detail.Data.FlagMessage))
			err = ledgerErr
			if err == nil && tag.RowsAffected() != 1 {
				err = errors.New("seller settlement ledger missing")
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			continue
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return count, err
		}
		if err = tx.Commit(ctx); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func settlementNumericID(value any) string {
	if value == nil {
		return ""
	}
	if v, ok := value.(float64); ok && (math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v <= 0) {
		return ""
	}
	id := gatewayID(value)
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil || parsed == 0 {
		return ""
	}
	return id
}

func normalizeSettlementStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "hold"), strings.Contains(value, "flag"):
		return "on_hold"
	case value == "successful", value == "success", value == "completed":
		return "settled"
	case value == "failed":
		return "failed"
	case value == "reversed":
		return "reversed"
	default:
		return "pending"
	}
}
func parseFlutterwaveTime(value string) any {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC()
	}
	return nil
}
