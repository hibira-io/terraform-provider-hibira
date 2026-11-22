resource "hibira_approvals_webhook" "decisions" {
  workspace_id = hibira_approvals_workspace.ops.id
  url          = "https://ops.example.com/hooks/approvals"
  events       = ["approval.decided", "approval.expired"]
}

# Check the Hibira-Signature header with this secret.
output "approvals_webhook_secret" {
  value     = hibira_approvals_webhook.decisions.secret
  sensitive = true
}
