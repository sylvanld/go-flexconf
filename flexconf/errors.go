package flexconf

import "errors"

// Sentinel errors, usable with errors.Is. Full taxonomy: docs/specs/errors.md.
var (
	// ErrConfigNotFound reports that no configured layer provides the
	// requested name.
	ErrConfigNotFound = errors.New("flexconf: config file not found in any layer")

	// ErrUnsupportedFormat reports a name with a non-.yaml/.yml extension.
	ErrUnsupportedFormat = errors.New("flexconf: unsupported config file format")

	// ErrInvalidName reports a name containing a path separator or "..".
	ErrInvalidName = errors.New("flexconf: invalid config name")

	// ErrUnknownField reports a resolved key with no matching struct field
	// (strict validation, at every level).
	ErrUnknownField = errors.New("flexconf: unknown config key")

	// ErrMissingRequired reports a required key absent from the merged tree.
	ErrMissingRequired = errors.New("flexconf: required config key missing")

	// ErrTypeMismatch reports a value that does not fit its target field type.
	ErrTypeMismatch = errors.New("flexconf: value does not fit field type")

	// ErrInvalidTag reports a malformed flexconf struct tag (a programming
	// error in the schema).
	ErrInvalidTag = errors.New("flexconf: invalid flexconf struct tag")
)
