-- Synthetic qualification only. NEVER run against production/restored data.
-- The CI caller supplies only its disposable foundation_recovery_fixture DB.
BEGIN;
DO $$ BEGIN
IF current_database()<>'foundation_recovery_fixture' THEN
 RAISE EXCEPTION 'Synthetic fixture target required';
END IF;
END $$;
INSERT INTO countries_config(country_code,currency_code) VALUES ('NG','NGN') ON CONFLICT(country_code) DO NOTHING;
INSERT INTO users(country_id,email,password_hash,full_name)
SELECT c.id,v.email,'synthetic-not-a-login',v.name FROM countries_config c
CROSS JOIN (VALUES ('owner@example.invalid','Synthetic Owner'),('customer@example.invalid','Synthetic Customer')) v(email,name)
WHERE c.country_code='NG';
INSERT INTO provider_organizations(owner_user_id,business_name,slug,contact_email,contact_phone,verification_status)
SELECT id,'Synthetic Provider','recovery-fixture','owner@example.invalid','synthetic-phone','approved'
FROM users WHERE email='owner@example.invalid';
INSERT INTO provider_members(provider_id,user_id,role)
SELECT id,owner_user_id,'owner' FROM provider_organizations WHERE slug='recovery-fixture';
INSERT INTO provider_listings(provider_id,listing_type,title,slug,description,status)
SELECT id,'car_wash','Synthetic Service','synthetic-service','Synthetic description','approved'
FROM provider_organizations WHERE slug='recovery-fixture';
INSERT INTO provider_subscriptions(provider_id,plan_id,status,tx_ref,customer_email,expected_amount_ngn,starts_at,current_period_end,billing_mode,duration_months)
SELECT p.id,plan.id,'active','synthetic-subscription','owner@example.invalid',plan.amount_ngn,now()-interval '1 day',now()+interval '1 month','one_time',1
FROM provider_organizations p CROSS JOIN LATERAL(SELECT id,amount_ngn FROM provider_subscription_plans ORDER BY amount_ngn,id LIMIT 1) plan
WHERE p.slug='recovery-fixture';
INSERT INTO provider_subscription_payments(subscription_id,flutterwave_transaction_id,tx_ref,amount,currency_code)
SELECT id,'synthetic-transaction','synthetic-subscription',expected_amount_ngn,'NGN' FROM provider_subscriptions WHERE tx_ref='synthetic-subscription';
INSERT INTO payments(provider,purpose,provider_subscription_id,user_id,country_code,amount,currency_code,provider_reference,provider_transaction_id,idempotency_key,payment_status,paid_at)
SELECT 'flutterwave','provider_subscription',s.id,p.owner_user_id,'NG',s.expected_amount_ngn,'NGN',s.tx_ref,'synthetic-transaction','synthetic-idempotency','succeeded',now()
FROM provider_subscriptions s JOIN provider_organizations p ON p.id=s.provider_id WHERE s.tx_ref='synthetic-subscription';
INSERT INTO payment_webhook_events(provider,event_key,payload_sha256,payload,processing_status)
VALUES('flutterwave','synthetic-event',repeat('0',64),'{}','processed');
INSERT INTO provider_conversations(listing_id,provider_id,user_id)
SELECT l.id,l.provider_id,u.id FROM provider_listings l CROSS JOIN users u
WHERE l.slug='synthetic-service' AND u.email='customer@example.invalid';
INSERT INTO provider_conversation_messages(conversation_id,sender_type,sender_user_id,body,client_message_id)
SELECT c.id,'buyer',c.user_id,'Synthetic message',gen_random_uuid() FROM provider_conversations c;
INSERT INTO chat_rooms(kind,owner_user_id,direct_low,direct_high)
SELECT 'direct',a.id,LEAST(a.id,b.id),GREATEST(a.id,b.id) FROM users a CROSS JOIN users b
WHERE a.email='customer@example.invalid' AND b.email='owner@example.invalid';
INSERT INTO chat_participants(room_id,user_id,role)
SELECT r.id,u.id,'member' FROM chat_rooms r CROSS JOIN users u WHERE r.kind='direct';
INSERT INTO chat_messages(room_id,sender_user_id,body,client_message_id)
SELECT id,owner_user_id,'Synthetic shared message',gen_random_uuid() FROM chat_rooms WHERE kind='direct';
INSERT INTO chat_rooms(kind,owner_user_id,title)
SELECT 'town_hall',id,'Synthetic request' FROM users WHERE email='customer@example.invalid';
INSERT INTO town_hall_requests(room_id,description,location,quantity,unit,target_unit_price)
SELECT id,'Synthetic request','Synthetic location',1,'item',500 FROM chat_rooms WHERE kind='town_hall';
INSERT INTO chat_participants(room_id,user_id,role)
SELECT id,owner_user_id,'buyer' FROM chat_rooms WHERE kind='town_hall';
INSERT INTO chat_participants(room_id,user_id,provider_id,role)
SELECT r.id,p.owner_user_id,p.id,'provider' FROM chat_rooms r CROSS JOIN provider_organizations p WHERE r.kind='town_hall';
INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id,state)
SELECT r.id,p.id,p.owner_user_id,'admitted' FROM chat_rooms r CROSS JOIN provider_organizations p WHERE r.kind='town_hall';
INSERT INTO town_hall_offers(room_id,provider_id,sender_user_id,unit_price)
SELECT r.id,p.id,p.owner_user_id,500 FROM chat_rooms r CROSS JOIN provider_organizations p WHERE r.kind='town_hall';
COMMIT;
