package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// fakeLiveChat is a Live Chat workspace API in memory, enough for the
// resource: settings, the two secrets and webhooks. Its rules follow
// pusher-server: embed_origins and sso_only need the SSO secret.
type fakeLiveChat struct {
	mu                  sync.Mutex
	settings            client.LiveChatSettings
	identityN, ssoN     int // rotations
	webhooks            map[string]*client.LiveChatWebhook
	nextHook            int
	failEmbed           bool // refuse the next embed_origins change
	failSSO             bool // refuse the next SSO secret
	key                 string
	webhookSecretRotate int
	// API keys made over /api-keys (Hibira API token only).
	apiKeys map[string]client.LiveChatAPIKey
	nextKey int
	byToken bool // this request came with the Hibira token
}

// liveChatTestToken is the provider token the fake takes for ws_test.
const liveChatTestToken = "hkb_pat_livechat"

func newFakeLiveChat(t *testing.T) (*fakeLiveChat, *httptest.Server) {
	f := &fakeLiveChat{webhooks: map[string]*client.LiveChatWebhook{}, key: "lck_ws_test.0123456789abcdef0123",
		apiKeys:  map[string]client.LiveChatAPIKey{},
		settings: client.LiveChatSettings{Origins: []string{}, EmbedOrigins: []string{}}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeLiveChat) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "code": code})
	}
	auth := r.Header.Get("Authorization")
	f.byToken = auth == "Bearer "+liveChatTestToken
	if f.byToken && r.Method == "GET" && strings.TrimPrefix(r.URL.Path, "/v1") == "/workspaces" {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []client.LiveChatWorkspaceRow{
			{ID: "ws_test", Name: "Test", Plan: "team", Role: "owner", API: true},
			{ID: "ws_twin1", Name: "Twin", Plan: "team", Role: "owner", API: true},
			{ID: "ws_twin2", Name: "Twin", Plan: "team", Role: "admin", API: true},
		}})
		return
	}
	switch {
	case f.byToken && r.Header.Get(client.WorkspaceHeader) != "ws_test":
		fail(http.StatusNotFound, "workspace_not_found")
		return
	case f.byToken:
	case auth != "Bearer "+f.key:
		_, hasKey := f.apiKeys[strings.TrimPrefix(auth, "Bearer ")]
		if !hasKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid API key"}`))
			return
		}
	}
	ok := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	switch {
	case strings.HasPrefix(path, "/api-keys") && !f.byToken:
		fail(http.StatusForbidden, "portal_token_required")
	case r.Method == "GET" && path == "/api-keys":
		out := []client.LiveChatAPIKey{}
		for _, k := range f.apiKeys {
			k.Key = ""
			out = append(out, k)
		}
		ok(map[string]any{"data": out})
	case r.Method == "POST" && path == "/api-keys":
		var in struct{ Name, Scope string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.nextKey++
		k := client.LiveChatAPIKey{ID: fmt.Sprintf("key_%d", f.nextKey), Name: in.Name, Scope: in.Scope,
			Key: fmt.Sprintf("lck_ws_test.server%d", f.nextKey)}
		f.apiKeys[k.Key] = k
		w.WriteHeader(http.StatusCreated)
		ok(k)
	case r.Method == "DELETE" && strings.HasPrefix(path, "/api-keys/"):
		id := strings.TrimPrefix(path, "/api-keys/")
		for secret, k := range f.apiKeys {
			if k.ID == id {
				delete(f.apiKeys, secret)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		fail(404, "not_found")
	case r.Method == "GET" && path == "/workspace":
		ok(client.LiveChatWorkspace{ID: "ws_test", Name: "Test", Plan: "team"})
	case path == "/settings" && r.Method == "GET":
		ok(f.settings)
	case path == "/settings" && r.Method == "POST":
		var in client.LiveChatSettingsUpdate
		_ = json.NewDecoder(r.Body).Decode(&in)
		if (in.EmbedOrigins != nil && len(*in.EmbedOrigins) > 0 || in.SSOOnly != nil && *in.SSOOnly) && !f.settings.SSO {
			fail(400, "sso_off")
			return
		}
		if in.EmbedOrigins != nil && f.failEmbed {
			f.failEmbed = false
			fail(400, "invalid_embed_origin")
			return
		}
		if in.Origins != nil {
			f.settings.Origins = *in.Origins
		}
		if in.EmbedOrigins != nil {
			f.settings.EmbedOrigins = *in.EmbedOrigins
		}
		if in.SSOOnly != nil {
			f.settings.SSOOnly = *in.SSOOnly
		}
		ok(f.settings)
	case r.Method == "POST" && path == "/identity-secret":
		f.identityN++
		f.settings.IdentityVerification = true
		ok(map[string]string{"secret": fmt.Sprintf("lcid_%d", f.identityN)})
	case r.Method == "POST" && path == "/sso-secret":
		if f.failSSO {
			f.failSSO = false
			fail(500, "internal")
			return
		}
		f.ssoN++
		f.settings.SSO = true
		ok(map[string]string{"secret": fmt.Sprintf("lcsso_%d", f.ssoN)})
	case r.Method == "GET" && path == "/webhooks":
		out := []client.LiveChatWebhook{}
		for _, h := range f.webhooks {
			out = append(out, *h)
		}
		ok(map[string]any{"data": out})
	case r.Method == "POST" && path == "/webhooks":
		var in struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.nextHook++
		h := &client.LiveChatWebhook{ID: fmt.Sprintf("wh_%d", f.nextHook), URL: in.URL, Events: in.Events}
		f.webhooks[h.ID] = h
		out := *h
		out.Secret = "lcwh_" + h.ID
		ok(out)
	case strings.HasPrefix(path, "/webhooks/"):
		rest := strings.TrimPrefix(path, "/webhooks/")
		id, sub, _ := strings.Cut(rest, "/")
		h := f.webhooks[id]
		if h == nil {
			fail(404, "not_found")
			return
		}
		switch {
		case r.Method == "GET" && sub == "":
			ok(h)
		case r.Method == "POST" && sub == "":
			var in struct {
				URL      string   `json:"url"`
				Events   []string `json:"events"`
				Disabled bool     `json:"disabled"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			h.URL, h.Events, h.Disabled = in.URL, in.Events, in.Disabled
			ok(h)
		case r.Method == "POST" && sub == "secret":
			f.webhookSecretRotate++
			ok(map[string]string{"secret": fmt.Sprintf("lcwh_rot%d", f.webhookSecretRotate)})
		case r.Method == "DELETE" && sub == "":
			delete(f.webhooks, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			fail(404, "not_found")
		}
	default:
		fail(404, "not_found")
	}
}

func (f *fakeLiveChat) check(fn func(f *fakeLiveChat) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		return fn(f)
	}
}

func (f *fakeLiveChat) with(fn func(f *fakeLiveChat)) func() {
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		fn(f)
	}
}

func liveChatMockConfig(endpoint, key, rotation, origins string) string {
	return fmt.Sprintf(`
provider "hibira" {
  api_token = "hkb_pat_unused"
}

resource "hibira_livechat_workspace" "test" {
  api_key       = %q
  endpoint      = %q
  origins       = [%s]
  embed_origins = ["https://admin.example.com"]
  sso_only      = true
  rotation      = %q
  webhook_url   = "https://shop.example.com/hook"
}
`, key, endpoint, origins, rotation)
}

const lcRes = "hibira_livechat_workspace.test"

func counts(identity, sso int) func(*fakeLiveChat) error {
	return func(f *fakeLiveChat) error {
		if f.identityN != identity || f.ssoN != sso {
			return fmt.Errorf("rotations: identity %d sso %d, want %d %d", f.identityN, f.ssoN, identity, sso)
		}
		return nil
	}
}

func TestLiveChatWorkspaceMock(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	origins := `"https://shop.example.com", "capacitor://localhost"`
	cfg := func(rotation string) string { return liveChatMockConfig(srv.URL, f.key, rotation, origins) }
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: cfg("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "id", "ws_test"),
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_only", "true"),
					resource.TestCheckResourceAttr(lcRes, "embed_origins.#", "1"),
					resource.TestCheckResourceAttr(lcRes, "webhook_id", "wh_1"),
					resource.TestCheckResourceAttr(lcRes, "webhook_secret", "lcwh_wh_1"),
					f.check(counts(1, 1)),
				),
			},
			{
				// Rotation makes both secrets again; the webhook stays.
				Config: cfg("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_2"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_2"),
					resource.TestCheckResourceAttr(lcRes, "webhook_secret", "lcwh_wh_1"),
					f.check(counts(2, 2)),
				),
			},
			{
				// Visitor verification turned off in Live Chat: only that
				// secret is made again.
				PreConfig: f.with(func(f *fakeLiveChat) { f.settings.IdentityVerification = false }),
				Config:    cfg("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_3"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_2"),
					f.check(counts(3, 2)),
				),
			},
			{
				// A disabled endpoint is enabled again, with the same secret.
				PreConfig: f.with(func(f *fakeLiveChat) { f.webhooks["wh_1"].Disabled = true }),
				Config:    cfg("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "webhook_id", "wh_1"),
					resource.TestCheckResourceAttr(lcRes, "webhook_secret", "lcwh_wh_1"),
					f.check(func(f *fakeLiveChat) error {
						if f.webhooks["wh_1"].Disabled {
							return fmt.Errorf("webhook still disabled")
						}
						return nil
					}),
					f.check(counts(3, 2)),
				),
			},
			{
				// A deleted endpoint is created again.
				PreConfig: f.with(func(f *fakeLiveChat) { delete(f.webhooks, "wh_1") }),
				Config:    cfg("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "webhook_id", "wh_2"),
					resource.TestCheckResourceAttr(lcRes, "webhook_secret", "lcwh_wh_2"),
					f.check(counts(3, 2)),
				),
			},
		},
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.webhooks) != 0 {
		t.Fatalf("destroy left webhooks: %v", f.webhooks)
	}
}

// A failure after the secrets exist must not taint the resource: the
// next apply finishes the job without making the secrets again.
func TestLiveChatWorkspaceMockPartialCreate(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	f.failEmbed = true
	cfg := liveChatMockConfig(srv.URL, f.key, "1", `"https://shop.example.com"`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config:             cfg,
				ExpectNonEmptyPlan: true, // embed_origins and sso_only still to do
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_1"),
					// Not done in Live Chat yet; the state holds the planned values
					// and the next refresh shows the gap (ExpectNonEmptyPlan).
					f.check(func(f *fakeLiveChat) error {
						if len(f.settings.EmbedOrigins) != 0 || f.settings.SSOOnly {
							return fmt.Errorf("embed/sso_only applied: %+v", f.settings)
						}
						return nil
					}),
					f.check(counts(1, 1)),
				),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					f.check(func(f *fakeLiveChat) error {
						if len(f.settings.EmbedOrigins) != 1 || !f.settings.SSOOnly {
							return fmt.Errorf("embed/sso_only not applied: %+v", f.settings)
						}
						return nil
					}),
					f.check(counts(1, 1)),
				),
			},
		},
	})
}

// The SSO secret failing after the identity secret was made: the identity
// secret is kept, and the next apply makes only the SSO secret.
func TestLiveChatWorkspaceMockSSOFails(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	f.failSSO = true
	cfg := liveChatMockConfig(srv.URL, f.key, "1", `"https://shop.example.com"`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config:             cfg,
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", ""),
					f.check(counts(1, 0)),
				),
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_only", "true"),
					f.check(counts(1, 1)),
				),
			},
		},
	})
}

// Origins not in Live Chat's stored form are refused at plan time.
func TestLiveChatWorkspaceValidation(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	bad := map[string]string{
		"trailing slash":    liveChatMockConfig(srv.URL, f.key, "1", `"https://shop.example.com/"`),
		"uppercase":         liveChatMockConfig(srv.URL, f.key, "1", `"https://Shop.example.com"`),
		"http":              liveChatMockConfig(srv.URL, f.key, "1", `"http://shop.example.com"`),
		"webhook http":      strings.Replace(liveChatMockConfig(srv.URL, f.key, "1", `"https://a.example"`), "https://shop.example.com/hook", "http://shop.example.com/hook", 1),
		"webhook local":     strings.Replace(liveChatMockConfig(srv.URL, f.key, "1", `"https://a.example"`), "https://shop.example.com/hook", "https://hooks.local/x", 1),
		"no events":         strings.Replace(liveChatMockConfig(srv.URL, f.key, "1", `"https://a.example"`), `webhook_url`, "webhook_events = []\n  webhook_url", 1),
		"key and workspace": strings.Replace(liveChatMockConfig(srv.URL, f.key, "1", `"https://a.example"`), `webhook_url`, "workspace_id = \"ws_test\"\n  webhook_url", 1),
		"neither":           strings.Replace(liveChatMockConfig(srv.URL, f.key, "1", `"https://a.example"`), `api_key       = "`+f.key+`"`, "", 1),
		"bad workspace id":  strings.Replace(liveChatTokenConfig(srv.URL, "1", "admin"), `"ws_test"`, `"WS_Test"`, 1),
		"bad scope":         liveChatTokenConfig(srv.URL, "1", "owner"),
	}
	for name, cfg := range bad {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviders,
				Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)invalid|must|exactly one|no attribute`)}},
			})
		})
	}
}

