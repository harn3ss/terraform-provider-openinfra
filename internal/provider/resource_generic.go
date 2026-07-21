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
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

// One resource implementation, driven by the kindSpec table in kinds.go.
//
// Every open-infra CRD has the same lifecycle — create a namespaced custom resource,
// read its status, replace its spec, delete it — so the per-kind work is describing the
// schema, not re-implementing CRUD. See kindspec.go for why this is a table.

var (
	_ resource.Resource                = (*genericResource)(nil)
	_ resource.ResourceWithConfigure   = (*genericResource)(nil)
	_ resource.ResourceWithImportState = (*genericResource)(nil)
)

type genericResource struct {
	spec kindSpec
	c    *client.Client
}

func newGenericResource(k kindSpec) func() resource.Resource {
	return func() resource.Resource { return &genericResource{spec: k} }
}

func (r *genericResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.spec.TypeName
}

func (r *genericResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := buildAttributes(r.spec.Attrs)

	// Identity and readiness are the same for every kind.
	attrs["name"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "Resource name. Changing it replaces the resource.",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attrs["namespace"] = schema.StringAttribute{
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString("default"),
		MarkdownDescription: "Kubernetes namespace. Changing it replaces the resource.",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attrs["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "`namespace/name`, the import identifier.",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["ready"] = schema.BoolAttribute{
		Computed: true,
		MarkdownDescription: "Whether the resource's Ready condition is True. Terraform returns as soon " +
			"as the object is accepted, so this is usually `false` immediately after apply — the platform " +
			"reconciles asynchronously.",
	}
	for _, s := range r.spec.Status {
		attrs[s.Name] = buildStatusAttribute(s)
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: r.spec.Description,
		Attributes:          attrs,
	}
}

func (r *genericResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ── plan ⇄ manifest ────────────────────────────────────────────────────────────

// toK8s renames an attribute's value from HCL's snake_case to the CRD's lowerCamelCase,
// recursively for nested blocks. Free-form maps (tStringMap) pass through untouched —
// their keys are user data (pod labels, for instance), not schema.
func toK8s(a attr, v any) any {
	switch a.Type {
	case tObject:
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		return renameToK8s(a.Nested, m)
	case tObjectList:
		items, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, 0, len(items))
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				out = append(out, it)
				continue
			}
			out = append(out, renameToK8s(a.Nested, m))
		}
		return out
	default:
		return v
	}
}

func renameToK8s(nestedAttrs []attr, m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for _, na := range nestedAttrs {
		v, ok := m[na.Name]
		if !ok || v == nil {
			continue
		}
		out[na.fieldName()] = toK8s(na, v)
	}
	return out
}

// fromK8s is toK8s in reverse, for rebuilding state from a live object.
func fromK8s(a attr, v any) any {
	switch a.Type {
	case tObject:
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		return renameFromK8s(a.Nested, m)
	case tObjectList:
		items, ok := v.([]any)
		if !ok {
			return nil
		}
		out := make([]any, 0, len(items))
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, renameFromK8s(a.Nested, m))
		}
		return out
	default:
		return v
	}
}

func renameFromK8s(nestedAttrs []attr, m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for _, na := range nestedAttrs {
		v, ok := m[na.fieldName()]
		if !ok {
			continue
		}
		out[na.Name] = fromK8s(na, v)
	}
	return out
}

// planData flattens a plan or state into a map keyed by HCL attribute name.
func planData(raw tftypes.Value) (map[string]any, error) {
	g, err := goFromTF(raw)
	if err != nil {
		return nil, err
	}
	m, ok := g.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected an object, got %T", g)
	}
	return m, nil
}

func nameNamespace(m map[string]any) (string, string) {
	name, _ := m["name"].(string)
	ns, _ := m["namespace"].(string)
	if ns == "" {
		ns = "default"
	}
	return name, ns
}

func (r *genericResource) manifest(m map[string]any) map[string]any {
	name, ns := nameNamespace(m)
	spec := map[string]any{}
	for _, a := range r.spec.Attrs {
		v, ok := m[a.Name]
		if !ok || v == nil {
			continue
		}
		setNested(spec, toK8s(a, v), a.specPath()...)
	}
	return map[string]any{
		"apiVersion": client.Group + "/" + client.Version,
		"kind":       r.spec.Kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       spec,
	}
}

