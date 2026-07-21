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

var (
	_ resource.Resource                = (*databaseResource)(nil)
	_ resource.ResourceWithConfigure   = (*databaseResource)(nil)
	_ resource.ResourceWithImportState = (*databaseResource)(nil)
)

func NewDatabaseResource() resource.Resource { return &databaseResource{} }

type databaseResource struct{ c *client.Client }

type databaseModel struct {
	Name             types.String `tfsdk:"name"`
	Namespace        types.String `tfsdk:"namespace"`
	Engine           types.String `tfsdk:"engine"`
	DatabaseName     types.String `tfsdk:"database_name"`
	HighAvailability types.Bool   `tfsdk:"high_availability"`
	Expose           types.Bool   `tfsdk:"expose"`
	Stopped          types.Bool   `tfsdk:"stopped"`
	ID               types.String `tfsdk:"id"`
	Ready            types.Bool   `tfsdk:"ready"`
	ConnectionSecret types.String `tfsdk:"connection_secret"`
}

func (r *databaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (r *databaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A managed database — a data-only `kind: Application`. Compiles to the " +
			"engine's operator resources plus a generated connection Secret.",
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
			"engine": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "One of `postgres`, `mysql`, `mongo`, `babelfish`. Changing the engine " +
					"replaces the database — the storage layout differs per engine.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"database_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Logical database name. Defaults to the resource name.",
			},
			"high_availability": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Run a replicated/clustered topology where the engine supports it.",
			},
			"expose": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Publish the database on a LAN address.",
			},
			"stopped": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Stop the database (RDS-style): compute is scaled to zero, storage retained.",
			},
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ready": schema.BoolAttribute{
				Computed: true,
			},
			"connection_secret": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Name of the generated Secret holding connection details. Read it with " +
					"the `kubernetes` provider if you need to wire credentials into another resource.",
			},
		},
	}
}

func (r *databaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m databaseModel) manifest() map[string]any {
	db := map[string]any{"engine": m.Engine.ValueString()}
	name := m.DatabaseName.ValueString()
	if name == "" {
		name = m.Name.ValueString()
	}
	db["name"] = name
	if !m.HighAvailability.IsNull() {
		db["highAvailability"] = m.HighAvailability.ValueBool()
	}
	if !m.Expose.IsNull() {
		db["expose"] = m.Expose.ValueBool()
	}
	if !m.Stopped.IsNull() {
		db["stopped"] = m.Stopped.ValueBool()
	}
	return map[string]any{
		"apiVersion": client.Group + "/" + client.Version,
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      m.Name.ValueString(),
			"namespace": m.Namespace.ValueString(),
		},
		"spec": map[string]any{"database": db},
	}
}

// connectionSecretName mirrors the composition's naming per engine.
func connectionSecretName(engine, name string) string {
	switch strings.ToLower(engine) {
	case "postgres":
		return name + "-db-app"
	case "mysql":
		return name + "-mysql-app"
	case "mongo":
		return name + "-mongo-app"
	case "babelfish":
		return name + "-babelfish"
	default:
		return ""
	}
}

func (r *databaseResource) apply(ctx context.Context, m *databaseModel, create bool) error {
	var (
		out any
		err error
	)
	if create {
		out, err = r.c.Create(ctx, appResource, m.Namespace.ValueString(), m.manifest())
	} else {
		out, err = r.c.Update(ctx, appResource, m.Namespace.ValueString(), m.manifest())
	}
	if err != nil {
		return err
	}
	m.ID = types.StringValue(m.Namespace.ValueString() + "/" + m.Name.ValueString())
	m.ConnectionSecret = types.StringValue(connectionSecretName(m.Engine.ValueString(), m.Name.ValueString()))
	if u, ok := out.(interface{ UnstructuredContent() map[string]any }); ok {
		m.Ready = types.BoolValue(readyFrom(u.UnstructuredContent()))
	} else {
		m.Ready = types.BoolValue(false)
	}
	return nil
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan, true); err != nil {
		resp.Diagnostics.AddError("Could not create database", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseModel
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
		resp.Diagnostics.AddError("Could not read database", err.Error())
		return
	}
	state.ID = types.StringValue(state.Namespace.ValueString() + "/" + state.Name.ValueString())
	state.Ready = types.BoolValue(readyFrom(out.Object))
	state.ConnectionSecret = types.StringValue(connectionSecretName(state.Engine.ValueString(), state.Name.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan, false); err != nil {
		resp.Diagnostics.AddError("Could not update database", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.Delete(ctx, appResource, state.Namespace.ValueString(), state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Could not delete database", err.Error())
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ns, name := "default", req.ID
	if before, after, found := strings.Cut(req.ID, "/"); found {
		ns, name = before, after
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), ns)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ns+"/"+name)...)
}
