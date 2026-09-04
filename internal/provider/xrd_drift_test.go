package provider

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"sigs.k8s.io/yaml"
)

// TestKindsMatchXRDs is the cross-repo drift guard the kinds.go header calls for, covering ALL
// provider kinds uniformly — the table-driven generic kinds AND the 3 bespoke resources.
//
// The provider mirrors open-infra's XRDs by hand, and until now nothing checked it: a spec field
// added to an XRD and forgotten here is a SILENT failure — no error, the field just can't be set
// in HCL. This parses the actual XRDs and asserts every `spec` field is expressible.
//
// Two granularities, matching how each kind is authored:
//   - Generic kinds mirror the WHOLE XRD via the kinds.go attr table, so they are checked at
//     LEAF granularity (nested objects recurse; e.g. `target.labelSelector`).
//   - Bespoke resources (Database, VirtualMachine, Application) hand-write a FLAT, curated schema,
//     so they are checked at TOP-LEVEL granularity — every top-level XRD spec field must be
//     exposed or listed in `omitted`. That still catches the thing that matters: a NEW top-level
//     field added upstream and forgotten. What a bespoke resource does NOT yet expose is recorded
//     honestly in `omitted` (a current gap / potential enhancement), not silently absent.
//
// XRDs come from $OPENINFRA_XRD_DIR or a sibling ../../../open-infra checkout; the test SKIPS
// locally when neither exists but FAILS under CI, so the guard can never silently no-op.
func TestKindsMatchXRDs(t *testing.T) {
	dir := xrdDir(t)
	docs := loadXRDDocs(t, dir)

	// Fields a kind's provider representation INTENTIONALLY does not expose in HCL, each with a
	// reason. Generic keys are "Kind.dotted.leaf.path"; bespoke keys are "<type_name>.field".
	// Every entry is a field a user cannot set through Terraform — keep them justified.
	omitted := map[string]bool{
		// ModelMonitor.threshold is a float; the generic attr table has no float type yet.
		// Use the default (0.2) or set it via the console/kubectl until a tFloat is added.
		"ModelMonitor.threshold": true,

		// --- generic ---
		// Replication.scheduling is an operational pod-placement knob (its stated purpose is the
		// chaos suite: pin the sandbox mesh onto tainted chaos nodes); set via the platform/GitOps,
		// and its `tolerations` is a preserve-unknown free-form list the typed provider can't model.
		"Replication.scheduling.nodeSelector": true,
		"Replication.scheduling.tolerations":  true,

		// --- bespoke: virtual_machine (flat curated VM resource) ---
		// Not currently exposed by the flat resource; potential enhancements, tracked here so a
		// genuinely NEW VM spec field still fails the guard rather than slipping in silently.
		"virtual_machine.existingRootClaim": true, // advanced restore path (adopt an existing root disk)
		"virtual_machine.expose":            true,
		"virtual_machine.ports":             true, // list; flat resource exposes no port list
		"virtual_machine.securityGroups":    true, // list; SG attach not yet in HCL
		"virtual_machine.sshKey":            true,

		// --- bespoke: application (flat curated app resource) ---
		"application.database":       true, // a data-only Application is the `openinfra_database` resource
		"application.domain":         true,
		"application.env":            true,
		"application.queues":         true,
		"application.scaling":        true,
		"application.secrets":        true,
		"application.securityGroups": true,
		"application.storage":        true,
		"application.sidecars":       true, // curated flat resource stays single-container; sidecars via kubectl/GitOps

		// --- bespoke: database (maps to Application spec.database) ---
		"database.name":   true, // exposed as `database_name` (metadata name is the Application name)
		"database.vector": true, // pgvector toggle not yet in HCL
	}

	var problems []string

	// --- Generic kinds: leaf-granularity completeness against the attr table. ---
	for _, k := range genericKinds {
		doc, ok := docs[k.Kind]
		if !ok {
			problems = append(problems, k.Kind+": no XRD found in "+dir+
				" (kind is in genericKinds but has no matching *xrd*.yaml — renamed or removed upstream?)")
			continue
		}
		want := map[string]bool{}
		collectLeaves("", xrdSpecProperties(doc), want)
		have := providerSpecPaths(k)
		for _, p := range sortedSet(want) {
			if omitted[k.Kind+"."+p] || have[p] {
				continue
			}
			problems = append(problems, k.Kind+": XRD spec field `"+p+
				"` is not mirrored in kinds.go (add the attr, or add \""+k.Kind+"."+p+"\" to `omitted` if deliberate)")
		}
	}

	// --- Bespoke resources: top-level completeness against the hand-written framework schema. ---
	for _, b := range bespokeResources() {
		doc, ok := docs[b.kind]
		if !ok {
			problems = append(problems, b.typeName+": no XRD found for kind "+b.kind+" in "+dir)
			continue
		}
		props := xrdSpecProperties(doc)
		for _, seg := range b.specRoot { // descend to e.g. spec.database
			sub, _ := props[seg].(map[string]any)
			if sub == nil {
				problems = append(problems, b.typeName+": XRD "+b.kind+" has no spec."+strings.Join(b.specRoot, ".")+" object")
				props = nil
				break
			}
			props, _ = sub["properties"].(map[string]any)
		}
		have := bespokeTopFields(b.r)
		for _, f := range sortedSet(topKeys(props)) {
			if omitted[b.typeName+"."+f] || have[f] {
				continue
			}
			problems = append(problems, b.typeName+" ("+b.kind+"): XRD spec field `"+f+
				"` is not exposed by the bespoke resource (expose it, or add \""+b.typeName+"."+f+"\" to `omitted`)")
		}
	}

	// --- New-kind drift: every XRD claim kind must have a provider resource OR a stated exclusion, so a
	// brand-new upstream kind can't ship with no Terraform support and still pass CI (as the IAM/governance
	// kinds below already do — intentionally console-managed, not Terraform-exposed). ---
	excludedKinds := map[string]string{
		"EncryptionKey":      "console/IAM-managed Vault Transit key; not Terraform-exposed",
		"DataClassification": "console-managed data-categorization policy; not Terraform-exposed",
		"Destruction":        "irreversible crypto-erase — a deliberate console/admin act, never Terraform-declared",
		"User":               "identity object, managed in the console Security & Identity plane",
		"Group":              "identity object, managed in the console Security & Identity plane",
		"Policy":             "IAM policy, managed in the console Security & Identity plane",
		"Role":               "IAM role, managed in the console Security & Identity plane",
		"Grant":              "temporal JIT access grant, issued from the console, not Terraform",
	}
	known := map[string]bool{}
	for _, k := range genericKinds {
		known[k.Kind] = true
	}
	for _, b := range bespokeResources() {
		known[b.kind] = true
	}
	for kind := range docs {
		if known[kind] || excludedKinds[kind] != "" {
			continue
		}
		problems = append(problems, kind+": XRD claim kind has no provider resource and is not in "+
			"`excludedKinds` — add it to genericKinds/bespoke, or to excludedKinds with a reason "+
			"(a new upstream kind must be consciously mirrored or excluded)")
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("provider has drifted from the open-infra XRDs — %d finding(s):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// bespoke describes one hand-written resource and where its schema maps in the XRDs.
type bespoke struct {
	typeName string
	kind     string
	specRoot []string // path under spec to compare against ([] = spec itself)
	r        resource.Resource
}

func bespokeResources() []bespoke {
	return []bespoke{
		{"virtual_machine", "VirtualMachine", nil, NewVirtualMachineResource()},
		{"application", "Application", nil, NewApplicationResource()},
		// A data-only Application: its schema maps to Application spec.database.
		{"database", "Application", []string{"database"}, NewDatabaseResource()},
	}
}

// bespokeTopFields returns the camelCased spec keys a bespoke resource exposes, excluding the
// metadata/computed attributes every resource adds (name/namespace/id/ready) so they can't
// falsely satisfy a same-named spec field (e.g. metadata `name` vs spec.database `name`).
func bespokeTopFields(r resource.Resource) map[string]bool {
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	reserved := map[string]bool{"name": true, "namespace": true, "id": true, "ready": true}
	out := map[string]bool{}
	for name := range sr.Schema.Attributes {
		if reserved[name] {
			continue
		}
		out[camel(name)] = true
	}
	return out
}

// topKeys returns the immediate keys of an openAPIV3Schema `properties` map.
func topKeys(props map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range props {
		out[k] = true
	}
	return out
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

// loadXRDDocs parses every XRD in dir and indexes the parsed document by its (claim) Kind.
func loadXRDDocs(t *testing.T, dir string) map[string]map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*xrd*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no XRDs matched %s/*xrd*.yaml: %v", dir, err)
	}
	out := map[string]map[string]any{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if kind := xrdKind(doc); kind != "" {
			out[kind] = doc
		}
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
				collectLeaves(path, sub, out)
				continue
			}
			out[path] = true
			continue
		}
		if typ == "array" {
			if items, _ := s["items"].(map[string]any); items != nil {
				if sub, _ := items["properties"].(map[string]any); len(sub) > 0 {
					collectLeaves(path, sub, out)
					continue
				}
			}
			out[path] = true
			continue
		}
		out[path] = true
	}
}

// providerSpecPaths returns the set of dotted `spec` leaf paths the kinds.go table covers for
// one generic kind — the mirror of collectLeaves, walking the attr tree instead of the XRD.
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
