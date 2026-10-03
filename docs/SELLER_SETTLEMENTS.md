# Seller settlements and deployment

A successful buyer charge confirms payment, not seller bank credit. Imported
orders remain blocked until Flutterwave reports the seller settlement released.
Local-stock fulfilment retains its existing workflow.

## Release order

1. Deploy the backend image after running migrations through
   `052_durable_seller_settlement_reconciliation.sql` against the primary
   database. Keep `RUN_MIGRATIONS=true` on the existing single API, or run
   `/app/across-migrate` with the direct `MIGRATION_DATABASE_URL` before API and
   worker rollout when using transaction pooling.
2. Ensure either `RUN_INLINE_WORKERS=true` or a separate `/app/across-worker`
   process is running with the same database and Flutterwave secret. Settlement
   jobs are checked every minute; a completed scan is scheduled again after
   30 minutes. Page continuations resume after one minute.
3. Deploy the provider portal, then refresh merchant orders. The asset version
   is `20261003-settlement-recovery`. Check expected share, reported payout,
   deductions, destination, last check, and gateway hold reason. A held amount
   must never appear as released.
4. Inspect `/api/v1/admin/ops/queue-health` as an authenticated admin. Its
   `seller_settlements` section reports due jobs, active leases, errors, and
   scheduling lag. Inspect `seller_settlement_jobs.last_error` on failures.
5. Run the report below in the production backend container and compare both
   payment and ledger states with the Flutterwave dashboard. A report is read
   only unless `-reconcile` is explicitly included.
6. Release the mobile changes through the user's chosen Expo/EAS process after
   backend/provider checks. No EAS build is triggered by these changes.

```sh
/app/across-settlements -from 2026-09-30 -to 2026-09-30
# Optional: import due jobs, then report. This records gateway state; it never
# initiates a payout, charge, transfer, refund, or manual balance adjustment.
/app/across-settlements -reconcile -from 2026-09-30 -to 2026-09-30
```

Dates in this command are UTC. Reports are capped at 100 payments; narrow the
date range if needed. The binary is included in the Docker image. On a configured
developer machine, use `go run ./cmd/settlements` with the same flags. `-schema`
lists public table names without changing them.

## Historical backfill and money handling

Migration 052 snapshots existing seller subaccount IDs only when the connected
account predates the payment. The public API has never allowed replacing an
existing payout account. Historical records missing a safe recipient match need
operator investigation; do not guess a replacement split recipient.

The migration enqueues existing successful charges and preserves their current
settlement state. It restores the expected entitlement where earlier pending
reconciliation overwrote `net_amount`. It never marks a charge settled merely
because the buyer paid.

New standard and saved-token checkouts store an immutable seller split recipient.
The saved-token charge includes the same seller split as standard checkout.
Only a matching successful payment, order reference, transaction ID, seller
recipient, and currency can update its ledger. Missing values remain unknown;
invalid, negative, nonfinite, or missing completed payout amounts are rejected.
Pending payouts can record the reported amount and deductions while preserving
the expected entitlement. Released payouts record the actual net amount.

Stale holds cannot downgrade a released payout. Reversals remain visible and
block imported fulfilment. A stale successful replay cannot erase a reversal;
a later payout requires evidence of a newer processed timestamp.

Individual fulfilment, bulk fulfilment, manifest creation, and manifest dispatch
enforce settlement server-side. Provider portal button visibility is additional
guidance, not the authorization boundary.

## Worker behavior and operational limits

PostgreSQL `SKIP LOCKED` claims and five-minute leases protect replica work.
Each invocation handles at most ten sellers and ten gateway requests per seller.
List and transaction page cursors are saved after each page, so no list is cut
off at page 20 or kept entirely in memory. Repeated pages and inconsistent
transaction counts produce observable errors instead of silently discarding
transactions. One failed seller does not stop other accounts.

Failed/on-hold/reversed payments stay eligible without a 120-day cutoff. Recent
releases are revisited over a seven-day window for corrections. Older settled
payments outside that window require an operator-triggered historical scan if
a later correction is reported. The queue's completed scan reschedules from
the oldest unresolved payment or seven days ago, whichever is earlier.

For an operator-requested historical scan, reset only the affected job after
confirming no active lease; never reset payment/ledger financial state:

```sql
UPDATE seller_settlement_jobs
SET from_date=DATE '2026-09-30',to_date=(now() AT TIME ZONE 'UTC')::date,
    list_page=1,detail_page=1,detail_seen=0,detail_hash='',list_hash='',
    settlement_ids='{}',list_finished=false,next_check_at=now(),updated_at=now()
WHERE subaccount_id='REPLACE_WITH_VERIFIED_SUBACCOUNT_ID'
  AND (locked_until IS NULL OR locked_until<now());
```

## Verification

The recovery machine's Go 1.26.4 Linux cross-build hit an internal compiler
error in the pre-existing purego image dependency. An attempt to obtain the
matching Go 1.22.12 compiler timed out. Confirm the Docker image build (which
uses Go 1.22) before deploying; passing Windows tests is not proof of Linux
image compilation. No production compiler or dependency was changed here.

`go test ./...` exercises HTTP envelope/amount validation. For the PostgreSQL
and real endpoint regressions on a configured loopback database:

```powershell
$env:SETTLEMENT_TEST_LOCAL='true'
go test ./... -count=1
```

The opt-in tests use randomly named temporary schemas and leave public tables
unchanged. They cover held funds, replay, reversal, split/currency/transaction
identity, missing-ledger rollback, migration backfill, more than 20 list pages,
multi-page settlements, worker leases, failure isolation, and manifest bypasses.

## Flutterwave threshold

Flutterwave documents an NGN 100 minimum for automatic bank settlements. This
does not apply to settlements into the payout balance. Ask Flutterwave support
about a special settlement when eligible funds remain below the threshold;
application code cannot force Flutterwave to release an on-hold settlement.

Sources: [threshold](https://flutterwave.com/tz/support/payments/minimum-settlement-threshold),
[settlement states and payload](https://developer.flutterwave.com/docs/settlements),
[list pagination](https://developer.flutterwave.com/reference/get-all-settlements),
[transaction pagination](https://developer.flutterwave.com/reference/get-a-settlement),
[saved-token splits](https://developer.flutterwave.com/reference/charge-with-token-1).
