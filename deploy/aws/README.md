# AWS pay-as-you-go deployment

Prepared 16 October 2026. **These templates are not deployed.** Creating the
foundation incurs AWS charges even while application tasks remain paused.
No AWS credentials, account ID or chosen region are embedded in the files.

## Included

- `foundation.json`: two-zone VPC, two NAT gateways, private RDS PostgreSQL with
  standby/backups/encryption, replicated encrypted Redis, ECR image registry,
  ECS cluster and optional GitHub OIDC build role. No customer data imported.
- `application.json`: HTTPS application load balancer, regional WAF, private
  Fargate API and worker, one-off migration task, CPU target-tracking scaling,
  CloudWatch error alarm and an unsubscribed SNS alert topic. Defaults **paused**.
- API starts with two 1-vCPU/2-GiB tasks when enabled, capped at ten. Worker is
  separate. CPU scale-out threshold55%, cooldown60s, scale-in300s. These are initial
  tuning settings, not measured traffic capacity. Worker autoscaling, database
  compute autoscaling and CloudFront are **not** provisioned by these templates.
- Each API pool has four connections. With maximum ten tasks, ECS deployment
  surge150% means up to15 API tasks/60 connections plus worker4 and recovery reserve.
  Include migration/restore/operator connections and verify actual PostgreSQL
  max_connections and memory before enabling. Do not increase ceilings blindly.

The earlier cost proposal included CloudFrontBusiness. This initial application
stack instead attaches a regional WAF directly to ALB; its rule/request charges
are separate. Recalculate the final regional quote accordingly. A CDN can be
attached in the following reviewed change; private chat responses remain uncached.
Neither autoscaling nor this WAF setup proves protection against unlimited spikes.

## Local validation (no AWS changes)

```sh
python deploy/aws/generate.py
python deploy/aws/test_safety.py
pip install cfn-lint==1.57.2
cfn-lint deploy/aws/foundation.json deploy/aws/application.json
```

Edit `generate.py`, regenerate JSON and run checks. The generated JSON is directly
uploadable to CloudFormation; the safety test rejects stale generated files.

## Account preparation

Use AWS CloudShell in the existing Atl xpres AWS account. Check identity, region,
Fargate on-demand vCPU quota, available RDS/cache sizes and two availability zones.
Choose the region after latency checks from Nigeria. S3 media remains in its
existing region (`MediaRegion` parameter, currently eu-north-1); do not confuse it
with the ECS deployment region. Request quota increases before promising bursts.

Do not create long-lived AWS keys for this deployment. GitHub image publishing
uses an existing IAM OpenID provider for `token.actions.githubusercontent.com`,
audience `sts.amazonaws.com`; supply its ARN as `GitHubOidcProviderArn` to the
foundation. Its optional build role trusts only this repository's main branch and
can push only its own ECR repository. No infrastructure-deployment IAM permission
is granted to GitHub. CloudShell uses the operator's authenticated AWS session.

## Review before spending

Upload `foundation.json` through CloudShell's file upload menu. After account,
region, class availability and cost-ceiling approval, prepare a CREATE change set:

```sh
aws cloudformation validate-template --template-body file://foundation.json
aws cloudformation create-change-set \
  --stack-name atlxpres-foundation \
  --change-set-name initial-review \
  --change-set-type CREATE \
  --capabilities CAPABILITY_IAM \
  --template-body file://foundation.json
aws cloudformation wait change-set-create-complete \
  --stack-name atlxpres-foundation --change-set-name initial-review
aws cloudformation describe-change-set \
  --stack-name atlxpres-foundation --change-set-name initial-review \
  --query 'Changes[].ResourceChange.{Action:Action,Type:ResourceType,Name:LogicalResourceId}'
```

That change set is review only; **do not execute it until the resources/costs are
approved**. Include optional GitHubOidcProviderArn in change-set parameters if using
image publishing. No template creates a dollar spending cap or budget alert.
Configure AWS Budgets/rapid-spend alerts and approved expansion maxima before use;
an alert is not a hard billing cap. Foundation creation normally takes minutes,
especially the standby database/cache; do not interrupt it because it is slow.

## Prepare the application while it is paused

