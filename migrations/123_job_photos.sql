-- Optional public workplace photos; existing jobs and allowance rules are preserved.
ALTER TABLE provider_jobs ADD COLUMN image_urls TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE provider_jobs ADD CONSTRAINT provider_jobs_photo_limit CHECK(cardinality(image_urls)<=4 AND array_position(image_urls,NULL) IS NULL);
