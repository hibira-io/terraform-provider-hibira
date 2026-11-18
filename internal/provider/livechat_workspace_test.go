package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccLiveChatWorkspace needs HIBIRA_ACC_LIVECHAT_KEY: an admin API key
// of a Live Chat workspace on the Team plan or above that may be changed
// (its secrets are made anew). HIBIRA_ACC_LIVECHAT_URL overrides Live
// Chat's address.
func TestAccLiveChatWorkspace(t *testing.T) {
	key := os.Getenv("HIBIRA_ACC_LIVECHAT_KEY")
	if key == "" {
		t.Skip("set HIBIRA_ACC_LIVECHAT_KEY to set up a Live Chat workspace")
	}
	t.Setenv("TF_VAR_livechat_key", key)
	endpoint := ""
	if u := os.Getenv("HIBIRA_ACC_LIVECHAT_URL"); u != "" {
		endpoint = `  endpoint = "` + u + `"` + "\n"
	}
	config := func(rotation, hook string) string {
		return `
variable "livechat_key" {
  type      = string
  sensitive = true
}

resource "hibira_livechat_workspace" "test" {
  api_key       = var.livechat_key
` + endpoint + `  origins       = ["https://tf-acc.example.com", "https://localhost"]
  embed_origins = ["https://tf-acc-admin.example.com"]
  sso_only      = true
  rotation      = "` + rotation + `"
  webhook_url   = "` + hook + `"
  webhook_events = ["conversation.created", "message.created"]
}`
	}
	const r = "hibira_livechat_workspace.test"
	var identity, webhook string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: config("1", "https://tf-acc.example.com/hook"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(r, "id", regexp.MustCompile(`^ws_`)),
					resource.TestMatchResourceAttr(r, "identity_secret", regexp.MustCompile(`^lcid_`)),
					resource.TestMatchResourceAttr(r, "sso_secret", regexp.MustCompile(`^lcsso_`)),
					resource.TestCheckResourceAttr(r, "sso_only", "true"),
					resource.TestCheckResourceAttr(r, "origins.#", "2"),
					resource.TestCheckResourceAttr(r, "embed_origins.#", "1"),
					resource.TestCheckResourceAttrSet(r, "webhook_id"),
					resource.TestCheckResourceAttrSet(r, "webhook_secret"),
					resource.TestCheckResourceAttrWith(r, "identity_secret", func(v string) error { identity = v; return nil }),
					resource.TestCheckResourceAttrWith(r, "webhook_id", func(v string) error { webhook = v; return nil }),
				),
			},
			{
				// Same config: no change, secrets kept.
				Config:   config("1", "https://tf-acc.example.com/hook"),
				PlanOnly: true,
			},
			{
				// Rotate the secrets and move the webhook.
				Config: config("2", "https://tf-acc.example.com/hook2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(r, "identity_secret", func(v string) error { return differs("identity_secret", identity, v) }),
					resource.TestCheckResourceAttrWith(r, "webhook_id", func(v string) error { return same("webhook_id", webhook, v) }),
					resource.TestCheckResourceAttr(r, "webhook_url", "https://tf-acc.example.com/hook2"),
				),
			},
		},
	})
}

func differs(name, old, v string) error {
	if v == "" || v == old {
		return fmt.Errorf("%s did not change", name)
	}
	return nil
}

func same(name, old, v string) error {
	if v != old {
		return fmt.Errorf("%s changed from %q to %q", name, old, v)
	}
	return nil
}
