package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// fakeHibira serves the parts of the customer API (/api/v1) and of Live
// Chat's REST API (/v1) the onboarding resources use, for one token. Its
// rules follow pusher-server: Live Chat needs the Hibira-Workspace
// header, deleting an Approvals workspace needs ?confirm=<name>.
type fakeHibira struct {
	mu        sync.Mutex
	token     string
	ws        client.ApprovalsWorkspace
	hooks     []client.ApprovalsWebhook
	teams     map[string]*client.LiveChatTeam
	replies   map[string]*client.SavedReply
	sources   map[string]*client.WebSource
	hours     client.BusinessHours
	n         int
	deletedWS bool
	ruleIDs   []string
}

func newFakeHibira(t *testing.T) (*fakeHibira, *httptest.Server) {
	f := &fakeHibira{token: "hkb_pat_test", teams: map[string]*client.LiveChatTeam{}, replies: map[string]*client.SavedReply{},
		sources: map[string]*client.WebSource{}, hours: client.BusinessHours{TimeZone: "UTC", Closed: []string{}}}
	for i := range f.hours.Week {
		f.hours.Week[i] = client.DayHours{Off: true, From: "09:00", To: "18:00"}
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeHibira) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s_%d", prefix, f.n)
}

func (f *fakeHibira) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "code": code})
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		fail(401, "unauthorized")
		return
	}
	ok := func(status int, v any) {
		w.WriteHeader(status)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	raw, _ := json.Marshal(body)
	into := func(v any) { _ = json.Unmarshal(raw, v) }
	strs := func(k string) []string {
		out := []string{}
		for _, x := range body[k].([]any) {
			out = append(out, x.(string))
		}
		return out
	}
	p := r.URL.Path
	if lc, isLC := strings.CutPrefix(p, "/v1"); isLC {
		if r.Header.Get(client.WorkspaceHeader) != "ws_1" {
			fail(404, "workspace_not_found")
			return
		}
		f.liveChat(r.Method, lc, body, into, strs, ok, fail)
		return
	}
	p = strings.TrimPrefix(p, "/api/v1/approvals/workspaces")
	switch {
	case r.Method == "POST" && p == "":
		f.ws = client.ApprovalsWorkspace{ID: "apw_1", Name: body["name"].(string), Plan: "team", AppID: "app_1", You: "me@example.com",
			Members: []client.ApprovalsMember{{Email: "me@example.com", Role: "admin"}}, Groups: []client.ApproverGroup{},
			Rules: []client.ApprovalRule{}, FollowUp: &client.ApprovalsFollowUp{}}
		ok(201, f.ws)
	case f.ws.ID == "" || f.deletedWS:
		fail(404, "not_found")
	case r.Method == "GET" && p == "/apw_1":
		ok(200, f.ws)
	case r.Method == "DELETE" && p == "/apw_1":
		if r.URL.Query().Get("confirm") != f.ws.Name {
			fail(400, "invalid_argument")
			return
		}
		f.deletedWS = true
		ok(204, nil)
	case r.Method == "PUT" && p == "/apw_1/rules":
		var in struct{ Rules []client.ApprovalRule }
		into(&in)
		f.ruleIDs = nil
		for i := range in.Rules {
			if in.Rules[i].ID == "" {
				in.Rules[i].ID = f.id("rul")
			}
			f.ruleIDs = append(f.ruleIDs, in.Rules[i].ID)
		}
		f.ws.Rules = in.Rules
		ok(200, f.ws)
	case r.Method == "PUT" && p == "/apw_1/groups":
		var in struct{ Groups []client.ApproverGroup }
		into(&in)
		f.ws.Groups = in.Groups
		ok(200, f.ws)
	case r.Method == "PUT" && p == "/apw_1/follow-up":
		var in client.ApprovalsFollowUp
		into(&in)
		f.ws.FollowUp = &in
		ok(200, f.ws)
	case r.Method == "GET" && p == "/apw_1/webhooks":
		ok(200, map[string]any{"webhooks": f.hooks})
	case r.Method == "POST" && p == "/apw_1/webhooks":
		h := client.ApprovalsWebhook{ID: f.id("wh"), URL: body["url"].(string), Events: strs("events"), Disabled: body["disabled"] == true}
		f.hooks = append(f.hooks, h)
		h.Secret = "aprwh_" + h.ID
		ok(201, h)
	case strings.HasPrefix(p, "/apw_1/webhooks/"):
		id := strings.TrimPrefix(p, "/apw_1/webhooks/")
		i := slices.IndexFunc(f.hooks, func(h client.ApprovalsWebhook) bool { return h.ID == id })
		if i < 0 {
			fail(404, "not_found")
			return
		}
		if r.Method == "DELETE" {
			f.hooks = slices.Delete(f.hooks, i, i+1)
			ok(204, nil)
			return
		}
		f.hooks[i].URL, f.hooks[i].Events, f.hooks[i].Disabled = body["url"].(string), strs("events"), body["disabled"] == true
		ok(200, f.hooks[i])
	default:
		fail(404, "not_found")
	}
}

