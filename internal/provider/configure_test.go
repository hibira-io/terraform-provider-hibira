package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configure(t *testing.T, settings map[string]string) *provider.ConfigureResponse {
	t.Helper()
	for _, env := range []string{"HIBIRA_API_TOKEN", "HIBIRA_ENDPOINT", "HIBIRA_CONSOLE_TOKEN", "CF_ACCESS_CLIENT_ID", "CF_ACCESS_CLIENT_SECRET"} {
		t.Setenv(env, "")
	}
	ctx := context.Background()
	p := New("test")()
	var sr provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sr)
	vals := map[string]tftypes.Value{}
	for name := range sr.Schema.Attributes {
		vals[name] = tftypes.NewValue(tftypes.String, nil)
		if v, ok := settings[name]; ok {
			vals[name] = tftypes.NewValue(tftypes.String, v)
		}
	}
	raw := tftypes.NewValue(sr.Schema.Type().TerraformType(ctx), vals)
	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Raw: raw, Schema: sr.Schema}}, &resp)
	return &resp
}

func TestConfigure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		settings    map[string]string
		err         string
		api, consol bool
	}{
		{name: "portal token", settings: map[string]string{"api_token": "hkb_pat_x"}, api: true},
		{name: "console token", settings: map[string]string{"console_token": "hbc_x", "cf_access_client_id": "id", "cf_access_client_secret": "s"}, consol: true},
		{name: "both", settings: map[string]string{"api_token": "hkb_pat_x", "console_token": "hbc_x"}, api: true, consol: true},
		{name: "none", settings: map[string]string{}, err: "Missing Hibira API token"},
		{name: "console token as api_token", settings: map[string]string{"api_token": "hbc_x"}, err: "Wrong kind"},
		{name: "portal token as console_token", settings: map[string]string{"console_token": "hkb_pat_x"}, err: "Wrong kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := configure(t, tc.settings)
			if tc.err != "" {
				if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), tc.err) {
					t.Fatalf("diagnostics %v, want %q", resp.Diagnostics, tc.err)
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("errors: %v", resp.Diagnostics)
			}
			cs := resp.ResourceData.(*clients)
			if (cs.api != nil) != tc.api || (cs.console != nil) != tc.consol {
				t.Fatalf("clients api=%v console=%v", cs.api != nil, cs.console != nil)
			}
		})
	}
}

func TestResourceNeedsItsToken(t *testing.T) {
	var resp provider.ConfigureResponse
	cs := &clients{}
	diags := resp.Diagnostics
	if configuredClient(cs, &diags); !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "API token") {
		t.Fatalf("customer resource without api_token: %v", diags)
	}
	diags = resp.Diagnostics
	if configuredConsole(cs, &diags); !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "console token") {
		t.Fatalf("console resource without console_token: %v", diags)
	}
}
