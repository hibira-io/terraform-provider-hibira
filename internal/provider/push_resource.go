package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hibira-io/terraform-provider-hibira/internal/client"
)

var (
	_ resource.Resource                = (*pushResource)(nil)
	_ resource.ResourceWithConfigure   = (*pushResource)(nil)
	_ resource.ResourceWithImportState = (*pushResource)(nil)
)

func newPushResource() resource.Resource { return &pushResource{} }

// pushResource is an app's push notification instance (Beams-compatible)
// and its APNs and FCM credentials.
type pushResource struct{ client *client.Client }

type apnsModel struct {
	KeyID       types.String `tfsdk:"key_id"`
	TeamID      types.String `tfsdk:"team_id"`
	BundleID    types.String `tfsdk:"bundle_id"`
	Environment types.String `tfsdk:"environment"`
	PrivateKey  types.String `tfsdk:"private_key"`
}

type fcmModel struct {
	ServiceAccount types.String `tfsdk:"service_account"`
}

type pushModel struct {
	ID             types.String `tfsdk:"id"`
	AppID          types.String `tfsdk:"app_id"`
	APNs           *apnsModel   `tfsdk:"apns"`
	FCM            *fcmModel    `tfsdk:"fcm"`
	InstanceID     types.String `tfsdk:"instance_id"`
	SecretKey      types.String `tfsdk:"secret_key"`
	VAPIDPublicKey types.String `tfsdk:"vapid_public_key"`
	Endpoint       types.String `tfsdk:"endpoint"`
}

func (r *pushResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_push"
}

func computedString(desc string, sensitive bool) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true, Sensitive: sensitive,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func (r *pushResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Push notifications for a Hibira app: turns on the app's Beams-compatible push instance " +
			"and sets its Apple (APNs) and Firebase (FCM) credentials. Web push needs no credentials. " +
			"Destroying it deletes the instance and every registered device.",
		Attributes: map[string]schema.Attribute{
			"id": computedString("The app ID.", false),
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The app's ID.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"apns": schema.SingleNestedAttribute{
				MarkdownDescription: "A token-based (.p8) APNs signing key, for iOS.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"key_id": schema.StringAttribute{
						MarkdownDescription: "The 10-character key ID from the Apple Developer portal.",
						Required:            true, Validators: []validator.String{stringvalidator.LengthBetween(10, 10)},
					},
					"team_id": schema.StringAttribute{
						MarkdownDescription: "The 10-character Apple team ID.",
						Required:            true, Validators: []validator.String{stringvalidator.LengthBetween(10, 10)},
					},
					"bundle_id": schema.StringAttribute{
						MarkdownDescription: "The app's bundle identifier (the default `apns-topic`).",
						Required:            true,
					},
					"environment": schema.StringAttribute{
						MarkdownDescription: "`production` (default) or `development`.",
						Optional:            true, Computed: true, Default: stringdefault.StaticString("production"),
						Validators: []validator.String{stringvalidator.OneOf("production", "development")},
					},
					"private_key": schema.StringAttribute{
						MarkdownDescription: "The .p8 file's contents, for example `file(\"AuthKey_ABC123DEFG.p8\")`.",
						Required:            true, Sensitive: true,
					},
				},
			},
			"fcm": schema.SingleNestedAttribute{
				MarkdownDescription: "A Firebase service account, for Android.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"service_account": schema.StringAttribute{
						MarkdownDescription: "The service account JSON file's contents, for example `file(\"firebase-service-account.json\")`.",
						Required:            true, Sensitive: true,
					},
				},
			},
			"instance_id":      computedString("The push instance ID (`instanceId` in the Beams SDKs).", false),
			"secret_key":       computedString("The push instance's secret key, for publishing from your server.", true),
			"vapid_public_key": computedString("The Web Push VAPID public key.", false),
			"endpoint":         computedString("The push API base URL to configure in the Beams SDKs.", false),
		},
	}
}

func (r *pushResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredClient(req.ProviderData, &resp.Diagnostics)
}

func apnsInput(m *apnsModel) *client.APNs {
	if m == nil {
		return nil
	}
	return &client.APNs{KeyID: m.KeyID.ValueString(), TeamID: m.TeamID.ValueString(), BundleID: m.BundleID.ValueString(),
		Environment: m.Environment.ValueString(), PrivateKey: m.PrivateKey.ValueString()}
}

