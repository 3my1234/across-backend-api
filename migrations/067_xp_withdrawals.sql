CREATE TABLE xp_withdrawals (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 request_key UUID NOT NULL,
 points INTEGER NOT NULL CHECK(points>=1000),
 bank_name TEXT NOT NULL,
 account_number TEXT NOT NULL CHECK(account_number ~ '^[0-9]{10}$'),
 account_name TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','processing','paid','rejected')),
 payout_reference TEXT NOT NULL DEFAULT '',
 admin_note TEXT NOT NULL DEFAULT '',
 reviewed_by UUID REFERENCES admins(id) ON DELETE SET NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,request_key),
 CHECK(status<>'paid' OR length(trim(payout_reference))>0)
);
CREATE UNIQUE INDEX xp_withdrawals_one_open ON xp_withdrawals(user_id) WHERE status IN ('pending','processing');
CREATE UNIQUE INDEX xp_withdrawals_payout_unique ON xp_withdrawals(payout_reference) WHERE payout_reference<>'';
CREATE INDEX xp_withdrawals_review ON xp_withdrawals(status,created_at);

-- Keep historical earned amounts; replace only the retired policy explanation.
UPDATE notifications SET body=replace(body,
 ' XP can reduce only the Atlantic Express service fee on eligible NGN product orders: 1 XP = NGN 1. It cannot pay for seller products, delivery or Flutterwave charges, and cannot be withdrawn.',
 ' 1 XP = NGN 1. Withdraw from 1,000 XP after admin review. Existing points remain eligible. XP cannot be used at checkout.')
WHERE type IN ('xp_earned','review_request');
