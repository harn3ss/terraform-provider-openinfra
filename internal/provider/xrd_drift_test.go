package provider

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestKindsMatchXRDs is the cross-repo drift guard the kinds.go header calls for.
//
// kinds.go is a HAND-MAINTAINED mirror of the open-infra XRDs. Until now nothing checked it
// against the source of truth, so adding a spec field to an XRD and forgetting it here was a
// SILENT failure: no error, the field simply couldn't be set in HCL. This parses the XRDs and
// asserts every `spec` leaf field is expressible through the generic-kinds table.
//
// The XRDs are found via, in order: $OPENINFRA_XRD_DIR, then a sibling `../../../open-infra`
// checkout. If neither exists the test SKIPS locally (a contributor without the sibling repo
// isn't blocked) but FAILS under CI — CI must provide the XRDs, or the guard is silently off.
//
// Scope: the 13 generic kinds in genericKinds. The three bespoke resources (Database,
// VirtualMachine, Application) hand-write their schemas in resource_*.go and are out of scope
// here — extending the guard to them via framework-schema introspection is a tracked follow-up.
func TestKindsMatchXRDs(t *testing.T) {
	dir := xrdDir(t)

	// XRD spec fields the provider INTENTIONALLY does not expose in HCL. Key: "Kind.dotted.path".
	// Every entry is a field a user cannot set through Terraform — keep it short and justified.
	omitted := map[string]bool{
		// Replication.scheduling is an operational pod-placement knob whose stated purpose is
		// the chaos suite (pin the disposable sandbox mesh onto tainted chaos nodes); it's set
		// via the platform/GitOps, not typical Terraform-managed replication. Its `tolerations`
		// is a free-form preserve-unknown list the typed provider can't cleanly express anyway.
		// Omitted deliberately — now visible and asserted here rather than a silent absence.
		"Replication.scheduling.nodeSelector": true,
		"Replication.scheduling.tolerations":  true,
	}

	byKind := loadXRDSpecFields(t, dir)

	var problems []string
	for _, k := range genericKinds {
		want, ok := byKind[k.Kind]
		if !ok {
			problems = append(problems, k.Kind+": no XRD found in "+dir+
				" (kind is in genericKinds but has no matching *xrd*.yaml — renamed or removed upstream?)")
			continue
		}
		have := providerSpecPaths(k)
		for _, p := range sortedSet(want) {
			if omitted[k.Kind+"."+p] {
				continue
			}
			if !have[p] {
				problems = append(problems, k.Kind+": XRD spec field `"+p+
					"` is not mirrored in kinds.go (add the attr, or add \""+k.Kind+"."+p+"\" to `omitted` if deliberate)")
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("kinds.go has drifted from the open-infra XRDs — %d field(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// xrdDir resolves where the open-infra XRDs live (see the test doc for the search order).
func xrdDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("OPENINFRA_XRD_DIR"); d != "" {
		return d
	}
	sibling := filepath.Join("..", "..", "..", "open-infra", "platform", "abstraction")
	if fi, err := os.Stat(sibling); err == nil && fi.IsDir() {
		return sibling
	}
	if os.Getenv("CI") != "" {
		t.Fatal("open-infra XRDs not found: set OPENINFRA_XRD_DIR or check out open-infra as a sibling " +
			"(required under CI — this cross-repo drift guard must not silently skip)")
	}
	t.Skip("open-infra XRDs not found; set OPENINFRA_XRD_DIR or check out open-infra as a sibling to run the drift guard")
	return ""
}

// loadXRDSpecFields parses every XRD in dir and returns, per Kind, the set of dotted `spec`
// LEAF field paths (e.g. "target.labelSelector"). Objects with sub-properties recurse to their
// leaves; maps, preserve-unknown blocks and scalar arrays are leaves themselves.
func loadXRDSpecFields(t *testing.T, dir string) map[string]map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*xrd*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no XRDs matched %s/*xrd*.yaml: %v", dir, err)
	}
	out := map[string]map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		kind := xrdKind(doc)
		if kind == "" {
			continue
		}
		fields := map[string]bool{}
		collectLeaves("", xrdSpecProperties(doc), fields)
		out[kind] = fields
	}
	return out
}

// xrdKind returns the claim kind (what users create) or, failing that, the composite kind.
func xrdKind(doc map[string]any) string {
	spec, _ := doc["spec"].(map[string]any)
	if spec == nil {
		return ""
	}
	if cn, _ := spec["claimNames"].(map[string]any); cn != nil {
		if k, _ := cn["kind"].(string); k != "" {
			return k
		}
	}
	if n, _ := spec["names"].(map[string]any); n != nil {
		if k, _ := n["kind"].(string); k != "" {
			return k
		}
	}
	return ""
}

// xrdSpecProperties digs out spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.
func xrdSpecProperties(doc map[string]any) map[string]any {
	spec, _ := doc["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	if len(versions) == 0 {
		return nil
	}
	v0, _ := versions[0].(map[string]any)
	schema, _ := v0["schema"].(map[string]any)
	oapi, _ := schema["openAPIV3Schema"].(map[string]any)
	props, _ := oapi["properties"].(map[string]any)
	specSchema, _ := props["spec"].(map[string]any)
	specProps, _ := specSchema["properties"].(map[string]any)
	return specProps
}

// collectLeaves walks an openAPIV3Schema `properties` map, adding a dotted path for each LEAF.
// An object WITH properties recurses; an object that is a map (additionalProperties) or
// preserve-unknown, a scalar array, or a plain scalar is itself a leaf.
func collectLeaves(prefix string, props map[string]any, out map[string]bool) {
	for name, raw := range props {
		s, _ := raw.(map[string]any)
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if s == nil {
			out[path] = true
			continue
		}
		typ, _ := s["type"].(string)
		if typ == "object" {
			if sub, _ := s["properties"].(map[string]any); len(sub) > 0 {
				collectLeaves(path, sub, out) // nested object → recurse to its leaves
				continue
			}
			out[path] = true // map / preserve-unknown → leaf
			continue
		}
		if typ == "array" {
			if items, _ := s["items"].(map[string]any); items != nil {
				if sub, _ := items["properties"].(map[string]any); len(sub) > 0 {
					collectLeaves(path, sub, out) // list-of-objects → recurse to item leaves
					continue
				}
			}
			out[path] = true // scalar array → leaf
			continue
		}
		out[path] = true // scalar → leaf
	}
}

// providerSpecPaths returns the set of dotted `spec` leaf paths the kinds.go table covers for
// one kind — the mirror of collectLeaves, walking the attr tree instead of the XRD.
func providerSpecPaths(k kindSpec) map[string]bool {
	out := map[string]bool{}
	var walk func(prefix []string, attrs []attr)
	walk = func(prefix []string, attrs []attr) {
		for _, a := range attrs {
			var path []string
			if len(prefix) == 0 {
				path = a.specPath()
			} else {
				path = append(append([]string{}, prefix...), a.fieldName())
			}
			if (a.Type == tObject || a.Type == tObjectList) && len(a.Nested) > 0 {
				walk(path, a.Nested)
			} else {
				out[strings.Join(path, ".")] = true
			}
		}
	}
	walk(nil, k.Attrs)
	return out
}

func sortedSet(m map[string]bool) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
