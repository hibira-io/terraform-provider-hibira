package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                     = (*liveChatWorkspaceResource)(nil)
	_ resource.ResourceWithConfigure        = (*liveChatWorkspaceResource)(nil)
	_ resource.ResourceWithModifyPlan       = (*liveChatWorkspaceResource)(nil)
	_ resource.ResourceWithConfigValidators = (*liveChatWorkspaceResource)(nil)
)

// liveChatKeyScopes are the scopes a Live Chat API key can have.
var liveChatKeyScopes = []string{"read", "write", "admin"}

// serverKeyName names the API key the resource makes for the site's server.
const serverKeyName = "terraform: server"

// liveChatWebhookEvents are the event types Live Chat sends.
var liveChatWebhookEvents = []string{"conversation.created", "conversation.assigned", "conversation.closed",
	"conversation.reopened", "conversation.rated", "conversation.team_changed", "message.created"}

var (
	liveChatKey = regexp.MustCompile(`^lck_ws_[A-Za-z0-9_-]{4,40}\.[A-Za-z0-9_-]{16,200}$`)
	// A workspace ID as Live Chat makes them (ValidWorkspaceID).
	liveChatWorkspaceID = regexp.MustCompile(`^ws_[a-z0-9_-]{3,37}$`)
	// Origins in the form Live Chat stores them (lowercase scheme and host,
	// no path or trailing slash), so what is planned is what is read back.
	liveChatOrigin    = regexp.MustCompile(`^(https://[a-z0-9.-]+(:[0-9]{1,5})?|http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?)$`)
	liveChatAppOrigin = regexp.MustCompile(`^(capacitor|ionic)://localhost$`)
	// Live Chat itself: https, or http on this machine (tests).
	liveChatEndpoint = regexp.MustCompile(`^(https://[^/\s]+|http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?)/?$`)
)

var numericLabel = regexp.MustCompile(`^(0x[0-9a-f]*|[0-9]+)$`)

const originHelp = "must be an origin as Live Chat stores it: lowercase, no path or trailing slash, like https://shop.example.com"

func newLiveChatWorkspaceResource() resource.Resource { return &liveChatWorkspaceResource{} }

// liveChatWorkspaceResource sets up an existing Hibira Live Chat workspace
// for a site, from one admin API key or from the provider's Hibira API
// token: the widget's sites, the console origins that may frame the inbox,
// single sign-on only, the verified-visitor and single sign-on secrets, one
// webhook endpoint and, with the token, the API key the site's server uses.
type liveChatWorkspaceResource struct {
	userAgent string
	api       *client.Client // the provider's, for workspace_id
}

