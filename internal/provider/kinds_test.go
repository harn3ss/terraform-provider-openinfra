package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The kindSpec table is hand-maintained, so these tests exist to catch the mistakes a
// table invites: a duplicated name, a nested block with no fields, a default whose Go
// type doesn't match the attribute's. None of them can see the XRDs — drift against
// open-infra is a cross-repo problem the README calls out, not something a unit test
// can detect.

// TestGenericKindsWellFormed is the table's own consistency check.
func TestGenericKindsWellFormed(t *testing.T) {
	// Attribute names the generic resource always adds. A kind reusing one would be
	// silently overwritten in Schema(), so the field would simply not work.
	reserved := map[string]bool{"name": true, "namespace": true, "id": true, "ready": true}

	seenType := map[string]bool{}
	seenPlural := map[string]bool{}

	for _, k := range genericKinds {
		if k.TypeName == "" || k.Kind == "" || k.Plural == "" {
			t.Errorf("incomplete kind: %+v", k)
			continue
		}
		if seenType[k.TypeName] {
			t.Errorf("duplicate terraform type name %q", k.TypeName)
		}
		if seenPlural[k.Plural] {
			t.Errorf("duplicate CRD plural %q", k.Plural)
		}
		seenType[k.TypeName] = true
		seenPlural[k.Plural] = true

		if k.Description == "" {
			t.Errorf("%s: no description — it becomes the resource's generated docs page", k.TypeName)
		}

		names := map[string]bool{}
		for _, a := range k.Attrs {
			if reserved[a.Name] {
				t.Errorf("%s: attribute %q collides with a built-in and would be overwritten", k.TypeName, a.Name)
			}
			if names[a.Name] {
				t.Errorf("%s: duplicate attribute %q", k.TypeName, a.Name)
			}
			names[a.Name] = true
			checkAttr(t, k.TypeName, a)
		}
		for _, s := range k.Status {
			if reserved[s.Name] {
				t.Errorf("%s: status attribute %q collides with a built-in", k.TypeName, s.Name)
			}
			if names[s.Name] {
				t.Errorf("%s: status attribute %q collides with a spec attribute — "+
					"give it a distinct name and set Path to the status field", k.TypeName, s.Name)
			}
			names[s.Name] = true
		}
	}
}

func checkAttr(t *testing.T, kind string, a attr) {
	t.Helper()

	switch a.Type {
	case tObject, tObjectList:
		if len(a.Nested) == 0 {
			t.Errorf("%s.%s: nested block with no fields", kind, a.Name)
		}
		inner := map[string]bool{}
		for _, n := range a.Nested {
			if inner[n.Name] {
				t.Errorf("%s.%s: duplicate nested field %q", kind, a.Name, n.Name)
			}
			inner[n.Name] = true
			// A default on a nested attribute would force the whole block computed;
			// kinds.go documents them in prose instead.
			if n.Default != nil {
				t.Errorf("%s.%s.%s: nested attributes must not carry Default", kind, a.Name, n.Name)
			}
			checkAttr(t, kind, n)
		}
	default:
		if len(a.Nested) > 0 {
			t.Errorf("%s.%s: Nested set on a non-object attribute", kind, a.Name)
		}
	}

	if a.Required && a.Default != nil {
		t.Errorf("%s.%s: a required attribute cannot have a default", kind, a.Name)
	}

	// A default of the wrong Go type is silently dropped by buildAttribute, producing
	// an attribute that is Computed with no value — which fails at apply, far from here.
	if a.Default != nil {
		var wantKind reflect.Kind
		switch a.Type {
		case tString:
			wantKind = reflect.String
		case tBool:
			wantKind = reflect.Bool
		case tInt:
			wantKind = reflect.Int64
		default:
			t.Errorf("%s.%s: Default is only supported on string/bool/int attributes", kind, a.Name)
			return
		}
		if got := reflect.ValueOf(a.Default).Kind(); got != wantKind {
			t.Errorf("%s.%s: Default is %s, want %s (a mismatched default is silently ignored)",
				kind, a.Name, got, wantKind)
		}
	}
}

// TestGenericSchemasValid runs the framework's own schema validation over every
// generated resource. This is what catches an Optional+Computed mistake, an invalid
// attribute name, or a default on a non-computed attribute — all of which otherwise
// surface only when a user runs `terraform plan`.
func TestGenericSchemasValid(t *testing.T) {
	ctx := context.Background()
	for _, k := range genericKinds {
		t.Run(k.TypeName, func(t *testing.T) {
			r := newGenericResource(k)()
			var resp fwresource.SchemaResponse
			r.Schema(ctx, fwresource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
			}
			diags := resp.Schema.ValidateImplementation(ctx)
			if diags.HasError() {
				t.Fatalf("invalid schema: %v", diags)
			}
		})
	}
}

