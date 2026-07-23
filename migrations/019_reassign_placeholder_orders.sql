-- Migration 019: Reassign orders from placeholder users to real users
--
-- During the buggy Google sign-in implementation, orders were created under
-- a placeholder user (privy.local@across.dev). When proper Google sign-in
-- was fixed, the real user was created with their actual email (e.g.
-- bazecop@gmail.com), but their historical orders remain attached to the
-- placeholder user.
--
-- This migration reassigns all orders, notifications, and identities
-- from the placeholder user to the correct real user.
--
-- IMPORTANT: Update the UUID values below with actual IDs from your database.
-- To find them:
--   SELECT id, email FROM users WHERE email = 'privy.local@across.dev';
--   SELECT id, email FROM users WHERE email = 'bazecop@gmail.com';

-- Reassign all orders from placeholder user to real user
UPDATE orders
SET user_id = '8a5bd9cc-2ef8-4dfa-a6cb-cb5b1dd4363e', updated_at = now()
WHERE user_id = 'e1278bac-201a-46d9-b689-4ede3b511395';

-- Reassign all notifications
UPDATE notifications
SET user_id = '8a5bd9cc-2ef8-4dfa-a6cb-cb5b1dd4363e'
WHERE user_id = 'e1278bac-201a-46d9-b689-4ede3b511395';

-- Link the placeholder identity as a legacy record on the real user
INSERT INTO user_identities (user_id, provider, provider_subject, email)
SELECT '8a5bd9cc-2ef8-4dfa-a6cb-cb5b1dd4363e', 'legacy', u.email, u.email
FROM users u
WHERE u.id = 'e1278bac-201a-46d9-b689-4ede3b511395'
ON CONFLICT (provider, provider_subject) DO NOTHING;

-- Deactivate and anonymize the placeholder user
UPDATE users
SET email = 'merged-e1278bac-201a-46d9-b689-4ede3b511395@across.dev',
    is_active = false,
    updated_at = now()
WHERE id = 'e1278bac-201a-46d9-b689-4ede3b511395';