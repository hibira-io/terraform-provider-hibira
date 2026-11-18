package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource               = (*liveChatWorkspaceResource)(nil)
	_ resource.ResourceWithConfigure  = (*liveChatWorkspaceResource)(nil)
	_ resource.ResourceWithModifyPlan = (*liveChatWorkspaceResource)(nil)
)

// liveChatWebhookEvents are the event types Live Chat sends.
var liveChatWebhookEvents = []string{"conversation.created", "conversation.assigned", "conversation.closed",
	"conversation.reopened", "conversation.rated", "conversation.team_changed", "message.created"}

var (
	liveChatKey = regexp.MustCompile(`^lck_ws_[A-Za-z0-9_-]{4,40}\.[A-Za-z0-9_-]{16,200}$`)
	// Origins in the form Live Chat stores them (lowercase scheme and host,
	// no path or trailing slash), so what is planned is what is read back.
	liveChatOrigin    = regexp.MustCompile(`^(https://[a-z0-9.-]+(:[0-9]{1,5})?|http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?)$`)
	liveChatAppOrigin = regexp.MustCompile(`^(capacitor|ionic)://localhost$`)
	// Live Chat itself: https, or http on this machine (tests).
	liveChatEndpoint = regexp.MustCompile(`^(https://[^/\s]+|http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?)/?$`)
)

const originHelp = "must be an origin as Live Chat stores it: lowercase, no path or trailing slash, like https://shop.example.com"

func newLiveChatWorkspaceResource() resource.Resource { return &liveChatWorkspaceResource{} }

// liveChatWorkspaceResource sets up an existing Hibira Live Chat workspace
// for a site from one admin API key: the widget's sites, the console
// origins that may frame the inbox, single sign-on only, the
// verified-visitor and single sign-on secrets, and one webhook endpoint.
type liveChatWorkspaceResource struct{ userAgent string }

type liveChatWorkspaceModel struct {
	ID             types.String `tfsdk:"id"`
	APIKey         types.String `tfsdk:"api_key"`
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
}

func (r *liveChatWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_workspace"
}

