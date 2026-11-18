variable "livechat_api_key" {
  type      = string
  sensitive = true # an admin API key of the workspace (lck_ws_...)
}

resource "hibira_livechat_workspace" "support" {
  api_key = var.livechat_api_key

  # The widget runs on the site and in its Capacitor apps; the inbox is
  # framed in the back office with single sign-on.
  origins       = ["https://shop.example.com", "https://localhost", "capacitor://localhost"]
  embed_origins = ["https://admin.example.com"]
  sso_only      = true

  webhook_url = "https://shop.example.com/api/support/livechat/webhook"
}

# Hand the secrets to the site, for example through a secret store.
output "livechat_identity_secret" {
  value     = hibira_livechat_workspace.support.identity_secret
  sensitive = true
}