func TestWebhookURLAllowed(t *testing.T) {
	for s, want := range map[string]bool{
		"https://shop.example.com/hook":        true,
		"https://hooks.example.com:8443/x?y=1": true,
		"https://8.8.8.8/hook":                 true,
		"http://shop.example.com/hook":         false,
		"https://localhost/hook":               false,
		"https://localhost./hook":              false,
		"https://a.localhost/hook":             false,
		"https://hooks.local/x":                false,
		"https://db.internal/x":                false,
		"https://127.0.0.1/x":                  false,
		"https://[::1]/x":                      false,
		"https://[::ffff:127.0.0.1]/x":         false,
		"https://0.0.0.0/x":                    false,
		"https://10.0.0.5/x":                   false,
		"https://192.168.1.2/x":                false,
		"https://169.254.169.254/latest":       false,
		"https://[fe80::1]/x":                  false,
		"https://2130706433/x":                 false,
		"https://0x7f000001/x":                 false,
		"https://user:pass@shop.example.com/x": false,
		"https://shop.example.com/x#frag":      false,
		" https://shop.example.com/x":          false,
		"https://localhost../x":                false,
		"https://shop..example.com/x":          false,
		"https://[::127.0.0.1]/x":              false,
		"https://[64:ff9b::7f00:1]/x":          false,
		"https://[64:ff9b::808:808]/x":         true,
		"https://[64:ff9b:1::a00:5]/x":         false,
		"https://255.255.255.255/x":            false,
		"https://240.0.0.1/x":                  false,
		"https://100.64.0.1/x":                 false,
		"https://[2001:4860:4860::8888]/x":     true,
		"https://shop.example.com./x":          true,
	} {
		u, err := url.Parse(s)
		if got := err == nil && webhookURLAllowed(s, u); got != want {
			t.Errorf("%q: allowed=%v, want %v", s, got, want)
		}
	}
}

