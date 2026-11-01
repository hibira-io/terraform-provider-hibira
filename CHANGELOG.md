## Unreleased

- Operator mode: with an operations console token (`console_token`, `hbc_...`) and the Cloudflare Access service token (`cf_access_client_id`, `cf_access_client_secret`), the provider manages customer accounts:
  - `hibira_account` resource: an existing account's custom plan, operator note and tags, and Chat API beta. It takes over an account and leaves it as it is when destroyed.
  - `hibira_account` data source (by ID or email) and `hibira_console_apps` data source (every account's apps, or one account's).
- A token in the wrong setting (a console token as `api_token`, or a portal token as `console_token`) stops with an error that says where it goes.

## 0.1.1

- README: cover image and screenshots.

## 0.1.0

First release.

- `hibira_app` resource: name, cluster, enabled, client events, subscription count, allowed origins, history, API allowlist, edge mode; key, secret and host as outputs; secret rotation with a grace period.
- `hibira_webhook` resource: one webhook endpoint of an app.
- `hibira_push` resource: push notifications with APNs and FCM credentials.
- `hibira_app` and `hibira_apps` data sources.
