# Provider bank accounts and verification email

## Bank account already registered

A Flutterwave response saying `A subaccount with the account number and bank already exists` is a duplicate-account rejection. It does not establish which Atlantic Express provider owns that account. The API returns HTTP 409 with support guidance, without creating a local connection to an unverified remote account. Concurrent requests for the same provider are serialized using a transaction advisory lock; only one can create the external account.

Do not delete a Flutterwave subaccount with payment or settlement history. Do not rotate AWS keys for this error; bank collection subaccounts are unrelated to image storage.

An authorized administrator should compare the existing Flutterwave collection subaccount with the provider's verified identity, business, bank country, bank code and account number. Check whether its RS identifier is already assigned locally:

```sql
SELECT p.id, p.business_name, p.contact_email,
       a.bank_name, a.account_number_last4, a.status,
       a.flutterwave_subaccount_id, a.flutterwave_subaccount_numeric_id
FROM provider_organizations p
JOIN provider_payout_accounts a ON a.provider_id = p.id
WHERE a.flutterwave_subaccount_id = 'RS-REPLACE_WITH_VERIFIED_SUBACCOUNT_ID';
```

An existing assignment must not be transferred automatically. If the gateway account exists but no local assignment exists, verify ownership and the collection split configuration before a reviewed reconciliation. This release improves error handling and prevents concurrent creation; it does not reconcile the already affected provider automatically.

## Verification email missing

Signup creates an inactive user and queues its verification email in one database transaction. An HTTP 202 response confirms queuing, not inbox delivery. Do not inspect or share `email_outbox.payload` or verification tokens.

In the production database, replace the placeholder with the affected email:

```sql
SELECT u.email, u.email_verified, u.created_at AS account_created,
       e.status, e.attempts, e.created_at AS email_queued,
       e.next_attempt_at, e.sent_at, e.last_error,
       s.reason AS suppression_reason
FROM users u
LEFT JOIN LATERAL (
  SELECT status, attempts, created_at, next_attempt_at, sent_at, last_error
  FROM email_outbox
  WHERE recipient_email = u.email AND template_type = 'verification'
  ORDER BY created_at DESC LIMIT 1
) e ON true
LEFT JOIN email_suppressions s ON s.email = u.email
WHERE u.email = lower(trim('REPLACE_WITH_AFFECTED_EMAIL'));
```

- No row: no account was committed with that email; inspect the signup response.
- `pending` with zero attempts and an old queue timestamp: check that inline workers are running (`RUN_INLINE_WORKERS=true`, the default), or that a separate `across-worker` service is running.
- `retry` or `failed`: inspect `last_error` and worker logs to identify SMTP authentication, connection or recipient failures. Fix the identified configuration rather than guessing.
- `suppressed`: inspect suppression reason and provider feedback. Do not bypass complaint or bounce suppression blindly.
- `sent`: SMTP accepted the message. Check email-provider delivery/bounce events and recipient inbox filtering; acceptance does not prove inbox delivery.

Never rotate SMTP credentials solely because AWS storage credentials changed. Determine the actual SMTP failure first. After correcting the cause, use the portal's Resend verification email button (subject to its five-minute cooldown). The portal blocks repeated clicks while a resend is in flight.

Deploy the backend and admin/provider portal together. These changes require no new database migration or mobile APK.
