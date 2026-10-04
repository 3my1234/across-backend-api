-- A product's stock location is not its delivery promise. Sellers declare
-- destinations separately; existing local stock is limited to its stock country.
INSERT INTO countries_config(country_code,currency_code,base_escrow_days,active_payment_gateways)
VALUES ('NG','NGN',14,ARRAY['flutterwave']) ON CONFLICT (country_code) DO NOTHING;

CREATE TABLE IF NOT EXISTS product_delivery_areas (
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  country_code TEXT NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
  state_key TEXT NOT NULL DEFAULT '',
  city_key TEXT NOT NULL DEFAULT '',
  delivered_price NUMERIC(14,2) NOT NULL CHECK (delivered_price > 0),
  currency_code TEXT NOT NULL CHECK (currency_code ~ '^[A-Z]{3}$'),
  PRIMARY KEY (product_id,country_code,state_key,city_key),
  CHECK (city_key='' OR state_key<>'')
);
CREATE INDEX IF NOT EXISTS idx_product_delivery_areas_destination
  ON product_delivery_areas(country_code,state_key,city_key,product_id);

INSERT INTO product_delivery_areas(product_id,country_code,delivered_price,currency_code)
SELECT id,'NG',local_selling_price,local_currency_code
FROM products
WHERE provider_id IS NOT NULL
  AND fulfillment_mode IN ('merchant_local','merchant_cross_border')
  AND local_currency_code='NGN'
  AND local_selling_price > 0
  AND (fulfillment_mode='merchant_cross_border' OR UPPER(inventory_country_code)='NG')
ON CONFLICT DO NOTHING;
