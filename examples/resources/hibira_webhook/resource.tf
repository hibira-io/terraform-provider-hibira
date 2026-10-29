resource "hibira_webhook" "presence" {
  app_id = hibira_app.chat.id
  url    = "https://example.com/hooks/hibira"
  events = ["member_added", "member_removed"]
}
