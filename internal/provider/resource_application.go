package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

const appResource = "applications"

var (
	_ resource.Resource                = (*applicationResource)(nil)
	_ resource.ResourceWithConfigure   = (*applicationResource)(nil)
	_ resource.ResourceWithImportState = (*applicationResource)(nil)
)

func NewApplicationResource() resource.Resource { return &applicationResource{} }

type applicationResource struct{ c *client.Client }

type applicationModel struct {
	Name      types.String `tfsdk:"name"`
	Namespace types.String `tfsdk:"namespace"`
	Image     types.String `tfsdk:"image"`
	Replicas  types.Int64  `tfsdk:"replicas"`
	Port      types.Int64  `tfsdk:"port"`
	Expose    types.Bool   `tfsdk:"expose"`
	Host      types.String `tfsdk:"host"`
	ID        types.String `tfsdk:"id"`
	Ready     types.Bool   `tfsdk:"ready"`
}

func (r *applicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func (r *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A `kind: Application` — a container workload compiled into a Deployment, " +
			"Service, Ingress and HPA. Use `openinfra_database` for a data-only application.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("default"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"image": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Container image to run.",
			},
			"replicas": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Desired replica count (minimum, when autoscaling is enabled).",
			},
			"port": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Container port the app listens on.",
			},
			"expose": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Publish the app through an Ingress.",
			},
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Ingress hostname when `expose` is set.",
			},
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ready": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the application reports Ready.",
			},
		},
	}
}

func (r *applicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", req.ProviderData))
		return
	}
	r.c = c
}

func (m applicationModel) manifest() map[string]any {
	spec := map[string]any{"image": m.Image.ValueString()}
	if !m.Replicas.IsNull() {
		spec["replicas"] = m.Replicas.ValueInt64()
	}
	if !m.Port.IsNull() {
		spec["port"] = m.Port.ValueInt64()
	}
	if !m.Expose.IsNull() {
		spec["expose"] = m.Expose.ValueBool()
	}
	if !m.Host.IsNull() && m.Host.ValueString() != "" {
		spec["host"] = m.Host.ValueString()
	}
	return map[string]any{
		"apiVersion": client.Group + "/" + client.Version,
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      m.Name.ValueString(),
			"namespace": m.Namespace.ValueString(),
		},
		"spec": spec,
	}
}

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.c.Create(ctx, appResource, plan.Namespace.ValueString(), plan.manifest())
	if err != nil {
		resp.Diagnostics.AddError("Could not create application", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Namespace.ValueString() + "/" + plan.Name.ValueString())
	plan.Ready = types.BoolValue(readyFrom(out.Object))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.c.Get(ctx, appResource, state.Namespace.ValueString(), state.Name.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read application", err.Error())
		return
	}
	state.ID = types.StringValue(state.Namespace.ValueString() + "/" + state.Name.ValueString())
	state.Ready = types.BoolValue(readyFrom(out.Object))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.c.Update(ctx, appResource, plan.Namespace.ValueString(), plan.manifest())
	if err != nil {
		resp.Diagnostics.AddError("Could not update application", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Namespace.ValueString() + "/" + plan.Name.ValueString())
	plan.Ready = types.BoolValue(readyFrom(out.Object))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.Delete(ctx, appResource, state.Namespace.ValueString(), state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Could not delete application", err.Error())
	}
}

func (r *applicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ns, name := "default", req.ID
	if before, after, found := strings.Cut(req.ID, "/"); found {
		ns, name = before, after
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), ns)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ns+"/"+name)...)
}

// readyFrom reads the standard Ready condition off a composite resource's status.
func readyFrom(obj map[string]any) bool {
	status, ok := obj["status"].(map[string]any)
	if !ok {
		return false
	}
	conds, ok := status["conditions"].([]any)
	if !ok {
		return false
	}
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] == "Ready" {
			return cm["status"] == "True"
		}
	}
	return false
}
