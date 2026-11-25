-- Flight prices a provider keeps beside an approved flight listing. They are separate
-- from the listing so that a price change does not send the listing back for review.
CREATE TABLE provider_flight_routes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 listing_id uuid NOT NULL REFERENCES provider_listings(id) ON DELETE CASCADE,
 flight_type text NOT NULL CHECK(flight_type IN ('local','international')),
 from_city text NOT NULL CHECK(char_length(btrim(from_city)) BETWEEN 2 AND 60),
 to_city text NOT NULL CHECK(char_length(btrim(to_city)) BETWEEN 2 AND 60),
 price numeric(14,2) NOT NULL CHECK(price BETWEEN 1 AND 100000000),
 discount_price numeric(14,2) CHECK(discount_price IS NULL OR (discount_price>=1 AND discount_price<price)),
 is_active boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(lower(btrim(from_city))<>lower(btrim(to_city)))
);
CREATE UNIQUE INDEX provider_flight_route_once ON provider_flight_routes(listing_id,flight_type,lower(btrim(from_city)),lower(btrim(to_city)));

-- What the customer asked for, exactly as it was shown to them.
ALTER TABLE provider_requests ADD COLUMN flight jsonb;