type liveChatWorkspaceModel struct {
	ID             types.String `tfsdk:"id"`
	APIKey         types.String `tfsdk:"api_key"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	WorkspaceName  types.String `tfsdk:"workspace_name"`
	Endpoint       types.String `tfsdk:"endpoint"`
	Name           types.String `tfsdk:"name"`
	Plan           types.String `tfsdk:"plan"`
	Origins        types.Set    `tfsdk:"origins"`
	EmbedOrigins   types.Set    `tfsdk:"embed_origins"`
	SSOOnly        types.Bool   `tfsdk:"sso_only"`
	IdentitySecret types.String `tfsdk:"identity_secret"`
	SSOSecret      types.String `tfsdk:"sso_secret"`
	Rotation       types.String `tfsdk:"rotation"`
	WebhookURL     types.String `tfsdk:"webhook_url"`
	WebhookEvents  types.Set    `tfsdk:"webhook_events"`
	WebhookID      types.String `tfsdk:"webhook_id"`
	WebhookSecret  types.String `tfsdk:"webhook_secret"`
	ServerKeyScope types.String `tfsdk:"server_key_scope"`
	ServerKeyID    types.String `tfsdk:"server_key_id"`
	ServerKey      types.String `tfsdk:"server_key"`
}

// byToken reports whether m uses the provider's token (workspace_id or
// workspace_name).
func (m liveChatWorkspaceModel) byToken() bool {
	return m.WorkspaceID.ValueString() != "" || m.WorkspaceName.ValueString() != ""
}

// sameWorkspace reports whether plan keeps state's workspace_id (unknown:
// workspace_name is to be looked up again).
func sameWorkspace(plan, state liveChatWorkspaceModel) bool {
	return !plan.WorkspaceID.IsUnknown() && plan.WorkspaceID.ValueString() == state.WorkspaceID.ValueString()
}

func (r *liveChatWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_workspace"
}

func (r *liveChatWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets up an existing [Hibira Live Chat](https://chat.hibira.io) workspace for a site, either with " +
			"the provider's `api_token` and the workspace's `workspace_id`, or with one of the workspace's **admin** API keys " +
			"(`api_key`, made in Live Chat › Settings › API & webhooks). Create the workspace and choose its plan in Live Chat " +
			"first: the API, webhooks, visitor verification and the embedded inbox need the Team plan or above.\n\n" +
			"**With `workspace_id`** the provider's token (read, write and delete, since secrets are rotated and keys " +
			"revoked) acts as its Hibira account's confirmed email, which must be an owner or admin of the workspace in " +
			"Live Chat (Team) with its invite accepted; in a workspace held to single sign-on or SCIM, the owner. The resource then also makes the API key the site's server runs on (`server_key`), makes a " +
			"new one when `rotation` or `server_key_scope` changes (revoking the old one), and revokes it on destroy. " +
			"With `api_key` there is no `server_key`: the site uses its own key.\n\n" +
			"Creating this resource sets the widget's sites and the webhook endpoint (an endpoint with the same URL is adopted " +
			"and gets a new secret), then makes a new verified-visitor secret and a new single sign-on secret, then sets the " +
			"framing origins and `sso_only`, which need the single sign-on secret. An earlier verified-visitor secret stops " +
			"working at once, an earlier single sign-on secret after a day. Once the secrets are made, a later step that fails " +
			"is a warning, not an error: the resource is saved with what was done and the next apply finishes it, so a typo " +
			"never makes the secrets again. Destroying the resource deletes the webhook endpoint and leaves the workspace, its " +
			"settings and its secrets as they are.\n\n" +
			"**State.** The state holds `api_key` or `server_key` and the three secrets in plain text, like every secret a Terraform provider " +
			"returns: keep it in an encrypted backend (for example S3 with server-side encryption and restricted access) and " +
			"never commit it.\n\n" +
			"**Outside changes.** Turning visitor verification or single sign-on off in Live Chat makes the next apply create " +
			"that secret again (only that one). A disabled or deleted webhook endpoint is enabled or created again. When " +
			"Live Chat refuses the key in state (revoked), the refresh is skipped with a warning; set the new key in `api_key`. " +
			"A `server_key` revoked in Live Chat is made again on the next apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`ws_...`).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "An admin API key of the workspace (`lck_ws_....`); set this or `workspace_id`. A key of " +
					"another workspace sets that workspace up from the start (new secrets and webhook); the old workspace's " +
					"webhook endpoint is deleted when the old key still works.",
				Optional:   true,
				Sensitive:  true,
				Validators: []validator.String{apiKeyValidator{}},
			},
			"workspace_id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`ws_...`, in Live Chat's address bar and settings), used with the " +
					"provider's `api_token`; set this, `workspace_name` or `api_key`. With `workspace_name` it is looked up " +
					"and kept in state. Another workspace is set up from the start, as with a key of another workspace.",
				Optional: true, Computed: true,
				PlanModifiers: keep,
				Validators:    []validator.String{stringvalidator.RegexMatches(liveChatWorkspaceID, "must be a Live Chat workspace ID (ws_...)")},
			},
			"workspace_name": schema.StringAttribute{
				MarkdownDescription: "Instead of `workspace_id`: the workspace's name in Live Chat. On create (or when it " +
					"changes) the provider's token lists the workspaces its account is in (`GET /v1/workspaces`) and takes the " +
					"one with this name, which must be unique; its ID is then kept in `workspace_id`, so renaming the " +
					"workspace in Live Chat later changes nothing here.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)},
			},
			"server_key_scope": schema.StringAttribute{
				MarkdownDescription: "With `workspace_id`: the scope of `server_key`, one of `read`, `write`, `admin` (default " +
					"`admin`, which a server that also sets the workspace up needs; `write` is enough to answer chats). Changing " +
					"it makes a new key and revokes the old one at once, with no overlap: roll the new key out to the server in " +
					"the same release.",
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("admin"),
				Validators: []validator.String{stringvalidator.OneOf(liveChatKeyScopes...)},
			},
			"server_key_id": schema.StringAttribute{
				MarkdownDescription: "The ID of `server_key` (`key_...`); empty with `api_key`.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"server_key": schema.StringAttribute{
				MarkdownDescription: "With `workspace_id`: an API key of the workspace (`lck_ws_...`) for the site's server, " +
					"named \"" + serverKeyName + "\" in Live Chat. A `rotation`, a scope change or a switch to `api_key` revokes the " +
					"old key at once (no overlap). Empty with `api_key`.",
				Computed: true, Sensitive: true,
				PlanModifiers: keep,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Live Chat's address (https). Defaults to `" + client.DefaultLiveChatEndpoint + "`. " +
					"Changing it replaces the resource.",
				Optional: true, Computed: true,
				Default:       stringdefault.StaticString(client.DefaultLiveChatEndpoint),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(liveChatEndpoint, "must be an https:// URL")},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The workspace's name (changed in Live Chat).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"plan": schema.StringAttribute{
				MarkdownDescription: "The workspace's plan (changed in Live Chat).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"origins": schema.SetAttribute{
				MarkdownDescription: "The sites the widget may run on, like `https://shop.example.com` (lowercase, no path or " +
					"trailing slash). Apps built with Capacitor add `https://localhost` (Android) and `capacitor://localhost` " +
					"(iOS). Unset: managed in Live Chat and only read here.",
				ElementType: types.StringType,
				Optional:    true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				Validators: []validator.Set{setvalidator.ValueStringsAre(stringvalidator.Any(
					stringvalidator.RegexMatches(liveChatOrigin, originHelp),
					stringvalidator.RegexMatches(liveChatAppOrigin, "or an app origin (capacitor://localhost, ionic://localhost)")))},
			},
			"embed_origins": schema.SetAttribute{
				MarkdownDescription: "The origins that may show the Live Chat inbox in a frame (with single sign-on), like " +
					"`https://admin.example.com`. Unset: managed in Live Chat and only read here.",
				ElementType: types.StringType,
				Optional:    true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				Validators:    []validator.Set{setvalidator.ValueStringsAre(stringvalidator.RegexMatches(liveChatOrigin, originHelp))},
			},
			"sso_only": schema.BoolAttribute{
				MarkdownDescription: "Agents sign in only through the site's single sign-on (the workspace owner keeps email sign-in). " +
					"Unset: managed in Live Chat and only read here.",
				Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"identity_secret": schema.StringAttribute{
				MarkdownDescription: "The verified-visitor secret (`lcid_...`): the site signs a signed-in user's ID with it " +
					"(`user_hash` = hex HMAC-SHA256 of the user ID).",
				Computed: true, Sensitive: true,
				PlanModifiers: keep,
			},
			"sso_secret": schema.StringAttribute{
				MarkdownDescription: "The single sign-on secret (`lcsso_...`) that signs agents' sign-in tokens (HS256 JWT).",
				Computed:            true, Sensitive: true,
				PlanModifiers: keep,
			},
			"rotation": schema.StringAttribute{
				MarkdownDescription: "Any value; changing it makes new `identity_secret` and `sso_secret` (the old single sign-on " +
					"secret keeps working for a day, the old verified-visitor secret stops at once).",
				Optional: true,
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "The https URL Live Chat posts conversation events to (not localhost, `.local`, `.internal` or a private, " +
					"loopback or link-local IP address). " +
					"An endpoint already there with this URL is adopted (and gets a new secret). Unset: no endpoint.",
				Optional:   true,
				Validators: []validator.String{webhookURLValidator{}},
			},
			"webhook_events": schema.SetAttribute{
				MarkdownDescription: "The event types to send: " + strings.Join(quoted(liveChatWebhookEvents), ", ") + ". Unset: all of them.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators: []validator.Set{setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(liveChatWebhookEvents...))},
			},
			"webhook_id": schema.StringAttribute{
				MarkdownDescription: "The webhook endpoint's ID.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"webhook_secret": schema.StringAttribute{
				MarkdownDescription: "The webhook signing secret (`lcwh_...`): `Hibira-Signature: t=<unix>,v1=<hex HMAC-SHA256 of \"<t>.<body>\">`.",
				Computed:            true, Sensitive: true,
				PlanModifiers: keep,
			},
		},
	}
}

