resource "hibira_approvals_workspace" "ops" {
  name = "Ops approvals"

  members = [
    { email = "you@example.com", role = "admin" }, # the API token's owner
    { email = "dba-lead@example.com", role = "approver" },
    { email = "auditor@example.com", role = "viewer" },
  ]

  groups = [
    { slug = "dba", name = "DBAs", members = ["dba-lead@example.com"] },
  ]

  # Paid plans: approve read-only queries at once, never a DROP.
  rules = [
    { name = "Read-only queries", titles = ["SELECT *", "SHOW *"], except = ["*DROP*"] },
  ]

  # Remind after 10 minutes, let the DBAs decide after 30.
  follow_up = {
    remind_minutes   = 10
    escalate_minutes = 30
    escalate_to      = ["dba"]
  }
}

# Agents and workflows send requests through the workspace's app; connect
# a tool with `npx -y @hibira-io/mcp-approval-gate login`.
output "approvals_app_id" {
  value = hibira_approvals_workspace.ops.app_id
}
