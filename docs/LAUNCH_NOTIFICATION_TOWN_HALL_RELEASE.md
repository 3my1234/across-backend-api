# Notification, workspace and Town Hall release

Town Hall has a hard 24-hour deadline from creation, even after provider
engagement. API reads and writes enforce it before the cleanup worker runs.
Ended/expired requests disappear from both public and owner feeds and inboxes.
Every 30 seconds the worker removes up to 250 closed requests with cascading
messages, applications and offers. Reported rooms stay hidden and read-only for
admin evidence review; private direct chats and their selected-offer snapshots
are independent and survive cleanup. This migration adjusts existing request
deadlines to creation plus 24 hours. Back up before migration: deletion is final.
Uploaded objects in S3 are not deleted by this database cleanup.

Deploy matching API and worker source, run `/app/across-migrate` before enabling
the new worker, then redeploy the provider PWA. Readiness checks migration 101.
Verify both API replicas and worker use the same commit after reconciliation.
The mobile safe-area, notification-overlay and expiry-copy fixes require a new
app build; changing the backend does not update an already installed APK.

Provider verification documents and business profile have dedicated navigation
entries and Overview shortcuts, also available in More. Settings retains device,
appearance, message privacy and sign-out controls.

## Browser push acceptance

Settings > Notifications and app > Send a test to this phone queues an encrypted
push for the authenticated browser endpoint only. Repeated taps within 30 seconds
reuse the test. Delivery status is scoped to that user. A `sent` result means the
push service accepted it; the user must confirm an Android notification appeared.
Test on the affected installed Samsung PWA both foreground and background, then
send a real product message and tap the notification to open its conversation.
No production message or test notification was sent by the local test suite.

If a test remains pending, inspect worker heartbeat and browser queue health. If
it fails, reconnect the subscription and check VAPID consistency between API and
worker. If accepted without display, inspect browser-specific Android categories,
battery restrictions and Do Not Disturb on that actual device. Do not describe
permission granted or a registered subscription as proven phone delivery.

## Email diagnosis still requires evidence

The original-email attachment directory was empty in this session. No SPF/DKIM/
DMARC failure or Amazon delay cause can be established without headers/events.
Do not change DNS blindly. Obtain Authentication-Results, From, Return-Path, Date
and Received headers without including verification links or tokens.

Compare application queue delay with the Received chain. These aggregates contain
no recipient addresses, payloads or verification tokens:

```sql
BEGIN READ ONLY;
SET LOCAL statement_timeout='10s';
SELECT template_type,status,count(*) AS emails,
       min(created_at) AS oldest_created,
       max(updated_at) AS latest_update,
       max(attempts) AS highest_attempts
FROM email_outbox WHERE created_at>now()-interval '48 hours'
GROUP BY template_type,status;
COMMIT;
```

SMTP acceptance is not inbox placement. SES Delivery events expose
`processingTimeMillis`, and DeliveryDelay events identify the stage/reason after
SES acceptance. Inspect these alongside queue age before blaming Amazon or
increasing server capacity. Reference:
https://docs.aws.amazon.com/ses/latest/dg/event-publishing-retrieving-sns-contents.html

## Acceptance before release

- On Android, open product details, message the seller, focus/close the keyboard;
  composer stays above native navigation and keyboard without duplicate padding.
- Tap a customer message alert while product details are open, from another tab,
  and after cold start; the exact conversation opens above any old overlay.
- Send text, photo and location both ways between APK and installed provider PWA.
- Verify documents are reachable from Overview, desktop navigation and More.
- Verify expired/ended requests disappear and cannot receive messages; verify an
  accepted-offer private chat remains available.

These are correctness checks, not a claim of measured high-user capacity.
