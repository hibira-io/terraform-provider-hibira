package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*accountResource)(nil)
	_ resource.ResourceWithImportState = (*accountResource)(nil)
)

func newAccountResource() resource.Resource { return &accountResource{} }

type accountResource struct{ console *client.Console }

type accountModel struct {
	ID            types.String     `tfsdk:"id"`
	Email         types.String     `tfsdk:"email"`
	Name          types.String     `tfsdk:"name"`
	Plan          types.String     `tfsdk:"plan"`
	EffectivePlan types.String     `tfsdk:"effective_plan"`
	Status        types.String     `tfsdk:"status"`
	Note          types.String     `tfsdk:"note"`
	Tags          types.Set        `tfsdk:"tags"`
	ChatBeta      types.Bool       `tfsdk:"chat_beta"`
	CustomPlan    *customPlanModel `tfsdk:"custom_plan"`
}

type customPlanModel struct {
	MaxApps        types.Int64  `tfsdk:"max_apps"`
	MaxConnections types.Int64  `tfsdk:"max_connections"`
	DailyMessages  types.Int64  `tfsdk:"daily_messages"`
	MonthlyPrice   types.Int64  `tfsdk:"monthly_price"`
	Currency       types.String `tfsdk:"currency"`
	Note           types.String `tfsdk:"note"`
}

func (r *accountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_account"
}

