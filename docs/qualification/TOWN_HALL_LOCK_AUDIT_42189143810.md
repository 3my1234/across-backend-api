# Town Hall contention audit

2026-11-06. Retained VPS run42189143810 and disposable PostgreSQL run42192009605.
No additional VPS setup, root SSH, production changes or application optimization
was performed during this audit.

## What the existing evidence establishes

| Question | Finding |
| --- | --- |
| Which operations lock the room? | `CommunityController.lockMember` takes `chat_rooms ... FOR UPDATE` for shared message sends and HallOffer. Owner/membership/closure paths and admission triggers also use the room lock. |
| Why does the lock exist? | Serialize admission, removal and closure with sends/offers, protect the ten-provider seat bound, and prevent stale membership/room-state authorization. It is a correctness boundary. |
| What remains inside the transaction? | Fresh membership, own-business and paid-provider checks; retry lookup; offer/message writes; revision/read-state updates; durable notification batches and their row triggers; commit. No external push delivery, Redis call, history response or response serialization occurs inside the inspected offer transaction. |
| Did the test concentrate traffic? | Yes. Retained gateway logs contain35 Town Hall write attempts across3rooms:33target one room (22offers,11messages), plus one message each to two personal rooms. These counts include retries. The fixture deliberately assigns one hot room per ten providers; 100050 total rooms does not imply uniform selection. |
| Was the SQL intrinsically expensive? | Not in the matched idle-plan test. On100050rooms, original custom-plan median0.0925ms and generic0.096ms. Equivalent left-join candidate medians0.084/0.0865ms. This does not explain the VPS observed mean586ms/max3067ms. Candidate rejected; runtime SQL and locking unchanged. |
| Is pool coupling proven? | Aggregate acquisition waits were1326ms/404acquisitions onAPI1 and1736ms/374 onAPI2, with no cancelled acquisitions. Retained pool snapshots peaked at4/8 and3/8 occupied connections. This supports some acquisition delay, not a finding of sustained pool exhaustion or a per-request explanation of catalogue latency. Do not increase pools blindly. |
| What about triggers/workers? | Message/offer foreign keys and notification delivery triggers remain part of transactions. Batched fanout SQL had24calls, mean51.32ms/max376.53ms. Paid-provider authorization had33calls, mean2.19ms/max38.32ms, so moving that check is not an evidence-supported large optimization. Expiry uses room locks with SKIP LOCKED; its role in the failing window is not established by aggregate lock-wait counts. |
| Was production unhealthy? | All267retained watchdog samples report healthy production, zero health failures; maximum sampled I/O wait5.013%. CPU steal was not separately retained, so shared-host scheduling interference cannot be excluded or quantified. Current host state was not independently queried. |
| Did writes disappear? | No acknowledged write was missing or duplicated:35/35storedonce. One additional offered write committed without recorded acknowledgement during an interrupted run. Abort-time cancellation is plausible, not proven for that individual key. Existing idempotency implementation and tests remain necessary; this is not a full reconciliation pass. |
| What does the short p95 establish? | The observed147-completion sample failed the configured acceptance gate. Fifteen seconds does not establish sustained p95, representative capacity, or the cause of every slow endpoint. Catalogue had only4measured SQL calls, mean246.67ms/max373.99ms, while its client p95was927.78ms. These are different populations/measurements; percentile subtraction cannot attribute the difference. |

## Transaction and correctness details

- `internal/controllers/community_controller.go`: `lockMember`, `Send`,
  `insertSharedMessage`, `notifyRoom`. All room state, membership and durable
  notification effects commit or roll back together. Switching all senders to
  FOR SHARE would require lock upgrades for revision updates; a weaker lock
  cannot be selected without proving closure/removal ordering remains safe.
- `internal/controllers/community_town_hall_controller.go`: HallOffer performs
  authorization before idempotency replay, rejects key reuse with changed content,
  avoids reverting a newer offer on an old replay, and commits offer plus chat
  message plus notifications atomically. HallChoose keeps the offer snapshot and
  direct-chat handoff idempotent. These guarantees must survive any correction.
- Migrations080/086/101/107 include admission/capacity/engagement/expiry locking.
  Notification batching still invokes per-row durable delivery triggers. Moving
  notifications after commit without a transactional durable outbox is not safe.
- Current fixture/client deliberately includes hot-room traffic. Preserve that
  case. A separate distributed-room diagnostic would clarify its contribution,
  but must not silently replace the existing qualification workload.

## Disposable evidence and limits

Run42192009605 at a021f31 compares the exact original lock statement with a
same-lock `LEFT JOIN` candidate using PostgreSQL16,2CPU/3GiB,100050synthetic rooms,
custom/generic plans and ABBA order (24actual EXPLAIN ANALYZE samples). It is an
uncontended query-plan diagnostic, not a transaction-concurrency benchmark or
application qualification. The earlier diagnostic-only run42191801215 failed
because its new fixture omitted required location; a021f31 corrects that fixture.
No application SQL was changed.

Retained artifacts: shared workspace qualification/lock-profile-42192009605,
qualification/lock-plan-results-42192009605.json and logs; existing VPS artifact
qualification/live-42189143810. API runtime remains15388a5: later release-branch
commits4fef54b/a021f31 contain diagnostic code/workflow only.

## Next justified engineering action

Measure query/commit time and lock-hold duration inside the existing offer/send
transaction on the full restored fixture in disposable CI, with concurrent
providers targeting the same room. Correlate it with lock wait and pool acquisition
time, distinguish first/cold requests from subsequent requests, and run a separate
distributed-room comparison. Keep request bodies, retries, notification triggers,
authorization and isolation unchanged. Adopt only a measured reduction in work
inside the critical section; rerun concurrent removal/closure, seat limit,
idempotency, rollback and selection tests. A query rewrite, global pool increase,
weaker lock or new VPS trial is not justified by the current evidence alone.
