package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configure(t *testing.T, token string) *provider.ConfigureResponse {
	t.Helper()
	t.Setenv("HIBIRA_API_TOKEN", "")
	t.Setenv("HIBIRA_ENDPOINT", "")
	ctx := context.Background()
	p := New("test")()
	var sr provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sr)
	typ := sr.Schema.Type().TerraformType(ctx)
	raw := tftypes.NewValue(typ, map[string]tftypes.Value{
		"api_token": tftypes.NewValue(tftypes.String, token),
		"endpoint":  tftypes.NewValue(tftypes.String, nil),
	})
	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Raw: raw, Schema: sr.Schema}}, &resp)
	return &resp
}

func TestConfigureRejectsConsoleToken(t *testing.T) {
	resp := configure(t, "hbc_example")
	if !resp.Diagnostics.HasError() {
		t.Fatal("console token accepted")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(got, "Wrong kind") {
		t.Fatalf("summary = %q", got)
	}
}

func TestConfigureAcceptsPortalToken(t *testing.T) {
	resp := configure(t, "hkb_pat_example")
	if resp.Diagnostics.HasError() {
		t.Fatalf("errors: %v", resp.Diagnostics)
	}
	if resp.ResourceData == nil {
		t.Fatal("no client")
	}
}
