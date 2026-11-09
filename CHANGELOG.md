## Unreleased

- `hibira_waiting_room` resource: a waiting room of an app (rate, state, start time, pass lifetime, title, notice, return URLs); import with `<app_id>/<name>`.
- The provider now stops with a clear error when given an operations console token (`hbc_...`) instead of a portal API token.

## 0.1.1

- README: cover image and screenshots.

## 0.1.0

First release.

- `hibira_app` resource: name, cluster, enabled, client events, subscription count, allowed origins, history, API allowlist, edge mode; key, secret and host as outputs; secret rotation with a grace period.
- `hibira_webhook` resource: one webhook endpoint of an app.
- `hibira_push` resource: push notifications with APNs and FCM credentials.
- `hibira_app` and `hibira_apps` data sources.