// computed fills in the attributes the server owns. Note what it does NOT do: it never
// overwrites a configured attribute from the server's response. A Composition fills in
// XRD defaults the plan didn't have — nested ones especially — and writing those back
// would make Terraform report "inconsistent result after apply" on create, and a
// permanent diff on every subsequent plan. See the drift note on refresh() below.
func (r *genericResource) computed(m map[string]any, obj map[string]any) {
	name, ns := nameNamespace(m)
	m["id"] = ns + "/" + name
	m["namespace"] = ns
	m["ready"] = readyFrom(obj)

	status, _ := obj["status"].(map[string]any)
	for _, s := range r.spec.Status {
		if status == nil {
			m[s.Name] = nil
			continue
		}
		m[s.Name] = nested(status, s.statusPath()...)
	}
}

// setRaw encodes m into a state value matching the schema.
func setRaw(ctx context.Context, schemaType tftypes.Type, m map[string]any) (tftypes.Value, error) {
	return tfFromGo(schemaType, m)
}

// ── CRUD ───────────────────────────────────────────────────────────────────────

func (r *genericResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	m, err := planData(req.Plan.Raw)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the plan", err.Error())
		return
	}
	_, ns := nameNamespace(m)
	out, err := r.c.Create(ctx, r.spec.Plural, ns, r.manifest(m))
	if err != nil {
		resp.Diagnostics.AddError("Could not create "+r.spec.Kind, err.Error())
		return
	}
	r.computed(m, out.Object)

	val, err := setRaw(ctx, resp.State.Schema.Type().TerraformType(ctx), m)
	if err != nil {
		resp.Diagnostics.AddError("Could not encode state", err.Error())
		return
	}
	resp.State.Raw = val
}

func (r *genericResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	m, err := planData(req.State.Raw)
	if err != nil {
		resp.Diagnostics.AddError("Could not read state", err.Error())
		return
	}
	name, ns := nameNamespace(m)
	out, err := r.c.Get(ctx, r.spec.Plural, ns, name)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read "+r.spec.Kind, err.Error())
		return
	}
	r.refresh(m, out.Object)
	r.computed(m, out.Object)

	val, err := setRaw(ctx, resp.State.Schema.Type().TerraformType(ctx), m)
	if err != nil {
		resp.Diagnostics.AddError("Could not encode state", err.Error())
		return
	}
	resp.State.Raw = val
}

// refresh pulls configured attributes back from the cluster so out-of-band edits show
// up as a diff.
//
// It deliberately refreshes ONLY scalar and scalar-list attributes that are already
// set in state. Two reasons, both about not manufacturing false diffs:
//
//   - An attribute the config never set is left alone. The XRD supplies a default, and
//     adopting the server's value would make Terraform want to "remove" it forever.
//   - Nested blocks are skipped entirely, because defaults inside them (a source's
//     port: 5432, schemas: ["public"]) come back populated even when the config gave
//     only the required keys.
//
// The honest consequence: an out-of-band change to a field you never set in HCL, or to
// anything inside a nested block, is not detected. It is fixed on the next apply that
// touches the resource, since apply always sends the full desired spec.
func (r *genericResource) refresh(m map[string]any, obj map[string]any) {
	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		return
	}
	for _, a := range r.spec.Attrs {
		switch a.Type {
		case tObject, tObjectList, tStringMap:
			continue
		}
		if m[a.Name] == nil {
			continue
		}
		if v := nested(spec, a.specPath()...); v != nil {
			m[a.Name] = fromK8s(a, v)
		}
	}
}

func (r *genericResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	m, err := planData(req.Plan.Raw)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the plan", err.Error())
		return
	}
	_, ns := nameNamespace(m)
	out, err := r.c.Update(ctx, r.spec.Plural, ns, r.manifest(m))
	if err != nil {
		resp.Diagnostics.AddError("Could not update "+r.spec.Kind, err.Error())
		return
	}
	r.computed(m, out.Object)

	val, err := setRaw(ctx, resp.State.Schema.Type().TerraformType(ctx), m)
	if err != nil {
		resp.Diagnostics.AddError("Could not encode state", err.Error())
		return
	}
	resp.State.Raw = val
}

func (r *genericResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	m, err := planData(req.State.Raw)
	if err != nil {
		resp.Diagnostics.AddError("Could not read state", err.Error())
		return
	}
	name, ns := nameNamespace(m)
	if err := r.c.Delete(ctx, r.spec.Plural, ns, name); err != nil {
		resp.Diagnostics.AddError("Could not delete "+r.spec.Kind, err.Error())
	}
}

func (r *genericResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ns, name := "default", req.ID
	if before, after, found := strings.Cut(req.ID, "/"); found {
		ns, name = before, after
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), ns)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ns+"/"+name)...)
}
