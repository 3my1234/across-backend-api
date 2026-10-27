# Chat-history query investigation — 27 October 2026

Status: isolated experiment completed; not merged or deployed. Branch: `perf/business-chat-history`, based on `b422273` (production application code plus the unmerged laboratory fixture corrections from PR 83). No production migration, index, configuration, service or infrastructure was changed. No paid resources or new CI runs were used.

## Finding and minimal change

The experiment confirms excessive work in the legacy business-history page query. The original `conversation_id IN (thread members)` query scans all qualifying messages and sorts them to choose a small global page. In the warm 10,000-message first-page case it consumed all 10,000 message rows, despite returning only 51. PostgreSQL selected the existing unread index for that scan; merely duplicating the existing pagination index would not address this plan shape.

The experimental query uses the **same thread-membership helper**, then a lateral ordered scan capped at `page size + 1` for each physical conversation, and finally applies the same global order and limit. The existing `(conversation_id, created_at, id)` index can now stop early. A single conversation contributes 51 rows; the ten-conversation grouped fixture contributes at most 510 before selecting the global 51, rather than all 10,000.

The bound is safe: any message ranked below N within its own conversation already has N globally eligible messages ahead of it, so it cannot be in the global top N. The same cursor predicate is applied before each local limit. UUIDs break equal-timestamp ties as before.

Only two runtime files change: `provider_threads.go` adds the query helper, and `provider_conversation_controller.go` calls it. Authorization, business membership, JSON compatibility fields, revision/ETag, media signing, message parsing, read acknowledgement, request timeouts and HTTP response construction remain unchanged. There is no schema change or new index.

This identifies a cause of the **reproduced synthetic history slowdown**, not proof that every production delay has this cause. The OR membership join and JSON compatibility checks remain in place; their removal was not necessary for the measured improvement. No per-function CPU profile was taken to attribute a percentage specifically to JSON conversion.

## Matched HTTP comparison

Both variants use the actual buyer-history handler over real loopback HTTP, not a stubbed SQL response. A test authentication middleware supplies synthetic user IDs; production JWT validation, rate limiting, ingress, Redis, object-storage delivery and other endpoints are deliberately outside this measurement. The existing handler still enforces conversation ownership.

The baseline binary uses a Go source overlay of the exact `b422273` controller file. The optimized binary uses the experimental controller. Both contain identical fixture, HTTP, validation and measurement code. The old membership helper is unchanged. The run order was **original → optimized → optimized → original**, recreating identical deterministic fixtures for each run and warming each case equally.

Environment:

- Windows/amd64, Go 1.27.3, four logical CPUs; generator and database share this workstation.
- Existing loopback-only PostgreSQL 18.0; 32 MB shared buffers, 4 MB work_mem, 30 maximum connections, JIT on. No database settings were changed.
- Eight measured database connections, 16 HTTP clients, eight-second closed-loop measurement windows plus request drain. This is a short bottleneck experiment, not a soak or launch qualification.
- 1,000 conversations and 317,105 synthetic messages. Tested histories: five messages; 1,000 messages; 10,000 messages; and ten grouped conversations with 10,000 messages in total.
- Equal rotation through first-page and older-cursor requests for all four history shapes. `limit=50`, `mark_read=false`; every returned page's IDs/order are checked against the original query. Warmup uses 80 requests per run. UTC fixture timestamps avoid dependence on the machine's local timezone.

| Metric | Original | Experimental |
| --- | ---: | ---: |
| Successful requests/sec, run 1 | 54.88 | 459.67 |
| Successful requests/sec, run 2 | 55.07 | 460.25 |
| HTTP p95, run 1 / run 2 | 741.61 / 734.92 ms | 83.47 / 79.38 ms |
| HTTP p99, run 1 / run 2 | 874.22 / 920.78 ms | 153.23 / 122.14 ms |
| HTTP p50, run 1 / run 2 | 225.91 / 213.46 ms | 27.60 / 29.53 ms |
| Successful measured requests, both runs | 921 | 7,388 |
| Failed measured requests | 0 | 0 |
| PostgreSQL query-backend CPU, weighted average | 326.23% of one core | 237.85% of one core |
| PostgreSQL query-backend CPU/request | 59.34 ms | 5.17 ms |
| Total pool acquisition duration/request | 95.78 ms | 3.84 ms |
| Client-observed history-query p95, run 1 / run 2 | 497.53 / 512.16 ms | 32.57 / 32.73 ms |
| Client-observed history-query p99, run 1 / run 2 | 633.60 / 639.94 ms | 63.75 / 54.45 ms |

