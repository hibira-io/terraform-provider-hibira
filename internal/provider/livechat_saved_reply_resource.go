package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*liveChatSavedReplyResource)(nil)
	_ resource.ResourceWithConfigure   = (*liveChatSavedReplyResource)(nil)
	_ resource.ResourceWithImportState = (*liveChatSavedReplyResource)(nil)
)

var replyShortcut = regexp.MustCompile(`^$|^[\p{L}\p{N}_-]{1,32}$`)

func newLiveChatSavedReplyResource() resource.Resource { return &liveChatSavedReplyResource{} }

// liveChatSavedReplyResource is a canned reply agents insert with /shortcut.
type liveChatSavedReplyResource struct{ liveChatBase }

type liveChatSavedReplyModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Title       types.String `tfsdk:"title"`
	Shortcut    types.String `tfsdk:"shortcut"`
	Text        types.String `tfsdk:"text"`
}

func (r *liveChatSavedReplyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_saved_reply"
}

func (r *liveChatSavedReplyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A saved reply in a [Hibira Live Chat](https://chat.hibira.io) workspace: text agents insert by " +
			"typing `/` and its shortcut in the reply box. Saved replies are on the Team and Business plans. Import with " +
			"`<workspace_id>/<saved reply id>`." + liveChatAccess,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("The saved reply's ID."),
			"workspace_id": workspaceIDAttribute(),
			"title": schema.StringAttribute{
				MarkdownDescription: "Shown in the list of saved replies.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 80)},
			},
			"shortcut": schema.StringAttribute{
				MarkdownDescription: "What follows `/` in the reply box, unique in the workspace: up to 32 letters, digits, `-` and `_`. " +
					"Empty (default): none.",
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString(""),
				Validators: []validator.String{stringvalidator.RegexMatches(replyShortcut, "must be up to 32 letters, digits, - or _")},
			},
			"text": schema.StringAttribute{
				MarkdownDescription: "The reply's text, at most 2,000 characters.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 2000)},
			},
		},
	}
}

func (m *liveChatSavedReplyModel) apply(s *client.SavedReply) {
	m.ID = types.StringValue(s.ID)
	m.Title = types.StringValue(s.Title)
	m.Shortcut = types.StringValue(s.Shortcut)
	m.Text = types.StringValue(s.Text)
}

func (m liveChatSavedReplyModel) reply() client.SavedReply {
	return client.SavedReply{ID: m.ID.ValueString(), Title: m.Title.ValueString(), Shortcut: m.Shortcut.ValueString(), Text: m.Text.ValueString()}
}

func (r *liveChatSavedReplyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatSavedReplyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.lc(plan.WorkspaceID).CreateSavedReply(ctx, plan.reply())
	if err != nil {
		addErr(&resp.Diagnostics, "Creating Live Chat saved reply", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *liveChatSavedReplyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state liveChatSavedReplyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.lc(state.WorkspaceID).SavedReply(ctx, state.ID.ValueString())
	if gone(ctx, err, &resp.State) {
		return
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Reading Live Chat saved reply", err)
		return
	}
	state.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *liveChatSavedReplyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan liveChatSavedReplyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.lc(plan.WorkspaceID).UpdateSavedReply(ctx, plan.reply())
	if err != nil {
		addErr(&resp.Diagnostics, "Updating Live Chat saved reply", err)
		return
	}
	plan.apply(s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *liveChatSavedReplyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state liveChatSavedReplyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.lc(state.WorkspaceID).DeleteSavedReply(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		addErr(&resp.Diagnostics, "Deleting Live Chat saved reply", err)
	}
}

func (r *liveChatSavedReplyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req.ID, "workspace_id", "ws_123/sr_abc", resp)
}
