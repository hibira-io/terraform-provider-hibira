package provider

import (
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccApprovalsWorkspace needs HIBIRA_ACC_APPROVALS=1 and
// HIBIRA_ACC_EMAIL (the token owner's verified email): each run makes a
// workspace, and Hibira allows one free workspace per email and does not
// delete them through the API yet.
func TestAccApprovalsWorkspace(t *testing.T) {
	if os.Getenv("HIBIRA_ACC_APPROVALS") == "" {
		t.Skip("set HIBIRA_ACC_APPROVALS=1 and HIBIRA_ACC_EMAIL to make an Approvals workspace")
	}
	me := os.Getenv("HIBIRA_ACC_EMAIL")
	config := func(name, members, groups string) string {
		return `
resource "hibira_approvals_workspace" "test" {
  name = "` + name + `"
` + members + groups + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: config("tf-acc-ops", `
  members = [
    { email = "`+me+`", role = "admin" },
    { email = "dba@example.com", role = "approver" },
  ]`, `
  groups = [{ slug = "dba", name = "DBAs", members = ["dba@example.com"] }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "plan", "free"),
					resource.TestCheckResourceAttrSet("hibira_approvals_workspace.test", "app_id"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "members.#", "2"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "groups.0.slug", "dba"),
				),
			},
			{
				ResourceName:      "hibira_approvals_workspace.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Rename, swap the approver, drop the groups.
				Config: config("tf-acc-ops-2", `
  members = [
    { email = "`+me+`", role = "admin" },
    { email = "lead@example.com", role = "viewer" },
  ]`, `
  groups = []`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "name", "tf-acc-ops-2"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "members.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("hibira_approvals_workspace.test", "members.*", map[string]string{"email": "lead@example.com", "role": "viewer"}),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.test", "groups.#", "0"),
				),
			},
			{
				// Leaving yourself out is refused before anything changes.
				Config: config("tf-acc-ops-2", `
  members = [{ email = "lead@example.com", role = "admin" }]`, ""),
				ExpectError: regexp.MustCompile(`members must include`),
			},
		},
	})
}
