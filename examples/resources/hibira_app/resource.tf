resource "hibira_app" "chat" {
  name            = "chat-prod"
  client_events   = true
  allowed_origins = ["https://example.com"]
}

# Pass the credentials straight to where your backend runs.
output "pusher_config" {
  value = {
    app_id = hibira_app.chat.id
    key    = hibira_app.chat.key
    host   = hibira_app.chat.host
  }
}

output "pusher_secret" {
  value     = hibira_app.chat.secret
  sensitive = true
}
