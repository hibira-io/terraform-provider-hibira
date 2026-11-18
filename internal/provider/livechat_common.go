package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// The hibira_livechat_* resources other than hibira_livechat_workspace
// use the provider's API token in one Live Chat workspace: Live Chat
// gives an owner's or admin's token with write access an admin key's
// access there (pusher-server#898).

// liveChatAccess is the note every such resource's description ends with.
const liveChatAccess = " Uses the provider's API token, which needs write access (and delete access to remove things) and an owner " +
	"or admin of the workspace as its owner; the workspace's plan must include the API (Team and up). Needs a Hibira server with " +
	"API tokens on Live Chat (pusher-server#898)."

// liveChatBase is what the resources share: the provider's client.
type liveChatBase struct{ client *client.Client }

func (b *liveChatBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	b.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

// lc is a Live Chat client for workspace ws.
func (b *liveChatBase) lc(ws types.String) *client.LiveChat {
	return b.client.LiveChatFor(ws.ValueString())
}

func workspaceIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "The Live Chat workspace's ID (`ws_...`, in Live Chat's Settings, or `GET /v1/workspaces`). Changing it " +
			"makes a new one there.",
		Required:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func idAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// importWorkspaceChild imports `<workspace_id>/<id>`.
func importWorkspaceChild(ctx context.Context, id, wsAttr, example string, resp *resource.ImportStateResponse) {
	ws, child, ok := strings.Cut(id, "/")
	if !ok || ws == "" || child == "" || strings.Contains(child, "/") {
		resp.Diagnostics.AddError("Invalid import ID", "Use <"+wsAttr+">/<id>, for example "+example+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(wsAttr), ws)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), child)...)
}

// gone handles a read that found nothing: the resource leaves the state.
func gone(ctx context.Context, err error, state interface {
	RemoveResource(context.Context)
}) bool {
	if client.IsNotFound(err) {
		state.RemoveResource(ctx)
		return true
	}
	return false
}

func addErr(diags *diag.Diagnostics, what string, err error) {
	diags.AddError(what+" failed", err.Error())
}