// webhookURLValidator accepts what Live Chat accepts for an endpoint:
// https, a host, no user info, not a local name.
// apiKeyValidator checks the API key's shape without repeating it:
// stringvalidator.RegexMatches puts the value in its error, which would
// print a mistyped (or newline-padded) real key in CI logs.
type apiKeyValidator struct{}

func (apiKeyValidator) Description(context.Context) string {
	return "must be a Live Chat API key (lck_ws_...)"
}
func (v apiKeyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v apiKeyValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || liveChatKey.MatchString(req.ConfigValue.ValueString()) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid Live Chat API key",
		v.Description(ctx)+" (the value is not shown; check for spaces or a line break around it)")
}

type webhookURLValidator struct{}

func (webhookURLValidator) Description(context.Context) string {
	return "must be an https:// URL whose host is not localhost, .local, .internal or a private, loopback or link-local address"
}
func (v webhookURLValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v webhookURLValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	u, err := url.Parse(s)
	shown := "(not a URL)"
	if err == nil {
		shown = u.Redacted() // never repeat a password from user info
	}
	if err != nil || !webhookURLAllowed(s, u) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid webhook URL", v.Description(ctx)+", got "+shown)
	}
}

// webhookURLAllowed is pusher-server's cleanWebhookURL (https, a host, no
// user info, no local names), plus what its client refuses at connect
// time: IP literals that are not public (including IPv4 inside IPv6), and
// hosts that are only digits (2130706433 is 127.0.0.1). This is a plan-time
// check of the text only: a DNS name that resolves to a private address
// passes here, and pusher-server's check when it connects is the guard.
func webhookURLAllowed(s string, u *url.URL) bool {
	if s != strings.TrimSpace(s) || len(s) > 2000 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if strings.HasSuffix(h, ".") {
		h = h[:len(h)-1] // one root dot is fine; "localhost.." is not a host
	}
	if h == "" || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") ||
		h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return false
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return publicIP(ip)
	}
	// A numeric last label is never a domain name: it is some decimal,
	// octal or hex form of an IPv4 address (2130706433, 0x7f000001).
	return !numericLabel.MatchString(h[strings.LastIndex(h, ".")+1:])
}

