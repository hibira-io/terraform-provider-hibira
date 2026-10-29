package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure = (*appDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*appsDataSource)(nil)
)

// appSummary is an app as the data sources show it (never its secret).
type appSummary struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Key               types.String `tfsdk:"key"`
	Cluster           types.String `tfsdk:"cluster"`
	Host              types.String `tfsdk:"host"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	ClientEvents      types.Bool   `tfsdk:"client_events"`
	SubscriptionCount types.Bool   `tfsdk:"subscription_count"`
	AllowedOrigins    types.Set    `tfsdk:"allowed_origins"`
	Plan              types.String `tfsdk:"plan"`
}

func summarize(ctx context.Context, app client.App, diags *diag.Diagnostics) appSummary {
	return appSummary{ID: types.StringValue(app.ID), Name: types.StringValue(app.Name), Key: types.StringValue(app.Key),
		Cluster: types.StringValue(app.Cluster), Host: types.StringValue(app.Host()), Enabled: types.BoolValue(app.Enabled),
		ClientEvents: types.BoolValue(app.ClientEvents), SubscriptionCount: types.BoolValue(app.SubscriptionCount),
		AllowedOrigins: setOf(ctx, app.AllowedOrigins, diags), Plan: types.StringValue(app.Plan)}
}

// summaryAttributes are appSummary's attributes; id is required when
// idRequired, else computed.
func summaryAttributes(idRequired bool) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":                 schema.StringAttribute{MarkdownDescription: "The app ID.", Required: idRequired, Computed: !idRequired},
		"name":               schema.StringAttribute{MarkdownDescription: "The app's name.", Computed: true},
		"key":                schema.StringAttribute{MarkdownDescription: "The app key.", Computed: true},
		"cluster":            schema.StringAttribute{MarkdownDescription: "The app's region.", Computed: true},
		"host":               schema.StringAttribute{MarkdownDescription: "The WebSocket host clients connect to.", Computed: true},
		"enabled":            schema.BoolAttribute{MarkdownDescription: "Whether the app is enabled.", Computed: true},
		"client_events":      schema.BoolAttribute{MarkdownDescription: "Whether client events are on.", Computed: true},
		"subscription_count": schema.BoolAttribute{MarkdownDescription: "Whether subscription count events are on.", Computed: true},
		"allowed_origins":    schema.SetAttribute{MarkdownDescription: "The allowed web origins.", ElementType: types.StringType, Computed: true},
		"plan":               schema.StringAttribute{MarkdownDescription: "The account plan.", Computed: true},
	}
}

func newAppDataSource() datasource.DataSource { return &appDataSource{} }

type appDataSource struct{ client *client.Client }

func (d *appDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (d *appDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an existing Hibira app. The secret is never returned; manage the app with `hibira_app` to get it.",
		Attributes:          summaryAttributes(true),
	}
}

func (d *appDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func (d *appDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg appSummary
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := d.client.App(ctx, cfg.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira app failed", err.Error())
		return
	}
	out := summarize(ctx, *app, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

func newAppsDataSource() datasource.DataSource { return &appsDataSource{} }

type appsDataSource struct{ client *client.Client }

type appsModel struct {
	Apps []appSummary `tfsdk:"apps"`
}

func (d *appsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_apps"
}

func (d *appsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the account's Hibira apps (only one, for a token limited to one app).",
		Attributes: map[string]schema.Attribute{
			"apps": schema.ListNestedAttribute{
				MarkdownDescription: "The apps.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: summaryAttributes(false)},
			},
		},
	}
}

func (d *appsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func (d *appsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.client.Apps(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing Hibira apps failed", err.Error())
		return
	}
	out := appsModel{Apps: []appSummary{}}
	for _, app := range list {
		out.Apps = append(out.Apps, summarize(ctx, app, &resp.Diagnostics))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
