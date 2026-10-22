-- Source-based aggregate validation; requires synthetic CI qualification.
-- Only for the isolated restored database, never production.
-- Run with psql -X -v ON_ERROR_STOP=1 using a SELECT-only drill role.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '15s';
SET LOCAL lock_timeout = '2s';
DO $$ BEGIN
  IF current_database() <> 'recovery_drill' THEN
    RAISE EXCEPTION 'STOPPED: isolated recovery_drill target required';
  END IF;
END $$;

-- Aggregate inventory: absence is distinct from a passing functional example.
SELECT 'providers' AS category, count(*) AS records FROM provider_organizations
UNION ALL SELECT 'members',count(*) FROM provider_members
UNION ALL SELECT 'documents',count(*) FROM provider_verification_documents
UNION ALL SELECT 'listings',count(*) FROM provider_listings
UNION ALL SELECT 'products',count(*) FROM products
UNION ALL SELECT 'subscriptions',count(*) FROM provider_subscriptions
UNION ALL SELECT 'subscription_payments',count(*) FROM provider_subscription_payments
UNION ALL SELECT 'payments',count(*) FROM payments
UNION ALL SELECT 'payment_webhooks',count(*) FROM payment_webhook_events
UNION ALL SELECT 'conversations',count(*) FROM provider_conversations
UNION ALL SELECT 'ordinary_messages',count(*) FROM provider_conversation_messages
UNION ALL SELECT 'rooms',count(*) FROM chat_rooms
UNION ALL SELECT 'participants',count(*) FROM chat_participants
UNION ALL SELECT 'shared_messages',count(*) FROM chat_messages
UNION ALL SELECT 'town_hall_requests',count(*) FROM town_hall_requests
UNION ALL SELECT 'town_hall_offers',count(*) FROM town_hall_offers
UNION ALL SELECT 'town_hall_applications',count(*) FROM town_hall_applications
UNION ALL SELECT 'renewal_preferences',count(*) FROM provider_renewal_preferences
UNION ALL SELECT 'renewal_cancellations',count(*) FROM provider_renewal_cancellations;

-- Findings are counts only. Any nonzero finding requires review, not repair.
SELECT 'provider_missing_owner' AS finding,count(*) AS records
FROM provider_organizations p LEFT JOIN users u ON u.id=p.owner_user_id WHERE u.id IS NULL
UNION ALL SELECT 'provider_missing_owner_membership',count(*) FROM provider_organizations p
WHERE NOT EXISTS(SELECT 1 FROM provider_members m WHERE m.provider_id=p.id AND m.user_id=p.owner_user_id AND m.role='owner')
UNION ALL SELECT 'listing_missing_provider',count(*) FROM provider_listings l
LEFT JOIN provider_organizations p ON p.id=l.provider_id WHERE p.id IS NULL
UNION ALL SELECT 'subscription_missing_provider_or_plan',count(*) FROM provider_subscriptions s
LEFT JOIN provider_organizations p ON p.id=s.provider_id
LEFT JOIN provider_subscription_plans plan ON plan.id=s.plan_id WHERE p.id IS NULL OR plan.id IS NULL
UNION ALL SELECT 'subscription_period_invalid',count(*) FROM provider_subscriptions s
WHERE (s.starts_at IS NOT NULL AND s.current_period_end IS NOT NULL AND s.current_period_end<=s.starts_at)
 OR s.expected_amount_ngn<=0 OR s.duration_months NOT IN(1,3,6,12)
 OR (s.billing_mode='recurring' AND s.duration_months<>1)
UNION ALL SELECT 'subscription_payment_invalid_reference_or_amount',count(*) FROM provider_subscription_payments sp
LEFT JOIN provider_subscriptions s ON s.id=sp.subscription_id
WHERE s.id IS NULL OR sp.amount<=0 OR btrim(sp.currency_code)=''
UNION ALL SELECT 'payment_purpose_link_invalid',count(*) FROM payments p
WHERE NOT ((p.purpose='order' AND p.order_id IS NOT NULL AND p.provider_subscription_id IS NULL)
 OR (p.purpose='provider_subscription' AND p.order_id IS NULL AND p.provider_subscription_id IS NOT NULL))
UNION ALL SELECT 'payment_idempotency_duplicate_groups',count(*) FROM
 (SELECT provider,idempotency_key FROM payments GROUP BY provider,idempotency_key HAVING count(*)>1) d