func sameAPNs(a, b *apnsModel) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.KeyID.Equal(b.KeyID) && a.TeamID.Equal(b.TeamID) && a.BundleID.Equal(b.BundleID) &&
		a.Environment.Equal(b.Environment) && a.PrivateKey.Equal(b.PrivateKey)
}

func sameFCM(a, b *fcmModel) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ServiceAccount.Equal(b.ServiceAccount)
}

// setPushState copies the instance's values into m.
func setPushState(m *pushModel, p *client.Push) {
	m.ID = m.AppID
	m.InstanceID = types.StringValue(p.InstanceID)
	m.SecretKey = types.StringValue(p.SecretKey)
	m.VAPIDPublicKey = types.StringValue(p.VAPIDPublicKey)
	m.Endpoint = types.StringValue(p.Endpoint)
}

func (r *pushResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan pushModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	appID := plan.AppID.ValueString()
	p, err := r.client.EnablePush(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Turning on Hibira push failed", err.Error())
		return
	}
	state := plan
	state.APNs, state.FCM = nil, nil
	if plan.APNs != nil {
		if p, err = r.client.SetAPNs(ctx, appID, apnsInput(plan.APNs)); err != nil {
			resp.Diagnostics.AddError("Setting the APNs key failed", err.Error())
		} else {
			state.APNs = plan.APNs
		}
	}
	if plan.FCM != nil && !resp.Diagnostics.HasError() {
		if p, err = r.client.SetFCM(ctx, appID, plan.FCM.ServiceAccount.ValueString()); err != nil {
			resp.Diagnostics.AddError("Setting the FCM service account failed", err.Error())
		} else {
			state.FCM = plan.FCM
		}
	}
	if p == nil {
		// A failed credential call returns no view; read the instance.
		if p, err = r.client.Push(ctx, appID); err != nil {
			resp.Diagnostics.AddError("Reading Hibira push failed", err.Error())
			return
		}
	}
	setPushState(&state, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *pushResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state pushModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.Push(ctx, state.AppID.ValueString())
	if client.IsNotFound(err) || (err == nil && !p.Enabled) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira push failed", err.Error())
		return
	}
	// Hibira never returns the private keys: keep the ones in the state,
	// but notice when a key was removed or replaced elsewhere.
	switch {
	case p.APNs == nil:
		state.APNs = nil
	case state.APNs == nil:
		// Set outside Terraform (or imported): the private key is unknown,
		// so a configured apns block will be applied again.
		state.APNs = &apnsModel{KeyID: types.StringValue(p.APNs.KeyID), TeamID: types.StringValue(p.APNs.TeamID),
			BundleID: types.StringValue(p.APNs.BundleID), Environment: types.StringValue(p.APNs.Environment), PrivateKey: types.StringValue("")}
	default:
		state.APNs.KeyID = types.StringValue(p.APNs.KeyID)
		state.APNs.TeamID = types.StringValue(p.APNs.TeamID)
		state.APNs.BundleID = types.StringValue(p.APNs.BundleID)
		state.APNs.Environment = types.StringValue(p.APNs.Environment)
	}
	switch {
	case p.FCM == nil:
		state.FCM = nil
	case state.FCM == nil:
		state.FCM = &fcmModel{ServiceAccount: types.StringValue("")}
	}
	setPushState(&state, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *pushResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior pushModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	appID := plan.AppID.ValueString()
	state := prior
	if !sameAPNs(plan.APNs, prior.APNs) {
		if _, err := r.client.SetAPNs(ctx, appID, apnsInput(plan.APNs)); err != nil {
			resp.Diagnostics.AddError("Setting the APNs key failed", err.Error())
		} else {
			state.APNs = plan.APNs
		}
	}
	if !sameFCM(plan.FCM, prior.FCM) {
		sa := ""
		if plan.FCM != nil {
			sa = plan.FCM.ServiceAccount.ValueString()
		}
		if _, err := r.client.SetFCM(ctx, appID, sa); err != nil {
			resp.Diagnostics.AddError("Setting the FCM service account failed", err.Error())
		} else {
			state.FCM = plan.FCM
		}
	}
	p, err := r.client.Push(ctx, appID)
	if err != nil {
		resp.Diagnostics.AddError("Reading Hibira push failed", err.Error())
		return
	}
	setPushState(&state, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *pushResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state pushModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DisablePush(ctx, state.AppID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Turning off Hibira push failed", err.Error())
	}
}

func (r *pushResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), req.ID)...)
}
