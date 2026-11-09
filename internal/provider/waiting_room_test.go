package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccWaitingRoom(t *testing.T) {
	config := func(room string) string {
		return `
resource "hibira_app" "test" {
  name = "tf-acc-waitroom"
}

resource "hibira_waiting_room" "test" {
  app_id = hibira_app.test.id
  name   = "presale"
` + room + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: config(`
  admit_per_minute = 300
  state            = "paused"
  starts_at        = "2030-12-04T12:00:00+09:00"
  title            = "Presale"
  return_urls      = ["https://shop.example.com/drop"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "state", "paused"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "starts_at", "2030-12-04T12:00:00+09:00"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "pass_ttl_seconds", "1800"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "channel", "cache-waitroom-presale"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "return_urls.0", "https://shop.example.com/drop"),
				),
			},
			{
				ResourceName:            "hibira_waiting_room.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"starts_at"}, // imported in UTC
			},
			{
				// Open it, clear the start time and title.
				Config: config(`
  admit_per_minute = 600
  pass_ttl_seconds = 600`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "state", "open"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "admit_per_minute", "600"),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "starts_at", ""),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "title", ""),
					resource.TestCheckResourceAttr("hibira_waiting_room.test", "return_urls.#", "0"),
				),
			},
		},
	})
}