// TestProviderRegistersEveryKind guards the wiring: a kind in the table that never
// reaches Resources() is invisible, with no error anywhere.
func TestProviderRegistersEveryKind(t *testing.T) {
	ctx := context.Background()
	p := New("test")()
	got := map[string]bool{}
	for _, f := range p.Resources(ctx) {
		var resp fwresource.MetadataResponse
		f().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "openinfra"}, &resp)
		got[resp.TypeName] = true
	}
	for _, k := range genericKinds {
		if !got["openinfra_"+k.TypeName] {
			t.Errorf("kind %q is in the table but not registered as a resource", k.TypeName)
		}
	}
	for _, want := range []string{"openinfra_application", "openinfra_database", "openinfra_virtual_machine"} {
		if !got[want] {
			t.Errorf("hand-written resource %q is no longer registered", want)
		}
	}
	_ = providerserver.NewProtocol6WithError(p)
}

// A kind addressable both as a resource and as a data source must use the SAME type
// name in both, or `openinfra_dataflow` (data source) and `openinfra_data_flow`
// (resource) end up as two names for one thing — confusing, and impossible to fix
// after publication without breaking configs.
func TestResourceAndDataSourceNamesAgree(t *testing.T) {
	dataSourceKinds := map[string]string{} // plural -> type name
	for _, k := range crdKinds {
		dataSourceKinds[k.plural] = k.typeName
	}
	for _, k := range genericKinds {
		ds, ok := dataSourceKinds[k.Plural]
		if !ok {
			t.Errorf("%s has a resource but no data source — add it to crdKinds", k.TypeName)
			continue
		}
		if ds != k.TypeName {
			t.Errorf("%s is exposed as data source openinfra_%s but resource openinfra_%s",
				k.Kind, ds, k.TypeName)
		}
	}
}

// TestCamel pins the snake_case → lowerCamelCase mapping, which is how every HCL
// attribute name is translated to its CRD field. A wrong translation writes a field
// the API server silently prunes, so this fails loudly rather than at runtime.
func TestCamel(t *testing.T) {
	cases := map[string]string{
		"image":               "image",
		"password_secret_ref": "passwordSecretRef",
		"auto_sync_tables":    "autoSyncTables",
		"source_url":          "sourceUrl", // NOT sourceURL — the XRD spells it this way
		"query_id":            "queryId",   // likewise
		"y":                   "y",
	}
	for in, want := range cases {
		if got := camel(in); got != want {
			t.Errorf("camel(%q) = %q, want %q", in, got, want)
		}
	}

	// nodeIP is the one XRD field that does capitalise an initialism, so it must use
	// an explicit Path rather than relying on camel().
	if got := camel("node_ip"); got == "nodeIP" {
		t.Error("camel() now handles initialisms — check sourceUrl/queryId still work " +
			"and drop the Path override on file_share.node_ip")
	}
	var fs kindSpec
	for _, k := range genericKinds {
		if k.TypeName == "file_share" {
			fs = k
		}
	}
	for _, a := range fs.Attrs {
		if a.Name == "node_ip" && a.specPath()[0] != "nodeIP" {
			t.Errorf("file_share.node_ip maps to %q, want nodeIP", a.specPath()[0])
		}
	}
}

// TestManifestRoundTrip drives a realistic Stream through manifest-building and back,
// exercising nested objects, nested-in-nested objects and lists in one pass.
func TestManifestRoundTrip(t *testing.T) {
	var stream kindSpec
	for _, k := range genericKinds {
		if k.TypeName == "stream" {
			stream = k
		}
	}
	r := &genericResource{spec: stream}

	plan := map[string]any{
		"name":      "orders-cdc",
		"namespace": "data",
		"source": map[string]any{
			"engine":              "postgres",
			"host":                "orders-db-rw",
			"port":                int64(5432),
			"database":            "orders",
			"username":            "app",
			"password_secret_ref": map[string]any{"name": "orders-db-app", "key": "password"},
			"tables":              []any{"public.orders"},
			"ssl":                 true,
		},
	}

	man := r.manifest(plan)
	if man["kind"] != "Stream" {
		t.Fatalf("kind = %v", man["kind"])
	}
	if got := nested(man, "metadata", "namespace"); got != "data" {
		t.Fatalf("namespace = %v", got)
	}
	// snake_case must have become the CRD's camelCase, two levels down.
	if got := nested(man, "spec", "source", "passwordSecretRef", "name"); got != "orders-db-app" {
		t.Fatalf("passwordSecretRef.name = %v (nested renaming is broken)", got)
	}
	if got := nested(man, "spec", "source", "password_secret_ref"); got != nil {
		t.Fatalf("the HCL name leaked into the manifest: %v", got)
	}
	if got := nested(man, "spec", "source", "ssl"); got != true {
		t.Fatalf("ssl = %v", got)
	}

	// And back, as Read would do it.
	spec := man["spec"].(map[string]any)
	back := fromK8s(stream.Attrs[0], spec["source"])
	bm := back.(map[string]any)
	if bm["password_secret_ref"].(map[string]any)["name"] != "orders-db-app" {
		t.Fatalf("round trip lost passwordSecretRef: %#v", bm)
	}
	if bm["engine"] != "postgres" {
		t.Fatalf("round trip lost engine: %#v", bm)
	}
}

