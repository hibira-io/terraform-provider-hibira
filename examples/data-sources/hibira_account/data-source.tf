data "hibira_account" "demo" {
  email = "demo@example.com"
}

output "demo_plan" {
  value = data.hibira_account.demo.effective_plan
}
