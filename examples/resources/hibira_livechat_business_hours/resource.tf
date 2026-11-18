resource "hibira_livechat_business_hours" "main" {
  workspace_id = "ws_0123456789abcdef"
  time_zone    = "Asia/Taipei"

  days = {
    monday    = { from = "09:00", to = "18:00" }
    tuesday   = { from = "09:00", to = "18:00" }
    wednesday = { from = "09:00", to = "18:00" }
    thursday  = { from = "09:00", to = "18:00" }
    friday    = { from = "09:00", to = "17:00" }
  }

  closed_dates = ["2026-12-25", "2027-01-01"]
  away_message = "We're away right now. Leave your email and we'll answer in the morning."
}
