// Package provider is the Hibira Terraform provider.
package provider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// New returns the provider factory for version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &hibiraProvider{version: version} }
}

type hibiraProvider struct {
	version string
}

type providerModel struct {
	APIToken           types.String `tfsdk:"api_token"`
	Endpoint           types.String `tfsdk:"endpoint"`
	ConsoleToken       types.String `tfsdk:"console_token"`
	AccessClientID     types.String `tfsdk:"cf_access_client_id"`
	AccessClientSecret types.String `tfsdk:"cf_access_client_secret"`
}

// clients is the provider data: the customer API client and the console
// client, either nil when its token isn't set.
type clients struct {
	api     *client.Client
	console *client.Console
}

func (p *hibiraProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hibira"
	resp.Version = p.version
}

func (p *hibiraProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [Hibira](https://hibira.io) apps, webhooks and push notification credentials, and, for operators, customer accounts.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				MarkdownDescription: "A Hibira API token (`hkb_pat_...`) with read and write access, made on the Hibira portal's API tokens page. " +
					"Can also be set with the `HIBIRA_API_TOKEN` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "The Hibira site URL. Defaults to `" + client.DefaultEndpoint + "`, or the `HIBIRA_ENDPOINT` environment variable.",
				Optional:            true,
			},
			"console_token": schema.StringAttribute{
				MarkdownDescription: "An operations console API token (`hbc_...`), for the operator resources (`hibira_account`, " +
					"`data.hibira_account`, `data.hibira_console_apps`). Made in the operations console under API tokens. " +
					"Can also be set with the `HIBIRA_CONSOLE_TOKEN` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"cf_access_client_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the Cloudflare Access service token the console sits behind. " +
					"Can also be set with the `CF_ACCESS_CLIENT_ID` environment variable.",
				Optional: true,
			},
			"cf_access_client_secret": schema.StringAttribute{
				MarkdownDescription: "The secret of that Cloudflare Access service token. " +
					"Can also be set with the `CF_ACCESS_CLIENT_SECRET` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *hibiraProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, v := range []types.String{cfg.APIToken, cfg.Endpoint, cfg.ConsoleToken, cfg.AccessClientID, cfg.AccessClientSecret} {
		if v.IsUnknown() {
			resp.Diagnostics.AddError("Unknown provider configuration",
				"The provider's settings must be known when it is configured; they can't depend on resources that aren't created yet.")
			return
		}
	}
	token := setting(cfg.APIToken, "HIBIRA_API_TOKEN")
	endpoint := setting(cfg.Endpoint, "HIBIRA_ENDPOINT")
	consoleToken := setting(cfg.ConsoleToken, "HIBIRA_CONSOLE_TOKEN")
	if token == "" && consoleToken == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Missing Hibira API token",
			"Set api_token in the provider block or the HIBIRA_API_TOKEN environment variable. "+
				"Make a token with read and write access in the Hibira portal.")
		return
	}
	if strings.HasPrefix(token, client.ConsolePrefix) {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Wrong kind of Hibira token",
			"This is an operations console token (hbc_...): set it as console_token (or HIBIRA_CONSOLE_TOKEN) instead. "+
				"api_token takes a token made on the Hibira portal's API tokens page (hkb_pat_...).")
		return
	}
	if consoleToken != "" && !strings.HasPrefix(consoleToken, client.ConsolePrefix) {
		resp.Diagnostics.AddAttributeError(path.Root("console_token"), "Wrong kind of Hibira token",
			"console_token takes an operations console token (hbc_...), made in the console under API tokens. "+
				"A portal API token (hkb_pat_...) goes in api_token.")
		return
	}
	ua := "terraform-provider-hibira/" + p.version
	data := &clients{}
	if token != "" {
		data.api = client.New(endpoint, token, ua)
	}
	if consoleToken != "" {
		data.console = client.NewConsole(endpoint, consoleToken,
			setting(cfg.AccessClientID, "CF_ACCESS_CLIENT_ID"), setting(cfg.AccessClientSecret, "CF_ACCESS_CLIENT_SECRET"), ua)
	}
	resp.DataSourceData = data
	resp.ResourceData = data
}

// setting is v, or the environment variable env when v isn't set.
func setting(v types.String, env string) string {
	if !v.IsNull() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *hibiraProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newAppResource, newWebhookResource, newPushResource, newAccountResource}
}

func (p *hibiraProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newAppDataSource, newAppsDataSource, newAccountDataSource, newConsoleAppsDataSource}
}
