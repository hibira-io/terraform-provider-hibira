// Package provider is the Hibira Terraform provider.
package provider

import (
	"context"
	"os"

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
	APIToken types.String `tfsdk:"api_token"`
	Endpoint types.String `tfsdk:"endpoint"`
}

func (p *hibiraProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hibira"
	resp.Version = p.version
}

func (p *hibiraProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [Hibira](https://hibira.io) apps, webhooks and push notification credentials.",
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
		},
	}
}

func (p *hibiraProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.APIToken.IsUnknown() || cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration",
			"api_token and endpoint must be known when the provider is configured; they can't depend on resources that aren't created yet.")
		return
	}
	token := os.Getenv("HIBIRA_API_TOKEN")
	if !cfg.APIToken.IsNull() {
		token = cfg.APIToken.ValueString()
	}
	endpoint := os.Getenv("HIBIRA_ENDPOINT")
	if !cfg.Endpoint.IsNull() {
		endpoint = cfg.Endpoint.ValueString()
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Missing Hibira API token",
			"Set api_token in the provider block or the HIBIRA_API_TOKEN environment variable. "+
				"Make a token with read and write access in the Hibira portal.")
		return
	}
	c := client.New(endpoint, token, "terraform-provider-hibira/"+p.version)
	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *hibiraProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newAppResource, newWebhookResource, newPushResource}
}

func (p *hibiraProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newAppDataSource, newAppsDataSource}
}
