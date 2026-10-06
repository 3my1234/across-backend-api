-- Migration 009 allowed awards only. Redemption writes a negative ledger entry.
-- Keep earned history intact and admit both credits and debits, but not zero.
ALTER TABLE xp_transactions DROP CONSTRAINT IF EXISTS xp_transactions_amount_check;
ALTER TABLE xp_transactions ADD CONSTRAINT xp_transactions_amount_check CHECK (amount <> 0);
