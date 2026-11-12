package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*approvalsWorkspaceResource)(nil)
	_ resource.ResourceWithConfigure   = (*approvalsWorkspaceResource)(nil)
	_ resource.ResourceWithImportState = (*approvalsWorkspaceResource)(nil)
)

var (
	lowerEmail = regexp.MustCompile(`^[^A-Z\s@]+@[^A-Z\s@]+\.[^A-Z\s@]+$`)
	groupSlug  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

func newApprovalsWorkspaceResource() resource.Resource { return &approvalsWorkspaceResource{} }

// approvalsWorkspaceResource is a Hibira Approvals workspace: its name,
// members and approver groups.
type approvalsWorkspaceResource struct{ client *client.Client }

type approvalsWorkspaceModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Plan    types.String `tfsdk:"plan"`
	AppID   types.String `tfsdk:"app_id"`
	Members types.Set    `tfsdk:"members"`
	Groups  types.List   `tfsdk:"groups"`
}

type memberModel struct {
	Email types.String `tfsdk:"email"`
	Role  types.String `tfsdk:"role"`
}

type groupModel struct {
	Slug    types.String `tfsdk:"slug"`
	Name    types.String `tfsdk:"name"`
	Members types.Set    `tfsdk:"members"`
}

var (
	memberType = types.ObjectType{AttrTypes: map[string]attr.Type{"email": types.StringType, "role": types.StringType}}
	groupType  = types.ObjectType{AttrTypes: map[string]attr.Type{"slug": types.StringType, "name": types.StringType, "members": types.SetType{ElemType: types.StringType}}}
)

func (r *approvalsWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_approvals_workspace"
}

func (r *approvalsWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A [Hibira Approvals](https://hibira.io/approvals) workspace: the people who answer approval requests " +
			"from agents and workflows, their roles and approver groups. The API token acts as its owner's email (which must be " +
			"verified): a new workspace makes that email its admin, and an existing one can be managed only by its admins. " +
			"Each workspace has its own app (`app_id`); tools connect to it with `npx -y @hibira-io/mcp-approval-gate login` or the " +
			"app's keys in the workspace's Settings.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The workspace's ID (`apw_...`).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The workspace's name, 1 to 60 characters.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 60)},
			},
			"plan": schema.StringAttribute{
				MarkdownDescription: "The workspace's plan (`free`, `starter`, `team`, `business`, `enterprise`). Changed on approvals.hibira.io.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The workspace's own app, which requests are sent through.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"members": schema.SetNestedAttribute{
				MarkdownDescription: "Everyone in the workspace. Include the token owner's email as an `admin`: Terraform would otherwise " +
					"remove its own access. Unset: members are managed on approvals.hibira.io and only read here. The plan limits how many " +
					"admins and approvers there can be; viewers are free.",
				Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"email": schema.StringAttribute{
						MarkdownDescription: "The person's email, in lowercase. They sign in to approvals.hibira.io with it.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(lowerEmail, "must be an email address in lowercase")},
					},
					"role": schema.StringAttribute{
						MarkdownDescription: "`admin` (settings, members, keys), `approver` (answers requests) or `viewer` (sees them).",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf("admin", "approver", "viewer")},
					},
				}},
			},
			"groups": schema.ListNestedAttribute{
				MarkdownDescription: "Approver groups: a request sent to a group's `slug` (for example `approvers: [\"dba\"]`) can be answered " +
					"only by its members. Unset: managed on approvals.hibira.io.",
				Optional: true, Computed: true,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{
						MarkdownDescription: "The group's ID in requests: lowercase letters, digits and dashes, at most 40.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(groupSlug, "must be lowercase letters, digits and dashes, at most 40")},
					},
					"name": schema.StringAttribute{
						MarkdownDescription: "Shown in the inbox.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.LengthBetween(1, 60)},
					},
					"members": schema.SetAttribute{
						MarkdownDescription: "Members' emails (they must be in the workspace), 1 to 50.",
						ElementType:         types.StringType,
						Required:            true,
					},
				}},
			},
		},
	}
}

