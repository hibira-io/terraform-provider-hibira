package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

// An Approvals workspace's auto-approve rules and follow-up (reminders and
// escalation), part of hibira_approvals_workspace.

type ruleModel struct {
	Name    types.String `tfsdk:"name"`
	Agents  types.List   `tfsdk:"agents"`
	Titles  types.List   `tfsdk:"titles"`
	Except  types.List   `tfsdk:"except"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

type followUpModel struct {
	RemindMinutes   types.Int64 `tfsdk:"remind_minutes"`
	EscalateMinutes types.Int64 `tfsdk:"escalate_minutes"`
	EscalateTo      types.Set   `tfsdk:"escalate_to"`
}

var objectAsOptions = basetypes.ObjectAsOptions{}

var (
	stringList   = types.ListType{ElemType: types.StringType}
	emptyStrList = types.ListValueMust(types.StringType, []attr.Value{})
	ruleType     = types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "agents": stringList, "titles": stringList,
		"except": stringList, "enabled": types.BoolType}}
	followUpType = types.ObjectType{AttrTypes: map[string]attr.Type{"remind_minutes": types.Int64Type, "escalate_minutes": types.Int64Type,
		"escalate_to": types.SetType{ElemType: types.StringType}}}
)

func patternList(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Optional:            true, Computed: true,
		Default:    listdefault.StaticValue(emptyStrList),
		Validators: []validator.List{listvalidator.SizeAtMost(20), listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 200))},
	}
}

func approvalsSettingsAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"rules": schema.ListNestedAttribute{
			MarkdownDescription: "Auto-approve rules (paid plans): a request that matches a rule is approved at once, kept in the history " +
				"with the rule's name, and notifies nobody. Patterns match the whole text, ignoring case; `*` stands for any run of " +
				"characters. A request that asks for a quorum is never auto-approved. Unset: managed in Hibira Approvals.",
			Optional: true, Computed: true,
			PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			Validators:    []validator.List{listvalidator.SizeAtMost(50)},
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"name": schema.StringAttribute{
					MarkdownDescription: "Shown on requests the rule approved; unique in the workspace.",
					Required:            true,
					Validators:          []validator.String{stringvalidator.LengthBetween(1, 60)},
				},
				"agents": patternList("Patterns for the request's agent, such as `Approval gate · staging`. Empty: any agent."),
				"titles": patternList("Patterns for the request's title, such as `SELECT *`. Empty: any title. A rule needs at least one " +
					"pattern in `agents` or `titles` that is not just `*`."),
				"except": patternList("The rule does not apply when one of these matches the title or a line of the details, such as `*DROP*`."),
				"enabled": schema.BoolAttribute{
					MarkdownDescription: "Whether the rule approves requests. Defaults to `true`.",
					Optional:            true, Computed: true,
					Default: booldefault.StaticBool(true),
				},
			}},
		},
		"follow_up": schema.SingleNestedAttribute{
			MarkdownDescription: "Reminders and escalation for requests nobody answers. Unset: managed in Hibira Approvals.",
			Optional:            true, Computed: true,
			PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
			Attributes: map[string]schema.Attribute{
				"remind_minutes": schema.Int64Attribute{
					MarkdownDescription: "Remind the approvers after this many minutes without an answer (up to 3 times). `0` (default): never.",
					Optional:            true, Computed: true,
					Default: int64default.StaticInt64(0),
				},
				"escalate_minutes": schema.Int64Attribute{
					MarkdownDescription: "Let `escalate_to` decide after this many minutes without an answer. `0` (default): never.",
					Optional:            true, Computed: true,
					Default: int64default.StaticInt64(0),
				},
				"escalate_to": schema.SetAttribute{
					MarkdownDescription: "Backup approvers: members' emails or approver group slugs, at most 10. Needed when `escalate_minutes` is set.",
					ElementType:         types.StringType,
					Optional:            true, Computed: true,
					Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				},
			},
		},
	}
}

func listOf(ctx context.Context, v []string, diags *diag.Diagnostics) types.List {
	if v == nil {
		v = []string{}
	}
	l, d := types.ListValueFrom(ctx, types.StringType, v)
	diags.Append(d...)
	return l
}

func listStrings(ctx context.Context, l types.List, diags *diag.Diagnostics) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	diags.Append(l.ElementsAs(ctx, &out, false)...)
	return out
}

// wantedSettings reads the configured rules and follow-up; nil means not
// managed.
func wantedSettings(ctx context.Context, rules types.List, followUp types.Object, diags *diag.Diagnostics) ([]client.ApprovalRule, *client.ApprovalsFollowUp) {
	var out []client.ApprovalRule
	if !rules.IsNull() && !rules.IsUnknown() {
		var list []ruleModel
		diags.Append(rules.ElementsAs(ctx, &list, false)...)
		out = []client.ApprovalRule{}
		for _, r := range list {
			out = append(out, client.ApprovalRule{Name: r.Name.ValueString(), Agents: listStrings(ctx, r.Agents, diags),
				Titles: listStrings(ctx, r.Titles, diags), Except: listStrings(ctx, r.Except, diags), Off: !r.Enabled.ValueBool()})
		}
	}
	var f *client.ApprovalsFollowUp
	if !followUp.IsNull() && !followUp.IsUnknown() {
		var m followUpModel
		diags.Append(followUp.As(ctx, &m, objectAsOptions)...)
		f = &client.ApprovalsFollowUp{RemindMinutes: int(m.RemindMinutes.ValueInt64()), EscalateMinutes: int(m.EscalateMinutes.ValueInt64()),
			EscalateTo: stringsOf(ctx, m.EscalateTo, diags)}
	}
	return out, f
}

// settingsValues are w's rules and follow-up as attribute values.
func settingsValues(ctx context.Context, w *client.ApprovalsWorkspace, diags *diag.Diagnostics) (types.List, types.Object) {
	rules := make([]attr.Value, 0, len(w.Rules))
	for _, r := range w.Rules {
		o, d := types.ObjectValue(ruleType.AttrTypes, map[string]attr.Value{"name": types.StringValue(r.Name),
			"agents": listOf(ctx, r.Agents, diags), "titles": listOf(ctx, r.Titles, diags), "except": listOf(ctx, r.Except, diags),
			"enabled": types.BoolValue(!r.Off)})
		diags.Append(d...)
		rules = append(rules, o)
	}
	l, d := types.ListValue(ruleType, rules)
	diags.Append(d...)
	f := client.ApprovalsFollowUp{}
	if w.FollowUp != nil {
		f = *w.FollowUp
	}
	o, d := types.ObjectValue(followUpType.AttrTypes, map[string]attr.Value{"remind_minutes": types.Int64Value(int64(f.RemindMinutes)),
		"escalate_minutes": types.Int64Value(int64(f.EscalateMinutes)), "escalate_to": setOf(ctx, f.EscalateTo, diags)})
	diags.Append(d...)
	return l, o
}

// reconcileSettings saves the wanted rules and follow-up (nil: leave as
// they are). Rules keep their IDs by name, so requests they approved stay
// linked to them.
func (r *approvalsWorkspaceResource) reconcileSettings(ctx context.Context, w *client.ApprovalsWorkspace, rules []client.ApprovalRule, f *client.ApprovalsFollowUp) (*client.ApprovalsWorkspace, error) {
	var err error
	if rules != nil && !sameRules(w.Rules, rules) {
		ids := map[string]string{}
		for _, x := range w.Rules {
			ids[x.Name] = x.ID
		}
		for i := range rules {
			rules[i].ID = ids[rules[i].Name]
		}
		if w, err = r.client.SetApprovalRules(ctx, w.ID, rules); err != nil {
			return nil, fmt.Errorf("rules: %w", err)
		}
	}
	if f != nil && !sameFollowUp(w.FollowUp, f) {
		if w, err = r.client.SetApprovalsFollowUp(ctx, w.ID, *f); err != nil {
			return nil, fmt.Errorf("follow_up: %w", err)
		}
	}
	return w, nil
}

func sameRules(have, want []client.ApprovalRule) bool {
	if len(have) != len(want) {
		return false
	}
	for i, h := range have {
		w := want[i]
		if h.Name != w.Name || h.Off != w.Off || !slices.Equal(h.Agents, w.Agents) || !slices.Equal(h.Titles, w.Titles) || !slices.Equal(h.Except, w.Except) {
			return false
		}
	}
	return true
}

func sameFollowUp(have, want *client.ApprovalsFollowUp) bool {
	h := client.ApprovalsFollowUp{}
	if have != nil {
		h = *have
	}
	if h.RemindMinutes != want.RemindMinutes || h.EscalateMinutes != want.EscalateMinutes || len(h.EscalateTo) != len(want.EscalateTo) {
		return false
	}
	seen := map[string]bool{}
	for _, e := range h.EscalateTo {
		seen[e] = true
	}
	for _, e := range want.EscalateTo {
		if !seen[e] {
			return false
		}
	}
	return true
}
