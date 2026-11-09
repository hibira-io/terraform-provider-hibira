package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*waitingRoomResource)(nil)
	_ resource.ResourceWithConfigure   = (*waitingRoomResource)(nil)
	_ resource.ResourceWithImportState = (*waitingRoomResource)(nil)
)

var roomName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func newWaitingRoomResource() resource.Resource { return &waitingRoomResource{} }

// waitingRoomResource is one waiting room of an app.
type waitingRoomResource struct{ client *client.Client }

type waitingRoomModel struct {
	ID             types.String `tfsdk:"id"`
	AppID          types.String `tfsdk:"app_id"`
	Name           types.String `tfsdk:"name"`
	AdmitPerMinute types.Int64  `tfsdk:"admit_per_minute"`
	State          types.String `tfsdk:"state"`
	StartsAt       types.String `tfsdk:"starts_at"`
	Burst          types.Int64  `tfsdk:"burst"`
	PassTTLSeconds types.Int64  `tfsdk:"pass_ttl_seconds"`
	Title          types.String `tfsdk:"title"`
	Notice         types.String `tfsdk:"notice"`
	ReturnURLs     types.List   `tfsdk:"return_urls"`
	Channel        types.String `tfsdk:"channel"`
}

func (r *waitingRoomResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_waiting_room"
}

