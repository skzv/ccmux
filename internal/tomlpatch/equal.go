package tomlpatch

import (
	"math"
	"reflect"
	"time"
)

// Equal reports whether two values decoded by BurntSushi/toml into
// `any` hold the same data. It differs from reflect.DeepEqual in the
// ways that matter for TOML: an array of tables decodes as
// []map[string]any from [[x]] headers but as []any from an inline
// array, NaN equals NaN, and datetimes compare by instant and zone.
func Equal(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, ok := bv[k]
			if !ok || !Equal(x, y) {
				return false
			}
		}
		return true
	case []any, []map[string]any:
		as, ok1 := asList(a)
		bs, ok2 := asList(b)
		if !ok1 || !ok2 || len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !Equal(as[i], bs[i]) {
				return false
			}
		}
		return true
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(av) || math.IsNaN(bv) {
			return math.IsNaN(av) && math.IsNaN(bv)
		}
		return av == bv && math.Signbit(av) == math.Signbit(bv)
	case time.Time:
		bv, ok := b.(time.Time)
		if !ok {
			return false
		}
		_, ao := av.Zone()
		_, bo := bv.Zone()
		return av.Equal(bv) && ao == bo && av.Location().String() == bv.Location().String()
	default:
		return reflect.DeepEqual(a, b)
	}
}

// asList views either array shape the decoder produces as []any.
func asList(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []map[string]any:
		out := make([]any, len(x))
		for i, m := range x {
			out[i] = m
		}
		return out, true
	}
	return nil, false
}

// tableList reports whether v is a non-empty array whose elements are
// all tables — the shape the encoder writes as [[x]] sections.
func tableList(v any) ([]map[string]any, bool) {
	l, ok := asList(v)
	if !ok || len(l) == 0 {
		return nil, false
	}
	out := make([]map[string]any, len(l))
	for i, e := range l {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		out[i] = m
	}
	return out, true
}
