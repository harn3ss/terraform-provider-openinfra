package provider

import (
	"fmt"
	"math/big"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Converting between Terraform's value model and plain Go, so that one generic
// resource implementation can serve every CRD.
//
// The alternative — a Go struct per kind with `tfsdk` tags — is what forces a
// hand-written resource per kind. Working with tftypes.Value directly means the
// schema table is the only per-kind artifact.

// goFromTF flattens a Terraform value into plain Go (map / slice / string / bool /
// int64 / nil). Null and unknown both become nil: an unknown is a value Terraform has
// not computed yet, which for manifest-building purposes is indistinguishable from
// absent — and sending an unknown to the API server is never right.
func goFromTF(v tftypes.Value) (any, error) {
	if !v.IsKnown() || v.IsNull() {
		return nil, nil
	}
	t := v.Type()

	switch {
	case t.Is(tftypes.String):
		var s string
		if err := v.As(&s); err != nil {
			return nil, err
		}
		return s, nil

	case t.Is(tftypes.Bool):
		var b bool
		if err := v.As(&b); err != nil {
			return nil, err
		}
		return b, nil

	case t.Is(tftypes.Number):
		var n *big.Float
		if err := v.As(&n); err != nil {
			return nil, err
		}
		i, _ := n.Int64()
		return i, nil

	case t.Is(tftypes.List{}) || t.Is(tftypes.Set{}) || t.Is(tftypes.Tuple{}):
		var items []tftypes.Value
		if err := v.As(&items); err != nil {
			return nil, err
		}
		out := make([]any, 0, len(items))
		for _, it := range items {
			g, err := goFromTF(it)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil

	case t.Is(tftypes.Map{}) || t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		if err := v.As(&m); err != nil {
			return nil, err
		}
		out := make(map[string]any, len(m))
		for k, val := range m {
			g, err := goFromTF(val)
			if err != nil {
				return nil, err
			}
			// Drop nils: a null attribute must not become an explicit JSON null in
			// the manifest, which the API server would treat as "unset this field"
			// and which defeats server-side defaulting.
			if g == nil {
				continue
			}
			out[k] = g
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported terraform type %s", t.String())
}

// tfFromGo builds a Terraform value of type t from plain Go data, filling in nulls for
// anything absent. It is driven by the SCHEMA type rather than by the data, so a field
// the cluster omitted still appears in state as a correctly-typed null instead of
// vanishing — Terraform rejects a state value whose type doesn't match the schema.
func tfFromGo(t tftypes.Type, data any) (tftypes.Value, error) {
	if data == nil {
		return tftypes.NewValue(t, nil), nil
	}

	switch {
	case t.Is(tftypes.String):
		switch s := data.(type) {
		case string:
			return tftypes.NewValue(t, s), nil
		default:
			// The CRD may hold a number or bool where the schema says string
			// (quantities like "8Gi" are strings, but "8" may arrive unquoted).
			return tftypes.NewValue(t, fmt.Sprintf("%v", data)), nil
		}

	case t.Is(tftypes.Bool):
		b, ok := data.(bool)
		if !ok {
			return tftypes.NewValue(t, nil), nil
		}
		return tftypes.NewValue(t, b), nil

	case t.Is(tftypes.Number):
		switch n := data.(type) {
		case int64:
			return tftypes.NewValue(t, n), nil
		case int:
			return tftypes.NewValue(t, int64(n)), nil
		case float64:
			return tftypes.NewValue(t, int64(n)), nil
		default:
			return tftypes.NewValue(t, nil), nil
		}

	case t.Is(tftypes.List{}):
		elem := t.(tftypes.List).ElementType
		items, ok := data.([]any)
		if !ok {
			return tftypes.NewValue(t, nil), nil
		}
		vals := make([]tftypes.Value, 0, len(items))
		for _, it := range items {
			v, err := tfFromGo(elem, it)
			if err != nil {
				return tftypes.Value{}, err
			}
			vals = append(vals, v)
		}
		return tftypes.NewValue(t, vals), nil

	case t.Is(tftypes.Map{}):
		elem := t.(tftypes.Map).ElementType
		m, ok := data.(map[string]any)
		if !ok {
			return tftypes.NewValue(t, nil), nil
		}
		vals := make(map[string]tftypes.Value, len(m))
		for k, it := range m {
			v, err := tfFromGo(elem, it)
			if err != nil {
				return tftypes.Value{}, err
			}
			vals[k] = v
		}
		return tftypes.NewValue(t, vals), nil

	case t.Is(tftypes.Object{}):
		attrTypes := t.(tftypes.Object).AttributeTypes
		m, _ := data.(map[string]any)
		vals := make(map[string]tftypes.Value, len(attrTypes))
		for name, at := range attrTypes {
			// Every declared attribute must be present, null if the data has no
			// value for it — an object value with a missing key is invalid.
			v, err := tfFromGo(at, m[name])
			if err != nil {
				return tftypes.Value{}, err
			}
			vals[name] = v
		}
		return tftypes.NewValue(t, vals), nil
	}
	return tftypes.Value{}, fmt.Errorf("unsupported terraform type %s", t.String())
}

// nested walks a map by key path, returning nil if any step is missing or is not
// itself a map. Used for both spec and status lookups.
func nested(obj map[string]any, path ...string) any {
	cur := any(obj)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[p]
		if !ok {
			return nil
		}
	}
	return cur
}

// setNested writes value at the given key path, creating intermediate maps.
func setNested(obj map[string]any, value any, path ...string) {
	cur := obj
	for _, p := range path[:len(path)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}
