package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

const vmResource = "virtualmachines"

var (
	_ resource.Resource                = (*virtualMachineResource)(nil)
	_ resource.ResourceWithConfigure   = (*virtualMachineResource)(nil)
	_ resource.ResourceWithImportState = (*virtualMachineResource)(nil)
)

func NewVirtualMachineResource() resource.Resource { return &virtualMachineResource{} }

type virtualMachineResource struct {
	c *client.Client
}

type virtualMachineModel struct {
	Name             types.String `tfsdk:"name"`
	Namespace        types.String `tfsdk:"namespace"`
	OS               types.String `tfsdk:"os"`
	CPU              types.Int64  `tfsdk:"cpu"`
	Memory           types.String `tfsdk:"memory"`
	DiskSize         types.String `tfsdk:"disk_size"`
	Running          types.Bool   `tfsdk:"running"`
	HighAvailability types.Bool   `tfsdk:"high_availability"`
	CPUModel         types.String `tfsdk:"cpu_model"`
	Network          types.String `tfsdk:"network"`
	ID               types.String `tfsdk:"id"`
	Ready            types.Bool   `tfsdk:"ready"`
	IP               types.String `tfsdk:"ip"`
}

func (r *virtualMachineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (r *virtualMachineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A `kind: VirtualMachine` — a KubeVirt VM (Linux or Windows) with a persistent root disk.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the virtual machine.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"namespace": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("default"),
				MarkdownDescription: "Namespace to create the VM in.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"os": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Guest OS image, e.g. `ubuntu-24.04` or `windows-server-2022`. " +
					"Changing it replaces the VM, since the root disk is cloned from that image.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"cpu": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "vCPUs.",
			},
			"memory": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Memory, e.g. `8Gi`.",
			},
			"disk_size": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Root disk size, e.g. `40Gi`. Ignored for Windows guests, whose root " +
					"disk is fixed by the golden image.",
			},
			"running": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the VM should be powered on. Set `false` to stop it while keeping its disk.",
			},
			"high_availability": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Put the root disk on Longhorn (node-independent, live-migratable) " +
					"instead of node-local storage. Required for VM snapshots.",
				PlanModifiers: []planmodifier.Bool{},
			},
			"cpu_model": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Guest CPU model. Leave unset for host-model (fastest, but pins the VM " +
					"to a node with that CPU). Set a common model on a mixed-CPU cluster so the VM can migrate.",
			},
			"network": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Network mode, e.g. `masquerade`.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`namespace/name` identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ready": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the VM reports Ready.",
			},
			"ip": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Primary IP address once running.",
			},
		},
	}
}

func (r *virtualMachineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// manifest renders the model as an open-infra VirtualMachine custom resource.
func (m virtualMachineModel) manifest() map[string]any {
	spec := map[string]any{"os": m.OS.ValueString()}
	if !m.CPU.IsNull() {
		spec["cpu"] = m.CPU.ValueInt64()
	}
	if !m.Memory.IsNull() {
		spec["memory"] = m.Memory.ValueString()
	}
	if !m.DiskSize.IsNull() {
		spec["diskSize"] = m.DiskSize.ValueString()
	}
	if !m.Running.IsNull() {
		spec["running"] = m.Running.ValueBool()
	}
	if !m.HighAvailability.IsNull() {
		spec["highAvailability"] = m.HighAvailability.ValueBool()
	}
	if !m.CPUModel.IsNull() && m.CPUModel.ValueString() != "" {
		spec["cpuModel"] = m.CPUModel.ValueString()
	}
	if !m.Network.IsNull() && m.Network.ValueString() != "" {
		spec["network"] = m.Network.ValueString()
	}
	return map[string]any{
		"apiVersion": client.Group + "/" + client.Version,
		"kind":       "VirtualMachine",
		"metadata": map[string]any{
			"name":      m.Name.ValueString(),
			"namespace": m.Namespace.ValueString(),
		},
		"spec": spec,
	}
}

func (r *virtualMachineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan virtualMachineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.c.Create(ctx, vmResource, plan.Namespace.ValueString(), plan.manifest())
	if err != nil {
		resp.Diagnostics.AddError("Could not create virtual machine", err.Error())
		return
	}
	applyStatus(&plan, out.Object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualMachineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state virtualMachineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.c.Get(ctx, vmResource, state.Namespace.ValueString(), state.Name.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			// Deleted outside Terraform — drop it so the next plan recreates it.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read virtual machine", err.Error())
		return
	}
	applyStatus(&state, out.Object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *virtualMachineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan virtualMachineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.c.Update(ctx, vmResource, plan.Namespace.ValueString(), plan.manifest())
	if err != nil {
		resp.Diagnostics.AddError("Could not update virtual machine", err.Error())
		return
	}
	applyStatus(&plan, out.Object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualMachineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state virtualMachineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.Delete(ctx, vmResource, state.Namespace.ValueString(), state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Could not delete virtual machine", err.Error())
	}
}

// ImportState accepts "namespace/name" (or bare "name" for the default namespace).
func (r *virtualMachineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ns, name := "default", req.ID
	if before, after, found := strings.Cut(req.ID, "/"); found {
		ns, name = before, after
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), ns)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ns+"/"+name)...)
}

// applyStatus copies server-side computed fields into the model.
func applyStatus(m *virtualMachineModel, obj map[string]any) {
	m.ID = types.StringValue(m.Namespace.ValueString() + "/" + m.Name.ValueString())

	ready := false
	ip := ""
	if status, ok := obj["status"].(map[string]any); ok {
		if v, ok := status["ready"].(bool); ok {
			ready = v
		}
		if v, ok := status["ip"].(string); ok {
			ip = v
		}
	}
	m.Ready = types.BoolValue(ready)
	m.IP = types.StringValue(ip)
}