1. Execute the approved foundation change set. Obtain its non-secret outputs.
2. Run `.github/workflows/aws-image.yml` on a **CI-green main commit** with approved
   region, foundation BuildRoleArn and ImageRepository outputs. It builds Linux
   AMD64 API/worker/migrator into one image and reports its immutable digest. Do
   not deploy `latest` or a mutable tag. The image includes the official RDS CA
   bundle for `sslmode=verify-full` PostgreSQL connections.
3. In the selected region obtain an ACM certificate for a canary API hostname.
   Complete DNS ownership validation. Do not redirect `api.atlxpres.com` yet.
4. Supply a private local JSON config in CloudShell, mode600, with **all** keys in
   `generate.SECRET_KEYS` except the three URL keys. Optional settings can be empty
   strings. Required auth/email/media/payment fields must match the existing
   deployment, including its JWT signing secret. Do not upload that JSON to GitHub,
   paste it in chat or print it. The generated task configuration retains legacy
   static S3 credentials in Secrets Manager until role-based S3 access is separately
   implemented. Keep their existing bucket-limited permissions.
5. Run the runtime preparer from this directory:

   ```sh
   python prepare_runtime.py --region REGION --foundation atlxpres-foundation \
     --config /private/path/runtime.json --name atlxpres/aws-runtime-initial
   ```

   It reads the new foundation credentials through the AWS CLI, creates encrypted
   PostgreSQL/Redis connection URLs in a new Secrets Manager secret and prints only
   its ARN. It never changes existing secrets. Secret contents are not CloudFormation
   parameters or outputs. Use an AWS-managed Secrets Manager encryption key with
   this template; custom KMS keys require a separately scoped decrypt policy.
6. Create an application change set with foundation name, immutable ImageUri,
   RuntimeSecretArn, certificate ARN, existing media bucket/region and
   **EnableService=false**. Review/execute it; tasks stay at zero. The load balancer
   and WAF still incur costs. No domain records change automatically.
7. Before live traffic, create a least-privilege application database login and
   update the runtime secret's `DATABASE_URL` to use it, retaining separate
   administrative `MIGRATION_DATABASE_URL`. The preparer initially uses bootstrap
   credentials for isolated staging/bootstrap, not the final production role.
   Provision grants for tables/sequences/default privileges after migration.
   Password rotation must coordinate DB changes, runtime secret versions and ECS
   task replacement. Automatic rotation is not enabled by these templates.

## Data and traffic cutover — one authoritative database

Run synthetic staging load, autoscaling, instance-failure, multi-zone and restore
tests first. Validate canary TLS, auth, messages/receipts, Town Hall, subscriptions,
media, notifications and WAF behavior. Common-rule BODY size restrictions are
count-only so image attachment requests are not indiscriminately rejected; test
other managed rules against actual client requests. Restrict/monitor canary access.

The new RDS database starts empty. An ECS migration task alone does not copy users,
subscriptions or messages. Import a backup and rehearse migration privately in the
VPC. CloudShell itself does not have direct access to the private database; use a
controlled VPC-connected restore task with scoped S3 backup access. That restore
task/credentials and production cutover are separate reviewed operations, not
implemented by these templates. Do not open PostgreSQL to the internet.

For final cutover: quiesce writes and old workers, take/verify the final backup or
replication catch-up, restore/sync it, run the one-off migration task, verify data,
then enable the AWS services and switch API routing. Verify payment webhooks and
all client writes target the same database. Avoid two workers/old/new APIs writing
to divergent databases. Existing users keep the same public API hostname; no new
APK is needed solely for DNS/backend-host migration.

Keep the old database/storage until acceptance. **After the new RDS accepts live
writes, do not roll back to the stale old database.** Roll back application code
within AWS against the same authoritative RDS, or perform a separately reconciled
data rollback. Before new writes, original routing can still be retained/reinstated.

Subscribe and confirm an authorized operations destination to AlertTopicArn, test
alarm delivery and add latency/unhealthy-target/DB/cache/queue/spend alarms. The
template's single error alarm is an initial signal, not complete monitoring.

## Current release state

No AWS stack is created, no image published, no data moved, no DNS changed and no
capacity claim made until the operator confirms these steps. Initial task maximum
ten is a bounded starting policy. Higher maxima require measured database/request
capacity, service quotas and explicit spend approval.