func TestWebhookURLErrorHidesPassword(t *testing.T) {
	var resp validator.StringResponse
	webhookURLValidator{}.ValidateString(context.Background(), validator.StringRequest{
		ConfigValue: types.StringValue("https://bob:hunter2@shop.example.com/hook")}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("user info accepted")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Detail(), "hunter2") {
			t.Fatalf("password in diagnostics: %s", d.Detail())
		}
	}
}

// A rotation whose SSO step fails keeps the new identity secret and makes
// only the SSO secret on the next apply.
func TestLiveChatWorkspaceMockRotationSSOFails(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	cfg := func(rotation string) string {
		return liveChatMockConfig(srv.URL, f.key, rotation, `"https://shop.example.com"`)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{Config: cfg("1"), Check: f.check(counts(1, 1))},
			{
				PreConfig:   f.with(func(f *fakeLiveChat) { f.failSSO = true }),
				Config:      cfg("2"),
				ExpectError: regexp.MustCompile(`single sign-on secret failed`),
			},
			{
				Config: cfg("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_2"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_2"),
					f.check(counts(2, 2)), // identity made once for rotation 2, not twice
				),
			},
			{
				// Events narrowed in Live Chat (config leaves them unset =
				// all) come back to all on the next apply.
				PreConfig: f.with(func(f *fakeLiveChat) {
					for _, h := range f.webhooks {
						h.Events = []string{"message.created"}
					}
				}),
				Config: cfg("2"),
				Check: f.check(func(f *fakeLiveChat) error {
					for _, h := range f.webhooks {
						if len(h.Events) != len(liveChatWebhookEvents) {
							return fmt.Errorf("events %v", h.Events)
						}
					}
					return nil
				}),
			},
		},
	})
}

