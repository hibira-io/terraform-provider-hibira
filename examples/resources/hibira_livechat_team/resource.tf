# The provider's API token works in the Live Chat workspaces its owner is
# an owner or admin of: GET https://chat.hibira.io/v1/workspaces lists them.
resource "hibira_livechat_team" "billing" {
  workspace_id = "ws_0123456789abcdef"
  name         = "Billing"
  agents       = ["mei@example.com", "kai@example.com"]
}
