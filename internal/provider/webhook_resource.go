package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*webhookResource)(nil)
	_ resource.ResourceWithConfigure   = (*webhookResource)(nil)
	_ resource.ResourceWithImportState = (*webhookResource)(nil)
)

// webhookEvents are the event types Hibira sends (Pusher's, plus
// cache_miss and chat_message).
var webhookEvents = []string{"channel_occupied", "channel_vacated", "member_added", "member_removed",
	"client_event", "subscription_count", "cache_miss", "chat_message"}

func newWebhookResource() resource.Resource { return &webhookResource{} }

// webhookResource is one of an app's webhook endpoints, found by its URL.
type webhookResource struct{ client *client.Client }

type webhookModel struct {
	ID     types.String `tfsdk:"id"`
	AppID  types.String `tfsdk:"app_id"`
	URL    types.String `tfsdk:"url"`
	Events types.Set    `tfsdk:"events"`
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A webhook endpoint of a Hibira app. Hibira signs each request with the app's secret " +
			"(`X-Pusher-Key` and `X-Pusher-Signature`, as Pusher does). An app has up to 5 endpoints, fewer on some plans.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<app_id>/<url>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The app's ID.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "The HTTPS URL Hibira posts events to. Changing it replaces the endpoint.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(httpsURL, "must be an http:// or https:// URL")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"events": schema.SetAttribute{
				MarkdownDescription: "The event types to send: " + strings.Join(quoted(webhookEvents), ", ") + ". Empty sends every type.",
				ElementType:         types.StringType,
				Optional:            true, Computed: true, Default: setdefault.StaticValue(emptySet()),
				Validators: []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf(webhookEvents...))},
			},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func findWebhook(app *client.App, url string) (int, bool) {
	for i, w := range app.Webhooks {
		if w.URL == url {
			return i, true
		}
	}
	return -1, false
}

// put adds or replaces the endpoint in the app's list.
func (r *webhookResource) put(ctx context.Context, m webhookModel, diags *diag.Diagnostics) error {
	events := stringsOf(ctx, m.Events, diags)
	if diags.HasError() {
		return fmt.Errorf("invalid events")
	}
	url := m.URL.ValueString()
	_, err := r.client.ModifyApp(ctx, m.AppID.ValueString(), func(app *client.App) (client.AppUpdate, bool) {
		list := append([]client.Webhook{}, app.Webhooks...)
		hook := client.Webhook{URL: url, Events: events}
		if i, ok := findWebhook(app, url); ok {
			if sameStrings(list[i].Events, events) {
				return client.AppUpdate{}, false
			}
			list[i] = hook
		} else {
			list = append(list, hook)
		}
		return client.AppUpdate{Webhooks: &list}, true
	})
	return err
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.App(ctx, plan.AppID.ValueString())
	if err == nil {
		if _, ok := findWebhook(app, plan.URL.ValueString()); ok {
			resp.Diagnostics.AddError("Webhook already exists",
				fmt.Sprintf("App %s already has a webhook for %s. Import it with: terraform import <address> %s/%s",
					app.ID, plan.URL.ValueString(), app.ID, plan.URL.ValueString()))
			return
		}
		err = r.put(ctx, plan, &resp.Diagnostics)
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating Hibira webhook failed", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.AppID.ValueString() + "/" + plan.URL.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.App(ctx, state.AppID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira webhook failed", err.Error())
		return
	}
	i, ok := findWebhook(app, state.URL.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Events = setOf(ctx, app.Webhooks[i].Events, &resp.Diagnostics)
	state.ID = types.StringValue(app.ID + "/" + app.Webhooks[i].URL)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, plan, &resp.Diagnostics); err != nil {
		resp.Diagnostics.AddError("Updating Hibira webhook failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	url := state.URL.ValueString()
	_, err := r.client.ModifyApp(ctx, state.AppID.ValueString(), func(app *client.App) (client.AppUpdate, bool) {
		i, ok := findWebhook(app, url)
		if !ok {
			return client.AppUpdate{}, false
		}
		list := append(append([]client.Webhook{}, app.Webhooks[:i]...), app.Webhooks[i+1:]...)
		return client.AppUpdate{Webhooks: &list}, true
	})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting Hibira webhook failed", err.Error())
	}
}

func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	appID, url, ok := strings.Cut(req.ID, "/")
	if !ok || appID == "" || url == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Use <app_id>/<url>, for example app_123/https://example.com/hooks/hibira.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), appID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("url"), url)...)
}
