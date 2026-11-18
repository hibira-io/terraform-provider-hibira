## 0.6.0

Needs a Hibira server with pusher-server#898 (API token delete scope and API tokens on Live Chat).

- `hibira_approvals_workspace`: `rules` (auto-approve rules; a changed rule keeps its ID) and `follow_up` (reminders and escalation). Destroying a workspace now confirms it with its name, and needs a token with delete access.
- `hibira_approvals_webhook` resource: an Approvals webhook endpoint with its events and signing secret; import with `<workspace_id>/<webhook id>`.
- Hibira Live Chat with the provider's API token, in a workspace named by `workspace_id`: `hibira_livechat_team` (members by email), `hibira_livechat_business_hours` (days, time zone, closed dates, away message; destroying turns them off), `hibira_livechat_saved_reply` and `hibira_livechat_knowledge_source` (a website the AI assistant answers from).
- Provider: `livechat_endpoint` (or `HIBIRA_LIVECHAT_ENDPOINT`) for those resources.

## 0.5.0

- `hibira_livechat_workspace`: set `workspace_id` instead of `api_key` to use the provider's `api_token`, so one Hibira API token manages both Realtime and Live Chat. The token needs read, write and delete access, and its account's email must be an owner or admin of the workspace in Live Chat. The resource then also makes the API key the site's server runs on (`server_key`, scope `server_key_scope`, default `admin`), makes a new one on `rotation` or a scope change and revokes the old one, makes it again if it is revoked in Live Chat, and revokes it on destroy. Needs Live Chat with hibira.io token support (pusher-server#898 and #899).

## 0.4.1

- `hibira_livechat_workspace`: an `api_key` that isn't a Live Chat key no longer shows the key in the error message (it could print a real key with a stray line break into CI logs).

## 0.4.0

- `hibira_livechat_workspace` resource: sets up an existing [Hibira Live Chat](https://chat.hibira.io) workspace from one admin API key: the widget's allowed origins, the origins that may frame the inbox, single sign-on only, a new verified-visitor secret and single sign-on secret (rotated with `rotation`), and one webhook endpoint with its signing secret. Needs Live Chat with the secret API (pusher-server#890). The state holds the API key and the secrets: use an encrypted backend.

## 0.3.1

- `hibira_approvals_workspace`: docs name Hibira Approvals instead of its old host.

## 0.3.0

- `hibira_approvals_workspace` resource: a Hibira Approvals workspace with its members, roles and approver groups; import with its `apw_...` ID. Needs a Hibira server with the Approvals workspace API (pusher-server#775).

## 0.2.0

- `hibira_waiting_room` resource: a waiting room of an app (rate, state, start time, pass lifetime, title, notice, return URLs); import with `<app_id>/<name>`.

## 0.1.2

- The provider now stops with a clear error when given an operations console token (`hbc_...`) instead of a portal API token.

## 0.1.1

- README: cover image and screenshots.

## 0.1.0

First release.

- `hibira_app` resource: name, cluster, enabled, client events, subscription count, allowed origins, history, API allowlist, edge mode; key, secret and host as outputs; secret rotation with a grace period.
- `hibira_webhook` resource: one webhook endpoint of an app.
- `hibira_push` resource: push notifications with APNs and FCM credentials.
- `hibira_app` and `hibira_apps` data sources.