func (r *waitingRoomResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A waiting room of a Hibira app: visitors take a queue number on the hosted waiting page and are " +
			"let in at `admit_per_minute`, each with a signed pass your edge or server checks. Live counters " +
			"(admitted, waiting) are not part of the state. A room nobody changes or joins for 30 days is removed by Hibira; " +
			"the next plan then creates it again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<app_id>/<name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The app's ID.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The room's name in URLs, passes and the `hibira_wr_<name>` cookie: 1 to 64 letters, digits, `-` or `_`. Changing it replaces the room.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(roomName, "must be 1 to 64 letters, digits, - or _")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"admit_per_minute": schema.Int64Attribute{
				MarkdownDescription: "How many visitors are let in per minute.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "`open` admits at the rate, `paused` keeps the queue but admits nobody, `closed` refuses new numbers (sold out). Default `open`.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString("open"),
				Validators: []validator.String{stringvalidator.OneOf("open", "paused", "closed")},
			},
			"starts_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 time before which the line does not move (visitors can already take numbers). Empty or unset: no start time.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString(""),
				Validators: []validator.String{rfc3339OrEmpty{}},
			},
			"burst": schema.Int64Attribute{
				MarkdownDescription: "How far the line may run ahead while the queue is empty. Default: ten seconds of admissions.",
				Optional:            true, Computed: true,
				Validators: []validator.Int64{int64validator.AtLeast(0)},
			},
			"pass_ttl_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long a pass stays valid, 60 to 86400 seconds. Default 1800.",
				Optional:            true, Computed: true,
				Validators:    []validator.Int64{int64validator.Between(60, 86400)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"title": schema.StringAttribute{
				MarkdownDescription: "Shown on the waiting page.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString(""),
			},
			"notice": schema.StringAttribute{
				MarkdownDescription: "An announcement shown on the waiting page.",
				Optional:            true, Computed: true, Default: stringdefault.StaticString(""),
			},
			"return_urls": schema.ListAttribute{
				MarkdownDescription: "Where the waiting page may send admitted visitors (https, or http for localhost): the same host and a path prefix. The first is the default.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true, Default: listdefault.StaticValue(types.ListValueMust(types.StringType, nil)),
				Validators: []validator.List{listvalidator.ValueStringsAre(stringvalidator.RegexMatches(httpsURL, "must be an http:// or https:// URL"))},
			},
			"channel": schema.StringAttribute{
				MarkdownDescription: "The cache channel the room's status is broadcast on (`cache-waitroom-<name>`), for your own waiting page.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *waitingRoomResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func (m waitingRoomModel) input(ctx context.Context, diags *diag.Diagnostics) client.WaitingRoomInput {
	in := client.WaitingRoomInput{
		AdmitPerMinute: m.AdmitPerMinute.ValueInt64Pointer(),
		State:          m.State.ValueStringPointer(),
		StartsAt:       m.StartsAt.ValueStringPointer(),
		Title:          m.Title.ValueStringPointer(),
		Notice:         m.Notice.ValueStringPointer(),
	}
	if !m.Burst.IsUnknown() && !m.Burst.IsNull() {
		in.Burst = m.Burst.ValueInt64Pointer()
	}
	if !m.PassTTLSeconds.IsUnknown() && !m.PassTTLSeconds.IsNull() {
		in.PassTTLSeconds = m.PassTTLSeconds.ValueInt64Pointer()
	}
	urls := []string{}
	if !m.ReturnURLs.IsNull() && !m.ReturnURLs.IsUnknown() {
		diags.Append(m.ReturnURLs.ElementsAs(ctx, &urls, false)...)
	}
	in.ReturnURLs = &urls
	return in
}

// apply copies what the API answered into m, keeping the configured
// spelling of starts_at when it is the same instant.
func (m *waitingRoomModel) apply(ctx context.Context, w *client.WaitingRoom, diags *diag.Diagnostics) {
	m.ID = types.StringValue(m.AppID.ValueString() + "/" + w.Room)
	m.Name = types.StringValue(w.Room)
	m.AdmitPerMinute = types.Int64Value(w.AdmitPerMinute)
	m.State = types.StringValue(w.State)
	if !sameInstant(m.StartsAt.ValueString(), w.StartsAt) {
		m.StartsAt = types.StringValue(w.StartsAt)
	}
	m.Burst = types.Int64Value(w.Burst)
	m.PassTTLSeconds = types.Int64Value(w.PassTTLSeconds)
	m.Title = types.StringValue(w.Title)
	m.Notice = types.StringValue(w.Notice)
	urls := w.ReturnURLs
	if urls == nil {
		urls = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, urls)
	diags.Append(d...)
	m.ReturnURLs = l
	m.Channel = types.StringValue(w.Channel)
}

func sameInstant(a, b string) bool {
	if a == b {
		return true
	}
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	return errA == nil && errB == nil && ta.Equal(tb)
}

func (r *waitingRoomResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan waitingRoomModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	in := plan.input(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	appID, name := plan.AppID.ValueString(), plan.Name.ValueString()
	w, err := r.client.CreateWaitingRoom(ctx, appID, name, in)
	if err != nil {
		var e *client.Error
		if errors.As(err, &e) && e.Status == 409 {
			resp.Diagnostics.AddError("Waiting room already exists",
				fmt.Sprintf("App %s already has a waiting room %q. Import it with: terraform import <address> %s/%s", appID, name, appID, name))
			return
		}
		resp.Diagnostics.AddError("Creating Hibira waiting room failed", err.Error())
		return
	}
	plan.apply(ctx, w, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *waitingRoomResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state waitingRoomModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.WaitingRoom(ctx, state.AppID.ValueString(), state.Name.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira waiting room failed", err.Error())
		return
	}
	state.apply(ctx, w, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *waitingRoomResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan waitingRoomModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	in := plan.input(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.UpdateWaitingRoom(ctx, plan.AppID.ValueString(), plan.Name.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Updating Hibira waiting room failed", err.Error())
		return
	}
	plan.apply(ctx, w, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *waitingRoomResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state waitingRoomModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteWaitingRoom(ctx, state.AppID.ValueString(), state.Name.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting Hibira waiting room failed", err.Error())
	}
}

func (r *waitingRoomResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	appID, name, ok := strings.Cut(req.ID, "/")
	if !ok || appID == "" || !roomName.MatchString(name) {
		resp.Diagnostics.AddError("Invalid import ID", "Use <app_id>/<room name>, for example app_123/presale.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), appID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

// rfc3339OrEmpty accepts "" or an RFC 3339 time.
type rfc3339OrEmpty struct{}

func (rfc3339OrEmpty) Description(context.Context) string { return "must be empty or an RFC 3339 time" }
func (v rfc3339OrEmpty) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (rfc3339OrEmpty) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || req.ConfigValue.ValueString() == "" {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid starts_at", "Use an RFC 3339 time such as 2026-12-04T12:00:00+09:00.")
	}
}
