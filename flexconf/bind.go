package flexconf

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Validator is the custom-validation hook: any bound value whose type
// implements it has Validate called after it is fully bound, bottom-up
// (innermost values first).
type Validator interface {
	Validate() error
}

// binder walks the resolved value tree and assigns it onto Go values.
type binder struct {
	loader *Loader // access to variant routing (later PRs); may be nil in tests
}

// bindStruct binds the merged tree onto the destination struct value
// (the temp of the all-or-nothing scheme).
func (b *binder) bindStruct(n *node, v reflect.Value, path string) error {
	fields, err := structFields(v.Type())
	if err != nil {
		return err
	}
	// Strict: every key in the tree must have a matching field.
	for _, key := range n.keys {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("%s: %s: %w", n.children[key].origin(), displayPath(joinPath(path, key)), ErrUnknownField)
		}
	}
	// Bind present keys; enforce required on the merged tree.
	for _, info := range fields {
		child, present := n.children[info.key]
		if !present {
			if info.required {
				return fmt.Errorf("%s: %w (field key %q)", displayPath(joinPath(path, info.key)), ErrMissingRequired, info.key)
			}
			continue // absent key keeps the pre-populated default
		}
		fv := fieldByIndexAlloc(v, info.index)
		if err := b.bindValue(child, fv, joinPath(path, info.key)); err != nil {
			return err
		}
	}
	return callValidate(v, path)
}

// fieldByIndexAlloc walks an index chain, allocating nil pointers to inlined
// structs along the way.
func fieldByIndexAlloc(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

// bindValue assigns one tree node onto one Go value.
func (b *binder) bindValue(n *node, v reflect.Value, path string) error {
	t := v.Type()

	// time.Duration / time.Time before generic kinds.
	if t == durationType {
		return b.bindDuration(n, v, path)
	}
	if t == timeType {
		return b.bindTime(n, v, path)
	}
	// encoding.TextUnmarshaler (on the pointer receiver, the common form).
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		if err := b.requireScalar(n, path); err != nil {
			return err
		}
		if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(n.value)); err != nil {
			return b.mismatch(n, path, t, err)
		}
		return callValidate(v, path)
	}

	switch t.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(t.Elem()))
		}
		return b.bindValue(n, v.Elem(), path)
	case reflect.Interface:
		return b.bindInterface(n, v, path)
	case reflect.Struct:
		if n.kind != kindMap {
			return b.mismatch(n, path, t, fmt.Errorf("expected a map, found a %s", n.kind))
		}
		return b.bindStruct(n, v, path)
	case reflect.Map:
		return b.bindMap(n, v, path)
	case reflect.Slice:
		return b.bindSlice(n, v, path)
	default:
		if err := b.requireScalar(n, path); err != nil {
			return err
		}
		if err := coerceScalar(n.value, v); err != nil {
			return b.mismatch(n, path, t, err)
		}
		return callValidate(v, path)
	}
}

// bindInterface handles untyped (any) targets by decoding the subtree
// generically. Variant-family interfaces are handled by the variant binding
// layer (see variants.go); a non-empty non-variant interface is unsupported.
func (b *binder) bindInterface(n *node, v reflect.Value, path string) error {
	if bound, err := b.bindVariant(n, v, path); bound || err != nil {
		return err
	}
	if v.Type().NumMethod() != 0 {
		return fmt.Errorf("%s: %s: unsupported interface target %s (register a variant family?)",
			n.origin(), displayPath(path), v.Type())
	}
	generic, err := n.generic()
	if err != nil {
		return fmt.Errorf("%s: %s: %w", n.origin(), displayPath(path), err)
	}
	if generic == nil {
		v.Set(reflect.Zero(v.Type()))
	} else {
		v.Set(reflect.ValueOf(generic))
	}
	return nil
}

func (b *binder) bindMap(n *node, v reflect.Value, path string) error {
	t := v.Type()
	if t.Key().Kind() != reflect.String {
		return fmt.Errorf("%s: unsupported map key type %s (only string keys)", displayPath(path), t.Key())
	}
	if n.kind != kindMap {
		return b.mismatch(n, path, t, fmt.Errorf("expected a map, found a %s", n.kind))
	}
	m := reflect.MakeMapWithSize(t, len(n.keys))
	for _, key := range n.keys {
		ev := reflect.New(t.Elem()).Elem()
		if err := b.bindValue(n.children[key], ev, joinPath(path, key)); err != nil {
			return err
		}
		m.SetMapIndex(reflect.ValueOf(key).Convert(t.Key()), ev)
	}
	v.Set(m)
	return nil
}

