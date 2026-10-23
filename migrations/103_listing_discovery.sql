-- Additive discovery controls and aggregate metrics; existing listing and
-- subscription rules remain authoritative. No behavioural/message history.
CREATE TABLE discovery_provider_settings (
 provider_id uuid PRIMARY KEY REFERENCES provider_organizations(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT true,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE discovery_listing_settings (
 provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('product','service')),
 listing_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 priority integer NOT NULL DEFAULT 1 CHECK(priority BETWEEN 1 AND 3),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(kind,listing_id)
);
CREATE INDEX discovery_listing_owner ON discovery_listing_settings(provider_id,kind,listing_id);
CREATE TABLE discovery_daily_metrics (
 provider_id uuid NOT NULL REFERENCES provider_organizations(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN ('product','service')),
 listing_id uuid NOT NULL,
 placement text NOT NULL CHECK(placement IN ('similar','recommended','promoted')),
 day date NOT NULL,
 impressions bigint NOT NULL DEFAULT 0,
 clicks bigint NOT NULL DEFAULT 0,
 detail_opens bigint NOT NULL DEFAULT 0,
 contact_taps bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(provider_id,kind,listing_id,placement,day)
);
CREATE INDEX discovery_metrics_day ON discovery_daily_metrics(day);
CREATE TABLE discovery_event_dedup (
 event_key text PRIMARY KEY,
 expires_at timestamptz NOT NULL
);
CREATE INDEX discovery_event_expiry ON discovery_event_dedup(expires_at);
CREATE INDEX discovery_service_country_id ON provider_listings(country_code,id) WHERE status='approved' AND is_available_now;
CREATE INDEX discovery_product_category ON products USING gin(category_path) WHERE is_active AND moderation_status='approved' AND inventory_count>0;
CREATE INDEX discovery_product_text ON products USING gin(to_tsvector('simple',title||' '||description)) WHERE is_active AND moderation_status='approved' AND inventory_count>0;
CREATE INDEX discovery_service_text ON provider_listings USING gin(to_tsvector('simple',title||' '||description||' '||category)) WHERE status='approved';