func (r *approvalsWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

// wanted reads the configured members and groups; nil means not managed.
func (m approvalsWorkspaceModel) wanted(ctx context.Context, diags *diag.Diagnostics) ([]client.ApprovalsMember, []client.ApproverGroup) {
	var members []client.ApprovalsMember
	if !m.Members.IsNull() && !m.Members.IsUnknown() {
		var list []memberModel
		diags.Append(m.Members.ElementsAs(ctx, &list, false)...)
		members = []client.ApprovalsMember{}
		for _, x := range list {
			members = append(members, client.ApprovalsMember{Email: x.Email.ValueString(), Role: x.Role.ValueString()})
		}
	}
	var groups []client.ApproverGroup
	if !m.Groups.IsNull() && !m.Groups.IsUnknown() {
		var list []groupModel
		diags.Append(m.Groups.ElementsAs(ctx, &list, false)...)
		groups = []client.ApproverGroup{}
		for _, g := range list {
			groups = append(groups, client.ApproverGroup{Slug: g.Slug.ValueString(), Name: g.Name.ValueString(), Members: stringsOf(ctx, g.Members, diags)})
		}
	}
	return members, groups
}

// apply copies the workspace into m.
func (m *approvalsWorkspaceModel) apply(ctx context.Context, w *client.ApprovalsWorkspace, diags *diag.Diagnostics) {
	m.ID = types.StringValue(w.ID)
	m.Name = types.StringValue(w.Name)
	m.Plan = types.StringValue(w.Plan)
	m.AppID = types.StringValue(w.AppID)
	members := make([]attr.Value, 0, len(w.Members))
	for _, x := range w.Members {
		o, d := types.ObjectValue(memberType.AttrTypes, map[string]attr.Value{"email": types.StringValue(strings.ToLower(x.Email)), "role": types.StringValue(x.Role)})
		diags.Append(d...)
		members = append(members, o)
	}
	s, d := types.SetValue(memberType, members)
	diags.Append(d...)
	m.Members = s
	groups := make([]attr.Value, 0, len(w.Groups))
	for _, g := range w.Groups {
		lower := make([]string, len(g.Members))
		for i, e := range g.Members {
			lower[i] = strings.ToLower(e)
		}
		o, d := types.ObjectValue(groupType.AttrTypes, map[string]attr.Value{"slug": types.StringValue(g.Slug), "name": types.StringValue(g.Name),
			"members": setOf(ctx, lower, diags)})
		diags.Append(d...)
		groups = append(groups, o)
	}
	l, d := types.ListValue(groupType, groups)
	diags.Append(d...)
	m.Groups = l
}

// reconcile brings w's members and groups to the wanted ones (nil: leave
// as they are): first add and change members, then groups, then remove
// the members no longer listed, so a group never names someone gone.
func (r *approvalsWorkspaceResource) reconcile(ctx context.Context, w *client.ApprovalsWorkspace, members []client.ApprovalsMember, groups []client.ApproverGroup) (*client.ApprovalsWorkspace, error) {
	if members != nil && w.You != "" && !slices.ContainsFunc(members, func(m client.ApprovalsMember) bool {
		return strings.EqualFold(m.Email, w.You) && m.Role == "admin"
	}) {
		return nil, fmt.Errorf("members must include %s (the API token's owner) as an admin, or Terraform would lose access to the workspace", w.You)
	}
	var err error
	if members != nil {
		have := map[string]string{}
		for _, m := range w.Members {
			have[strings.ToLower(m.Email)] = m.Role
		}
		// Promote before demoting, so the workspace always has an admin.
		ordered := slices.Clone(members)
		slices.SortStableFunc(ordered, func(a, b client.ApprovalsMember) int {
			return boolInt(b.Role == "admin") - boolInt(a.Role == "admin")
		})
		for _, m := range ordered {
			if have[m.Email] == m.Role {
				continue
			}
			if w, err = r.client.SetApprovalsMember(ctx, w.ID, m); err != nil {
				return nil, fmt.Errorf("member %s: %w", m.Email, err)
			}
		}
	}
	if groups != nil {
		if w, err = r.client.SetApproverGroups(ctx, w.ID, groups); err != nil {
			return nil, fmt.Errorf("groups: %w", err)
		}
	}
	if members != nil {
		keep := map[string]bool{}
		for _, m := range members {
			keep[m.Email] = true
		}
		for _, m := range w.Members {
			if keep[strings.ToLower(m.Email)] {
				continue
			}
			if err := r.client.RemoveApprovalsMember(ctx, w.ID, m.Email); err != nil && !client.IsNotFound(err) {
				return nil, fmt.Errorf("removing %s: %w", m.Email, err)
			}
		}
		if w, err = r.client.ApprovalsWorkspace(ctx, w.ID); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *approvalsWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan approvalsWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	members, groups := plan.wanted(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.CreateApprovalsWorkspace(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating Hibira Approvals workspace failed", err.Error())
		return
	}
	// Keep the workspace in the state even if setting it up fails, so the
	// next apply finishes it instead of making another.
	plan.apply(ctx, w, &resp.Diagnostics)
	if w2, err := r.reconcile(ctx, w, members, groups); err != nil {
		resp.Diagnostics.AddError("Setting up Hibira Approvals workspace failed", err.Error())
	} else {
		plan.apply(ctx, w2, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *approvalsWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state approvalsWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, err := r.client.ApprovalsWorkspace(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira Approvals workspace failed", err.Error())
		return
	}
	state.apply(ctx, w, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *approvalsWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state approvalsWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	members, groups := plan.wanted(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	w, err := r.client.ApprovalsWorkspace(ctx, id)
	if err == nil && w.Name != plan.Name.ValueString() {
		w, err = r.client.RenameApprovalsWorkspace(ctx, id, plan.Name.ValueString())
	}
	if err == nil {
		w, err = r.reconcile(ctx, w, members, groups)
	}
	if err != nil {
		resp.Diagnostics.AddError("Updating Hibira Approvals workspace failed", err.Error())
		return
	}
	plan.apply(ctx, w, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *approvalsWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state approvalsWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApprovalsWorkspace(ctx, state.ID.ValueString())
	var e *client.Error
	if errors.As(err, &e) && (e.Status == 404 || e.Status == 405) {
		resp.Diagnostics.AddWarning("Approvals workspace kept",
			fmt.Sprintf("Hibira does not delete Approvals workspaces through the API here, so %s (%s) was only removed from the Terraform state. "+
				"Delete it in its Settings on approvals.hibira.io.", state.Name.ValueString(), state.ID.ValueString()))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Deleting Hibira Approvals workspace failed", err.Error())
	}
}

func (r *approvalsWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !strings.HasPrefix(req.ID, "apw_") {
		resp.Diagnostics.AddError("Invalid import ID", "Use the workspace's ID, for example apw_0123456789abcdef (in its Settings on approvals.hibira.io).")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
