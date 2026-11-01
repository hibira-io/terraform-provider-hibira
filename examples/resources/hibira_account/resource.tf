data "hibira_account" "demo" {
  email = "demo@example.com"
}

# A $0 custom plan with one app, tagged as an internal account.
resource "hibira_account" "demo" {
  id   = data.hibira_account.demo.id
  tags = ["internal"]

  custom_plan = {
    max_apps        = 1
    max_connections = 1000
    daily_messages  = 200000
    currency        = "usd"
    note            = "Demo account"
  }
}
