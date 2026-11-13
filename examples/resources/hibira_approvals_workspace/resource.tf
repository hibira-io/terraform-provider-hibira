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
}

# Agents and workflows send requests through the workspace's app; connect
# a tool with `npx -y @hibira-io/mcp-approval-gate login`.
output "approvals_app_id" {
  value = hibira_approvals_workspace.ops.app_id
}
