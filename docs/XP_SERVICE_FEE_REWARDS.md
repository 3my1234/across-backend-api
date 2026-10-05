# XP rewards and visible-page freshness

Approved policy: 1 XP reduces the Atlantic Express service fee by NGN 1,
using whole points, capped at the fee. No automatic currency conversion.
Seller item and delivery amounts remain payable in full before gateway/statutory
deductions. For NGN 65,000 item+delivery, the NGN 650 fee can be waived with 650 XP.
For NGN 100 item+delivery, only 1 XP can be used. A fee below NGN 1 cannot consume a
whole point. XP is not cash, a transferable wallet or a seller-funded promotion.

## Deployment

Deploy backend then run `cd /app && ./across-migrate` (059 and 060).
059 adds order discount snapshots, indexed reservations and corrects historical
reward-notification explanations without changing earned points. Before 059,
balance remains readable, redemption is disabled, and ordinary checkout still
works. Old app builds remain compatible and send no `use_xp` selection.
Deploy portals. Build a new mobile preview using the normal EAS preview command.
No EAS build was started by this implementation.

## Checkout and reservation lifecycle

- Server calculates item, delivery, original fee and discount. The client never
  supplies an authoritative price, XP debit or seller net amount.
- A user advisory lock and order lock serialize quote/payment transitions.
  A quote reserves points atomically; available balance excludes them.
- A new quote releases earlier unstarted reservations. Otherwise an unstarted
  quote expires after 15 minutes and balance/quote reads reclaim its reservation.
  Expired or replaced discounted quotes are rejected at payment initialization.
- Buyers can explicitly release a quote before payment starts. Opening or
  closing a payment browser is not evidence of failure. An initiated or ambiguous
  charge retains its reservation until resolved; it is not recycled on a timer.
- An XP order has one initialization attempt and reuses its hosted checkout URL.
  Concurrent/repeated initialization cannot create a second discounted charge.
  If initialization is ambiguous with no returned URL, the buyer must check
  payment status or contact support; do not blindly charge again or free points.
- Verified success consumes the reservation and inserts one uniquely keyed
  negative XP transaction in the same transaction as order/payment/seller ledger
  changes. Duplicate verification/webhooks do not deduct again. Rollback undoes
  both debit and state transition. The remaining fee is the Flutterwave flat
  marketplace allocation, including an explicit numeric zero when fully waived.
- XP used on a paid order is consumed. This change does not introduce automatic
  refunds/chargeback adjudication; the existing payment system has no verified
  refund lifecycle. Do not credit XP based on a browser redirect, a client
  cancellation or an unverified refund claim. A future refund policy must verify
  full/partial gateway refunds and use a unique compensating ledger event.

## Notifications and account copy

Welcome (650 once), daily login (1), purchase tiers (1/2/5/10/25), product review
and completed-service first review (10) explain the service-fee-only restriction.
The Account screen displays available points, reserved points, award rules and
the cap. Checkout offers an explicit opt-in and shows the actual applied discount.

## Freshness, chat and layout

Catalogue freshness retains the shared 5-second revision watcher and cached
snapshots. Private visible pages revalidate on entry/foreground and poll every
12 seconds while active; hidden/background pages do not poll. This is bounded
polling, not a claim of instantaneous websocket delivery. Session/request guards
prevent late responses from replacing newer or another user's data. Profile
editing and portal draft/modal interactions are preserved.

Support loads on opening the tab and shows loading/errors without pretending
failed fetches are empty histories. Threads use 50-message pages (maximum 100),
ordered by timestamp/ID with an indexed cursor, both in admin and mobile.
Older pages remain available via Load earlier. Mobile uses a virtualized thread,
left/right bubbles, and an anchored composer. Sending failure preserves the draft.
Services combines search/location controls, one location/cache summary row, compact
single-row category/rating chips and the existing two-column results. All service
rating displays use the same SVG stars as product reviews. Physical-device visual,
large-text, keyboard, iPhone safe-area and live gateway acceptance remain required.

## Acceptance

1. Confirm 059/060 applied. Account and every new XP reward describe the same cap.
2. NGN 100 seller price + NGN 1 fee: select XP; discount 1, seller gross 100,
   displayed checkout total 100 before Flutterwave processing fees. Verify live
   Flutterwave honors zero platform allocation and statutory deductions separately.
3. Larger fee/limited balance: discount is the lower whole-point amount; unused
   points remain. A fee below NGN 1 spends no XP.
4. Two concurrent orders cannot reserve more than the available balance.
   Replace/release/expire an unstarted quote; its old payment must be rejected.
5. Start checkout, close/reopen: same URL, points stay reserved. Verified success
   deducts once, including duplicate verification. Network failure does not free
   points that might still fund a live charge.
6. Open Support after using other tabs for five minutes: history loads itself.
   Admin reply appears without a manual refresh within the foreground poll window.
   Older messages paginate without duplicates, drafts survive reply failure,
   and closed-ticket state updates.
7. Services on narrow Android/iPhone and enlarged text: chips stay one row,
   yellow SVG review stars, more results visible, no header overlap.

Automated coverage includes the real quote/verified-payment flow, seller
preservation, underpayment/currency rejection, repeated-payment idempotency,
reservation races/expiry/ownership, support paging with equal timestamps,
and frontend entry/foreground/background/stale-response/session isolation.
