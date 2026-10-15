# Related discovery

Product recommendations exclude the current product and require an overlapping
category path. Exact leaf-category matches rank first, followed by other products
in that category family; paid-first discovery remains within each relevance group.
There is no unrelated fallback to fill empty slots. Publication, provider approval,
stock and destination eligibility still apply. Responses are bounded to 20.

Service detail uses the public listings endpoint with category, type, exclude and
limit=8. Category matching trims whitespace and is case-insensitive; descriptions
cannot cause unrelated services to match. Existing visibility and paid/free cursor
rules apply. Invalid exclusion IDs are rejected. Generic artisan listings without
a category show no recommendations rather than unrelated professions.

Deploy API before the mobile build using these filters. No new migration,
environment variable, payment policy or worker change is required. Existing
pending migrations from earlier releases should still be applied normally.

Tests exercise the actual PostgreSQL endpoints for exact/family clothing matches,
unrelated/uncategorized/hidden products, revoked providers, category case handling,
self-exclusion, malformed filters and paid/free pagination.