UNION ALL SELECT 'payment_transaction_duplicate_groups',count(*) FROM
 (SELECT provider,provider_transaction_id FROM payments WHERE provider_transaction_id IS NOT NULL AND provider_transaction_id<>''
 GROUP BY provider,provider_transaction_id HAVING count(*)>1) d
UNION ALL SELECT 'webhook_duplicate_groups',count(*) FROM
 (SELECT provider,event_key FROM payment_webhook_events GROUP BY provider,event_key HAVING count(*)>1) d
UNION ALL SELECT 'conversation_thread_pair_mismatch',count(*) FROM provider_conversations c
LEFT JOIN provider_conversations root ON root.id=c.thread_id
WHERE root.id IS NULL OR root.provider_id<>c.provider_id OR root.user_id<>c.user_id OR root.thread_id<>root.id
UNION ALL SELECT 'ordinary_message_missing_conversation_or_sender',count(*) FROM provider_conversation_messages m
LEFT JOIN provider_conversations c ON c.id=m.conversation_id LEFT JOIN users u ON u.id=m.sender_user_id
WHERE c.id IS NULL OR u.id IS NULL
UNION ALL SELECT 'ordinary_message_retry_duplicate_groups',count(*) FROM
 (SELECT sender_user_id,client_message_id FROM provider_conversation_messages WHERE client_message_id IS NOT NULL
 GROUP BY sender_user_id,client_message_id HAVING count(*)>1) d
UNION ALL SELECT 'shared_message_missing_room_or_sender',count(*) FROM chat_messages m
LEFT JOIN chat_rooms r ON r.id=m.room_id LEFT JOIN users u ON u.id=m.sender_user_id WHERE r.id IS NULL OR u.id IS NULL
UNION ALL SELECT 'shared_message_retry_duplicate_groups',count(*) FROM
 (SELECT sender_user_id,client_message_id FROM chat_messages GROUP BY sender_user_id,client_message_id HAVING count(*)>1) d
UNION ALL SELECT 'town_hall_wrong_room_kind',count(*) FROM town_hall_requests h
LEFT JOIN chat_rooms r ON r.id=h.room_id WHERE r.id IS NULL OR r.kind<>'town_hall'
UNION ALL SELECT 'town_hall_over_ten_active_provider_seats',count(*) FROM
 (SELECT h.room_id FROM town_hall_requests h JOIN chat_participants p ON p.room_id=h.room_id
 WHERE p.role='provider' AND p.state='active' GROUP BY h.room_id HAVING count(*)>10) d
UNION ALL SELECT 'unvalidated_public_constraints',count(*) FROM pg_constraint
WHERE connamespace='public'::regnamespace AND NOT convalidated
UNION ALL SELECT 'invalid_public_indexes',count(*) FROM pg_index i
JOIN pg_class t ON t.oid=i.indrelid WHERE t.relnamespace='public'::regnamespace AND NOT i.indisvalid;

-- No provider references, message contents or webhook payloads are emitted.
SELECT 'subscription_ledger_amount_mismatch' AS finding,count(*) AS records
FROM provider_subscription_payments sp JOIN provider_subscriptions s ON s.id=sp.subscription_id
WHERE sp.amount<>s.expected_amount_ngn OR sp.currency_code<>'NGN'
UNION ALL SELECT 'subscription_payment_identity_mismatch',count(*) FROM payments p
JOIN provider_subscription_payments sp ON sp.flutterwave_transaction_id=p.provider_transaction_id
WHERE p.provider='flutterwave' AND p.purpose='provider_subscription'
 AND (p.provider_subscription_id<>sp.subscription_id OR p.amount<>sp.amount OR p.currency_code<>sp.currency_code)
UNION ALL SELECT 'town_hall_offer_missing_request_or_provider',count(*) FROM town_hall_offers o
LEFT JOIN town_hall_requests h ON h.room_id=o.room_id LEFT JOIN provider_organizations p ON p.id=o.provider_id
WHERE h.room_id IS NULL OR p.id IS NULL
UNION ALL SELECT 'chat_reply_crosses_room',count(*) FROM chat_messages m
JOIN chat_messages parent ON parent.id=m.reply_to_id WHERE parent.room_id<>m.room_id;

SELECT payment_status,currency_code,count(*) AS records,sum(amount) AS amount_total
FROM payments GROUP BY payment_status,currency_code ORDER BY payment_status,currency_code;
SELECT status,billing_mode,duration_months,count(*) AS records
FROM provider_subscriptions GROUP BY status,billing_mode,duration_months;
SELECT processing_status,count(*) AS records FROM payment_webhook_events GROUP BY processing_status;
COMMIT;