// nonPublicV4 are IPv4 ranges netip has no predicate for: "this network",
// shared address space (carrier NAT), IETF protocol assignments,
// benchmarking, and the reserved 240/4 (which holds 255.255.255.255).
var nonPublicV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// IPv6 prefixes whose last 32 bits are an IPv4 address: IPv4-compatible
// (::a.b.c.d, deprecated) and the well-known NAT64 prefix (RFC 6052).
var embedsV4 = []netip.Prefix{
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// localNAT64 is the local-use NAT64 prefix (RFC 8215); where the IPv4
// address sits inside it depends on the site, so it is refused whole.
var localNAT64 = netip.MustParsePrefix("64:ff9b:1::/48")

// publicIP reports whether ip is an address a webhook may be sent to.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range embedsV4 {
		if ip.Is6() && p.Contains(ip) {
			b := ip.As16()
			ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
			break
		}
	}
	if localNAT64.Contains(ip) || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublicV4 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ConfigValidators: one way in, api_key or workspace_id.
func (r *liveChatWorkspaceResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("api_key"), path.MatchRoot("workspace_id"), path.MatchRoot("workspace_name")),
	}
}

// resolve fills m's workspace_id: "" with api_key, else (when unknown)
// the one workspace named workspace_name that the provider's token's
// account is in.
func (r *liveChatWorkspaceResource) resolve(ctx context.Context, m *liveChatWorkspaceModel, diags *diag.Diagnostics) bool {
	if !m.byToken() {
		m.WorkspaceID = types.StringValue("")
		return true
	}
	if known(m.WorkspaceID) && m.WorkspaceID.ValueString() != "" {
		return true
	}
	if r.api == nil {
		diags.AddError("Provider not configured", "workspace_name needs the provider's api_token.")
		return false
	}
	name := m.WorkspaceName.ValueString()
	list, err := r.api.LiveChat(m.Endpoint.ValueString(), "").Workspaces(ctx)
	if err != nil {
		diags.AddError("Listing Live Chat workspaces failed", err.Error())
		return false
	}
	var ids []string
	for _, w := range list {
		if w.Name == name && !w.Disabled {
			ids = append(ids, w.ID)
		}
	}
	switch len(ids) {
	case 1:
		m.WorkspaceID = types.StringValue(ids[0])
		return true
	case 0:
		diags.AddAttributeError(path.Root("workspace_name"), "No Live Chat workspace with this name",
			fmt.Sprintf("The provider's api_token can't see a workspace named %q. Create it in Live Chat signed in as the "+
				"token's Hibira account (which becomes its owner), on the Team plan, or set workspace_id.", name))
	default:
		diags.AddAttributeError(path.Root("workspace_name"), "More than one Live Chat workspace with this name",
			fmt.Sprintf("Workspaces %s are all named %q; set workspace_id instead.", strings.Join(ids, ", "), name))
	}
	return false
}

// lc is the Live Chat client for m: its api_key, or the provider's token
// on its workspace_id.
func (r *liveChatWorkspaceResource) lc(m liveChatWorkspaceModel) *client.LiveChat {
	if m.byToken() {
		if r.api == nil {
			// Not configured (can't happen once the provider is): the
			// empty token is refused with a clear 401.
			return client.NewLiveChat(m.Endpoint.ValueString(), "", r.userAgent)
		}
		return r.api.LiveChat(m.Endpoint.ValueString(), m.WorkspaceID.ValueString())
	}
	return client.NewLiveChat(m.Endpoint.ValueString(), m.APIKey.ValueString(), r.userAgent)
}

// Configure takes the provider's client: its User-Agent always, its token
// for workspace_id (api_key calls use only the key).
func (r *liveChatWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c, ok := req.ProviderData.(*client.Client); ok {
		r.userAgent = c.UserAgent()
		r.api = c
	}
}

// liveChatEvents is the configured event list, every event when unset.
func liveChatEvents(ctx context.Context, m liveChatWorkspaceModel, diags *diag.Diagnostics) []string {
	if m.WebhookEvents.IsNull() || m.WebhookEvents.IsUnknown() {
		return append([]string{}, liveChatWebhookEvents...)
	}
	return stringsOf(ctx, m.WebhookEvents, diags)
}

func known[T interface {
	IsNull() bool
	IsUnknown() bool
}](v T) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// apply is one run of Create or Update: it makes the workspace match plan,
// starting from prev (the state; empty on create). Steps that need no
// secret come first: the widget's sites and the webhook. Then the secrets
// that are missing (or all, on rotation), then the framing origins and
// sso_only, which Live Chat accepts only with a single sign-on secret.
//
// On create, a failure after a secret was made is a warning: the caller
// saves the state (an error would taint the resource, and replacing it
// would make the secrets again). plan always holds what was done.
type liveChatRun struct {
	ctx   context.Context
	lc    *client.LiveChat
	plan  *liveChatWorkspaceModel
	diags *diag.Diagnostics
	soft  bool // failures are warnings from here on
	// identityDone: the identity secret was made in this run.
	identityDone bool
}

func (run *liveChatRun) fail(summary string, err error) {
	if run.soft {
		run.diags.AddWarning(summary, err.Error()+"\n\nThe resource is saved with the steps done so far; the next apply finishes it.")
		return
	}
	run.diags.AddError(summary, err.Error())
}