func (b *binder) bindSlice(n *node, v reflect.Value, path string) error {
	t := v.Type()
	if n.kind != kindSeq {
		return b.mismatch(n, path, t, fmt.Errorf("expected a sequence, found a %s", n.kind))
	}
	s := reflect.MakeSlice(t, len(n.items), len(n.items))
	for i, item := range n.items {
		if err := b.bindValue(item, s.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	v.Set(s)
	return nil
}

func (b *binder) bindDuration(n *node, v reflect.Value, path string) error {
	if err := b.requireScalar(n, path); err != nil {
		return err
	}
	d, err := time.ParseDuration(n.value)
	if err != nil {
		return b.mismatch(n, path, durationType, fmt.Errorf("invalid duration"))
	}
	v.SetInt(int64(d))
	return nil
}

func (b *binder) bindTime(n *node, v reflect.Value, path string) error {
	if err := b.requireScalar(n, path); err != nil {
		return err
	}
	ts, err := time.Parse(time.RFC3339, n.value)
	if err != nil {
		return b.mismatch(n, path, timeType, fmt.Errorf("invalid RFC 3339 timestamp"))
	}
	v.Set(reflect.ValueOf(ts))
	return callValidate(v, path)
}

func (b *binder) requireScalar(n *node, path string) error {
	if n.kind != kindScalar {
		return fmt.Errorf("%s: %s: %w: expected a scalar, found a %s",
			n.origin(), displayPath(path), ErrTypeMismatch, n.kind)
	}
	return nil
}

// mismatch builds a type-mismatch error naming the field, key path, and
// offending value — redacting the value when it originated from a secret.
func (b *binder) mismatch(n *node, path string, t reflect.Type, cause error) error {
	value := strconv.Quote(n.value)
	if n.secret {
		value = "(redacted secret)"
	}
	if n.kind != kindScalar {
		value = "a " + n.kind.String()
	}
	return fmt.Errorf("%s: %s: %w: cannot bind %s to %s: %v",
		n.origin(), displayPath(path), ErrTypeMismatch, value, t, cause)
}

// callValidate runs the Validate() hook if the value (or its address)
// implements Validator; the returned error is wrapped with the key path.
func callValidate(v reflect.Value, path string) error {
	var hook Validator
	if v.CanInterface() {
		if h, ok := v.Interface().(Validator); ok {
			hook = h
		}
	}
	if hook == nil && v.CanAddr() && v.Addr().CanInterface() {
		if h, ok := v.Addr().Interface().(Validator); ok {
			hook = h
		}
	}
	if hook == nil {
		return nil
	}
	if err := hook.Validate(); err != nil {
		return fmt.Errorf("%s: validation failed: %w", displayPath(path), err)
	}
	return nil
}

// coerceScalar parses a scalar string into a Go value exactly as the same
// literal would bind: "8080" → int, "true" → bool.
func coerceScalar(s string, v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid bool")
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer")
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid unsigned integer")
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid float")
		}
		v.SetFloat(f)
	default:
		return fmt.Errorf("unsupported target type %s", v.Type())
	}
	return nil
}

// generic converts a subtree into untyped Go values (map[string]any, []any,
// or a scalar typed by its YAML tag) for binding into an `any` target.
func (n *node) generic() (any, error) {
	switch n.kind {
	case kindMap:
		m := make(map[string]any, len(n.keys))
		for _, key := range n.keys {
			v, err := n.children[key].generic()
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		return m, nil
	case kindSeq:
		s := make([]any, len(n.items))
		for i, item := range n.items {
			v, err := item.generic()
			if err != nil {
				return nil, err
			}
			s[i] = v
		}
		return s, nil
	default:
		return scalarByTag(n.value, n.tag), nil
	}
}

// scalarByTag infers a scalar's Go type from its YAML tag; untagged values
// (e.g. substituted tokens) infer from content like a literal would.
func scalarByTag(value, tag string) any {
	switch tag {
	case "!!str":
		return value
	case "!!null":
		return nil
	case "!!bool":
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
		return value
	case "!!int":
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
		return value
	case "!!float":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
		return value
	default:
		// Untagged: infer from content.
		if value == "" {
			return ""
		}
		if strings.EqualFold(value, "null") || value == "~" {
			return nil
		}
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
		return value
	}
}
