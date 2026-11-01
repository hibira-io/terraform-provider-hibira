data "hibira_console_apps" "demo" {
  account_id = data.hibira_account.demo.id
}

output "demo_app_keys" {
  value = [for app in data.hibira_console_apps.demo.apps : app.key]
}
