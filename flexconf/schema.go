package flexconf

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// fieldInfo describes one bindable struct field after tag resolution.
type fieldInfo struct {
	key      string
	index    []int // reflect field index chain (through inlined structs)
	required bool
	selector bool
	typ      reflect.Type
}

// tagOptions is the parsed form of a `flexconf:"name,opt1,opt2"` tag.
type tagOptions struct {
	name     string
	skip     bool
	required bool
	selector bool
	inline   bool
}

func parseTag(field reflect.StructField) (tagOptions, error) {
	var o tagOptions
	tag, ok := field.Tag.Lookup("flexconf")
	if !ok {
		o.name = strings.ToLower(field.Name)
		return o, nil
	}
	parts := strings.Split(tag, ",")
	switch parts[0] {
	case "-":
		o.skip = true
		return o, nil
	case "":
		o.name = strings.ToLower(field.Name)
	default:
		o.name = parts[0]
	}
	for _, opt := range parts[1:] {
		switch opt {
		case "required":
			o.required = true
		case "selector":
			o.selector = true
		case "inline":
			o.inline = true
		case "":
			return o, fmt.Errorf("%w: empty option in %q on field %s", ErrInvalidTag, tag, field.Name)
		default:
			return o, fmt.Errorf("%w: unknown option %q in %q on field %s", ErrInvalidTag, opt, tag, field.Name)
		}
	}
	return o, nil
}

// structFields resolves a struct type's bindable fields, promoting inlined /
// anonymous-embedded struct fields into the parent level. Key collisions
// after promotion are tag errors.
func structFields(t reflect.Type) (map[string]fieldInfo, error) {
	out := map[string]fieldInfo{}
	if err := collectFields(t, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

func collectFields(t reflect.Type, prefix []int, out map[string]fieldInfo) error {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			// An anonymous field of unexported struct type still promotes its
			// exported fields (matching encoding/json); anything else is skipped.
			if !field.Anonymous || field.Type.Kind() != reflect.Struct {
				continue
			}
		}
		opts, err := parseTag(field)
		if err != nil {
			return err
		}
		if opts.skip {
			continue
		}
		index := append(append([]int{}, prefix...), i)
		ft := field.Type

		// An anonymous (embedded) struct with no explicit name tag, or any
		// field tagged inline, promotes its fields into this level.
		_, hasTag := field.Tag.Lookup("flexconf")
		inlined := opts.inline || (field.Anonymous && !hasTag)
		if inlined {
			st := ft
			if st.Kind() == reflect.Pointer {
				st = st.Elem()
			}
			if st.Kind() != reflect.Struct {
				return fmt.Errorf("%w: inline on non-struct field %s", ErrInvalidTag, field.Name)
			}
			if err := collectFields(st, index, out); err != nil {
				return err
			}
			continue
		}

		if _, dup := out[opts.name]; dup {
			return fmt.Errorf("%w: duplicate config key %q in %s", ErrInvalidTag, opts.name, t)
		}
		out[opts.name] = fieldInfo{
			key: opts.name, index: index, required: opts.required,
			selector: opts.selector, typ: ft,
		}
	}
	return nil
}

var (
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	durationType        = reflect.TypeOf(time.Duration(0))
	timeType            = reflect.TypeOf(time.Time{})
)

// expectedKind reports the node kind a field type expects, or exempt=true for
// shapes the engine cannot know statically (interfaces / polymorphic fields,
// and any-typed maps).
func expectedKind(t reflect.Type) (k nodeKind, exempt bool) {
	if t == durationType || t == timeType {
		return kindScalar, false
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) || t.Implements(textUnmarshalerType) {
		return kindScalar, false
	}
	switch t.Kind() {
	case reflect.Pointer:
		return expectedKind(t.Elem())
	case reflect.Interface:
		return 0, true // polymorphic: differing shapes are legitimate
	case reflect.Struct, reflect.Map:
		return kindMap, false
	case reflect.Slice, reflect.Array:
		return kindSeq, false
	default:
		return kindScalar, false
	}
}

// validateShape checks one layer's parsed tree against the schema: the shape
// (map / scalar / sequence) of every key the file contains must match the
// field's kind. Only present keys are checked (partial files are valid);
// required-ness is checked later, on the merged tree. Polymorphic
// (interface-typed) fields are exempt.
func validateShape(n *node, t reflect.Type, path string) error {
	if t == durationType || t == timeType ||
		reflect.PointerTo(t).Implements(textUnmarshalerType) || t.Implements(textUnmarshalerType) {
		return checkKind(n, kindScalar, path)
	}
	switch t.Kind() {
	case reflect.Pointer:
		return validateShape(n, t.Elem(), path)
	case reflect.Interface:
		return nil
	case reflect.Struct:
		if err := checkKind(n, kindMap, path); err != nil {
			return err
		}
		fields, err := structFields(t)
		if err != nil {
			return err
		}
		for _, key := range n.keys {
			info, ok := fields[key]
			if !ok {
				continue // unknown keys are a bind-time (merged-tree) concern
			}
			if err := validateShape(n.children[key], info.typ, joinPath(path, key)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if err := checkKind(n, kindMap, path); err != nil {
			return err
		}
		for _, key := range n.keys {
			if err := validateShape(n.children[key], t.Elem(), joinPath(path, key)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		if err := checkKind(n, kindSeq, path); err != nil {
			return err
		}
		for i, item := range n.items {
			if err := validateShape(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return checkKind(n, kindScalar, path)
	}
}

func checkKind(n *node, want nodeKind, path string) error {
	if n.kind != want {
		return fmt.Errorf("%s: %s: expected a %s, found a %s", n.origin(), displayPath(path), want, n.kind)
	}
	return nil
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func displayPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}
