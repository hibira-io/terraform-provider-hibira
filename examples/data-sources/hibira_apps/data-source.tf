data "hibira_apps" "all" {}

output "app_names" {
  value = [for app in data.hibira_apps.all.apps : app.name]
}