// A null attribute must be ABSENT from the manifest, not an explicit null. An explicit
// null tells the API server to clear the field, which defeats XRD defaulting.
func TestManifestOmitsNulls(t *testing.T) {
	var fn kindSpec
	for _, k := range genericKinds {
		if k.TypeName == "function" {
			fn = k
		}
	}
	r := &genericResource{spec: fn}
	man := r.manifest(map[string]any{"name": "hello", "image": "ghcr.io/x/y:1"})
	spec := man["spec"].(map[string]any)
	if _, present := spec["gpu"]; present {
		t.Errorf("an unset attribute was written to the manifest: %#v", spec)
	}
	if len(spec) != 1 || spec["image"] != "ghcr.io/x/y:1" {
		t.Errorf("spec = %#v, want only image", spec)
	}
	if nested(man, "metadata", "namespace") != "default" {
		t.Errorf("namespace should default to 'default', got %v", nested(man, "metadata", "namespace"))
	}
}

// tfFromGo is driven by the schema type, so a field the cluster omitted must still
// appear as a correctly-typed null. Terraform rejects state whose type doesn't match
// the schema, so getting this wrong breaks every Read.
func TestTFFromGoFillsMissingWithNull(t *testing.T) {
	objType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"a": tftypes.String,
		"b": tftypes.Bool,
		"c": tftypes.List{ElementType: tftypes.String},
	}}
	v, err := tfFromGo(objType, map[string]any{"a": "set"})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Type().Is(objType) {
		t.Fatalf("type = %s", v.Type())
	}
	var m map[string]tftypes.Value
	if err := v.As(&m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("missing attributes: %#v", m)
	}
	if !m["b"].IsNull() || !m["c"].IsNull() {
		t.Fatalf("absent fields should be null: %#v", m)
	}
}

// goFromTF must treat unknown exactly like null: sending an unknown to the API server
// is never right, and it is indistinguishable from absent when building a manifest.
func TestGoFromTFDropsUnknownAndNull(t *testing.T) {
	objType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"known":   tftypes.String,
		"unknown": tftypes.String,
		"null":    tftypes.String,
	}}
	v := tftypes.NewValue(objType, map[string]tftypes.Value{
		"known":   tftypes.NewValue(tftypes.String, "yes"),
		"unknown": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"null":    tftypes.NewValue(tftypes.String, nil),
	})
	g, err := goFromTF(v)
	if err != nil {
		t.Fatal(err)
	}
	m := g.(map[string]any)
	if len(m) != 1 || m["known"] != "yes" {
		t.Fatalf("got %#v, want only the known value", m)
	}
}

// refresh must not adopt server-side values for attributes the config never set, or
// every plan would want to remove the XRD's own defaults.
func TestRefreshLeavesUnsetAttributesAlone(t *testing.T) {
	var fn kindSpec
	for _, k := range genericKinds {
		if k.TypeName == "function" {
			fn = k
		}
	}
	r := &genericResource{spec: fn}

	state := map[string]any{"name": "hello", "image": "old:1", "gpu": nil}
	live := map[string]any{"spec": map[string]any{
		"image": "new:2",   // managed by the config → drift, must be adopted
		"gpu":   int64(1),  // never set in HCL → must stay null
		"port":  int64(80), // never set in HCL → must stay absent
	}}
	r.refresh(state, live)

	if state["image"] != "new:2" {
		t.Errorf("out-of-band change to a managed field was not detected: %v", state["image"])
	}
	if state["gpu"] != nil {
		t.Errorf("adopted a server value for an unset attribute: %v", state["gpu"])
	}
	if state["port"] != nil {
		t.Errorf("adopted a server value for an absent attribute: %v", state["port"])
	}
}
