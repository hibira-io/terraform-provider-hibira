package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*liveChatTeamResource)(nil)
	_ resource.ResourceWithConfigure   = (*liveChatTeamResource)(nil)
	_ resource.ResourceWithImportState = (*liveChatTeamResource)(nil)
)

func newLiveChatTeamResource() resource.Resource { return &liveChatTeamResource{} }

// liveChatTeamResource is a team of Live Chat agents.
type liveChatTeamResource struct{ liveChatBase }

type liveChatTeamModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Name        types.String `tfsdk:"name"`
	Agents      types.Set    `tfsdk:"agents"`
}

func (r *liveChatTeamResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_team"
}

func (r *liveChatTeamResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A team of agents in a [Hibira Live Chat](https://chat.hibira.io) workspace: conversations can be " +
			"sent to a team, and routing rules and the widget's topics can name it. Teams are on the Team and Business plans; a " +
			"workspace has at most 20. Import with `<workspace_id>/<team id>`." + liveChatAccess,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("The team's ID."),
			"workspace_id": workspaceIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "The team's name, shown to agents (and to visitors when a topic names it).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"agents": schema.SetAttribute{
				MarkdownDescription: "The members' emails. Each must already be an agent in the workspace (invite people in Live Chat's " +
					"Settings › Team first).",
				ElementType: types.StringType,
				Required:    true,
				Validators:  []validator.Set{setvalidator.SizeAtMost(200)},
			},
		},
	}
}

// agentIDs maps emails to agent IDs, refusing an email that is no agent.
func agentIDs(ctx context.Context, lc *client.LiveChat, emails []string) ([]string, error) {
	agents, err := lc.Agents(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(emails))
	for _, e := range emails {
		i := slices.IndexFunc(agents, func(a client.LiveChatAgent) bool { return strings.EqualFold(a.Email, e) })
		if i < 0 {
			return nil, fmt.Errorf("%s is not an agent in this workspace: invite them in Live Chat's Settings › Team first", e)
		}
		ids = append(ids, agents[i].ID)
	}
	return ids, nil
}

// apply copies t into m, naming its agents by email.
func (m *liveChatTeamModel) apply(ctx context.Context, lc *client.LiveChat, t *client.LiveChatTeam, diags *diag.Diagnostics) {
	agents, err := lc.Agents(ctx)
	if err != nil {
		addErr(diags, "Reading Live Chat agents", err)
		return
	}
	emails := []string{}
	for _, id := range t.AgentIDs {
		if i := slices.IndexFunc(agents, func(a client.LiveChatAgent) bool { return a.ID == id }); i >= 0 {
			emails = append(emails, strings.ToLower(agents[i].Email))
		}
	}
	m.ID = types.StringValue(t.ID)
	m.Name = types.StringValue(t.Name)
	m.Agents = setOf(ctx, emails, diags)
}

func (r *liveChatTeamResource) save(ctx context.Context, m *liveChatTeamModel, create bool, diags *diag.Diagnostics) {
	lc := r.lc(m.WorkspaceID)
	emails := stringsOf(ctx, m.Agents, diags)
	if diags.HasError() {
		return
	}
	ids, err := agentIDs(ctx, lc, emails)
	if err != nil {
		addErr(diags, "Saving Live Chat team", err)
		return
	}
	var t *client.LiveChatTeam
	if create {
		t, err = lc.CreateTeam(ctx, m.Name.ValueString(), ids)
	} else {
		t, err = lc.UpdateTeam(ctx, m.ID.ValueString(), m.Name.ValueString(), ids)
	}
	if err != nil {
		addErr(diags, "Saving Live Chat team", err)
		return
	}
	m.apply(ctx, lc, t, diags)
}

func (r *liveChatTeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatTeamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.save(ctx, &plan, true, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *liveChatTeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state liveChatTeamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lc := r.lc(state.WorkspaceID)
	t, err := lc.Team(ctx, state.ID.ValueString())
	if gone(ctx, err, &resp.State) {
		return
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Reading Live Chat team", err)
		return
	}
	state.apply(ctx, lc, t, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *liveChatTeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan liveChatTeamModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.save(ctx, &plan, false, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *liveChatTeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state liveChatTeamModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.lc(state.WorkspaceID).DeleteTeam(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		addErr(&resp.Diagnostics, "Deleting Live Chat team", err)
	}
}

func (r *liveChatTeamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req.ID, "workspace_id", "ws_123/team_abc", resp)
}