func TestAPIKeyErrorHidesKey(t *testing.T) {
	key := "lck_ws_abcd1234.supersecretvalue0123456789\n"
	var resp validator.StringResponse
	apiKeyValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(key)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("key with a line break accepted")
	}
	for _, d := range resp.Diagnostics {
		if strings.Contains(d.Detail(), "supersecretvalue") || strings.Contains(d.Summary(), "supersecretvalue") {
			t.Fatalf("error repeats the key: %s", d.Detail())
		}
	}
	resp = validator.StringResponse{}
	apiKeyValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue("lck_ws_abcd1234.supersecretvalue0123456789")}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("good key refused: %v", resp.Diagnostics)
	}
}

// keyIDs is the fake's API key IDs and scopes, sorted.
func (f *fakeLiveChat) keyIDs() string {
	var l []string
	for _, k := range f.apiKeys {
		l = append(l, k.ID+":"+k.Scope)
	}
	slices.Sort(l)
	return strings.Join(l, ",")
}

func liveChatTokenConfig(endpoint, rotation, scope string) string {
	return fmt.Sprintf(`
provider "hibira" {
  api_token = %q
}

resource "hibira_livechat_workspace" "test" {
  workspace_id     = "ws_test"
  endpoint         = %q
  origins          = ["https://shop.example.com"]
  embed_origins    = ["https://admin.example.com"]
  sso_only         = true
  rotation         = %q
  server_key_scope = %q
  webhook_url      = "https://shop.example.com/hook"
}
`, liveChatTestToken, endpoint, rotation, scope)
}

