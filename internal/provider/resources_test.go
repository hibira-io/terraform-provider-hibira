package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccApp(t *testing.T) {
	var firstSecret string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "hibira_app" "test" {
  name            = "tf-acc"
  client_events   = true
  allowed_origins = ["https://example.com"]
}

resource "hibira_webhook" "presence" {
  app_id = hibira_app.test.id
  url    = "https://example.com/hooks/presence"
  events = ["member_added", "member_removed"]
}

data "hibira_app" "test" {
  id         = hibira_app.test.id
  depends_on = [hibira_webhook.presence]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_app.test", "name", "tf-acc"),
					resource.TestCheckResourceAttr("hibira_app.test", "enabled", "true"),
					resource.TestCheckResourceAttr("hibira_app.test", "client_events", "true"),
					resource.TestCheckTypeSetElemAttr("hibira_app.test", "allowed_origins.*", "https://example.com"),
					resource.TestMatchResourceAttr("hibira_app.test", "key", regexp.MustCompile(`.+`)),
					resource.TestMatchResourceAttr("hibira_app.test", "secret", regexp.MustCompile(`.+`)),
					resource.TestCheckResourceAttr("hibira_webhook.presence", "events.#", "2"),
					resource.TestCheckResourceAttrPair("data.hibira_app.test", "key", "hibira_app.test", "key"),
					func(s *terraform.State) error {
						firstSecret = s.RootModule().Resources["hibira_app.test"].Primary.Attributes["secret"]
						return nil
					},
				),
			},
			{
				ResourceName:            "hibira_app.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
			},
			{
				ResourceName:      "hibira_webhook.presence",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Rename, reset settings to defaults, change the webhook's events and rotate
				// the secret.
				Config: `
resource "hibira_app" "test" {
  name               = "tf-acc-renamed"
  subscription_count = true
  secret_rotation    = "1"
}

resource "hibira_webhook" "presence" {
  app_id = hibira_app.test.id
  url    = "https://example.com/hooks/presence"
  events = ["channel_occupied"]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_app.test", "name", "tf-acc-renamed"),
					resource.TestCheckResourceAttr("hibira_app.test", "client_events", "false"),
					resource.TestCheckResourceAttr("hibira_app.test", "subscription_count", "true"),
					resource.TestCheckResourceAttr("hibira_app.test", "allowed_origins.#", "0"),
					resource.TestCheckResourceAttr("hibira_webhook.presence", "events.#", "1"),
					func(s *terraform.State) error {
						if s.RootModule().Resources["hibira_app.test"].Primary.Attributes["secret"] == firstSecret {
							return errorf("secret was not rotated")
						}
						return nil
					},
				),
			},
			{
				// Dropping the webhook resource removes the endpoint.
				Config: `
resource "hibira_app" "test" {
  name               = "tf-acc-renamed"
  subscription_count = true
  secret_rotation    = "1"
}

data "hibira_apps" "all" {
  depends_on = [hibira_app.test]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs("data.hibira_apps.all", "apps.*", map[string]string{"name": "tf-acc-renamed"}),
					testAppWebhooks("hibira_app.test", 0),
				),
			},
		},
	})
}