func (f *fakeHibira) liveChat(method, p string, body map[string]any, into func(any), strs func(string) []string,
	ok func(int, any), fail func(int, string)) {
	switch {
	case method == "GET" && p == "/agents":
		ok(200, map[string]any{"data": []client.LiveChatAgent{{ID: "ag_1", Email: "Mei@example.com"}, {ID: "ag_2", Email: "kai@example.com"}}})
	case method == "POST" && p == "/teams":
		t := &client.LiveChatTeam{ID: f.id("team"), Name: body["name"].(string), AgentIDs: strs("agent_ids")}
		f.teams[t.ID] = t
		ok(201, t)
	case strings.HasPrefix(p, "/teams/"):
		t := f.teams[strings.TrimPrefix(p, "/teams/")]
		switch {
		case t == nil:
			fail(404, "not_found")
		case method == "GET":
			ok(200, t)
		case method == "DELETE":
			delete(f.teams, t.ID)
			ok(204, nil)
		default:
			t.Name, t.AgentIDs = body["name"].(string), strs("agent_ids")
			ok(200, t)
		}
	case p == "/hours" && method == "GET":
		ok(200, f.hours)
	case p == "/hours":
		f.hours = client.BusinessHours{}
		into(&f.hours)
		for i, d := range f.hours.Week {
			if d.Off {
				f.hours.Week[i] = client.DayHours{Off: true, From: "09:00", To: "18:00"}
			}
		}
		ok(200, f.hours)
	case method == "POST" && p == "/saved-replies":
		s := &client.SavedReply{ID: f.id("sr")}
		into(s)
		s.ID = fmt.Sprintf("sr_%d", f.n)
		f.replies[s.ID] = s
		ok(201, s)
	case strings.HasPrefix(p, "/saved-replies/"):
		s := f.replies[strings.TrimPrefix(p, "/saved-replies/")]
		switch {
		case s == nil:
			fail(404, "not_found")
		case method == "GET":
			ok(200, s)
		case method == "DELETE":
			delete(f.replies, s.ID)
			ok(204, nil)
		default:
			id := s.ID
			into(s)
			s.ID = id
			ok(200, s)
		}
	case method == "POST" && p == "/knowledge/sources":
		s := &client.WebSource{ID: f.id("src"), URL: body["url"].(string), Status: "syncing"}
		f.sources[s.ID] = s
		ok(201, s)
	case strings.HasPrefix(p, "/knowledge/sources/"):
		s := f.sources[strings.TrimPrefix(p, "/knowledge/sources/")]
		switch {
		case s == nil:
			fail(404, "not_found")
		case method == "DELETE":
			delete(f.sources, s.ID)
			ok(204, nil)
		default:
			s.Status, s.Found = "ok", s.URL+"llms.txt"
			ok(200, s)
		}
	default:
		fail(404, "not_found")
	}
}

func onboardingConfig(url, rules, team, hours, reply string) string {
	return fmt.Sprintf(`
provider "hibira" {
  api_token         = "hkb_pat_test"
  endpoint          = %[1]q
  livechat_endpoint = %[1]q
}

resource "hibira_approvals_workspace" "ops" {
  name = "Ops"
%[2]s
}

resource "hibira_approvals_webhook" "ops" {
  workspace_id = hibira_approvals_workspace.ops.id
  url          = "https://hooks.example.com/approvals"
  events       = ["approval.decided"]
}

resource "hibira_livechat_team" "support" {
  workspace_id = "ws_1"
  name         = "Support"
  agents       = [%[3]s]
}

resource "hibira_livechat_business_hours" "main" {
  workspace_id = "ws_1"
  time_zone    = "Asia/Taipei"
  days = {
%[4]s
  }
  closed_dates = ["2026-12-25"]
}

resource "hibira_livechat_saved_reply" "refund" {
  workspace_id = "ws_1"
  title        = "Refunds"
  shortcut     = "refund"
  text         = %[5]q
}

resource "hibira_livechat_knowledge_source" "docs" {
  workspace_id = "ws_1"
  url          = "https://shop.example.com/"
}
`, url, rules, team, hours, reply)
}

