package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// A workspace set up with the old default endpoint keeps it when the
// configuration leaves endpoint out, so the new default doesn't replace it.
func TestKeepLegacyLiveChatEndpoint(t *testing.T) {
	for _, c := range []struct {
		name          string
		config, state types.String
		want          string
	}{
		{"old default kept", types.StringNull(), types.StringValue(client.LegacyLiveChatEndpoint), client.LegacyLiveChatEndpoint},
		{"new resource", types.StringNull(), types.StringNull(), client.DefaultLiveChatEndpoint},
		{"set in config", types.StringValue(client.DefaultLiveChatEndpoint), types.StringValue(client.LegacyLiveChatEndpoint), client.DefaultLiveChatEndpoint},
		{"other endpoint", types.StringNull(), types.StringValue("https://lc.example.com"), client.DefaultLiveChatEndpoint},
	} {
		plan := types.StringValue(client.DefaultLiveChatEndpoint)
		if !c.config.IsNull() {
			plan = c.config
		}
		resp := &planmodifier.StringResponse{PlanValue: plan}
		keepLegacyLiveChatEndpoint{}.PlanModifyString(context.Background(), planmodifier.StringRequest{ConfigValue: c.config, StateValue: c.state, PlanValue: plan}, resp)
		if got := resp.PlanValue.ValueString(); got != c.want {
			t.Errorf("%s: plan = %q, want %q", c.name, got, c.want)
		}
	}
	for _, ok := range []string{"https://api.hibira.io/live-chat", "https://chat.hibira.io", "https://chat.hibira.io/", "http://localhost:8080/live-chat"} {
		if !liveChatEndpoint.MatchString(ok) {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"http://chat.hibira.io", "https://api.hibira.io/live-chat?x=1"} {
		if liveChatEndpoint.MatchString(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
}
