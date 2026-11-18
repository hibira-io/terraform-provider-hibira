package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*liveChatKnowledgeSourceResource)(nil)
	_ resource.ResourceWithConfigure   = (*liveChatKnowledgeSourceResource)(nil)
	_ resource.ResourceWithImportState = (*liveChatKnowledgeSourceResource)(nil)
)

func newLiveChatKnowledgeSourceResource() resource.Resource {
	return &liveChatKnowledgeSourceResource{}
}

// liveChatKnowledgeSourceResource is a website the AI assistant learns from.
type liveChatKnowledgeSourceResource struct{ liveChatBase }

type liveChatKnowledgeSourceModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	URL         types.String `tfsdk:"url"`
	Found       types.String `tfsdk:"found"`
	Status      types.String `tfsdk:"status"`
}

func (r *liveChatKnowledgeSourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_livechat_knowledge_source"
}

func (r *liveChatKnowledgeSourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A website the AI assistant of a [Hibira Live Chat](https://chat.hibira.io) workspace answers from. " +
			"Live Chat reads the site's `llms-full.txt`, `llms.txt` or sitemap (or the page itself) into the knowledge base and " +
			"keeps it up to date; destroying the resource removes the entries read from it. The plan sets how many web pages can " +
			"be read. Import with `<workspace_id>/<source id>`." + liveChatAccess,
		Attributes: map[string]schema.Attribute{
			"id":           idAttribute("The source's ID."),
			"workspace_id": workspaceIDAttribute(),
			"url": schema.StringAttribute{
				MarkdownDescription: "The site's or page's https URL. Changing it replaces the source.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(httpsURL, "must be an https:// URL")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"found": schema.StringAttribute{
				MarkdownDescription: "The file Live Chat read: the `llms-full.txt`, `llms.txt` or sitemap it found, or the page.",
				Computed:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`syncing`, `ok` or `failed`; reading a site takes a few minutes.",
				Computed:            true,
			},
		},
	}
}

func (m *liveChatKnowledgeSourceModel) apply(s *client.WebSource) {
	m.ID = types.StringValue(s.ID)
	m.URL = types.StringValue(s.URL)
	m.Found = types.StringValue(s.Found)
	m.Status = types.StringValue(s.Status)
}

func (r *liveChatKnowledgeSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan liveChatKnowledgeSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.lc(plan.WorkspaceID).AddWebSource(ctx, plan.URL.ValueString())
	if err != nil {
		addErr(&resp.Diagnostics, "Adding Live Chat knowledge source", err)
		return
	}
	u := plan.URL
	plan.apply(s)
	// Keep the URL as written; Live Chat may tidy it (a trailing slash).
	plan.URL = u
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *liveChatKnowledgeSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state liveChatKnowledgeSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.lc(state.WorkspaceID).WebSource(ctx, state.ID.ValueString())
	if gone(ctx, err, &resp.State) {
		return
	}
	if err != nil {
		addErr(&resp.Diagnostics, "Reading Live Chat knowledge source", err)
		return
	}
	u := state.URL
	state.apply(s)
	if !u.IsNull() && !u.IsUnknown() {
		state.URL = u
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update has nothing to change: url replaces the source.
func (r *liveChatKnowledgeSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan liveChatKnowledgeSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *liveChatKnowledgeSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state liveChatKnowledgeSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.lc(state.WorkspaceID).DeleteWebSource(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		addErr(&resp.Diagnostics, "Removing Live Chat knowledge source", err)
	}
}

func (r *liveChatKnowledgeSourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importWorkspaceChild(ctx, req.ID, "workspace_id", "ws_123/src_abc", resp)
}