// store fills the settings the plan leaves to Live Chat (unknown) with
// what it answered; configured ones stay as planned.
func (run *liveChatRun) store(s *client.LiveChatSettings) {
	p := run.plan
	if p.Origins.IsUnknown() {
		p.Origins = setOf(run.ctx, s.Origins, run.diags)
	}
	if p.EmbedOrigins.IsUnknown() {
		p.EmbedOrigins = setOf(run.ctx, s.EmbedOrigins, run.diags)
	}
	if p.SSOOnly.IsUnknown() {
		p.SSOOnly = types.BoolValue(s.SSOOnly)
	}
}

// settings sends the configured fields among origins (secretless) or
// embed_origins and sso_only; nothing configured still reads them back.
func (run *liveChatRun) settings(secretBound bool) bool {
	var in client.LiveChatSettingsUpdate
	p := run.plan
	if !secretBound && known(p.Origins) {
		v := stringsOf(run.ctx, p.Origins, run.diags)
		in.Origins = &v
	}
	if secretBound && known(p.EmbedOrigins) {
		v := stringsOf(run.ctx, p.EmbedOrigins, run.diags)
		in.EmbedOrigins = &v
	}
	if secretBound && known(p.SSOOnly) {
		v := p.SSOOnly.ValueBool()
		in.SSOOnly = &v
	}
	var s *client.LiveChatSettings
	var err error
	if in.Origins != nil || in.EmbedOrigins != nil || in.SSOOnly != nil {
		s, err = run.lc.UpdateSettings(run.ctx, in)
	} else {
		s, err = run.lc.Settings(run.ctx)
	}
	if err != nil {
		// The planned values stay in the plan (Terraform wants them back as
		// planned); the next refresh reads Live Chat's and shows the gap.
		run.fail("Changing Live Chat settings failed", err)
		return false
	}
	run.store(s)
	return true
}

// webhook makes the endpoint match the plan: none when webhook_url is
// unset, else the one with that URL (adopted and given a new secret, or
// created). prev is the endpoint already managed.
func (run *liveChatRun) webhook(prev liveChatWorkspaceModel) bool {
	p, lc, ctx := run.plan, run.lc, run.ctx
	prevID := prev.WebhookID.ValueString()
	p.WebhookID, p.WebhookSecret = prev.WebhookID, prev.WebhookSecret
	if p.WebhookURL.IsNull() || p.WebhookURL.ValueString() == "" {
		if prevID != "" {
			if err := lc.DeleteWebhook(ctx, prevID); err != nil && !client.IsNotFound(err) {
				run.fail("Deleting the Live Chat webhook failed", err)
				return false
			}
		}
		p.WebhookID, p.WebhookSecret = types.StringValue(""), types.StringValue("")
		return true
	}
	u, events := p.WebhookURL.ValueString(), liveChatEvents(ctx, *p, run.diags)
	if prevID != "" {
		h, err := lc.UpdateWebhook(ctx, prevID, u, events)
		if err == nil {
			p.WebhookID = types.StringValue(h.ID)
			return true
		}
		if !client.IsNotFound(err) {
			run.fail("Changing the Live Chat webhook failed", err)
			return false
		}
		p.WebhookID, p.WebhookSecret = types.StringValue(""), types.StringValue("")
	}
	list, err := lc.Webhooks(ctx)
	if err != nil {
		run.fail("Reading Live Chat webhooks failed", err)
		return false
	}
	for _, h := range list {
		if h.URL != u {
			continue
		}
		if _, err := lc.UpdateWebhook(ctx, h.ID, u, events); err != nil {
			run.fail("Changing the Live Chat webhook failed", err)
			return false
		}
		secret, err := lc.RotateWebhookSecret(ctx, h.ID)
		if err != nil {
			run.fail("Rotating the Live Chat webhook secret failed", err)
			return false
		}
		p.WebhookID, p.WebhookSecret = types.StringValue(h.ID), types.StringValue(secret)
		return true
	}
	h, err := lc.CreateWebhook(ctx, u, events)
	if err != nil {
		run.fail("Creating the Live Chat webhook failed", err)
		return false
	}
	p.WebhookID, p.WebhookSecret = types.StringValue(h.ID), types.StringValue(h.Secret)
	return true
}

// secrets makes the identity secret and/or the SSO secret, storing each as
// soon as it exists. create turns later failures into warnings.
func (run *liveChatRun) secrets(identity, sso, create bool) bool {
	if identity {
		s, err := run.lc.RotateIdentitySecret(run.ctx)
		if err != nil {
			run.fail("Making the Live Chat verified-visitor secret failed", err)
			return false
		}
		run.plan.IdentitySecret = types.StringValue(s)
		run.identityDone = true
		run.soft = run.soft || create
	}
	if sso {
		s, err := run.lc.RotateSSOSecret(run.ctx)
		if err != nil {
			run.fail("Making the Live Chat single sign-on secret failed", err)
			return false
		}
		run.plan.SSOSecret = types.StringValue(s)
		run.soft = run.soft || create
	}
	return true
}

