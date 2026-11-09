resource "hibira_waiting_room" "presale" {
  app_id           = hibira_app.shop.id
  name             = "presale"
  admit_per_minute = 300
  state            = "paused"
  starts_at        = "2026-12-04T12:00:00+09:00"
  title            = "Winter tour presale"
  return_urls      = ["https://shop.example.com/drop"]

  # Pausing or closing the room from the portal during the sale? Keep
  # Terraform from switching it back on the next apply:
  # lifecycle { ignore_changes = [state, notice] }
}
