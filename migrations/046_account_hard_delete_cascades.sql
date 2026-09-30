-- Hard deletion is reserved for test/pre-launch accounts and explicit
-- super-admin removal. Account-owned data is removed atomically. Orders placed
-- with a deleted seller are retained for the buyer, but detached from the
-- seller organization.

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_user_id_fkey;
ALTER TABLE orders
  ADD CONSTRAINT orders_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE provider_organizations
  DROP CONSTRAINT IF EXISTS provider_organizations_owner_user_id_fkey;
ALTER TABLE provider_organizations
  ADD CONSTRAINT provider_organizations_owner_user_id_fkey
  FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_order_id_fkey;
ALTER TABLE payments
  ADD CONSTRAINT payments_order_id_fkey
  FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE;
ALTER TABLE payments
  DROP CONSTRAINT IF EXISTS payments_provider_subscription_id_fkey;
ALTER TABLE payments
  ADD CONSTRAINT payments_provider_subscription_id_fkey
  FOREIGN KEY (provider_subscription_id)
  REFERENCES provider_subscriptions(id) ON DELETE CASCADE;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_user_id_fkey;
ALTER TABLE payments
  ADD CONSTRAINT payments_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE provider_requests
  DROP CONSTRAINT IF EXISTS provider_requests_listing_id_fkey;
ALTER TABLE provider_requests
  ADD CONSTRAINT provider_requests_listing_id_fkey
  FOREIGN KEY (listing_id) REFERENCES provider_listings(id) ON DELETE CASCADE;
ALTER TABLE provider_requests
  DROP CONSTRAINT IF EXISTS provider_requests_provider_id_fkey;
ALTER TABLE provider_requests
  ADD CONSTRAINT provider_requests_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE CASCADE;
ALTER TABLE provider_requests
  DROP CONSTRAINT IF EXISTS provider_requests_user_id_fkey;
ALTER TABLE provider_requests
  ADD CONSTRAINT provider_requests_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE provider_listing_reports
  DROP CONSTRAINT IF EXISTS provider_listing_reports_user_id_fkey;
ALTER TABLE provider_listing_reports
  ADD CONSTRAINT provider_listing_reports_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE products DROP CONSTRAINT IF EXISTS products_provider_id_fkey;
ALTER TABLE products
  ADD CONSTRAINT products_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE CASCADE;

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_provider_id_fkey;
ALTER TABLE orders
  ADD CONSTRAINT orders_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE SET NULL;

ALTER TABLE order_items DROP CONSTRAINT IF EXISTS order_items_provider_id_fkey;
ALTER TABLE order_items
  ADD CONSTRAINT order_items_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE SET NULL;

ALTER TABLE merchant_ledger
  DROP CONSTRAINT IF EXISTS merchant_ledger_provider_id_fkey;
ALTER TABLE merchant_ledger
  ADD CONSTRAINT merchant_ledger_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE CASCADE;
ALTER TABLE merchant_ledger DROP CONSTRAINT IF EXISTS merchant_ledger_order_id_fkey;
ALTER TABLE merchant_ledger
  ADD CONSTRAINT merchant_ledger_order_id_fkey
  FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE;

ALTER TABLE order_fulfillments
  DROP CONSTRAINT IF EXISTS order_fulfillments_provider_id_fkey;
ALTER TABLE order_fulfillments
  ADD CONSTRAINT order_fulfillments_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE SET NULL;

ALTER TABLE merchant_manifests
  DROP CONSTRAINT IF EXISTS merchant_manifests_provider_id_fkey;
ALTER TABLE merchant_manifests
  ADD CONSTRAINT merchant_manifests_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE CASCADE;
ALTER TABLE merchant_manifest_orders
  DROP CONSTRAINT IF EXISTS merchant_manifest_orders_order_id_fkey;
ALTER TABLE merchant_manifest_orders
  ADD CONSTRAINT merchant_manifest_orders_order_id_fkey
  FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE;

ALTER TABLE provider_conversations
  DROP CONSTRAINT IF EXISTS provider_conversations_listing_id_fkey;
ALTER TABLE provider_conversations
  ADD CONSTRAINT provider_conversations_listing_id_fkey
  FOREIGN KEY (listing_id) REFERENCES provider_listings(id) ON DELETE CASCADE;
ALTER TABLE provider_conversations
  DROP CONSTRAINT IF EXISTS provider_conversations_provider_id_fkey;
ALTER TABLE provider_conversations
  ADD CONSTRAINT provider_conversations_provider_id_fkey
  FOREIGN KEY (provider_id) REFERENCES provider_organizations(id) ON DELETE CASCADE;
ALTER TABLE provider_conversations
  DROP CONSTRAINT IF EXISTS provider_conversations_user_id_fkey;
ALTER TABLE provider_conversations
  ADD CONSTRAINT provider_conversations_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE provider_conversation_messages
  DROP CONSTRAINT IF EXISTS provider_conversation_messages_sender_user_id_fkey;
ALTER TABLE provider_conversation_messages
  ADD CONSTRAINT provider_conversation_messages_sender_user_id_fkey
  FOREIGN KEY (sender_user_id) REFERENCES users(id) ON DELETE CASCADE;