// serverKey makes a new server key when need (with workspace_id), then
// revokes the one in prev. The new key is stored before the old one is
// revoked; a revoke that fails is a warning to do it by hand.
func (run *liveChatRun) serverKey(prev liveChatWorkspaceModel, need bool) bool {
	p := run.plan
	if !p.byToken() || !need {
		return true
	}
	k, err := run.lc.CreateAPIKey(run.ctx, serverKeyName, p.ServerKeyScope.ValueString())
	if err != nil {
		run.fail("Making the Live Chat server key failed", err)
		return false
	}
	p.ServerKeyID, p.ServerKey = types.StringValue(k.ID), types.StringValue(k.Key)
	run.soft = true
	if old := prev.ServerKeyID.ValueString(); old != "" && old != k.ID {
		if err := run.lc.RevokeAPIKey(run.ctx, old); err != nil && !client.IsNotFound(err) {
			run.diags.AddWarning("Could not revoke the old Live Chat server key",
				fmt.Sprintf("Revoke API key %s in Live Chat › Settings › API & webhooks by hand: %v", old, err))
		}
	}
	return true
}

func (r *liveChatWorkspaceResource) workspace(ctx context.Context, lc *client.LiveChat, m *liveChatWorkspaceModel) error {
	w, err := lc.Workspace(ctx)
	if err != nil {
		return err
	}
	m.ID, m.Name, m.Plan = types.StringValue(w.ID), types.StringValue(w.Name), types.StringValue(w.Plan)
	return nil
}

