# Subscription choices and operating-cost assumptions

Prepared 9 October 2026. This is a planning model, not a supplier quote or a profitability forecast.

## Revenue

Buyers are currently free. Revenue is based on paying providers, not registrations or all active users.
At NGN 500 per provider per month, before gateway fees, taxes, refunds and operating expenses:

| Paying providers | Gross monthly revenue |
| --- | ---: |
| 10,000 | NGN 5 million |
| 100,000 | NGN 50 million |
| 1,000,000 | NGN 500 million |

One million registrations do not establish one million monthly active users or paying providers.

## The large cost to check first: authentication

The app uses Privy for Google sign-in; ordinary backend email/password login does not imply a Privy authenticated MAU. Obtain the actual Privy dashboard MAU and your account's contracted pricing.

Privy's public pricing says usage above 10,000 MAU uses a USD 2,000 volume PAYG base and USD 0.05 per additional MAU. Applying that published usage component:

| Privy authenticated MAU | Illustrative usage component | At an assumed NGN 1,500/USD |
| --- | ---: | ---: |
| 100,000 | USD 6,500/month | NGN 9.75 million/month |
| 1,000,000 | USD 51,500/month | NGN 77.25 million/month |

The exchange rate is a modelling assumption, not today's verified exchange rate. Plan/signature fees, tax and negotiated enterprise terms may change the actual invoice. This alone can exceed revenue if there are many authenticated buyers but few paying providers.
Source: https://www.privy.io/pricing

## Images: downloads often cost more than storage

Illustration for one million users, not a forecast of actual behaviour:
- Ten saved photos each averaging 250 KB: approximately 2,500 GB stored.
- 100 image views each monthly averaging 200 KB: approximately 20,000 GB delivered monthly.
- At a deliberately assumed storage budget rate of USD 0.03/GB-month: about USD 75/month for this stored image volume.
- At a planning internet delivery rate of USD 0.09/GB: about USD 1,800/month for this downloaded volume before free allowances, tier discounts, requests or CDN pricing.

These rates are assumptions for comparison; quote Stockholm/eu-north-1 storage, request, delivery and CDN rates in AWS Calculator for the real bucket. AWS's pricing page includes a Europe (Ireland) internet-delivery example at USD 0.09/GB. Ten full-size 5 MB photos each would instead occupy approximately 50,000 GB: about USD 1,500/month at the assumed storage rate, before accumulated chat images, backups and versions.
Sources: https://aws.amazon.com/s3/pricing/ and https://calculator.aws/

## Other costs

Budget separately for API servers, PostgreSQL, Redis, redundancy, backups/restores, image processing, monitoring, email, payment charges, fraud/moderation, customer support, development, marketing and taxes. The current VPS cannot be assumed to handle one million active users without load tests and capacity planning. No production hosting invoice or Privy/AWS account billing report was accessed for this model.

Flutterwave currently publishes 2% for Nigerian local collections; merchant pricing, applicable taxes and who bears the checkout fee determine the net receipt. At NGN 500, 2% is NGN 10 before applicable taxes/other charges.
Source: https://flutterwave.com/ng/pricing

## Proposed commercial structure

Keep Basic at the agreed NGN 500/month as an entry plan with a controlled active-listing limit. Offer higher listing-limit plans for larger businesses. The admin already supports creating named plans with prices and listing limits; approve their specific prices/limits before putting them on sale. Buyers should remain free unless a separate decision changes this.

This release adds 1, 3, 6 and 12 month prepaid bank-transfer periods at each existing plan's monthly price multiplied by its duration. No discount or new tier price is invented. Monthly cards retain their existing recurring charge. For Basic at NGN 500/month, the prepaid base totals are NGN 500, 1,500, 3,000 and 6,000, before gateway charges. Prepaid transfers do not auto-renew; current paid access must expire before another checkout begins.

Jiji publicly distinguishes Boost tiers and prepaid durations. These are reference ideas; do not copy its traffic-increase claims or sell exposure benefits until this platform actually implements and measures them.
Source: https://jiji.ng/faq/premium-services-types

## Before scaling

Measure cost per active user and per paying provider using real invoices. Add compressed images/thumbnails, cache public product media through a CDN, apply explicit storage/retention limits, and avoid constant chat/catalogue polling at high concurrency. Private chat attachments must stay participant-only. Obtain negotiated authentication pricing, run load tests, and establish a monthly spending budget and alert thresholds before significant acquisition spending.
