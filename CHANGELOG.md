## 0.9.0

- Hibira Live Chat's default address is now https://api.hibira.io/live-chat (its REST API on api.hibira.io, pusher-server#958). `endpoint` and `livechat_endpoint` accept a path. A `hibira_livechat_workspace` set up with the old default (https://chat.hibira.io) keeps it and is not replaced; that address keeps working.

## 0.8.0

- Needs pusher-server v0.2.166 or later for the resources below: API tokens with the delete scope, and Live Chat settings with an API token on every plan (a feature the plan doesn't include is refused with a message naming the plan).
- `hibira_approvals_workspace`: `rules` (auto-approve rules; a changed rule keeps its ID) and `follow_up` (reminders and escalation). Destroying a workspace now confirms it with its name, and needs a token with delete access.
- `hibira_approvals_webhook` resource: an Approvals webhook endpoint with its events and signing secret; import with `<workspace_id>/<webhook id>`.
- Hibira Live Chat with the provider's API token, in a workspace named by `workspace_id`: `hibira_livechat_team` (members by email), `hibira_livechat_business_hours` (days, time zone, closed dates, away message; destroying turns them off), `hibira_livechat_saved_reply` and `hibira_livechat_knowledge_source` (a website the AI assistant answers from).
- Provider: `livechat_endpoint` (or `HIBIRA_LIVECHAT_ENDPOINT`) for those resources.

## 0.7.0

- `hibira_livechat_workspace`: `create_workspace = true` (with `workspace_name`) opens the workspace when the token's account has none by that name, owned by that account (`POST /v1/workspaces`, pusher-server#903). It starts on Free and the REST API needs Team, so the first apply opens it and stops with what to do; once its plan is changed (bought in the Hibira portal or set by a Hibira operator) the next apply finds it by name and sets it up. Destroying the resource never deletes the workspace.
- `workspace_name` now skips workspaces the token can't act in (a pending invite, or a single sign-on/SCIM workspace it doesn't own, from the server's `usable`) and turned-off ones, and names them when nothing matches. A workspace whose plan lacks the REST API stops at the lookup with a clear message instead of a later `402`.

## 0.6.0

- `hibira_livechat_workspace`: `workspace_name` instead of `workspace_id`. The provider's token lists the workspaces its account is in and takes the one with that name (it must be unique); the ID is kept in `workspace_id` in state, so nobody has to copy it. Needs pusher-server with `GET /v1/workspaces` (#898).

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
