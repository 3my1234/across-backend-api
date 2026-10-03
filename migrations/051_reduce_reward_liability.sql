-- Reduce promotional liabilities while preserving rewards that users already claimed.
ALTER TABLE review_rewards
  ALTER COLUMN reward_amount SET DEFAULT 10;

UPDATE review_rewards
SET reward_amount = 10
WHERE is_claimed = false
  AND reward_amount > 10;
