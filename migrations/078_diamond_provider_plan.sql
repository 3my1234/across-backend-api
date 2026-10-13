-- Same monthly billing and existing capabilities; listing capacity agreed for Diamond.
INSERT INTO provider_subscription_plans(code,name,description,amount_ngn,listing_limit,features,badge_tier)
VALUES ('provider-diamond','Diamond','80 product listings and 80 service/property listings. Buyer chat, bookings, orders and tracking included.',5000,80,'{"public_contact":true,"verified_badge":true}','diamond')
ON CONFLICT(code) DO NOTHING;
