-- A committed change invalidates all public catalogue cache variants, across
-- API replicas. Buyers poll this one indexed row rather than whole catalogues.
CREATE TABLE catalog_revision (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  revision BIGINT NOT NULL DEFAULT 1
);
INSERT INTO catalog_revision(singleton) VALUES(true);

CREATE OR REPLACE FUNCTION bump_catalog_revision() RETURNS trigger AS $$
BEGIN
  UPDATE catalog_revision SET revision=revision+1 WHERE singleton=true;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE table_name TEXT;
BEGIN
  FOREACH table_name IN ARRAY ARRAY['products','product_delivery_areas',
    'provider_listings','provider_availability_slots','provider_organizations','provider_payout_accounts',
    'provider_subscriptions','provider_subscription_plans','provider_subscription_access','countries_config']
  LOOP
    IF to_regclass(table_name) IS NOT NULL THEN
      EXECUTE format('CREATE TRIGGER catalog_changed AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION bump_catalog_revision()',table_name);
    END IF;
  END LOOP;
END $$;
