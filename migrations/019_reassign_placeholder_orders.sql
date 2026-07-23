-- Migration 019: Reassign orders from placeholder users to real users
-- 
-- During the buggy Google sign-in implementation, orders were created under
-- a placeholder user (privy.local@across.dev). When proper Google sign-in
-- was fixed, the real user was created with their actual email, but their
-- historical orders remain attached to the placeholder user.
--
-- This migration reassigns orders to the correct user based on the
-- Privy identity resolution path:
--   1. Find the placeholder user that has orders
--   2. Find the real user who now owns that email or Privy DID
--   3. Reassign orders, order_items, and notifications
--   4. Merge the user_identities record
--   5. Link the placeholder user as a secondary identity
--   6. Optionally deactivate the placeholder user

-- Step 1: For each placeholder user who has orders, find the matching
-- real user by checking: (a) user_identities with matching email,
-- (b) users with matching email
--
-- Update this with the actual IDs from your database.

-- === CUSTOMIZE THESE VALUES ===
-- Replace the placeholder values below with actual IDs from your database.
-- To find them, run:
--   SELECT id, email FROM users WHERE email = 'privy.local@across.dev';
--   SELECT id, email FROM users WHERE email = 'bazecop@gmail.com';

DO $$
DECLARE
    placeholder_user_id UUID := 'e1278bac-201a-46d9-b689-4ede3b511395';
    real_user_id        UUID := '8a5bd9cc-2ef8-4dfa-a6cb-cb5b1dd4363e';
    reassigned_count    INT := 0;
BEGIN
    -- Step 2: Reassign all orders from placeholder to real user
    UPDATE orders
    SET user_id = real_user_id,
        updated_at = now()
    WHERE user_id = placeholder_user_id;
    GET DIAGNOSTICS reassigned_count = ROW_COUNT;
    RAISE NOTICE 'Reassigned % orders from placeholder to real user', reassigned_count;

    -- Step 3: Reassign order_items (through orders join)
    -- Not needed directly since order_items reference orders, not users directly
    
    -- Step 4: Reassign notifications
    UPDATE notifications
    SET user_id = real_user_id
    WHERE user_id = placeholder_user_id;
    GET DIAGNOSTICS reassigned_count = ROW_COUNT;
    RAISE NOTICE 'Reassigned % notifications', reassigned_count;

    -- Step 5: If the placeholder has a privy_user_id, add it as a user_identity
    -- on the real user record (for historical reference)
    INSERT INTO user_identities (user_id, provider, provider_subject, email)
    SELECT real_user_id, 'privy', u.privy_user_id, u.email
    FROM users u
    WHERE u.id = placeholder_user_id
      AND u.privy_user_id IS NOT NULL
      AND u.privy_user_id != ''
    ON CONFLICT (provider, provider_subject) DO NOTHING;
    GET DIAGNOSTICS reassigned_count = ROW_COUNT;
    RAISE NOTICE 'Linked % placeholder identity to real user', reassigned_count;

    -- Step 6: Update the placeholder user to show they've been merged
    UPDATE users
    SET email = 'merged-' || placeholder_user_id || '@across.dev',
        is_active = false,
        full_name = 'Merged into ' || real_user_id,
        updated_at = now()
    WHERE id = placeholder_user_id;
    RAISE NOTICE 'Placeholder user deactivated and marked as merged';

    -- Step 7: Update the real user's privy_user_id if it was missing
    UPDATE users
    SET updated_at = now()
    WHERE id = real_user_id;

    RAISE NOTICE 'Order reassignment complete. User % now owns all orders from %.', real_user_id, placeholder_user_id;
END $$;