func keysAre(want string) func(*fakeLiveChat) error {
	return func(f *fakeLiveChat) error {
		if got := f.keyIDs(); got != want {
			return fmt.Errorf("api keys %q, want %q", got, want)
		}
		return nil
	}
}

// With workspace_id the provider's token sets the workspace up and makes
// the server's key, a new one on rotation, scope change or revocation.
func TestLiveChatWorkspaceMockToken(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config: liveChatTokenConfig(srv.URL, "1", "admin"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "id", "ws_test"),
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_1"),
					resource.TestCheckResourceAttr(lcRes, "sso_secret", "lcsso_1"),
					resource.TestCheckResourceAttr(lcRes, "webhook_id", "wh_1"),
					resource.TestCheckResourceAttr(lcRes, "server_key_id", "key_1"),
					resource.TestCheckResourceAttr(lcRes, "server_key", "lck_ws_test.server1"),
					f.check(keysAre("key_1:admin")),
					f.check(counts(1, 1)),
				),
			},
			{
				// Rotation makes the secrets and the key again; the old key goes.
				Config: liveChatTokenConfig(srv.URL, "2", "admin"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "identity_secret", "lcid_2"),
					resource.TestCheckResourceAttr(lcRes, "server_key", "lck_ws_test.server2"),
					f.check(keysAre("key_2:admin")),
					f.check(counts(2, 2)),
				),
			},
			{
				// Revoked in Live Chat: made again, and nothing else.
				PreConfig: f.with(func(f *fakeLiveChat) { delete(f.apiKeys, "lck_ws_test.server2") }),
				Config:    liveChatTokenConfig(srv.URL, "2", "admin"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "server_key_id", "key_3"),
					f.check(keysAre("key_3:admin")),
					f.check(counts(2, 2)),
				),
			},
			{
				// A new scope is a new key.
				Config: liveChatTokenConfig(srv.URL, "2", "write"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "server_key_id", "key_4"),
					f.check(keysAre("key_4:write")),
					f.check(counts(2, 2)),
				),
			},
		},
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.webhooks) != 0 || len(f.apiKeys) != 0 {
		t.Fatalf("destroy left webhooks %v, keys %v", f.webhooks, f.keyIDs())
	}
}

// A workspace the token's person doesn't manage is a clear error.
func TestLiveChatWorkspaceMockTokenNotAdmin(t *testing.T) {
	_, srv := newFakeLiveChat(t)
	cfg := strings.Replace(liveChatTokenConfig(srv.URL, "1", "admin"), `"ws_test"`, `"ws_other"`, 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{{
			Config:      cfg,
			ExpectError: regexp.MustCompile(`workspace_not_found`),
		}},
	})
}

// workspace_name is looked up once and its ID kept in state.
func TestLiveChatWorkspaceMockTokenByName(t *testing.T) {
	f, srv := newFakeLiveChat(t)
	cfg := func(name string) string {
		return strings.Replace(liveChatTokenConfig(srv.URL, "1", "admin"), `workspace_id     = "ws_test"`, `workspace_name   = "`+name+`"`, 1)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{Config: cfg("Nope"), ExpectError: regexp.MustCompile(`No Live Chat workspace with this name`)},
			{Config: cfg("Twin"), ExpectError: regexp.MustCompile(`ws_twin1, ws_twin2`)},
			{
				Config: cfg("Test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "workspace_id", "ws_test"),
					resource.TestCheckResourceAttr(lcRes, "id", "ws_test"),
					resource.TestCheckResourceAttr(lcRes, "server_key_id", "key_1"),
					f.check(keysAre("key_1:admin")),
					f.check(counts(1, 1)),
				),
			},
			{
				// Applied again: the ID in state is used, nothing changes.
				Config: cfg("Test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(lcRes, "server_key_id", "key_1"),
					f.check(counts(1, 1)),
				),
			},
		},
	})
}
