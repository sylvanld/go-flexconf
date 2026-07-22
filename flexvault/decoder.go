package flexvault

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// Decoder unmarshals a driver's non-secret configuration section into a value
// the driver owns. It is the type of the callback passed to
// VaultDriver.Configure.
type Decoder = func(target any) error

// MapDecoder returns a Decoder that binds the given map onto the driver's
// config struct, matching keys against `flexconf` struct tags (or the
// lowercased field name when untagged). Unknown map keys are errors — a typo
// in a vault definition must not be silently dropped.
func MapDecoder(m map[string]any) Decoder {
	return func(target any) error {
		return decodeMap(m, target)
	}
}

// EnvDecoder returns a Decoder that binds environment variables onto the
// driver's config struct: each field is read from prefix + KEY, where KEY is
// the field's `flexconf` tag name (or lowercased field name) uppercased.
// E.g. EnvDecoder("FLEXCONF_")("path") reads FLEXCONF_PATH. Unset variables
// leave the field at its current value.
func EnvDecoder(prefix string) Decoder {
	return func(target any) error {
		rv, err := settableStruct(target)
		if err != nil {
			return err
		}
		for i := 0; i < rv.NumField(); i++ {
			field := rv.Type().Field(i)
			key, skip := fieldKey(field)
			if skip {
				continue
			}
			env := prefix + strings.ToUpper(key)
			val, ok := os.LookupEnv(env)
			if !ok {
				continue
			}
			if err := assignScalar(rv.Field(i), val); err != nil {
				return fmt.Errorf("flexvault: env %s: %w", env, err)
			}
		}
		return nil
	}
}

func settableStruct(target any) (reflect.Value, error) {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("flexvault: decode target must be a non-nil struct pointer, got %T", target)
	}
	return rv.Elem(), nil
}

// fieldKey resolves the config key for a struct field from its `flexconf` tag,
// mirroring the tag convention used by the flexconf loader: explicit name,
// "-" to skip, empty/absent → lowercased field name.
func fieldKey(field reflect.StructField) (key string, skip bool) {
	if !field.IsExported() {
		return "", true
	}
	tag := field.Tag.Get("flexconf")
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return "", true
	case "":
		return strings.ToLower(field.Name), false
	default:
		return name, false
	}
}

func decodeMap(m map[string]any, target any) error {
	rv, err := settableStruct(target)
	if err != nil {
		return err
	}
	fields := map[string]reflect.Value{}
	for i := 0; i < rv.NumField(); i++ {
		key, skip := fieldKey(rv.Type().Field(i))
		if skip {
			continue
		}
		fields[key] = rv.Field(i)
	}
	for key, val := range m {
		fv, ok := fields[key]
		if !ok {
			return fmt.Errorf("flexvault: unknown config key %q", key)
		}
		if err := assignValue(fv, val); err != nil {
			return fmt.Errorf("flexvault: config key %q: %w", key, err)
		}
	}
	return nil
}

func assignValue(fv reflect.Value, val any) error {
	if s, ok := val.(string); ok {
		return assignScalar(fv, s)
	}
	rv := reflect.ValueOf(val)
	if rv.IsValid() && rv.Type().AssignableTo(fv.Type()) {
		fv.Set(rv)
		return nil
	}
	if rv.IsValid() && rv.Type().ConvertibleTo(fv.Type()) &&
		rv.Kind() != reflect.String && fv.Kind() != reflect.String {
		fv.Set(rv.Convert(fv.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", val, fv.Type())
}

// assignScalar parses a string into the field's type, mirroring how the same
// literal would bind from YAML ("8080" → int, "true" → bool).
func assignScalar(fv reflect.Value, s string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("invalid bool %q", s)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer %q", s)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid unsigned integer %q", s)
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid float %q", s)
		}
		fv.SetFloat(f)
	default:
		return fmt.Errorf("unsupported field type %s", fv.Type())
	}
	return nil
}