func (r *liveChatWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets up an existing [Hibira Live Chat](https://chat.hibira.io) workspace for a site, with one of the " +
			"workspace's **admin** API keys (Live Chat › Settings › API & webhooks). Create the workspace and choose its plan in " +
			"Live Chat first: the API, webhooks, visitor verification and the embedded inbox need the Team plan or above.\n\n" +
			"Creating this resource sets the widget's sites and the webhook endpoint (an endpoint with the same URL is adopted " +
			"and gets a new secret), then makes a new verified-visitor secret and a new single sign-on secret, then sets the " +
			"framing origins and `sso_only`, which need the single sign-on secret. An earlier verified-visitor secret stops " +
			"working at once, an earlier single sign-on secret after a day. Once the secrets are made, a later step that fails " +
			"is a warning, not an error: the resource is saved with what was done and the next apply finishes it, so a typo " +
			"never makes the secrets again. Destroying the resource deletes the webhook endpoint and leaves the workspace, its " +
			"settings and its secrets as they are.\n\n" +
			"**State.** The state holds `api_key` and the three secrets in plain text, like every secret a Terraform provider " +
			"returns: keep it in an encrypted backend (for example S3 with server-side encryption and restricted access) and " +
			"never commit it.\n\n" +
			"**Outside changes.** Turning visitor verification or single sign-on off in Live Chat makes the next apply create " +
			"that secret again (only that one). A disabled or deleted webhook endpoint is enabled or created again. When " +
			"Live Chat refuses the key in state (revoked), the refresh is skipped with a warning; set the new key in `api_key`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`ws_...`).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "An admin API key of the workspace (`lck_ws_....`). A key of another workspace sets that " +
					"workspace up from the start (new secrets and webhook); the old workspace's webhook endpoint is deleted when " +
					"the old key still works.",
				Required:   true,
				Sensitive:  true,
				Validators: []validator.String{stringvalidator.RegexMatches(liveChatKey, "must be a Live Chat API key (lck_ws_...)")},
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
				MarkdownDescription: "The https URL Live Chat posts conversation events to (not localhost, `.local` or `.internal`). " +
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
type webhookURLValidator struct{}

func (webhookURLValidator) Description(context.Context) string {
	return "must be an https:// URL whose host is not localhost, .local or .internal"
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
	bad := err != nil || s != strings.TrimSpace(s) || len(s) > 2000 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != ""
	if !bad {
		h := strings.ToLower(u.Hostname())
		bad = h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
	}
	if bad {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid webhook URL", v.Description(ctx)+", got "+s)
	}
}

func (r *liveChatWorkspaceResource) lc(m liveChatWorkspaceModel) *client.LiveChat {
	return client.NewLiveChat(m.Endpoint.ValueString(), m.APIKey.ValueString(), r.userAgent)
}

// Configure takes the provider's User-Agent; Live Chat calls use the
// resource's own api_key, not the provider token.
func (r *liveChatWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c, ok := req.ProviderData.(*client.Client); ok {
		r.userAgent = c.UserAgent()
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
	if rotate || state.SSOSecret.ValueString() == "" {
		plan.SSOSecret = types.StringUnknown()
	}
	if !plan.WebhookURL.Equal(state.WebhookURL) || (state.WebhookID.ValueString() == "" && plan.WebhookURL.ValueString() != "") {
		plan.WebhookID, plan.WebhookSecret = types.StringUnknown(), types.StringUnknown()
	}
	if !plan.APIKey.Equal(state.APIKey) {
		// The key may belong to another workspace: everything may change.
		plan.ID, plan.Name, plan.Plan = types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
		plan.IdentitySecret, plan.SSOSecret = types.StringUnknown(), types.StringUnknown()
		plan.WebhookID, plan.WebhookSecret = types.StringUnknown(), types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *liveChatWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lc := r.lc(plan)
	if err := r.workspace(ctx, lc, &plan); err != nil {
		resp.Diagnostics.AddError("Reading the Live Chat workspace failed", err.Error())
		return
	}
	empty := liveChatWorkspaceModel{IdentitySecret: types.StringValue(""), SSOSecret: types.StringValue(""),
		WebhookID: types.StringValue(""), WebhookSecret: types.StringValue("")}
	plan.IdentitySecret, plan.SSOSecret = empty.IdentitySecret, empty.SSOSecret
	run := &liveChatRun{ctx: ctx, lc: lc, plan: &plan, diags: &resp.Diagnostics}
	// Before any secret exists, a failure is an ordinary error and nothing
	// is saved (a webhook made here is adopted by URL on the next apply).
	if !run.settings(false) || !run.webhook(empty) {
		return
	}
	if run.secrets(true, true, true) {
		run.settings(true)
	}
	fillUnknown(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// fillUnknown gives computed values that a failed step left unknown a
// known value, so the state can be saved.
func fillUnknown(m *liveChatWorkspaceModel) {
	for _, s := range []*types.String{&m.WebhookID, &m.WebhookSecret, &m.IdentitySecret, &m.SSOSecret} {
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
	lc := r.lc(state)
	if err := r.workspace(ctx, lc, &state); err != nil {
		var e *client.Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			// A revoked key: keep the state as it is so a plan with the new
			// key in api_key can run.
			resp.Diagnostics.AddWarning("Live Chat refused the API key in state",
				"The refresh of hibira_livechat_workspace was skipped. If the key was revoked, set the new one in api_key.")
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
			if !state.WebhookEvents.IsNull() {
				state.WebhookEvents = setOf(ctx, h.Events, &resp.Diagnostics)
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *liveChatWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state liveChatWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
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
		if id := state.WebhookID.ValueString(); id != "" {
			if err := r.lc(state).DeleteWebhook(ctx, id); err != nil && !client.IsNotFound(err) {
				resp.Diagnostics.AddWarning("Could not delete the old workspace's webhook endpoint",
					fmt.Sprintf("Delete endpoint %s in workspace %s by hand: %v", id, state.ID.ValueString(), err))
			}
		}
		state.IdentitySecret, state.SSOSecret = types.StringValue(""), types.StringValue("")
		state.WebhookID, state.WebhookSecret = types.StringValue(""), types.StringValue("")
	}
	plan.IdentitySecret, plan.SSOSecret = state.IdentitySecret, state.SSOSecret
	rotate := !plan.Rotation.Equal(state.Rotation)
	needID, needSSO := rotate || state.IdentitySecret.ValueString() == "", rotate || state.SSOSecret.ValueString() == ""
	// Unknown values the run doesn't reach keep what the state had.
	plan.WebhookID, plan.WebhookSecret = state.WebhookID, state.WebhookSecret
	run := &liveChatRun{ctx: ctx, lc: lc, plan: &plan, diags: &resp.Diagnostics}
	rotated := false
	if run.settings(false) && run.webhook(state) && run.secrets(needID, needSSO, false) {
		rotated = true
		run.settings(true)
	}
	if rotate && !rotated {
		// Keep the old keeper so the next plan asks for the rotation again.
		plan.Rotation = state.Rotation
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
}