Weighted throughput improved **8.37×** in this specific read workload. CPU time per completed request fell **91.29%**. The CPU percentage counts kernel plus user time of the eight measured PostgreSQL backend processes, sampled through Windows GetProcessTimes; 100% means one logical core. It excludes the generator, autovacuum, background writer and checkpointer, so it must not be represented as total database-service CPU.

Pool acquisition duration is summed over all request acquisitions and includes immediate acquisition overhead, not just empty-pool wait. The pgx query tracer starts after acquisition and measures the SQL round trip through row consumption. Neither is identical to PostgreSQL execution time; server execution measurements are below. Reducing query work also reduced queueing behind the eight connections. Increasing the pool was not needed.

Do not average p95/p99 values into a purported aggregate percentile: the table reports each run's measured percentile. The throughput/CPU summaries combine completed-request counts and elapsed time. No statistical confidence interval or longer-term stability claim follows from two short runs.

The earlier 199/226 requests/sec figures belong to a different, mixed two-replica Docker workload. They are **not** directly comparable to these single-endpoint Windows results. This experiment does not establish an eightfold increase in whole-platform capacity or support for thousands of active users.

## Server query plans and execution time

The investigation prepares each statement on one held connection, explicitly selects custom or generic planning, warms each case twice, and saves `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE ...` for the third execution. Generic plans retain parameter symbols; custom plans substitute their values. This follows PostgreSQL's [prepared-plan inspection procedure](https://www.postgresql.org/docs/17/sql-prepare.html).

These are individual warm server execution samples, **not p95/p99 values**:

| History/page | Plan | Original execution | Experimental execution |
| --- | --- | ---: | ---: |
| Five messages, first page | Custom | 0.451 ms | 0.441 ms |
| Five messages, first page | Generic | 0.361 ms | 0.334 ms |
| 1,000 messages, first page | Custom | 16.254 ms | 1.550 ms |
| 1,000 messages, first page | Generic | 16.506 ms | 1.356 ms |
| 10,000 messages, first page | Custom | 287.438 ms | 1.087 ms |
| 10,000 messages, first page | Generic | 144.701 ms | 1.932 ms |
| Ten grouped conversations, first page | Custom | 156.430 ms | 10.084 ms |
| Ten grouped conversations, first page | Generic | 145.909 ms | 9.250 ms |
| Very old page in 10,000-message history | Custom | 4.188 ms | 1.793 ms |
| Very old page in 10,000-message history | Generic | 9.531 ms | 8.724 ms |

For the hot first page, message rows consumed fell from 10,000 to 51. Top-plan shared buffer hits fell from 742 to 13. For the grouped first page they fell from 3,378 to 63, with 1,000 rows per member reduced to 51. The retained message indexes are unchanged. The improvement uses PostgreSQL's documented [ordered-index/limited-result behavior](https://www.postgresql.org/docs/15/indexes-ordering.html), rather than disabling correctness constraints.

The high variation between custom/generic first-page samples is one reason to retain full plans and repeated HTTP results rather than advertise a single maximum query-time speedup.

## Correctness and regression evidence

All 13 selected tests passed in a 53.296-second run, including the explicit query-plan investigation. `regressions.log` retains the actual test names/results.

- A new equivalence test compares every returned field with the original query, traversing complete grouped histories through page sizes 1, 2, 17 and 51, including equal timestamps, a physical alias and a nonexistent conversation. It checks for duplicates and exactly 160 eligible messages; a different customer's messages and a different thread are excluded.
- Old-schema fallback is exercised after removing optional thread/location/context columns, with uncached and statement-cache execution paths.
- Existing tests passed for grouped product/service history, cross-account authorization, buyer lookup scoping, provider read ownership, pagination, receipts, read acknowledgements and conditional reads. One existing test also issued 100 concurrent conditional reads.
- Attachment permissions/retries and private idempotent static-location messages passed their integration tests. New query output preserves location/context fields.
- The four HTTP runs validated 8,309 successful measured pages in total against the original IDs/order; all had zero errors and unchanged message counts. The benchmark sends no messages; it is **not** a new write-throughput or full durable-send/recovery qualification. Existing functional tests cover sends and replay behavior.

The first exploratory harness run failed its fixture-count assertion because mixed local/UTC fixture literals prevented the intended small-history deletion. Fixture timestamps were corrected to explicit UTC, the small-history count asserted, and **all four final comparisons rerun** with the corrected fixtures. Only evidence under `verified/` supports the tables here. Earlier exploratory files are retained separately and are not treated as passing benchmark evidence.

