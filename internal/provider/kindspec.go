package provider

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Describing a CRD once, instead of writing a resource per kind.
//
// open-infra has fifteen-odd kinds and they are all the same shape: a namespaced custom
// resource whose spec is a plain object and whose status carries readiness plus a couple
// of computed fields. Hand-writing a resource each meant ~250 lines of identical CRUD,
// and — worse — fifteen separate places to silently drift from the XRD that actually
// defines the schema. A kind is declared here as data; resource_generic.go turns it into
// a working Terraform resource.
//
// KEEP IN SYNC with platform/abstraction/*-xrd.yaml in the open-infra repo. A field
// missing here is simply not expressible in HCL — there is no error, just an absence.
// This is no longer only a convention: xrd_drift_test.go (TestKindsMatchXRDs) parses the
// actual XRDs and fails CI when a spec field isn't mirrored here (deliberate omissions go in
// its `omitted` allowlist). kinds_test.go still checks the table's own internal consistency.

type attrType int

const (
	tString attrType = iota
	tBool
	tInt
	tStringList
	tIntList
	tStringMap
	tObject     // a nested block, Nested describes its fields
	tObjectList // a list of nested blocks
)

// attr is one Terraform attribute and where it lives in the CRD spec.
type attr struct {
	// Name is the HCL attribute name, snake_case by Terraform convention.
	Name string
	// Path is the location under `spec` as a sequence of keys. Empty means a
	// single key derived from Name by camelCasing it — which covers most fields.
	Path []string
	Type attrType
	// Nested describes the fields of a tObject / tObjectList.
	Nested []attr

	Required bool
	// Replaces marks a field the platform cannot change in place, so Terraform
	// must destroy and recreate rather than issue an update that silently no-ops.
	Replaces bool
	// Default mirrors the XRD's own default. Setting it here is not cosmetic: an
	// attribute with a server-side default MUST be Computed as well as Optional,
	// or every plan shows a spurious diff the moment the server fills it in.
	Default any

	Description string
	Sensitive   bool
}

// statusAttr is a read-only attribute sourced from the resource's status.
type statusAttr struct {
	Name        string
	Path        []string // under `status`; empty means the camelCased Name
	Type        attrType
	Description string
}

// kindSpec is everything needed to serve one CRD as a Terraform resource.
type kindSpec struct {
	TypeName    string // openinfra_<TypeName>
	Kind        string // Kubernetes Kind
	Plural      string // Kubernetes resource (plural, lowercase)
	Description string
	Attrs       []attr
	Status      []statusAttr
}

// specPath is where an attribute's value lives under `spec`.
func (a attr) specPath() []string {
	if len(a.Path) > 0 {
		return a.Path
	}
	return []string{camel(a.Name)}
}

// fieldName is the CRD key for a nested attribute, where a full Path makes no sense
// but an override may still be needed.
func (a attr) fieldName() string {
	if len(a.Path) == 1 {
		return a.Path[0]
	}
	return camel(a.Name)
}

func (s statusAttr) statusPath() []string {
	if len(s.Path) > 0 {
		return s.Path
	}
	return []string{camel(s.Name)}
}

// camel converts a snake_case HCL name to the lowerCamelCase Kubernetes uses.
//
// Deliberately naive about initialisms. The XRDs are not consistent — `nodeIP` but
// `sourceUrl` and `queryId` — so any general rule would be right for one field and
// wrong for the next. Fields that don't follow plain camelCasing set Path explicitly;
// that way the exception is visible next to the field it applies to.
func camel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// buildAttribute renders one attr as a framework schema attribute.
//
// The Optional/Computed pairing is the subtle part. A field with an XRD default is
// declared Optional AND Computed: Optional so it can be omitted, Computed so that when
// the server supplies the default, Terraform accepts the value rather than reporting
// drift against a null config on every plan.
func buildAttribute(a attr) schema.Attribute {
	optional := !a.Required
	computed := !a.Required && a.Default != nil

	var mods []planmodifier.String
	if a.Replaces {
		mods = append(mods, stringplanmodifier.RequiresReplace())
	}

	switch a.Type {
	case tString:
		at := schema.StringAttribute{
			Required:            a.Required,
			Optional:            optional,
			Computed:            computed,
			Sensitive:           a.Sensitive,
			MarkdownDescription: a.Description,
			PlanModifiers:       mods,
		}
		if s, ok := a.Default.(string); ok {
			at.Default = stringdefault.StaticString(s)
		}
		return at

	case tBool:
		at := schema.BoolAttribute{
			Required:            a.Required,
			Optional:            optional,
			Computed:            computed,
			MarkdownDescription: a.Description,
		}
		if b, ok := a.Default.(bool); ok {
			at.Default = booldefault.StaticBool(b)
		}
		return at

	case tInt:
		at := schema.Int64Attribute{
			Required:            a.Required,
			Optional:            optional,
			Computed:            computed,
			MarkdownDescription: a.Description,
		}
		if n, ok := a.Default.(int64); ok {
			at.Default = int64default.StaticInt64(n)
		}
		return at

	case tIntList:
		return schema.ListAttribute{
			ElementType:         types.Int64Type,
			Required:            a.Required,
			Optional:            optional,
			MarkdownDescription: a.Description,
		}

	case tStringList:
		return schema.ListAttribute{
			ElementType:         types.StringType,
			Required:            a.Required,
			Optional:            optional,
			MarkdownDescription: a.Description,
		}

	case tStringMap:
		return schema.MapAttribute{
			ElementType:         types.StringType,
			Required:            a.Required,
			Optional:            optional,
			MarkdownDescription: a.Description,
		}

	case tObject:
		return schema.SingleNestedAttribute{
			Attributes:          buildAttributes(a.Nested),
			Required:            a.Required,
			Optional:            optional,
			MarkdownDescription: a.Description,
		}

	case tObjectList:
		return schema.ListNestedAttribute{
			NestedObject:        schema.NestedAttributeObject{Attributes: buildAttributes(a.Nested)},
			Required:            a.Required,
			Optional:            optional,
			MarkdownDescription: a.Description,
		}
	}
	panic("unknown attr type for " + a.Name)
}

func buildAttributes(attrs []attr) map[string]schema.Attribute {
	out := make(map[string]schema.Attribute, len(attrs))
	for _, a := range attrs {
		out[a.Name] = buildAttribute(a)
	}
	return out
}

func buildStatusAttribute(s statusAttr) schema.Attribute {
	switch s.Type {
	case tBool:
		return schema.BoolAttribute{Computed: true, MarkdownDescription: s.Description}
	case tInt:
		return schema.Int64Attribute{Computed: true, MarkdownDescription: s.Description}
	case tStringList:
		return schema.ListAttribute{ElementType: types.StringType, Computed: true, MarkdownDescription: s.Description}
	default:
		return schema.StringAttribute{Computed: true, MarkdownDescription: s.Description}
	}
}
