# Customer service reviews

Customers review a completed service request from **Services > My requests**.
They select one to five stars, optionally write up to 1,000 characters, then
submit. The same customer can edit that review. Merely viewing a listing,
revealing contact details or opening a conversation does not qualify for a
review. The provider accepts the customer's request and marks it completed once
the work is finished. Active provider members cannot review their own business.

Reviews belong to the service listing and show the customer name, rating, text
and date, without exposing account IDs, email addresses or phone numbers. The
service card and detail show its average and review count. Unreviewed services
are labelled as new, rather than displaying an invented score. Public reviews
paginate in batches of ten in the app. Editing a review updates its totals and
does not create an additional review or XP award.

The **4★ & up** toggle combines with service category, search and location.
Both browse endpoints apply `min_rating=4` to the unrounded average before
limiting results. Nearby results retain distance ordering within the selected
radius and provider coverage. Clearing the rating toggle restores all ratings.
Offline results are explicitly marked; filtered results do not overwrite the
unfiltered offline nearby snapshot.

Deploy backend main and run:

```sh
cd /app && ./across-migrate
```

Migration **058_service_review_totals.sql** replaces the old full-recount trigger
with atomic deltas and repairs existing totals. The trigger's listing update
also advances the catalogue revision introduced in migration 057, allowing
foreground mobile screens to refresh their scores. Apply all pending migrations
before acceptance testing. A new mobile build is required.

Acceptance with separate artisan and buyer accounts:

1. Find the artisan in Services and send a booking/enquiry from the service page.
2. Artisan accepts the request in the provider portal, performs the service, then
   marks it completed. Check that pending/rejected requests offer no review form.
3. Buyer opens My requests, selects stars, writes a comment and submits. Check
   the comment and score in the public service detail and score on its card.
4. Edit the same review. The count remains unchanged; the average changes. XP is
   awarded only once for the request.
5. Enable 4★ & up and search the same category nearby. Scores below four and
   listings without reviews must be excluded. Turn it off to restore them.
6. A different customer cannot review someone else's completed request, and a
   provider member cannot rate their own listing.

Verification: the isolated PostgreSQL test exercises real HTTP handlers for
ownership, completed status, self-review, input limits, public score fields,
both discovery filters, review edits/XP and pagination. It deliberately overlaps
two review transactions while one holds the listing lock, verifies exact totals,
then verifies deletion adjusts totals. Run `SETTLEMENT_TEST_LOCAL=true go test
./...` against the configured loopback database only.