func TestOnboardingResourcesMock(t *testing.T) {
	f, srv := newFakeHibira(t)
	rules := `
  rules = [{ name = "Reads", titles = ["SELECT *"], except = ["*DROP*"] }]
  follow_up = { remind_minutes = 10, escalate_minutes = 30, escalate_to = ["me@example.com"] }`
	weekdays := `
    monday  = { from = "09:00", to = "18:00" }
    tuesday = { from = "09:00", to = "18:00" }`
	var first string
	keep := func(n int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.ruleIDs) != n || first != "" && (f.ruleIDs[0] != first || f.ruleIDs[1] == first) {
				return fmt.Errorf("rule IDs %v, first was %s", f.ruleIDs, first)
			}
			first = f.ruleIDs[0]
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: onboardingConfig(srv.URL, rules, `"mei@example.com"`, weekdays, "We refund within 14 days."),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_approvals_workspace.ops", "rules.0.enabled", "true"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.ops", "rules.0.agents.#", "0"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.ops", "follow_up.remind_minutes", "10"),
					resource.TestMatchResourceAttr("hibira_approvals_webhook.ops", "secret", regexp.MustCompile(`^aprwh_wh_\d+$`)),
					resource.TestCheckResourceAttr("hibira_approvals_webhook.ops", "enabled", "true"),
					resource.TestCheckResourceAttr("hibira_livechat_team.support", "agents.0", "mei@example.com"),
					resource.TestCheckResourceAttr("hibira_livechat_business_hours.main", "days.%", "2"),
					resource.TestCheckResourceAttr("hibira_livechat_business_hours.main", "enabled", "true"),
					resource.TestCheckResourceAttr("hibira_livechat_saved_reply.refund", "shortcut", "refund"),
					resource.TestCheckResourceAttrSet("hibira_livechat_knowledge_source.docs", "id"),
					keep(1),
				),
			},
			{
				// A changed rule keeps its ID; a new one gets its own.
				Config: onboardingConfig(srv.URL, `
  rules = [
    { name = "Reads", titles = ["SELECT *", "SHOW *"], except = ["*DROP*"] },
    { name = "Staging", agents = ["* · staging"], enabled = false },
  ]`, `"mei@example.com", "kai@example.com"`, weekdays+`
    saturday = { from = "10:00", to = "14:00" }`, "We refund within 30 days."),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("hibira_approvals_workspace.ops", "rules.1.enabled", "false"),
					resource.TestCheckResourceAttr("hibira_approvals_workspace.ops", "follow_up.remind_minutes", "10"),
					resource.TestCheckResourceAttr("hibira_livechat_team.support", "agents.#", "2"),
					resource.TestCheckResourceAttr("hibira_livechat_business_hours.main", "days.saturday.from", "10:00"),
					resource.TestCheckResourceAttr("hibira_livechat_saved_reply.refund", "text", "We refund within 30 days."),
					keep(2),
				),
			},
			{ResourceName: "hibira_livechat_team.support", ImportState: true, ImportStateVerify: true, ImportStateIdFunc: childID("hibira_livechat_team.support")},
			{ResourceName: "hibira_livechat_saved_reply.refund", ImportState: true, ImportStateVerify: true, ImportStateIdFunc: childID("hibira_livechat_saved_reply.refund")},
			{ResourceName: "hibira_livechat_business_hours.main", ImportState: true, ImportStateVerify: true, ImportStateId: "ws_1"},
			{ResourceName: "hibira_approvals_webhook.ops", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret"},
				ImportStateIdFunc: childID("hibira_approvals_webhook.ops")},
		},
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.deletedWS || len(f.hooks) != 0 || len(f.teams) != 0 || len(f.replies) != 0 || len(f.sources) != 0 || f.hours.On {
				return fmt.Errorf("left behind: ws deleted %v, hooks %d, teams %d, replies %d, sources %d, hours on %v",
					f.deletedWS, len(f.hooks), len(f.teams), len(f.replies), len(f.sources), f.hours.On)
			}
			return nil
		},
	})
}

// childID is `<workspace_id>/<id>` of resource name.
func childID(name string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		a := s.RootModule().Resources[name].Primary.Attributes
		return a["workspace_id"] + "/" + a["id"], nil
	}
}
