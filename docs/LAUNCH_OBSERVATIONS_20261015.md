# Launch observations — 15 October 2026

## Corrections

- Mobile Settings crash: a same-line whitespace JSX node under ScrollView was
  rendered as native text. Removed it and added a test that renders the actual
  Settings JSX in light/dark mode and rejects unwrapped native text. The gear is
  now exposed once, on Account, rather than on every tab.
- Chat composer: main navigation is a normal sibling of the content, not an
  overlay. Removed the additional navigation/safe-area padding inside conversation
  composers. Existing measured keyboard avoidance remains in place. Support already
  used a compact composer. Physical keyboard/gesture-navigation checks remain needed.
- Town Hall: message-only request detail displays the description once; empty chat
  guidance no longer interpolates the entire request as a person's name. Room header
  identifies a bargaining room. Mobile floating Hall entry appears only on Home,
  Services and Account. Provider floating entry is restricted to Home/services views.
- Account actions have clearer bordered cards. Claimed XP and withdrawal-threshold
  states use readable status colours rather than reduced opacity on primary buttons.
- Track removed from main mobile navigation. Existing order records, order notification
  routes and payment history remain accessible; Account only offers Past orders when
  records exist. This does not delete historical transactions or disable logistics APIs.
- Dark login: generic theme conversion turned the translucent photo overlay into an
  opaque dark surface. Auth now uses an explicit translucent overlay in both themes.
- Listing photos retry the original before falling back, recover from stalled requests,
  retry on foreground after failure, ignore stale callbacks and preserve stable candidates
  across equal URI arrays. Native resize decoding reduces oversized bitmap memory use.
- New provider listing uploads above 350 KB are best-effort resized to at most 1600px
  and encoded as WebP at 0.82 quality when that reduces size. Small/unsupported/animated
  formats stay original. Verification documents are untouched. Existing uploaded photos
  are not automatically recompressed; slower old originals may still need CDN/thumbnail
  work after measurement. No global Promise.all image download burst was introduced.
- Town Hall busy state is attached to the clicked action only. Other buttons no longer
  dim together. Shared chat does not disable the whole screen. Subscription checkout
  and admin listing/product moderation use in-flight guards with feedback on the chosen
  action. Approval/rejection and checkout duplication protections remain intact.
- Buyer welcome email copy now describes direct contact rather than checkout/tracking.

## Email delivery investigation (not resolved by UI changes)

A verification email reached Hotmail Junk. SMTP success proves acceptance by the mail
service, not Inbox placement. Read-only public DNS inspection found:

    _dmarc.atlxpres.com: v=DMARC1; p=none; adkim=r; aspf=r; pct=100
    atlxpres.com SPF: v=spf1 include:spf.efwd.registrar-servers.com ~all

The root SPF record authorizes a forwarding service. This does not by itself prove
SES-generated mail fails DMARC: aligned DKIM can pass independently, and SES may use a
different envelope-sender domain. Requested the specific email's Authentication-Results
(SPF, DKIM, DMARC), excluding its verification URL/token. Those headers and SES identity/
custom MAIL FROM status are needed before proposing a DNS change. Do not blindly add
multiple SPF records or enforce DMARC rejection. No DNS/SES configuration was changed.

Microsoft reference: https://learn.microsoft.com/en-us/defender-office-365/email-authentication-about

## Chat capacity and Telegram comparison

Telegram TDLib explicitly maintains a local chat/message database and handles updates
and consistency: https://core.telegram.org/tdlib/getting-started . ATL now also uses
encrypted bounded local recent-history snapshots and nonblocking refresh. That does
not establish capacity for thousands of concurrent users. Existing staging-only
scripts/chat-capacity.mjs and scripts/diagnose-capacity.sql remain the tools for a
representative load test. No production load test or capacity guarantee was made.

## Deployment and acceptance

No migration or new environment variable. Deploy API and worker for the updated email
copy, and deploy admin and standalone provider resources separately. Provider PWA shell
is 20261015-v11; installed PWA uses Refresh app. Mobile needs the user's new EAS build.
Codex submits no cloud build. Prior release's encrypted chat caches and privacy guards
remain. Verify new account photos without restarting; switch auth light/system-dark;
open Account Settings; test chat keyboard/composer with gesture and button navigation;
check Hall description/entry placement and action feedback; verify old order access.
Spam placement still requires header/SES investigation and is not declared fixed.
