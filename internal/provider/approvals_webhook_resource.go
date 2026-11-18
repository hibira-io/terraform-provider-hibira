package provider

import (
	"context"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*approvalsWebhookResource)(nil)
	_ resource.ResourceWithConfigure   = (*approvalsWebhookResource)(nil)
	_ resource.ResourceWithImportState = (*approvalsWebhookResource)(nil)
)

// approvalsEvents are the events Hibira Approvals posts.
var approvalsEvents = []string{"approval.requested", "approval.decided", "approval.expired"}

func newApprovalsWebhookResource() resource.Resource { return &approvalsWebhookResource{} }

// approvalsWebhookResource is an endpoint Hibira Approvals posts requests
// and decisions to.
type approvalsWebhookResource struct{ client *client.Client }

type approvalsWebhookModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	URL         types.String `tfsdk:"url"`
	Events      types.Set    `tfsdk:"events"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Secret      types.String `tfsdk:"secret"`
}

func (r *approvalsWebhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_approvals_webhook"
}

func (r *approvalsWebhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	all := make([]attr.Value, len(approvalsEvents))
	for i, e := range approvalsEvents {
		all[i] = types.StringValue(e)
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A webhook endpoint of a [Hibira Approvals](https://hibira.io/approvals) workspace: Hibira posts each new " +
			"request, decision and expiry to it, signed with the endpoint's `secret` (`Hibira-Signature`). A workspace has up to 5. " +
			"The API token's owner must be an admin of the workspace; removing an endpoint needs a token with delete access. " +
			"Import with `<workspace_id>/<webhook id>`; an imported endpoint's `secret` stays empty. Needs pusher-server#898.\n\n" +
			"**State.** The state holds `secret` in plain text: use an encrypted backend.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("The endpoint's ID (`wh_...`)."),
			"workspace_id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`apw_...`), such as `hibira_approvals_workspace.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "The https URL Hibira posts to.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(httpsURL, "must be an https:// URL")},
			},
			"events": schema.SetAttribute{
				MarkdownDescription: "The events to send: " + strings.Join(quoted(approvalsEvents), ", ") + ". Defaults to all.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true,
				Default: setdefault.StaticValue(types.SetValueMust(types.StringType, all)),
				Validators: []validator.Set{setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(approvalsEvents...))},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Hibira posts to the endpoint. Defaults to `true`.",
				Optional:            true, Computed: true,
				Default: booldefault.StaticBool(true),
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "The signing secret, set when the endpoint is made. Check `Hibira-Signature` with it.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *approvalsWebhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func (r *approvalsWebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan approvalsWebhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	events := stringsOf(ctx, plan.Events, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	slices.Sort(events)
	h, err := r.client.CreateApprovalsWebhook(ctx, plan.WorkspaceID.ValueString(),
		client.ApprovalsWebhook{URL: plan.URL.ValueString(), Events: events, Disabled: !plan.Enabled.ValueBool()})
	if err != nil {
		addErr(&resp.Diagnostics, "Creating Hibira Approvals webhook", err)
		return
	}
	plan.ID = types.StringValue(h.ID)
	plan.Secret = types.StringValue(h.Secret)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *approvalsWebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state approvalsWebhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := r.client.ApprovalsWebhooks(ctx, state.WorkspaceID.ValueString())
	if gone(ctx, err, &resp.State) {
		return
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Reading Hibira Approvals webhook", err)
		return
	}
	i := slices.IndexFunc(list, func(h client.ApprovalsWebhook) bool { return h.ID == state.ID.ValueString() })
	if i < 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	h := list[i]
	state.URL = types.StringValue(h.URL)
	state.Events = setOf(ctx, h.Events, &resp.Diagnostics)
	state.Enabled = types.BoolValue(!h.Disabled)
	if state.Secret.IsNull() || state.Secret.IsUnknown() {
		state.Secret = types.StringValue("")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *approvalsWebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan approvalsWebhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	events := stringsOf(ctx, plan.Events, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	slices.Sort(events)
	_, err := r.client.UpdateApprovalsWebhook(ctx, plan.WorkspaceID.ValueString(),
		client.ApprovalsWebhook{ID: plan.ID.ValueString(), URL: plan.URL.ValueString(), Events: events, Disabled: !plan.Enabled.ValueBool()})
	if err != nil {
		addErr(&resp.Diagnostics, "Updating Hibira Approvals webhook", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *approvalsWebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state approvalsWebhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApprovalsWebhook(ctx, state.WorkspaceID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		addErr(&resp.Diagnostics, "Deleting Hibira Approvals webhook", err)
	}
}

func (r *approvalsWebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req.ID, "workspace_id", "apw_123/wh_abc", resp)
}