func (r *accountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An operator's settings on an existing customer account: its custom (Enterprise) plan, " +
			"operator note and tags, and Chat API beta. Needs the provider's `console_token` (scopes `accounts:read` and `accounts:write`) " +
			"and the Cloudflare Access service token.\n\n" +
			"Terraform doesn't create or delete accounts: creating this resource takes over an account that exists, and destroying it " +
			"only stops managing it, leaving the account as it is. `note`, `tags` and `chat_beta` are managed only when set. " +
			"`custom_plan` is always managed: leaving it out means the account has no custom plan.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The account ID (`acct_...`). Look one up by email with `data.hibira_account`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"email":          schema.StringAttribute{MarkdownDescription: "The account's email.", Computed: true, PlanModifiers: keep},
			"name":           schema.StringAttribute{MarkdownDescription: "The account's name.", Computed: true, PlanModifiers: keep},
			"plan":           schema.StringAttribute{MarkdownDescription: "The plan the account pays for.", Computed: true},
			"effective_plan": schema.StringAttribute{MarkdownDescription: "The plan whose limits apply (`enterprise` with a custom plan).", Computed: true},
			"status":         schema.StringAttribute{MarkdownDescription: "The subscription status.", Computed: true},
			"note": schema.StringAttribute{
				MarkdownDescription: "The operator note (only operators see it). Managed only when set.",
				Optional:            true, Computed: true, PlanModifiers: keep,
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "The operator tags, such as `internal`. Managed only when set.",
				ElementType:         types.StringType, Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"chat_beta": schema.BoolAttribute{
				MarkdownDescription: "Whether the account is in the Chat API beta. Managed only when set.",
				Optional:            true, Computed: true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"custom_plan": schema.SingleNestedAttribute{
				MarkdownDescription: "Enterprise terms that replace the account's plan limits. Leave it out for none.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"max_apps":        schema.Int64Attribute{MarkdownDescription: "Apps the account may have.", Required: true},
					"max_connections": schema.Int64Attribute{MarkdownDescription: "Concurrent connections per app.", Required: true},
					"daily_messages":  schema.Int64Attribute{MarkdownDescription: "Messages per day.", Required: true},
					"monthly_price": schema.Int64Attribute{
						MarkdownDescription: "The price per month in the currency's smallest unit (cents for `usd`). Defaults to 0.",
						Optional:            true, Computed: true, Default: int64default.StaticInt64(0),
					},
					"currency": schema.StringAttribute{
						MarkdownDescription: "The currency in lower case, such as `usd`.", Required: true,
						Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z]{3}$`), "must be a lower-case ISO 4217 code, such as usd")},
					},
					"note": schema.StringAttribute{
						MarkdownDescription: "A note on the terms.",
						Optional:            true, Computed: true, Default: stringdefault.StaticString(""),
					},
				},
			},
		},
	}
}

func (r *accountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.console = configuredConsole(req.ProviderData, &resp.Diagnostics)
}

func (r *accountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan accountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	acct, err := r.console.Account(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira account failed", err.Error())
		return
	}
	if plan.CustomPlan == nil && acct.CustomPlan != nil {
		resp.Diagnostics.AddAttributeError(path.Root("custom_plan"), "Account has a custom plan",
			"The account already has a custom plan, which this configuration would remove. Add custom_plan to keep it, "+
				"or import the account (terraform import) to see the change in a plan first.")
		return
	}
	acct, err = r.apply(ctx, acct, &plan, &resp.Diagnostics)
	if acct == nil {
		if err != nil {
			resp.Diagnostics.AddError("Changing Hibira account failed", err.Error())
		}
		return
	}
	setAccountState(ctx, &plan, acct, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *accountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state accountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	acct, err := r.console.Account(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira account failed", err.Error())
		return
	}
	setAccountState(ctx, &state, acct, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *accountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan accountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	acct, err := r.console.Account(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira account failed", err.Error())
		return
	}
	acct, err = r.apply(ctx, acct, &plan, &resp.Diagnostics)
	if acct == nil {
		if err != nil {
			resp.Diagnostics.AddError("Changing Hibira account failed", err.Error())
		}
		return
	}
	setAccountState(ctx, &plan, acct, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only stops managing the account.
func (r *accountResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("Hibira account left as it is",
		"Terraform no longer manages this account. Its plan, labels and Chat API beta are unchanged.")
}

func (r *accountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply makes acct match plan, calling only the endpoints whose settings
// differ. It returns the account after the changes, or nil and the error.
func (r *accountResource) apply(ctx context.Context, acct *client.Account, plan *accountModel, diags *diag.Diagnostics) (*client.Account, error) {
	id := plan.ID.ValueString()
	want := customPlanOf(plan.CustomPlan)
	if !sameCustomPlan(want, acct.CustomPlan) {
		a, err := r.console.SetCustomPlan(ctx, id, want)
		if err != nil {
			return nil, err
		}
		acct = a
	}
	note, tags := acct.Note, acct.Tags
	if known(plan.Note) {
		note = plan.Note.ValueString()
	}
	if !plan.Tags.IsNull() && !plan.Tags.IsUnknown() {
		tags = stringsOf(ctx, plan.Tags, diags)
		if diags.HasError() {
			return nil, nil
		}
	}
	if note != acct.Note || !sameStrings(tags, acct.Tags) {
		a, err := r.console.SetLabels(ctx, id, note, tags)
		if err != nil {
			return nil, err
		}
		acct = a
	}
	if !plan.ChatBeta.IsNull() && !plan.ChatBeta.IsUnknown() && plan.ChatBeta.ValueBool() != acct.ChatBeta {
		a, err := r.console.SetChatBeta(ctx, id, plan.ChatBeta.ValueBool())
		if err != nil {
			return nil, err
		}
		acct = a
	}
	return acct, nil
}

func known(v types.String) bool { return !v.IsNull() && !v.IsUnknown() }

func customPlanOf(m *customPlanModel) *client.CustomPlan {
	if m == nil {
		return nil
	}
	return &client.CustomPlan{MaxApps: int(m.MaxApps.ValueInt64()), MaxConnections: int(m.MaxConnections.ValueInt64()),
		DailyMessageLimit: m.DailyMessages.ValueInt64(), MonthlyPrice: m.MonthlyPrice.ValueInt64(),
		Currency: m.Currency.ValueString(), Note: m.Note.ValueString()}
}

func sameCustomPlan(a, b *client.CustomPlan) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// setAccountState copies what the console says about acct into m.
func setAccountState(ctx context.Context, m *accountModel, acct *client.Account, diags *diag.Diagnostics) {
	m.ID = types.StringValue(acct.ID)
	m.Email = types.StringValue(acct.Email)
	m.Name = types.StringValue(acct.Name)
	m.Plan = types.StringValue(acct.Plan)
	m.EffectivePlan = types.StringValue(acct.EffectivePlan)
	m.Status = types.StringValue(acct.Status)
	m.Note = types.StringValue(acct.Note)
	m.Tags = setOf(ctx, acct.Tags, diags)
	m.ChatBeta = types.BoolValue(acct.ChatBeta)
	m.CustomPlan = nil
	if p := acct.CustomPlan; p != nil {
		m.CustomPlan = &customPlanModel{MaxApps: types.Int64Value(int64(p.MaxApps)), MaxConnections: types.Int64Value(int64(p.MaxConnections)),
			DailyMessages: types.Int64Value(p.DailyMessageLimit), MonthlyPrice: types.Int64Value(p.MonthlyPrice),
			Currency: types.StringValue(p.Currency), Note: types.StringValue(p.Note)}
	}
}
