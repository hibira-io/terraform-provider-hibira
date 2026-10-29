package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*appResource)(nil)
	_ resource.ResourceWithConfigure   = (*appResource)(nil)
	_ resource.ResourceWithImportState = (*appResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*appResource)(nil)
)

func newAppResource() resource.Resource { return &appResource{} }

type appResource struct{ client *client.Client }

type appModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Cluster            types.String `tfsdk:"cluster"`
	Enabled            types.Bool   `tfsdk:"enabled"`
	ClientEvents       types.Bool   `tfsdk:"client_events"`
	SubscriptionCount  types.Bool   `tfsdk:"subscription_count"`
	AllowedOrigins     types.Set    `tfsdk:"allowed_origins"`
	History            types.Bool   `tfsdk:"history"`
	APIAllowlist       types.Set    `tfsdk:"api_allowlist"`
	EdgeMode           types.String `tfsdk:"edge_mode"`
	SecretRotation     types.String `tfsdk:"secret_rotation"`
	SecretGraceMinutes types.Int64  `tfsdk:"secret_grace_minutes"`
	Key                types.String `tfsdk:"key"`
	Secret             types.String `tfsdk:"secret"`
	Host               types.String `tfsdk:"host"`
	Plan               types.String `tfsdk:"plan"`
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func emptySet() types.Set { return types.SetValueMust(types.StringType, []attr.Value{}) }

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Hibira app: one set of Pusher-compatible credentials (app ID, key and secret) with its own settings. " +
			"Manage its webhooks with `hibira_webhook` and its push notifications with `hibira_push`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The app ID (the Pusher `app_id`).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The app's name, up to 100 characters.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"cluster": schema.StringAttribute{
				MarkdownDescription: "The region the app runs in, such as `hkg`. Defaults to the account's default region. Changing it replaces the app.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether clients and the HTTP API can use the app. Defaults to `true`.",
				Optional:            true, Computed: true, Default: booldefault.StaticBool(true),
			},
			"client_events": schema.BoolAttribute{
				MarkdownDescription: "Whether clients may trigger `client-` events on private and presence channels. Defaults to `false`.",
				Optional:            true, Computed: true, Default: booldefault.StaticBool(false),
			},
			"subscription_count": schema.BoolAttribute{
				MarkdownDescription: "Whether channels send `pusher:subscription_count` events. Defaults to `false`.",
				Optional:            true, Computed: true, Default: booldefault.StaticBool(false),
			},
			"allowed_origins": schema.SetAttribute{
				MarkdownDescription: "The web origins (such as `https://example.com`) that may connect. Empty allows any origin.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true, Default: setdefault.StaticValue(emptySet()),
			},
			"history": schema.BoolAttribute{
				MarkdownDescription: "Whether channel history is kept (plans with history only). Defaults to `false`.",
				Optional:            true, Computed: true, Default: booldefault.StaticBool(false),
			},
			"api_allowlist": schema.SetAttribute{
				MarkdownDescription: "IP addresses or CIDR ranges the app's HTTP API accepts (plans with an API allowlist only). Empty accepts any.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true, Default: setdefault.StaticValue(emptySet()),
			},
			"edge_mode": schema.StringAttribute{
				MarkdownDescription: "Edge fan-out: `auto`, `direct` or `edge` (plans with edge fan-out only). Left unset, the app keeps its current mode.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.OneOf("auto", "direct", "edge")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret_rotation": schema.StringAttribute{
				MarkdownDescription: "Any value; changing it rotates the app's secret. The previous secret keeps working for `secret_grace_minutes`.",
				Optional:            true,
			},
			"secret_grace_minutes": schema.Int64Attribute{
				MarkdownDescription: "How long the previous secret keeps working after a rotation, in minutes. Defaults to `60`.",
				Optional:            true, Computed: true, Default: int64default.StaticInt64(60),
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "The app key, used by clients.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "The app secret, for your server. Hibira returns it only when the app is created or its secret is rotated, " +
					"so it is empty for an imported app until `secret_rotation` changes.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"host": schema.StringAttribute{
				MarkdownDescription: "The WebSocket host clients connect to (`wsHost` in pusher-js).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"plan": schema.StringAttribute{
				MarkdownDescription: "The account plan the app runs on.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	origins := stringsOf(ctx, plan.AllowedOrigins, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.CreateApp(ctx, client.AppCreate{Name: plan.Name.ValueString(), AllowedOrigins: origins,
		ClientEvents: plan.ClientEvents.ValueBool(), Cluster: plan.Cluster.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Creating Hibira app failed", err.Error())
		return
	}
	secret := app.Secret
	// Save the app straight away, so a failed update below leaves it in
	// the state instead of orphaned.
	plan.Secret = types.StringValue(secret)
	if final, err := r.applyChanges(ctx, app.ID, plan, &resp.Diagnostics); err == nil {
		app = final
	} else {
		resp.Diagnostics.AddError("Configuring Hibira app failed", err.Error())
	}
	state := plan
	setAppState(ctx, &state, app, &resp.Diagnostics)
	state.Secret = types.StringValue(secret)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// applyChanges brings the app's settings to plan; the create call only
// takes some of them.
func (r *appResource) applyChanges(ctx context.Context, id string, plan appModel, diags *diag.Diagnostics) (*client.App, error) {
	origins := stringsOf(ctx, plan.AllowedOrigins, diags)
	allowlist := stringsOf(ctx, plan.APIAllowlist, diags)
	if diags.HasError() {
		return nil, fmt.Errorf("invalid configuration")
	}
	return r.client.ModifyApp(ctx, id, func(app *client.App) (client.AppUpdate, bool) {
		var up client.AppUpdate
		changed := false
		if v := plan.Name.ValueString(); v != app.Name {
			up.Name, changed = &v, true
		}
		if v := plan.Enabled.ValueBool(); v != app.Enabled {
			up.Enabled, changed = &v, true
		}
		if v := plan.ClientEvents.ValueBool(); v != app.ClientEvents {
			up.ClientEvents, changed = &v, true
		}
		if v := plan.SubscriptionCount.ValueBool(); v != app.SubscriptionCount {
			up.SubscriptionCount, changed = &v, true
		}
		if !sameStrings(origins, app.AllowedOrigins) {
			up.AllowedOrigins, changed = &origins, true
		}
		if v := plan.History.ValueBool(); v != app.History {
			up.History, changed = &v, true
		}
		if !sameStrings(allowlist, app.APIAllowlist) {
			up.APIAllowlist, changed = &allowlist, true
		}
		if !plan.EdgeMode.IsNull() && !plan.EdgeMode.IsUnknown() {
			if v := plan.EdgeMode.ValueString(); app.Edge == nil || v != app.Edge.Mode {
				up.EdgeMode, changed = &v, true
			}
		}
		return up, changed
	})
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.App(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira app failed", err.Error())
		return
	}
	setAppState(ctx, &state, app, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.ID.ValueString()
	app, err := r.applyChanges(ctx, id, plan, &resp.Diagnostics)
	if err != nil {
		resp.Diagnostics.AddError("Updating Hibira app failed", err.Error())
		return
	}
	secret := prior.Secret
	if !plan.SecretRotation.Equal(prior.SecretRotation) && !plan.SecretRotation.IsNull() {
		rotated, err := r.client.RotateSecret(ctx, id, time.Duration(plan.SecretGraceMinutes.ValueInt64())*time.Minute)
		if err != nil {
			resp.Diagnostics.AddError("Rotating the Hibira app secret failed", err.Error())
			return
		}
		app, secret = rotated, types.StringValue(rotated.Secret)
	}
	state := plan
	state.ID = prior.ID
	setAppState(ctx, &state, app, &resp.Diagnostics)
	state.Secret = secret
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ModifyPlan marks the secret as changing when secret_rotation changes.
func (r *appResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planned, prior types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("secret_rotation"), &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("secret_rotation"), &prior)...)
	if !planned.IsNull() && !planned.Equal(prior) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("secret"), types.StringUnknown())...)
	}
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteApp(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting Hibira app failed", err.Error())
	}
}

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("secret"), types.StringNull())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("secret_grace_minutes"), types.Int64Value(60))...)
}

