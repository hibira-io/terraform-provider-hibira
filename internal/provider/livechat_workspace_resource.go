package provider

import (
	"context"
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

var liveChatKey = regexp.MustCompile(`^lck_ws_[A-Za-z0-9_-]{4,40}\.[A-Za-z0-9_-]{16,200}$`)

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
			"Creating this resource makes a new verified-visitor secret and a new single sign-on secret (any made earlier stop " +
			"working) and adopts or creates the webhook endpoint. Destroying it deletes the webhook endpoint and leaves the " +
			"workspace, its settings and its secrets as they are.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`ws_...`).",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "An admin API key of the workspace (`lck_ws_....`).",
				Required:            true,
				Sensitive:           true,
				Validators:          []validator.String{stringvalidator.RegexMatches(liveChatKey, "must be a Live Chat API key (lck_ws_...)")},
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Live Chat's address. Defaults to `" + client.DefaultLiveChatEndpoint + "`.",
				Optional:            true, Computed: true,
				Default:       stringdefault.StaticString(client.DefaultLiveChatEndpoint),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(httpsURL, "must be an http:// or https:// URL")},
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
				MarkdownDescription: "The sites the widget may run on, like `https://shop.example.com`. Apps built with Capacitor " +
					"add `https://localhost` (Android) and `capacitor://localhost` (iOS). Unset: managed in Live Chat and only read here.",
				ElementType: types.StringType,
				Optional:    true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"embed_origins": schema.SetAttribute{
				MarkdownDescription: "The origins that may show the Live Chat inbox in a frame (with single sign-on). " +
					"Unset: managed in Live Chat and only read here.",
				ElementType: types.StringType,
				Optional:    true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
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
				MarkdownDescription: "The URL Live Chat posts conversation events to. An endpoint already there with this URL is " +
					"adopted (and gets a new secret). Unset: no endpoint.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(httpsURL, "must be an http:// or https:// URL")},
			},
			"webhook_events": schema.SetAttribute{
				MarkdownDescription: "The event types to send: " + strings.Join(quoted(liveChatWebhookEvents), ", ") + ". Unset: all of them.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf(liveChatWebhookEvents...))},
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

func (r *liveChatWorkspaceResource) lc(m liveChatWorkspaceModel) *client.LiveChat {
	return client.NewLiveChat(m.Endpoint.ValueString(), m.APIKey.ValueString(), r.userAgent)
}

// events is the configured event list, every event when unset.
func liveChatEvents(ctx context.Context, m liveChatWorkspaceModel, diags *diag.Diagnostics) []string {
	if m.WebhookEvents.IsNull() || m.WebhookEvents.IsUnknown() {
		return append([]string{}, liveChatWebhookEvents...)
	}
	return stringsOf(ctx, m.WebhookEvents, diags)
}

// applySettings sends the configured settings (unset = unchanged) and
// stores what Live Chat answers. sso_only needs the SSO secret, so it is
// sent after the secrets exist.
func (r *liveChatWorkspaceResource) applySettings(ctx context.Context, lc *client.LiveChat, plan *liveChatWorkspaceModel, diags *diag.Diagnostics) {
	var in client.LiveChatSettingsUpdate
	if !plan.Origins.IsNull() && !plan.Origins.IsUnknown() {
		v := stringsOf(ctx, plan.Origins, diags)
		in.Origins = &v
	}
	if !plan.EmbedOrigins.IsNull() && !plan.EmbedOrigins.IsUnknown() {
		v := stringsOf(ctx, plan.EmbedOrigins, diags)
		in.EmbedOrigins = &v
	}
	if !plan.SSOOnly.IsNull() && !plan.SSOOnly.IsUnknown() {
		v := plan.SSOOnly.ValueBool()
		in.SSOOnly = &v
	}
	if diags.HasError() {
		return
	}
	var s *client.LiveChatSettings
	var err error
	if in.Origins != nil || in.EmbedOrigins != nil || in.SSOOnly != nil {
		s, err = lc.UpdateSettings(ctx, in)
	} else {
		s, err = lc.Settings(ctx)
	}
	if err != nil {
		diags.AddError("Changing Live Chat settings failed", err.Error())
		return
	}
	plan.Origins = setOf(ctx, s.Origins, diags)
	plan.EmbedOrigins = setOf(ctx, s.EmbedOrigins, diags)
	plan.SSOOnly = types.BoolValue(s.SSOOnly)
}

func (r *liveChatWorkspaceResource) rotateSecrets(ctx context.Context, lc *client.LiveChat, m *liveChatWorkspaceModel, diags *diag.Diagnostics) {
	id, err := lc.RotateIdentitySecret(ctx)
	if err != nil {
		diags.AddError("Making the Live Chat verified-visitor secret failed", err.Error())
		return
	}
	sso, err := lc.RotateSSOSecret(ctx)
	if err != nil {
		diags.AddError("Making the Live Chat single sign-on secret failed", err.Error())
		return
	}
	m.IdentitySecret, m.SSOSecret = types.StringValue(id), types.StringValue(sso)
}

// syncWebhook makes the endpoint match the plan: none when webhook_url is
// unset, else the one with that URL (adopted and given a new secret, or
// created). prev is the endpoint the state already manages.
func (r *liveChatWorkspaceResource) syncWebhook(ctx context.Context, lc *client.LiveChat, plan *liveChatWorkspaceModel, prev liveChatWorkspaceModel, diags *diag.Diagnostics) {
	prevID := prev.WebhookID.ValueString()
	if plan.WebhookURL.IsNull() || plan.WebhookURL.ValueString() == "" {
		if prevID != "" {
			if err := lc.DeleteWebhook(ctx, prevID); err != nil && !client.IsNotFound(err) {
				diags.AddError("Deleting the Live Chat webhook failed", err.Error())
				return
			}
		}
		plan.WebhookID, plan.WebhookSecret = types.StringValue(""), types.StringValue("")
		return
	}
	url, events := plan.WebhookURL.ValueString(), liveChatEvents(ctx, *plan, diags)
	if diags.HasError() {
		return
	}
	if prevID != "" {
		h, err := lc.UpdateWebhook(ctx, prevID, url, events)
		if err == nil {
			plan.WebhookID, plan.WebhookSecret = types.StringValue(h.ID), prev.WebhookSecret
			return
		}
		if !client.IsNotFound(err) {
			diags.AddError("Changing the Live Chat webhook failed", err.Error())
			return
		}
	}
	list, err := lc.Webhooks(ctx)
	if err != nil {
		diags.AddError("Reading Live Chat webhooks failed", err.Error())
		return
	}
	for _, h := range list {
		if h.URL != url {
			continue
		}
		if _, err := lc.UpdateWebhook(ctx, h.ID, url, events); err != nil {
			diags.AddError("Changing the Live Chat webhook failed", err.Error())
			return
		}
		secret, err := lc.RotateWebhookSecret(ctx, h.ID)
		if err != nil {
			diags.AddError("Rotating the Live Chat webhook secret failed", err.Error())
			return
		}
		plan.WebhookID, plan.WebhookSecret = types.StringValue(h.ID), types.StringValue(secret)
		return
	}
	h, err := lc.CreateWebhook(ctx, url, events)
	if err != nil {
		diags.AddError("Creating the Live Chat webhook failed", err.Error())
		return
	}
	plan.WebhookID, plan.WebhookSecret = types.StringValue(h.ID), types.StringValue(h.Secret)
}

func (r *liveChatWorkspaceResource) workspace(ctx context.Context, lc *client.LiveChat, m *liveChatWorkspaceModel, diags *diag.Diagnostics) {
	w, err := lc.Workspace(ctx)
	if err != nil {
		diags.AddError("Reading the Live Chat workspace failed", err.Error())
		return
	}
	m.ID, m.Name, m.Plan = types.StringValue(w.ID), types.StringValue(w.Name), types.StringValue(w.Plan)
}

// Configure takes the provider's User-Agent; Live Chat calls use the
// resource's own api_key, not the provider token.
func (r *liveChatWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if c, ok := req.ProviderData.(*client.Client); ok {
		r.userAgent = c.UserAgent()
	}
}

// ModifyPlan marks the values an update will make anew as unknown: the
// secrets when rotation changes (or one is missing), and the webhook's ID
// and secret when its URL changes or the endpoint is gone.
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
	if !plan.Rotation.Equal(state.Rotation) || state.IdentitySecret.ValueString() == "" || state.SSOSecret.ValueString() == "" {
		plan.IdentitySecret, plan.SSOSecret = types.StringUnknown(), types.StringUnknown()
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
	if r.workspace(ctx, lc, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	if r.rotateSecrets(ctx, lc, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	// The secrets exist from here on: keep them in state even if a later
	// step fails, so the next apply does not make another pair.
	if r.applySettings(ctx, lc, &plan, &resp.Diagnostics); !resp.Diagnostics.HasError() {
		r.syncWebhook(ctx, lc, &plan, liveChatWorkspaceModel{}, &resp.Diagnostics)
	}
	fillUnknown(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// fillUnknown gives computed values that a failed step left unknown a
// known empty value, so the state can be saved.
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
	if r.workspace(ctx, lc, &state, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	s, err := lc.Settings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading Live Chat settings failed", err.Error())
		return
	}
	// A secret turned off in Live Chat (or made again there) can't be read
	// back: drop the resource so the next apply sets the workspace up again.
	if !s.IdentityVerification || !s.SSO {
		resp.Diagnostics.AddWarning("Live Chat secrets changed outside Terraform",
			"Visitor verification or single sign-on is off in Live Chat; the next apply makes new secrets.")
		resp.State.RemoveResource(ctx)
		return
	}
	state.Origins = setOf(ctx, s.Origins, &resp.Diagnostics)
	state.EmbedOrigins = setOf(ctx, s.EmbedOrigins, &resp.Diagnostics)
	state.SSOOnly = types.BoolValue(s.SSOOnly)
	if id := state.WebhookID.ValueString(); id != "" {
		h, err := lc.Webhook(ctx, id)
		switch {
		case client.IsNotFound(err):
			// Gone: the next apply creates it again.
			state.WebhookID, state.WebhookSecret = types.StringValue(""), types.StringValue("")
			state.WebhookURL = types.StringNull()
		case err != nil:
			resp.Diagnostics.AddError("Reading the Live Chat webhook failed", err.Error())
			return
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
	if r.workspace(ctx, lc, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
		return
	}
	if plan.ID.ValueString() != state.ID.ValueString() {
		// A key of another workspace: set that one up from scratch.
		state.IdentitySecret, state.SSOSecret = types.StringValue(""), types.StringValue("")
		state.WebhookID, state.WebhookSecret = types.StringValue(""), types.StringValue("")
	}
	plan.IdentitySecret, plan.SSOSecret = state.IdentitySecret, state.SSOSecret
	if !plan.Rotation.Equal(state.Rotation) || state.IdentitySecret.ValueString() == "" || state.SSOSecret.ValueString() == "" {
		if r.rotateSecrets(ctx, lc, &plan, &resp.Diagnostics); resp.Diagnostics.HasError() {
			return
		}
	}
	if r.applySettings(ctx, lc, &plan, &resp.Diagnostics); !resp.Diagnostics.HasError() {
		r.syncWebhook(ctx, lc, &plan, state, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		// Keep the state's webhook and any new secrets.
		if plan.WebhookID.IsUnknown() {
			plan.WebhookID, plan.WebhookSecret = state.WebhookID, state.WebhookSecret
		}
		if plan.IdentitySecret.IsUnknown() {
			plan.IdentitySecret, plan.SSOSecret = state.IdentitySecret, state.SSOSecret
		}
	}
	fillUnknown(&plan)
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
