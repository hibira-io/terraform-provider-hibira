# With the provider's api_token: the token's person (its creator, else the
# Hibira account's owner) must be an owner or admin of the workspace in
# Live Chat. The resource also makes the key the site's server runs on.
resource "hibira_livechat_workspace" "support" {
  workspace_id = "ws_abc123"

  # The widget runs on the site and in its Capacitor apps; the inbox is
  # framed in the back office with single sign-on.
  origins       = ["https://shop.example.com", "https://localhost", "capacitor://localhost"]
  embed_origins = ["https://admin.example.com"]
  sso_only      = true

  webhook_url = "https://shop.example.com/api/support/livechat/webhook"
}

# Hand the key and the secrets to the site, for example through a secret store.
output "livechat_server_key" {
  value     = hibira_livechat_workspace.support.server_key
  sensitive = true
}

output "livechat_identity_secret" {
  value     = hibira_livechat_workspace.support.identity_secret
  sensitive = true
}

# Or with one of the workspace's admin API keys instead of workspace_id
# (no server_key then: the site uses its own key).
variable "livechat_api_key" {
  type      = string
  sensitive = true # an admin API key of the workspace (lck_ws_...)
}

resource "hibira_livechat_workspace" "by_key" {
  api_key = var.livechat_api_key
  origins = ["https://other.example.com"]
}
