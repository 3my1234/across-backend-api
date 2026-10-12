# Off-server backups and a restore drill, 12 October 2026

Current facts: PostgreSQL 16 is on the same 4-vCPU/8-GB Contabo VPS as Coolify,
API/worker and Redis. User confirmed no automated off-server DB backup and no
restore test. No infrastructure split should precede this recovery check.
The agent has not created a bucket, credential, backup schedule or restored DB.

## First action: private backup destination

Create a dedicated AWS S3 bucket, separate from the public/provider image bucket.
Suggested name: `atlxpres-db-backups-<unique-suffix>`; record the actual name.
Use eu-north-1 if continuing with Stockholm, keep Block all public access ON,
ACLs disabled, default encryption SSE-S3, and enable versioning. Keep backup
objects private: they include customer records, messages and financial data.
Do not add public website/CDN rules to this bucket.

Create a dedicated programmatic IAM user such as `atlxpres-backup-user`. Attach
the bucket-scoped example in ../deploy/backup-s3-policy.json after replacing
REPLACE_WITH_BACKUP_BUCKET. These credentials belong in Coolify S3 Storage,
not API/mobile variables, EXPO_PUBLIC_* or Git. Do not replace working image
storage keys. Backup retention needs DeleteObject; versioning preserves earlier
versions/delete markers. Plan version retention deliberately to bound costs.
Versioning alone is not an immutable/account-compromise recovery guarantee.

## Connect Coolify and take the first backup

Add the destination under Coolify S3 Storage and validate it. For AWS Stockholm:

| Field | Value |
|---|---|
| Name | Atlantic Express database backups |
| Bucket | Your actual dedicated backup bucket |
| Region | eu-north-1 |
| Protocol | https |
| Host | s3.eu-north-1.amazonaws.com |
| Port / Path | Empty |
| Access / Secret key | Dedicated backup user's keys |

If your Coolify form has one Endpoint field, use
`https://s3.eu-north-1.amazonaws.com`, without the bucket name.

Open the actual PostgreSQL database resource's Backups page, not the API's
scheduled tasks and not Coolify instance backup. Select across_db specifically.
Enable S3 upload to the validated destination and run Back up now. Check both
the dump result and S3 upload, and confirm a nonempty object in the bucket.
A successful local dump with a failed S3 upload is not an off-server backup.
Record object path, completion time, database name and file size privately.

Recommended launch policy: start with hourly dumps (`17 * * * *`) while the DB
is small, 48 hourly copies locally at most and 30 days in S3; adjust to measured
dump duration, storage usage and budget. Check the timezone displayed in Coolify
before choosing a scheduled time. These are recommendations, not configured
settings. Successful hourly backups can still lose up to roughly an hour of
recent writes. Before meaningful transaction volume, adopt tested WAL/PITR
(managed PostgreSQL or an operated tool such as pgBackRest/WAL-G), rather than
treating scheduled dumps as continuous protection. Alert on failed S3 uploads
and on no successful backup in two hours. Back up Coolify configuration/secrets
separately; a database dump does not contain S3 media or all server configuration.

## Restore test on an isolated target

Create a separate temporary PostgreSQL 16 resource, named `across-restore-test`,
with private networking, a new password and no public DB port/domain. Use a
separate staging server when possible; if on this VPS, monitor memory/disk/CPU
and run off peak. It is a separate instance, not the production DB resource.

On that target only, use Import Backup and choose the confirmed S3 backup.
Review the generated command and target DB before importing. A selected-DB
custom archive should restore into the target's configured empty database.
Follow the import output through successful completion. Leave production
DATABASE_URL unchanged. Do not attach production API/worker to this copy.

Verify the recovery using private psql access:

    SELECT current_database(),version();
    SELECT count(*) FROM users;
    SELECT count(*) FROM provider_organizations;
    SELECT count(*) FROM provider_listings;
    SELECT count(*) FROM products;
    SELECT count(*) FROM orders;
    SELECT count(*) FROM provider_conversations;
    SELECT count(*) FROM provider_conversation_messages;
    SELECT count(*) FROM support_tickets;
    SELECT count(*) FROM support_messages;

Compare with a dated baseline and the expected backup time; live counts may
increase after the dump. Check representative known order totals/statuses,
provider records, message histories, foreign keys and migrations privately.
Record restore duration and the time span of potentially lost writes.
If testing an application against the restored copy, use isolated staging
credentials and disable outbound email/push/payment actions first. A restored
copy includes outbox jobs and must not start sending real customer messages.
Keep the drill database private until the drill is recorded; removal is a
separate explicit operation, never a command targeting the live database.

After recovery succeeds, collect production p95/error rate, CPU/RAM/disk and DB
pool waits; run the staging capacity test. Then decide dedicated DB sizing and
cutover. A second application host comes after the shared DB/Redis/network and
load-balancer plan. See PRODUCTION_SCALING_ROADMAP.md.

Sources:
https://coolify.io/docs/core/s3-storage/aws
https://coolify.io/docs/core/s3-storage/overview
https://coolify.io/docs/databases/backups
https://coolify.io/docs/databases/restore
https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-management.html
