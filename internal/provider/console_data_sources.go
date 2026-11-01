package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure        = (*accountDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*accountDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*consoleAppsDataSource)(nil)
)

func newAccountDataSource() datasource.DataSource { return &accountDataSource{} }

type accountDataSource struct{ console *client.Console }

type accountDataModel struct {
	ID            types.String `tfsdk:"id"`
	Email         types.String `tfsdk:"email"`
	Name          types.String `tfsdk:"name"`
	Plan          types.String `tfsdk:"plan"`
	EffectivePlan types.String `tfsdk:"effective_plan"`
	Status        types.String `tfsdk:"status"`
	Suspended     types.Bool   `tfsdk:"suspended"`
	EmailVerified types.Bool   `tfsdk:"email_verified"`
	Note          types.String `tfsdk:"note"`
	Tags          types.Set    `tfsdk:"tags"`
	ChatBeta      types.Bool   `tfsdk:"chat_beta"`
}

func (d *accountDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_account"
}

func (d *accountDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a customer account by ID or email. Needs the provider's `console_token` (scope `accounts:read`) " +
			"and the Cloudflare Access service token.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{MarkdownDescription: "The account ID. Set this or `email`.", Optional: true, Computed: true},
			"email":          schema.StringAttribute{MarkdownDescription: "The account's email. Set this or `id`.", Optional: true, Computed: true},
			"name":           schema.StringAttribute{MarkdownDescription: "The account's name.", Computed: true},
			"plan":           schema.StringAttribute{MarkdownDescription: "The plan the account pays for.", Computed: true},
			"effective_plan": schema.StringAttribute{MarkdownDescription: "The plan whose limits apply.", Computed: true},
			"status":         schema.StringAttribute{MarkdownDescription: "The subscription status.", Computed: true},
			"suspended":      schema.BoolAttribute{MarkdownDescription: "Whether the account is suspended.", Computed: true},
			"email_verified": schema.BoolAttribute{MarkdownDescription: "Whether the email is verified.", Computed: true},
			"note":           schema.StringAttribute{MarkdownDescription: "The operator note.", Computed: true},
			"tags":           schema.SetAttribute{MarkdownDescription: "The operator tags.", ElementType: types.StringType, Computed: true},
			"chat_beta":      schema.BoolAttribute{MarkdownDescription: "Whether the account is in the Chat API beta.", Computed: true},
		},
	}
}

func (d *accountDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("email"))}
}

func (d *accountDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.console = configuredConsole(req.ProviderData, &resp.Diagnostics)
}

func (d *accountDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg accountDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var acct *client.Account
	var err error
	if known(cfg.ID) {
		acct, err = d.console.Account(ctx, cfg.ID.ValueString())
	} else {
		acct, err = d.console.AccountByEmail(ctx, cfg.Email.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira account failed", err.Error())
		return
	}
	out := accountDataModel{ID: types.StringValue(acct.ID), Email: types.StringValue(acct.Email), Name: types.StringValue(acct.Name),
		Plan: types.StringValue(acct.Plan), EffectivePlan: types.StringValue(acct.EffectivePlan), Status: types.StringValue(acct.Status),
		Suspended: types.BoolValue(acct.Suspended), EmailVerified: types.BoolValue(acct.EmailVerified), Note: types.StringValue(acct.Note),
		Tags: setOf(ctx, acct.Tags, &resp.Diagnostics), ChatBeta: types.BoolValue(acct.ChatBeta)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

func newConsoleAppsDataSource() datasource.DataSource { return &consoleAppsDataSource{} }

type consoleAppsDataSource struct{ console *client.Console }

type consoleAppModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Key            types.String `tfsdk:"key"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	ClientEvents   types.Bool   `tfsdk:"client_events"`
	AllowedOrigins types.Set    `tfsdk:"allowed_origins"`
	AccountID      types.String `tfsdk:"account_id"`
	Plan           types.String `tfsdk:"plan"`
}

type consoleAppsModel struct {
	AccountID types.String      `tfsdk:"account_id"`
	Apps      []consoleAppModel `tfsdk:"apps"`
}

func (d *consoleAppsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_console_apps"
}

func (d *consoleAppsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the apps of every account, or of one. Needs the provider's `console_token` (scope `apps:read`) " +
			"and the Cloudflare Access service token. Secrets are never returned.",
		Attributes: map[string]schema.Attribute{
			"account_id": schema.StringAttribute{MarkdownDescription: "List only this account's apps.", Optional: true},
			"apps": schema.ListNestedAttribute{
				MarkdownDescription: "The apps.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":              schema.StringAttribute{MarkdownDescription: "The app ID.", Computed: true},
					"name":            schema.StringAttribute{MarkdownDescription: "The app's name.", Computed: true},
					"key":             schema.StringAttribute{MarkdownDescription: "The app key.", Computed: true},
					"enabled":         schema.BoolAttribute{MarkdownDescription: "Whether the app is enabled.", Computed: true},
					"client_events":   schema.BoolAttribute{MarkdownDescription: "Whether client events are on.", Computed: true},
					"allowed_origins": schema.SetAttribute{MarkdownDescription: "The allowed web origins.", ElementType: types.StringType, Computed: true},
					"account_id":      schema.StringAttribute{MarkdownDescription: "The owning account (empty for the service's own apps).", Computed: true},
					"plan":            schema.StringAttribute{MarkdownDescription: "The app's plan.", Computed: true},
				}},
			},
		},
	}
}

func (d *consoleAppsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.console = configuredConsole(req.ProviderData, &resp.Diagnostics)
}

func (d *consoleAppsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg consoleAppsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.console.Apps(ctx, cfg.AccountID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Listing Hibira apps failed", err.Error())
		return
	}
	cfg.Apps = []consoleAppModel{}
	for _, a := range list {
		cfg.Apps = append(cfg.Apps, consoleAppModel{ID: types.StringValue(a.ID), Name: types.StringValue(a.Name), Key: types.StringValue(a.Key),
			Enabled: types.BoolValue(a.Enabled), ClientEvents: types.BoolValue(a.ClientEvents),
			AllowedOrigins: setOf(ctx, a.AllowedOrigins, &resp.Diagnostics), AccountID: types.StringValue(a.AccountID),
			Plan: types.StringValue(a.Plan)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
