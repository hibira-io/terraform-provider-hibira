resource "hibira_push" "chat" {
  app_id = hibira_app.chat.id

  apns = {
    key_id      = "ABC123DEFG"
    team_id     = "TEAM123456"
    bundle_id   = "com.example.chat"
    private_key = file("AuthKey_ABC123DEFG.p8")
  }

  fcm = {
    service_account = file("firebase-service-account.json")
  }
}

output "beams_instance_id" {
  value = hibira_push.chat.instance_id
}
