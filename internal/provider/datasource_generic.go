package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

// Data sources for reading existing open-infra resources.
//
// One generic implementation serves every kind: the spec/status shapes differ per
// CRD and hand-mirroring all fifteen would guarantee drift (a new field in the
// platform silently becomes unreadable here). Instead the whole spec and status
// are surfaced as JSON strings, which stay correct as the CRDs evolve — callers
// pick fields out with jsondecode(). Typed *resources* still exist for the kinds
// you author; data sources are for reading what's already there.

type genericDataSource struct {
	c        *client.Client
	typeName string // e.g. "virtual_machine"
	resource string // CRD plural, e.g. "virtualmachines"
	kind     string // e.g. "VirtualMachine"
}

// newDataSource builds a data source for one CRD.
func newDataSource(typeName, resource, kind string) func() datasource.DataSource {
	return func() datasource.DataSource {
		return &genericDataSource{typeName: typeName, resource: resource, kind: kind}
	}
}

type genericDataSourceModel struct {
	Name      types.String `tfsdk:"name"`
	Namespace types.String `tfsdk:"namespace"`
	ID        types.String `tfsdk:"id"`
	Spec      types.String `tfsdk:"spec"`
	Status    types.String `tfsdk:"status"`
	Ready     types.Bool   `tfsdk:"ready"`
}

func (d *genericDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.typeName
}

func (d *genericDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: fmt.Sprintf(
			"Read an existing `kind: %s`. `spec` and `status` are returned as JSON strings so this "+
				"stays correct as the platform adds fields — decode them with `jsondecode()`.", d.kind),
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the resource to read.",
			},
			"namespace": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Namespace to read from. Defaults to `default`.",
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`namespace/name` identifier.",
			},
			"spec": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The resource's `spec`, JSON-encoded.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The resource's `status`, JSON-encoded (empty object if absent).",
			},
			"ready": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the resource reports a Ready condition of True.",
			},
		},
	}
}

func (d *genericDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", req.ProviderData))
		return
	}
	d.c = c
}

func (d *genericDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg genericDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ns := cfg.Namespace.ValueString()
	if ns == "" {
		ns = "default"
	}

	out, err := d.c.Get(ctx, d.resource, ns, cfg.Name.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError(
				fmt.Sprintf("%s not found", d.kind),
				fmt.Sprintf("No %s named %q exists in namespace %q.", d.kind, cfg.Name.ValueString(), ns))
			return
		}
		resp.Diagnostics.AddError(fmt.Sprintf("Could not read %s", d.kind), err.Error())
		return
	}

	specJSON := "{}"
	if spec, ok := out.Object["spec"]; ok {
		if b, err := json.Marshal(spec); err == nil {
			specJSON = string(b)
		}
	}
	statusJSON := "{}"
	if st, ok := out.Object["status"]; ok {
		if b, err := json.Marshal(st); err == nil {
			statusJSON = string(b)
		}
	}

	cfg.Namespace = types.StringValue(ns)
	cfg.ID = types.StringValue(ns + "/" + cfg.Name.ValueString())
	cfg.Spec = types.StringValue(specJSON)
	cfg.Status = types.StringValue(statusJSON)
	cfg.Ready = types.BoolValue(readyFrom(out.Object))

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
