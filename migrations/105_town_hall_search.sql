-- Existing request text and manual areas only. No GPS/trip history or new service.
CREATE INDEX town_hall_request_search ON town_hall_requests USING gin
 (to_tsvector('simple'::regconfig,coalesce(description,'')||' '||coalesce(category,'')||' '||coalesce(location,'')));
CREATE INDEX town_hall_pickup_search ON town_hall_requests USING gin
 (to_tsvector('simple'::regconfig,coalesce(location,'')));
CREATE INDEX town_hall_category_search ON town_hall_requests USING gin(to_tsvector('simple'::regconfig,coalesce(category,''))); 
CREATE INDEX town_hall_request_kind_feed ON chat_rooms((context->>'request_kind'),created_at DESC,id DESC)
 WHERE kind='town_hall' AND state='open';
