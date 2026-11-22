package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*liveChatHoursResource)(nil)
	_ resource.ResourceWithConfigure   = (*liveChatHoursResource)(nil)
	_ resource.ResourceWithImportState = (*liveChatHoursResource)(nil)
)

var (
	// weekdays are in Live Chat's order (its week starts on Sunday).
	weekdays  = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	clockTime = regexp.MustCompile(`^(([01][0-9]|2[0-3]):[0-5][0-9]|24:00)$`)
	isoDate   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dayType   = types.ObjectType{AttrTypes: map[string]attr.Type{"from": types.StringType, "to": types.StringType}}
)

func newLiveChatBusinessHoursResource() resource.Resource { return &liveChatHoursResource{} }

// liveChatHoursResource is a Live Chat workspace's business hours; there
// is one per workspace.
type liveChatHoursResource struct{ liveChatBase }

type liveChatHoursModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	TimeZone    types.String `tfsdk:"time_zone"`
	Days        types.Map    `tfsdk:"days"`
	Closed      types.Set    `tfsdk:"closed_dates"`
	Away        types.String `tfsdk:"away_message"`
}

type dayModel struct {
	From types.String `tfsdk:"from"`
	To   types.String `tfsdk:"to"`
}

func (r *liveChatHoursResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_business_hours"
}

func (r *liveChatHoursResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The business hours of a [Hibira Live Chat](https://chat.hibira.io) workspace: outside them the " +
			"widget shows the away message and asks for an email instead of promising a quick answer. A workspace has one set; " +
			"destroying the resource turns business hours off. Business hours are on the Team and Business plans. Import with " +
			"the workspace's ID." + liveChatAccess,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": workspaceIDAttribute(),
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the hours apply. Defaults to `true`.",
				Optional:            true, Computed: true,
				Default: booldefault.StaticBool(true),
			},
			"time_zone": schema.StringAttribute{
				MarkdownDescription: "An IANA time zone, such as `Asia/Taipei`. Defaults to `UTC`.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString("UTC"),
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"days": schema.MapNestedAttribute{
				MarkdownDescription: "Opening hours by weekday (`monday` … `sunday`); a day left out is closed. Times are `HH:MM`, " +
					"`to` may be `24:00`.",
				Required: true,
				Validators: []validator.Map{mapvalidator.KeysAre(stringvalidator.OneOf(weekdays...)),
					mapvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"from": schema.StringAttribute{
						MarkdownDescription: "Opens at, such as `09:00`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(clockTime, "must be HH:MM")},
					},
					"to": schema.StringAttribute{
						MarkdownDescription: "Closes at, such as `18:00`; after `from`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(clockTime, "must be HH:MM")},
					},
				}},
			},
			"closed_dates": schema.SetAttribute{
				MarkdownDescription: "Holidays and other closed days, as `YYYY-MM-DD` in `time_zone`; at most 60.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true,
				Default: setdefault.StaticValue(emptySet()),
				Validators: []validator.Set{setvalidator.SizeAtMost(60),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(isoDate, "must be a date like 2026-12-25"))},
			},
			"away_message": schema.StringAttribute{
				MarkdownDescription: "Shown in the widget outside business hours, at most 300 characters. Empty (default): Live Chat's own.",
				Optional:            true, Computed: true,
				Default:    stringdefault.StaticString(""),
				Validators: []validator.String{stringvalidator.LengthAtMost(300)},
			},
		},
	}
}

func (m liveChatHoursModel) hours(ctx context.Context, diags *diag.Diagnostics) client.BusinessHours {
	h := client.BusinessHours{On: m.Enabled.ValueBool(), TimeZone: m.TimeZone.ValueString(), Away: m.Away.ValueString(),
		Closed: stringsOf(ctx, m.Closed, diags)}
	days := map[string]dayModel{}
	diags.Append(m.Days.ElementsAs(ctx, &days, false)...)
	for i, name := range weekdays {
		d, ok := days[name]
		if !ok {
			h.Week[i] = client.DayHours{Off: true}
			continue
		}
		h.Week[i] = client.DayHours{From: d.From.ValueString(), To: d.To.ValueString()}
	}
	slices.Sort(h.Closed)
	return h
}

func (m *liveChatHoursModel) apply(ctx context.Context, h *client.BusinessHours, diags *diag.Diagnostics) {
	m.ID = m.WorkspaceID
	m.Enabled = types.BoolValue(h.On)
	m.TimeZone = types.StringValue(h.TimeZone)
	m.Away = types.StringValue(h.Away)
	m.Closed = setOf(ctx, h.Closed, diags)
	days := map[string]attr.Value{}
	for i, d := range h.Week {
		if d.Off {
			continue
		}
		o, dd := types.ObjectValue(dayType.AttrTypes, map[string]attr.Value{"from": types.StringValue(d.From), "to": types.StringValue(d.To)})
		diags.Append(dd...)
		days[weekdays[i]] = o
	}
	v, dd := types.MapValue(dayType, days)
	diags.Append(dd...)
	m.Days = v
}

func (r *liveChatHoursResource) set(ctx context.Context, m *liveChatHoursModel, diags *diag.Diagnostics) {
	h := m.hours(ctx, diags)
	if diags.HasError() {
		return
	}
	out, err := r.lc(m.WorkspaceID).SetHours(ctx, h)
	if err != nil {
		addErr(diags, "Saving Live Chat business hours", err)
		return
	}
	m.apply(ctx, out, diags)
}

func (r *liveChatHoursResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatHoursModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *liveChatHoursResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state liveChatHoursModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.WorkspaceID.IsNull() {
		state.WorkspaceID = state.ID
	}
	h, err := r.lc(state.WorkspaceID).Hours(ctx)
	if gone(ctx, err, &resp.State) {
		return
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Reading Live Chat business hours", err)
		return
	}
	state.apply(ctx, h, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *liveChatHoursResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan liveChatHoursModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

// Delete turns business hours off and keeps the rest as it was.
func (r *liveChatHoursResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state liveChatHoursModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lc := r.lc(state.WorkspaceID)
	h, err := lc.Hours(ctx)
	if client.IsNotFound(err) {
		return
	}
	if err == nil && h.On {
		h.On = false
		_, err = lc.SetHours(ctx, *h)
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Turning off Live Chat business hours", err)
	}
}

func (r *liveChatHoursResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Use the workspace's ID, for example %s.", "ws_123"))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), req.ID)...)
}
