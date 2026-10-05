-- Matching-currency delivery offers can follow the primary item price, while
-- explicit destination prices remain independent. Do not guess about existing
-- mismatches: only link offers whose item price already equals the primary.
ALTER TABLE product_delivery_areas
  ADD COLUMN IF NOT EXISTS uses_primary_price BOOLEAN NOT NULL DEFAULT false;

UPDATE product_delivery_areas a
SET uses_primary_price=true
FROM products p
WHERE p.id=a.product_id AND a.currency_code=p.local_currency_code
  AND a.delivered_price-a.delivery_fee=p.local_selling_price;

CREATE OR REPLACE FUNCTION apply_product_primary_price() RETURNS trigger AS $$
DECLARE primary_price NUMERIC; primary_currency TEXT;
BEGIN
  IF NEW.uses_primary_price THEN
    SELECT local_selling_price,local_currency_code INTO primary_price,primary_currency
    FROM products WHERE id=NEW.product_id;
    IF NEW.currency_code<>primary_currency THEN
      RAISE EXCEPTION 'A primary-price delivery offer must use the primary currency';
    END IF;
    NEW.delivered_price:=primary_price+NEW.delivery_fee;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER product_delivery_primary_price
BEFORE INSERT OR UPDATE ON product_delivery_areas
FOR EACH ROW EXECUTE FUNCTION apply_product_primary_price();

CREATE OR REPLACE FUNCTION sync_product_primary_price() RETURNS trigger AS $$
BEGIN
  UPDATE product_delivery_areas
  SET uses_primary_price=(currency_code=NEW.local_currency_code),
      delivered_price=CASE WHEN currency_code=NEW.local_currency_code
        THEN NEW.local_selling_price+delivery_fee ELSE delivered_price END
  WHERE product_id=NEW.id AND uses_primary_price;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER product_primary_price_changed
AFTER UPDATE OF local_selling_price,local_currency_code ON products
FOR EACH ROW WHEN (OLD.local_selling_price IS DISTINCT FROM NEW.local_selling_price
  OR OLD.local_currency_code IS DISTINCT FROM NEW.local_currency_code)
EXECUTE FUNCTION sync_product_primary_price();
