# Nigeria-first public launch

The mobile app may be installed only from Nigerian App Store and Google Play storefronts at first. Store distribution is configured in App Store Connect (Pricing and Availability > App Availability > Specific Countries or Regions) and Play Console (Production > Countries/regions), not in `app.json` or EAS. Select Nigeria only in both consoles before the first public release. Store country is based on the customer's store account and is not a reliable delivery address.

The API remains the transaction boundary. `countries_config` must have only NG/NGN enabled for Flutterwave at launch. Product catalogue, quotes, standard checkout, saved-token charge, and public service listings/requests respect that active-market setting. A customer must enter a real Nigerian delivery address; IP location is only a browsing hint. An overseas-stock product can be sold to a Nigerian buyer only if the seller has a matching NGN delivery offer and an active Flutterwave payout subaccount. Verify that subaccount's actual settlement route before approving the seller's first live order.

Run these read-only checks against the **production primary** before release:

```sql
SELECT country_code,currency_code,is_active,active_payment_gateways
FROM countries_config ORDER BY country_code;

SELECT country_code,currency_code,payment_methods,is_active
FROM payment_method_policies ORDER BY country_code,currency_code;

SELECT name FROM schema_migrations
WHERE name LIKE '053%' OR name LIKE '054%' OR name LIKE '055%'
ORDER BY name;
```

Confirm `/api/v1/buyer-markets` returns NG/NGN alone. Confirm that a Nigerian address can obtain an NGN quote and live Flutterwave checkout for a verified seller, and that a non-enabled country cannot quote or pay, including with a saved card. Change the saved delivery address after obtaining a quote: the old quote must be rejected for both standard and saved-card checkout, and the app must request a new quote. Check the charged amount against the quote, then verify the seller's expected share and actual Flutterwave settlement separately. Test an imported-on-demand order stays blocked until the seller settlement is released. Test local and overseas-stock offers to Nigeria, and a Nigerian service listing. A service in an inactive country must not appear publicly or accept a new request.

Production must use `APP_ENV=production`, a live `FLUTTERWAVE_SECRET_KEY`, and `ENABLE_MOCK_PAYMENTS=false` (the default). Mock charges require both `ENABLE_MOCK_PAYMENTS=true` and a development/test environment; never enable them in Coolify. Confirm the webhook secret and background settlement worker are configured. EAS internal `preview` APKs are test builds, not store releases. Build a fresh Android production AAB and iOS production build from the reviewed mobile commit, then set Nigeria-only availability in the respective store consoles. Complete a real low-value purchase and inspect the provider's bank settlement before increasing volume. Store availability changes and Flutterwave account settlement capabilities cannot be verified from source code alone.

Do not activate another buyer market merely to make products visible. Add a country only after its currency collection, seller split/settlement, delivery, refunds, support and country-specific provider pricing are tested. Public release notes should say "Available in Nigeria" until then.