## Remaining bottlenecks and limits

1. **Very old pages under generic plans:** the preserved optional-cursor OR predicate can prevent the tuple cursor becoming an index boundary. The experimental generic hot-cursor plan still filters 9,850 newer rows; it is only modestly faster than the original. A separate cursor-specific statement shape may help, but has not been implemented or benchmarked in this change.
2. **Membership/revision/inbox work:** OR membership joins and compatibility JSON lookups remain, and grouped-member work grows with the number of physical conversations. Inbox unread and preview queries are not optimized here. They need per-operation mixed-load attribution before another change.
3. **Environment fidelity:** PostgreSQL 18/Windows and a shared generator workstation differ from the previous PostgreSQL 16/Linux Docker lab and production network/host topology. Production hardware, network waits and data skew can change results. No production plan was collected.
4. **Load fidelity:** the workload is read-only and short, bypasses JWT authentication and ingress, and omits workers, media bytes, discovery/GPS updates and competing writes. It isolates this handler's bottleneck rather than certifying application-wide capacity.
5. **Data-volume/age:** messages are synthetic, mostly short text and no live signed media. JSON equivalence and attachment/location correctness are tested separately, but realistic media-heavy and much older/larger grouped histories remain to qualify.
6. **CPU accounting:** measured query-backend CPU is useful for before/after work attribution, not a complete service/host CPU, disk or memory profile.

## Recommendation and next gate

Keep this small query change as the candidate for the next **isolated** qualification phase. Do not merge or deploy based solely on this experiment.

First repeat the existing mixed 20/50/100-pair Docker workload on matched original/experimental application builds with the same PostgreSQL 16/Linux resource limits and identical seeds, recording per-operation p50/p95/p99, database CPU, pool acquisition, admission, locks and exact write reconciliation. This tests whether history savings also improve sends/support under competition, and identifies the next bottleneck. The laboratory fixture correction in PR 83 remains a separate, unmerged dependency to reconcile before any release.

After that comparison passes, the next independent-user milestone is **100 sessions** with realistic polling and a bounded three-minute stage, then sustained measurement under the audit's criteria. Do not jump to 500 or 10,000 because this endpoint is faster. Extend transport/media/worker workload coverage as specified in the audit before claiming complete launch qualification.

Any cursor-specific follow-up, higher-resource campaign, PR merge, deployment or production change must remain separately reviewable. For this query-only candidate, code rollback is a return to the previous application revision; there is no schema rollback or data rewrite. Production rollback/activation still requires the existing exact-source, retained-image and operator approval process.

## Reproduction and evidence

The tests are opt-in. Their shared database fixture helper rejects non-loopback database hosts and creates/drops only a random owned test schema. Do not point them at production, even if tunneled through localhost. Use a disposable local database as in this experiment. They do not start or stop a database service.

1. Set `SETTLEMENT_TEST_LOCAL=true`, `DATABASE_URL` to the disposable loopback database, `CHAT_HISTORY_INVESTIGATE=true`, and `CHAT_HISTORY_EVIDENCE_DIR` to an owned evidence directory.
2. Run `go test ./internal/controllers -run '^TestBusinessHistoryQueryInvestigation$' -count=1 -v` for the custom/generic plans. No benchmark labels are needed for this step.
3. Compile two test binaries using identical investigation/test files. Build the baseline with a Go `-overlay` JSON that replaces only `internal/controllers/provider_conversation_controller.go` with its `git show b422273:internal/controllers/provider_conversation_controller.go` content; build the candidate without that overlay. Both use the unchanged membership helper and identical fixtures. Source files in the working tree need not be swapped.
4. Run each binary from `internal/controllers` with `-test.run=^TestBusinessHistoryEndpointPerformance$ -test.v=true -test.timeout=180s`. Set `CHAT_HISTORY_VARIANT=original` or `optimized` to match the binary, and `CHAT_HISTORY_RUN=1` or `2`. These labels are not automatic source verification: retain each binary/source hash in the manifest. Use A/B/B/A order, one run at a time, without competing tests.
5. Run the named correctness tests in the retained regression log with the candidate and the same isolated database. Verify real PASS results rather than treating opt-in skips as evidence.

Local evidence is retained at `qualification/chat-history-performance-20261027/verified/` in the shared workspace: four HTTP JSON reports, 32 complete query plans, two summary JSON reports, the regression log, and a source/binary hash manifest. The platform-specific CPU helpers support local Windows/Linux PostgreSQL processes; no cross-host CPU claim is made.
