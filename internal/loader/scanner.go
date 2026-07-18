package loader

import (
	"fmt"
	"regexp"
	"strings"
)

// namespace identifies which of the three reference kinds a $( … ) names.
type namespace int

const (
	nsEnv namespace = iota
	nsSecret
	nsConfig
)

func (ns namespace) String() string {
	switch ns {
	case nsEnv:
		return "env"
	case nsSecret:
		return "secret"
	default:
		return "config"
	}
}

// ref is one parsed $( namespace:name ) reference.
type ref struct {
	ns     namespace
	name   string // env var, secret key, or include path — validated per-ns
	def    string // env-only :- fallback, verbatim
	hasDef bool
}

// segment is one piece of a scanned scalar: either literal text or a
// reference. A scalar with no references scans to a single literal segment
// (or none, when empty).
type segment struct {
	lit string
	ref *ref
}

var (
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)
)

// scan splits a scalar value into literal and reference segments. $$( is the
// escape for a literal $(. Only a contiguous "$(" triggers the scanner. A $(
// that does not parse as $( namespace:name ) is an error — a typo fails loud,
// never silently survives as text.
func scan(s string) ([]segment, error) {
	var segs []segment
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			segs = append(segs, segment{lit: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] == '$' && i+2 < len(s) && s[i+1] == '$' && s[i+2] == '(' {
			lit.WriteString("$(") // the $$( escape
			i += 3
			continue
		}
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '(' {
			end := strings.IndexByte(s[i:], ')')
			if end < 0 {
				return nil, fmt.Errorf("unterminated reference %q (a literal $( is escaped as $$( )", s[i:])
			}
			r, err := parseRef(s[i+2 : i+end])
			if err != nil {
				return nil, fmt.Errorf("malformed reference %q: %w", s[i:i+end+1], err)
			}
			flush()
			segs = append(segs, segment{ref: r})
			i += end + 1
			continue
		}
		lit.WriteByte(s[i])
		i++
	}
	flush()
	return segs, nil
}

// parseRef parses the inside of $( … ). Whitespace around the namespace and
// name is insignificant; an env :- fallback is taken verbatim.
func parseRef(inner string) (*ref, error) {
	colon := strings.IndexByte(inner, ':')
	if colon < 0 {
		return nil, fmt.Errorf("want $( namespace:name )")
	}
	nsName := strings.TrimSpace(inner[:colon])
	rest := inner[colon+1:]

	var ns namespace
	switch nsName {
	case "env":
		ns = nsEnv
	case "secret":
		ns = nsSecret
	case "config":
		ns = nsConfig
	default:
		return nil, fmt.Errorf("unknown namespace %q (want env, secret or config)", nsName)
	}

	r := &ref{ns: ns}
	if cut := strings.Index(rest, ":-"); cut >= 0 {
		if ns != nsEnv {
			return nil, fmt.Errorf("only $(env:…) supports a :- default; a missing %s always errors", ns)
		}
		r.def, r.hasDef = rest[cut+2:], true
		rest = rest[:cut]
	}
	r.name = strings.TrimSpace(rest)

	switch ns {
	case nsEnv:
		if !envNameRe.MatchString(r.name) {
			return nil, fmt.Errorf("invalid env var name %q", r.name)
		}
	case nsSecret:
		if !secretNameRe.MatchString(r.name) {
			return nil, fmt.Errorf("invalid secret name %q (want path-like segments of [A-Za-z0-9_.-]+)", r.name)
		}
	case nsConfig:
		if r.name == "" {
			return nil, fmt.Errorf("empty include path")
		}
		if ext := strings.ToLower(r.name); !strings.HasSuffix(ext, ".yaml") && !strings.HasSuffix(ext, ".yml") {
			return nil, fmt.Errorf("include path %q must name a .yaml/.yml file", r.name)
		}
	}
	return r, nil
}

// wholeRef reports whether the scan is exactly one reference with no
// surrounding text — the only position a $(config:…) include may appear in.
func wholeRef(segs []segment) *ref {
	if len(segs) == 1 && segs[0].ref != nil {
		return segs[0].ref
	}
	return nil
}

// hasRef reports whether any segment is a reference.
func hasRef(segs []segment) bool {
	for _, seg := range segs {
		if seg.ref != nil {
			return true
		}
	}
	return false
}
