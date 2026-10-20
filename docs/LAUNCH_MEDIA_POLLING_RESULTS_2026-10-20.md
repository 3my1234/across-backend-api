# Follow-up: independent polling and real photo bytes

Source `c96064f` in PR61 extends the initial smoke evidence without changing
production application code or storage endpoints. Only disposable CI was used.

## Photo correctness

[Passing photo run 39506485883](https://github.com/3my1234/across-backend-api/actions/runs/39506485883)
built upstream MinIO RELEASE.2025-10-15T17-29-55Z from source, using internal
virtual-host DNS, disposable TLS and synthetic signing credentials. This is an
S3-compatible service, not AWS S3. Normal certificate verification remained on.
No cloud bucket, real user photograph or production storage credential was used.

| JPEG dimensions | Actual bytes | Directions |
|---|---:|---|
| 256 × 256 | 59,332 | Customer → provider and provider → customer |
| 1,024 × 1,024 | 940,448 | Both |
| 2,048 × 2,048 | 3,760,136 | Both |

All six actual uploads, attachments and recipient downloads passed. Total bytes
uploaded: 9,519,832; six distinct messages persisted with no replay duplicates.
Every recipient SHA-256 matched the uploaded file. Each case also rejected an
attachment before upload, a wrong-owner attachment and an unsigned private GET.

Per-stage observed ranges across these six sequential cases:

- API presign: 0.747–6.276 ms.
- Object PUT: 2.546–16.523 ms.
- Message attachment including object HEAD: 5.347–47.845 ms.
- Recipient object GET: 1.175–13.289 ms.

These are local-network functional timings, not statistically meaningful p95s,
internet/device timings, AWS IAM/CORS qualification or media throughput. No image
rendering, cellular throttling, parallel upload or production failure reproduction
was performed. The screenshot's incident root cause remains unknown.

## Independent chat polling

[Passing session run 39506485825](https://github.com/3my1234/across-backend-api/actions/runs/39506485825)
used four distinct VUs for 45 seconds with the existing small lab. Conversation
polling now runs every 15 seconds independently of random browsing/other activity,
including during thinking. No missed-interval catch-up storm is generated.

Predeclared polling latency gates were p95 <17 s and p99 <20 s, reflecting a
15-second observation timer plus scheduling/server budget. These are not an
instant-delivery promise. Existing API p95 <500 ms/p99 <1,500 ms gates were retained.

| Measurement | Observed |
|---|---:|
| Successful API p95 / p99 | 26.71 / 182.29 ms |
| Incoming polling observation p95 / p99 | 14.28 / 14.54 s |
| Maximum incoming polling observation | 14.61 s |
| Marketplace messages sent / observed during workload | 10 / 10 |
| Intended-peer visibility after workload | 10 / 10 |
| Total writes including Town Hall / stored | 11 / 11 |
| Unexpected request failures / duplicated marketplace writes | 0 / 0 |
| Native-push jobs recovered through mock | 11 / 11 |

Signup/onboarding, write reconciliation and the existing small replica stop/restart
checks also passed. After stopping one API, three consecutive survivor responses
were confirmed within 12 seconds after probing began; transition interruption was
still observable. The mock queue check is not phone notification evidence.

## Release implications

The prior mixed test's roughly 37-second maximum observation was affected by its
random polling schedule. The improved test confirms this model's independent
polling behavior and message completeness. A 15-second polling design can still
produce visible delay; real push-triggered refresh and foreground app behavior
must be measured on devices. No change to production polling intervals is implied.

12,000 concurrent sessions remain unqualified. Continue affected-phone notification
and upload validation; then complete media/concurrent workload modelling, an
external generator, production-like staging and the reviewed larger-run budget.
No paid infrastructure was created, no production load test was run, and these
backend lab changes require no production API redeploy.
