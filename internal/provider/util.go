package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// configuredClient is the client the provider made, or nil before the
// provider is configured.
func configuredClient(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", data))
	}
	return c
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
