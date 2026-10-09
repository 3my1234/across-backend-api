-- Preserve Basic's ID and recurring gateway agreement for existing subscribers.
UPDATE provider_subscription_plans SET name='Basic',
  description='5 product listings and 5 service/property listings. Buyer chat, bookings, orders and tracking included.',
  listing_limit=5, updated_at=now()
WHERE code='provider-monthly' AND amount_ngn=500;

INSERT INTO provider_subscription_plans(code,name,description,amount_ngn,listing_limit,features)
VALUES
 ('provider-growth','Growth','15 product listings and 15 service/property listings. Buyer chat, bookings, orders and tracking included.',1000,15,'{"public_contact":true,"verified_badge":true}'),
 ('provider-business','Business','40 product listings and 40 service/property listings. Buyer chat, bookings, orders and tracking included.',2000,40,'{"public_contact":true,"verified_badge":true}')
ON CONFLICT(code) DO NOTHING;
