-- The API and onboarding support artisan services; keep the database check in
-- sync so valid mechanic/plumber/carpenter listings are not rejected as 500s.
ALTER TABLE provider_listings
  DROP CONSTRAINT IF EXISTS provider_listings_listing_type_check;

ALTER TABLE provider_listings
  ADD CONSTRAINT provider_listings_listing_type_check
  CHECK (listing_type IN (
    'hotel','short_let','car_rental','car_wash','mechanic','plumber',
    'carpenter','fuel_station','food_vendor','artisan','shop_rental',
    'property','land'
  ));