// ModifyPlan marks the values an update will make anew as unknown: a
// secret when rotation changes or it is missing, and the webhook's ID and
// secret when its URL changes or the endpoint is gone.
func (r *liveChatWorkspaceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state liveChatWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rotate := !plan.Rotation.Equal(state.Rotation)
	if rotate || state.IdentitySecret.ValueString() == "" {
		plan.IdentitySecret = types.StringUnknown()
	}
	if rotate || ssoPending(ctx, req.Private) || state.SSOSecret.ValueString() == "" {
		plan.SSOSecret = types.StringUnknown()
	}
	if !plan.WebhookURL.Equal(state.WebhookURL) || (state.WebhookID.ValueString() == "" && plan.WebhookURL.ValueString() != "") {
		plan.WebhookID, plan.WebhookSecret = types.StringUnknown(), types.StringUnknown()
	}
	switch {
	case !plan.byToken():
		plan.WorkspaceID = types.StringValue("")
	case plan.WorkspaceName.ValueString() != "" && !plan.WorkspaceName.Equal(state.WorkspaceName):
		// A new name (or a switch from workspace_id): look it up again.
		plan.WorkspaceID = types.StringUnknown()
	}
	if !plan.APIKey.Equal(state.APIKey) || !sameWorkspace(plan, state) {
		// The key may belong to another workspace: everything may change.
		plan.ID, plan.Name, plan.Plan = types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
		plan.IdentitySecret, plan.SSOSecret = types.StringUnknown(), types.StringUnknown()
		plan.WebhookID, plan.WebhookSecret = types.StringUnknown(), types.StringUnknown()
		plan.ServerKeyID, plan.ServerKey = types.StringUnknown(), types.StringUnknown()
	}
	switch {
	case !plan.byToken():
		plan.ServerKeyID, plan.ServerKey = types.StringValue(""), types.StringValue("")
	case serverKeyNeeded(ctx, plan, state, req.Private):
		plan.ServerKeyID, plan.ServerKey = types.StringUnknown(), types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

// serverKeyNeeded reports whether an update makes a new server key: with
// workspace_id, when there is none, rotation or the scope changes, a
// rotation left it pending, or the workspace changes.
func serverKeyNeeded(ctx context.Context, plan, state liveChatWorkspaceModel, p privateGetter) bool {
	return plan.byToken() && (state.ServerKeyID.ValueString() == "" || !plan.Rotation.Equal(state.Rotation) ||
		!plan.ServerKeyScope.Equal(state.ServerKeyScope) || !sameWorkspace(plan, state) ||
		!plan.APIKey.Equal(state.APIKey) || privateFlag(ctx, p, privateKeyPending))
}

// privateKeyPending marks (in private state) a server key still to be
// made after a rotation whose secrets were made but whose key was not.
const privateKeyPending = "server_key_pending"

// privateSSOPending marks (in private state) an SSO secret still to be
// made after a rotation that made only the identity secret.
const privateSSOPending = "sso_pending"

type privateGetter interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

func ssoPending(ctx context.Context, p privateGetter) bool {
	return privateFlag(ctx, p, privateSSOPending)
}

func privateFlag(ctx context.Context, p privateGetter, key string) bool {
	if p == nil {
		return false
	}
	b, _ := p.GetKey(ctx, key)
	return string(b) == "true"
}

func (r *liveChatWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.resolve(ctx, &plan, &resp.Diagnostics) {
		return
	}
	lc := r.lc(plan)
	if err := r.workspace(ctx, lc, &plan); err != nil {
		resp.Diagnostics.AddError("Reading the Live Chat workspace failed", err.Error())
		return
	}
	empty := liveChatWorkspaceModel{IdentitySecret: types.StringValue(""), SSOSecret: types.StringValue(""),
		WebhookID: types.StringValue(""), WebhookSecret: types.StringValue(""),
		ServerKeyID: types.StringValue(""), ServerKey: types.StringValue("")}
	plan.IdentitySecret, plan.SSOSecret = empty.IdentitySecret, empty.SSOSecret
	plan.ServerKeyID, plan.ServerKey = empty.ServerKeyID, empty.ServerKey
	run := &liveChatRun{ctx: ctx, lc: lc, plan: &plan, diags: &resp.Diagnostics}
	// Before any secret exists, a failure is an ordinary error and nothing
	// is saved (a webhook made here is adopted by URL on the next apply).
	if !run.settings(false) || !run.webhook(empty) {
		return
	}
	// From here on a secret may exist (the webhook's, made or rotated on
	// adoption; or, with no webhook_url, the identity and SSO secrets made
	// next), so the state must be saved and failures are warnings.
	run.soft = true
	if run.secrets(true, true, true) {
		run.settings(true)
	}
	run.serverKey(empty, true)
	fillUnknown(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// fillUnknown gives computed values that a failed step left unknown a
// known value, so the state can be saved.
func fillUnknown(m *liveChatWorkspaceModel) {
	for _, s := range []*types.String{&m.WebhookID, &m.WebhookSecret, &m.IdentitySecret, &m.SSOSecret, &m.ServerKeyID, &m.ServerKey} {
		if s.IsUnknown() {
			*s = types.StringValue("")
		}
	}
	if m.Origins.IsUnknown() {
		m.Origins = types.SetValueMust(types.StringType, nil)
	}
	if m.EmbedOrigins.IsUnknown() {
		m.EmbedOrigins = types.SetValueMust(types.StringType, nil)
	}
	if m.SSOOnly.IsUnknown() {
		m.SSOOnly = types.BoolValue(false)
	}
}

func (r *liveChatWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state liveChatWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// State from before workspace_id: the server key fields are new.
	if state.WorkspaceID.IsNull() {
		state.WorkspaceID = types.StringValue("")
	}
	if state.ServerKeyID.IsNull() {
		state.ServerKeyID, state.ServerKey = types.StringValue(""), types.StringValue("")
	}
	if state.ServerKeyScope.IsNull() {
		state.ServerKeyScope = types.StringValue("admin")
	}
	lc := r.lc(state)
	if err := r.workspace(ctx, lc, &state); err != nil {
		var e *client.Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			// A revoked key: keep the state as it is so a plan with the new
			// key in api_key can run.
			detail := "The refresh of hibira_livechat_workspace was skipped. If the key was revoked, set the new one in api_key."
			if state.byToken() {
				detail = "The refresh of hibira_livechat_workspace was skipped. Check the provider's api_token."
			}
			resp.Diagnostics.AddWarning("Live Chat refused the API key in state", detail)
			return
		}
		resp.Diagnostics.AddError("Reading the Live Chat workspace failed", err.Error())
		return
	}
	s, err := lc.Settings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading Live Chat settings failed", err.Error())
		return
	}
	// A secret turned off in Live Chat can't be read back: forget it, so
	// the next apply makes that one again.
	if !s.IdentityVerification {
		state.IdentitySecret = types.StringValue("")
	}
	if !s.SSO {
		state.SSOSecret = types.StringValue("")
	}
	state.Origins = setOf(ctx, s.Origins, &resp.Diagnostics)
	state.EmbedOrigins = setOf(ctx, s.EmbedOrigins, &resp.Diagnostics)
	state.SSOOnly = types.BoolValue(s.SSOOnly)
	if id := state.WebhookID.ValueString(); id != "" {
		h, err := lc.Webhook(ctx, id)
		switch {
		case client.IsNotFound(err):
			// Gone: the next apply adopts or creates one again.
			state.WebhookID, state.WebhookSecret = types.StringValue(""), types.StringValue("")
			state.WebhookURL = types.StringNull()
		case err != nil:
			resp.Diagnostics.AddError("Reading the Live Chat webhook failed", err.Error())
			return
		case h.Disabled:
			// Disabled in Live Chat (or after failed deliveries): show a
			// change so the next apply enables it again.
			state.WebhookURL = types.StringNull()
		default:
			state.WebhookURL = types.StringValue(h.URL)
			// Unset events mean all of them: a narrower list set in Live
			// Chat shows up as a change back to all. Live Chat stores the
			// list as given and refuses an empty one, so "all" reads back
			// as the full list, never [].
			if !state.WebhookEvents.IsNull() || !sameStrings(h.Events, liveChatWebhookEvents) {
				state.WebhookEvents = setOf(ctx, h.Events, &resp.Diagnostics)
			}
		}
	}
	if id := state.ServerKeyID.ValueString(); id != "" && state.byToken() {
		keys, err := lc.APIKeys(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Reading Live Chat API keys failed", err.Error())
			return
		}
		if !slices.ContainsFunc(keys, func(k client.LiveChatAPIKey) bool { return k.ID == id }) {
			// Revoked in Live Chat: the next apply makes one again.
			state.ServerKeyID, state.ServerKey = types.StringValue(""), types.StringValue("")
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *liveChatWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state liveChatWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.resolve(ctx, &plan, &resp.Diagnostics) {
		return
	}
	lc := r.lc(plan)
	if err := r.workspace(ctx, lc, &plan); err != nil {
		resp.Diagnostics.AddError("Reading the Live Chat workspace failed", err.Error())
		return
	}
	if plan.ID.ValueString() != state.ID.ValueString() {
		// A key of another workspace: remove our endpoint from the old one
		// if its key still works, then set the new one up from the start.
		old := r.lc(state)
		if id := state.WebhookID.ValueString(); id != "" {
			if err := old.DeleteWebhook(ctx, id); err != nil && !client.IsNotFound(err) {
				resp.Diagnostics.AddWarning("Could not delete the old workspace's webhook endpoint",
					fmt.Sprintf("Delete endpoint %s in workspace %s by hand: %v", id, state.ID.ValueString(), err))
			}
		}
		r.revokeServerKey(ctx, state, &resp.Diagnostics, true)
		state.IdentitySecret, state.SSOSecret = types.StringValue(""), types.StringValue("")
		state.WebhookID, state.WebhookSecret = types.StringValue(""), types.StringValue("")
		state.ServerKeyID, state.ServerKey = types.StringValue(""), types.StringValue("")
	} else if !plan.byToken() && state.byToken() {
		// Same workspace, now by api_key: the server key is no longer
		// managed here.
		r.revokeServerKey(ctx, state, &resp.Diagnostics, true)
		state.ServerKeyID, state.ServerKey = types.StringValue(""), types.StringValue("")
	}
	plan.IdentitySecret, plan.SSOSecret = state.IdentitySecret, state.SSOSecret
	rotate := !plan.Rotation.Equal(state.Rotation)
	pending := ssoPending(ctx, req.Private)
	needID := rotate || state.IdentitySecret.ValueString() == ""
	needSSO := rotate || pending || state.SSOSecret.ValueString() == ""
	needKey := serverKeyNeeded(ctx, plan, state, req.Private)
	// Unknown values the run doesn't reach keep what the state had.
	plan.WebhookID, plan.WebhookSecret = state.WebhookID, state.WebhookSecret
	plan.ServerKeyID, plan.ServerKey = state.ServerKeyID, state.ServerKey
	if !plan.byToken() {
		plan.ServerKeyID, plan.ServerKey = types.StringValue(""), types.StringValue("")
	}
	run := &liveChatRun{ctx: ctx, lc: lc, plan: &plan, diags: &resp.Diagnostics}
	rotated, keyed := false, !needKey
	if run.settings(false) && run.webhook(state) && run.secrets(needID, needSSO, false) {
		rotated = true
		run.settings(true)
		keyed = run.serverKey(state, needKey)
	}
	switch {
	case rotated && !keyed:
		// The secrets rotated but the server key didn't: keep the rotation
		// and make only the key next time.
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateSSOPending, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyPending, []byte(`true`))...)
	case rotated:
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateSSOPending, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyPending, nil)...)
	case run.identityDone && needSSO:
		// The identity secret is new but the SSO secret failed: keep the
		// rotation (making the identity secret again would end the new one
		// at once) and remember to make only the SSO secret next time.
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateSSOPending, []byte(`true`))...)
	case rotate:
		// Nothing rotated: keep the old keeper so the next plan asks again.
		plan.Rotation = state.Rotation
	}
	if needKey && !rotated {
		// No server key was made: keep the old scope so the next plan
		// asks again.
		plan.ServerKeyScope = state.ServerKeyScope
	}
	fillUnknown(&plan)
	// Saved even after an error, so secrets made in this run are kept.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *liveChatWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state liveChatWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if id := state.WebhookID.ValueString(); id != "" {
		if err := r.lc(state).DeleteWebhook(ctx, id); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Deleting the Live Chat webhook failed", err.Error())
		}
	}
	r.revokeServerKey(ctx, state, &resp.Diagnostics, false)
}

// revokeServerKey revokes the server key in m (workspace_id only); a
// failure is a warning when soft, else an error.
func (r *liveChatWorkspaceResource) revokeServerKey(ctx context.Context, m liveChatWorkspaceModel, diags *diag.Diagnostics, soft bool) {
	id := m.ServerKeyID.ValueString()
	if id == "" || !m.byToken() {
		return
	}
	err := r.lc(m).RevokeAPIKey(ctx, id)
	if err == nil || client.IsNotFound(err) {
		return
	}
	if soft {
		diags.AddWarning("Could not revoke the old Live Chat server key",
			fmt.Sprintf("Revoke API key %s in workspace %s by hand: %v", id, m.WorkspaceID.ValueString(), err))
		return
	}
	diags.AddError("Revoking the Live Chat server key failed", err.Error())
}