// setAppState copies what the API says about app into m. The secret is
// left alone: the API doesn't return it.
func setAppState(ctx context.Context, m *appModel, app *client.App, diags *diag.Diagnostics) {
	m.ID = types.StringValue(app.ID)
	m.Name = types.StringValue(app.Name)
	// A single-region deployment reports no cluster.
	if app.Cluster != "" || m.Cluster.IsNull() || m.Cluster.IsUnknown() {
		m.Cluster = types.StringValue(app.Cluster)
	}
	m.Enabled = types.BoolValue(app.Enabled)
	m.ClientEvents = types.BoolValue(app.ClientEvents)
	m.SubscriptionCount = types.BoolValue(app.SubscriptionCount)
	m.AllowedOrigins = setOf(ctx, app.AllowedOrigins, diags)
	m.History = types.BoolValue(app.History)
	m.APIAllowlist = setOf(ctx, app.APIAllowlist, diags)
	if app.Edge != nil {
		m.EdgeMode = types.StringValue(app.Edge.Mode)
	} else {
		m.EdgeMode = types.StringNull()
	}
	m.Key = types.StringValue(app.Key)
	m.Host = types.StringValue(app.Host())
	m.Plan = types.StringValue(app.Plan)
	if m.SecretGraceMinutes.IsNull() || m.SecretGraceMinutes.IsUnknown() {
		m.SecretGraceMinutes = types.Int64Value(60)
	}
}

func sameStrings(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
