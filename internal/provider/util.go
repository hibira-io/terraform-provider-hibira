package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// configuredClient is the customer API client the provider made, or nil
// before the provider is configured. It adds an error when the provider
// has no api_token.
func configuredClient(data any, diags *diag.Diagnostics) *client.Client {
	cs := providerClients(data, diags)
	if cs == nil {
		return nil
	}
	if cs.api == nil {
		diags.AddError("Missing Hibira API token",
			"This resource uses the customer API: set api_token in the provider block or the HIBIRA_API_TOKEN environment variable "+
				"(a token made on the Hibira portal's API tokens page).")
	}
	return cs.api
}

// configuredConsole is the console client the provider made, or nil
// before the provider is configured. It adds an error when the provider
// has no console_token.
func configuredConsole(data any, diags *diag.Diagnostics) *client.Console {
	cs := providerClients(data, diags)
	if cs == nil {
		return nil
	}
	if cs.console == nil {
		diags.AddError("Missing Hibira console token",
			"This resource uses the operations console: set console_token in the provider block or the HIBIRA_CONSOLE_TOKEN "+
				"environment variable, and the Cloudflare Access service token (cf_access_client_id and cf_access_client_secret).")
	}
	return cs.console
}

func providerClients(data any, diags *diag.Diagnostics) *clients {
	if data == nil {
		return nil
	}
	cs, ok := data.(*clients)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *clients, got %T", data))
	}
	return cs
}

// stringsOf is a set of strings as a slice (never nil).
func stringsOf(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	out := []string{}
	if s.IsNull() || s.IsUnknown() {
		return out
	}
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

// setOf is a slice as a set of strings (empty, not null, for nil).
func setOf(ctx context.Context, v []string, diags *diag.Diagnostics) types.Set {
	if v == nil {
		v = []string{}
	}
	s, d := types.SetValueFrom(ctx, types.StringType, v)
	diags.Append(d...)
	return s
}

// httpsURL matches an absolute http(s) URL; Hibira itself requires https
// in production.
var httpsURL = regexp.MustCompile(`^https?://[^/\s]+`)

func quoted(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = "`" + s + "`"
	}
	return out
}
