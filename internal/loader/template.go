package loader

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sylvanld/go-flexconf/secrets"
)

// NodeSet marks nodes of a templated tree, used for the secret taint set.
type NodeSet map[*yaml.Node]struct{}

// Add records n in the set.
func (s NodeSet) Add(n *yaml.Node) { s[n] = struct{}{} }

// Has reports whether n is in the set.
func (s NodeSet) Has(n *yaml.Node) bool { _, ok := s[n]; return ok }

// Merge folds every node of o into s.
func (s NodeSet) Merge(o NodeSet) {
	for n := range o {
		s[n] = struct{}{}
	}
}

// templater is one templating pass over a parsed tree. It rewrites scalar
// leaves in place, accumulates every unresolved/invalid reference (with
// file:line) instead of stopping at the first, and records secret-sourced
// nodes as tainted.
type templater struct {
	l           *loader
	secrets     SecretResolver
	allowSecret bool     // false while templating the secrets block (bootstrap)
	stack       []string // include chain (fsys-relative paths) for cycle detection
	tainted     NodeSet
	errs        []error
}

func (t *templater) errorf(file string, n *yaml.Node, format string, args ...any) {
	t.errs = append(t.errs, fmt.Errorf("%s:%d: %s", file, n.Line, fmt.Sprintf(format, args...)))
}

// walk visits values only — keys are never templated: a $(env:X) mapping key
// stays literal and fails the unknown-key check later, which is the right
// outcome.
func (t *templater) walk(n *yaml.Node, file string) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			t.walk(c, file)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			t.walk(n.Content[i+1], file)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			t.walk(c, file)
		}
	case yaml.ScalarNode:
		t.scalar(n, file)
	}
}

// scalar templates one leaf. env/secret references substitute text (whole or
// embedded). Substitution happens on the parsed node tree, never by re-parsing
// YAML text, so an injected value can only ever be a scalar's content — it can
// never introduce a mapping, sequence, tag, or alias. The substituted scalar is
// left untagged (Tag "") so it resolves like any hand-written value: a numeric
// $(env:PORT) decodes into an int field, "true" into a bool, and text into a
// string — native types and arbitrary structs need no wrapper types. Resolved
// values are not re-scanned.
func (t *templater) scalar(n *yaml.Node, file string) {
	segs, err := scan(n.Value)
	if err != nil {
		t.errorf(file, n, "%v", err)
		return
	}
	if !hasRef(segs) {
		if len(segs) == 1 && segs[0].lit != n.Value { // $$( escape only
			n.Value = segs[0].lit
			n.Tag, n.Style = "", 0
		}
		return
	}
	if r := wholeRef(segs); r != nil && r.ns == nsConfig {
		t.include(n, r, file)
		return
	}

	var b strings.Builder
	secret := false
	for _, seg := range segs {
		if seg.ref == nil {
			b.WriteString(seg.lit)
			continue
		}
		switch r := seg.ref; r.ns {
		case nsEnv:
			v, ok := t.l.env.Lookup(r.name)
			if !ok {
				if !r.hasDef {
					t.errorf(file, n, "unresolved $(env:%s): variable is not set and has no :- default", r.name)
					return
				}
				v = r.def
			}
			b.WriteString(v)
		case nsSecret:
			if !t.allowSecret {
				t.errorf(file, n, "$(secret:%s): secret references are not allowed in the secrets block (bootstrap is env-only)", r.name)
				return
			}
			if t.secrets == nil {
				t.errorf(file, n, "unresolved $(secret:%s): no secret backend configured", r.name)
				return
			}
			v, err := t.secrets.Secret(r.name)
			if err != nil {
				if errors.Is(err, secrets.ErrNotFound) {
					t.errorf(file, n, "unresolved $(secret:%s): not found in the secret store", r.name)
				} else {
					t.errorf(file, n, "$(secret:%s): %v", r.name, err)
				}
				return
			}
			secret = true
			b.WriteString(v)
		case nsConfig:
			t.errorf(file, n, "$(config:%s) must be the whole value — an include splices structure and cannot be embedded in text", r.name)
			return
		}
	}
	n.Value = b.String()
	n.Tag, n.Style = "", 0
	if secret {
		t.tainted.Add(n)
	}
}

// include replaces a whole-value $(config:PATH) reference with the parsed,
// recursively-templated tree of the target file. The path is resolved
// relative to the file that contains the reference and is a literal — never
// itself templated — so the include graph is statically knowable.
// maxIncludeDepth guards against a pathologically deep (but acyclic) include
// graph; cycles error separately.
const maxIncludeDepth = 16

func (t *templater) include(n *yaml.Node, r *ref, file string) {
	target := path.Clean(path.Join(path.Dir(file), r.name))
	if !fs.ValidPath(target) {
		t.errorf(file, n, "$(config:%s): path escapes the config directory", r.name)
		return
	}
	if len(t.stack) > maxIncludeDepth {
		t.errorf(file, n, "$(config:%s): include depth exceeds %d (%s)", r.name, maxIncludeDepth, strings.Join(t.stack, " → "))
		return
	}
	for _, s := range t.stack {
		if s == target {
			t.errorf(file, n, "$(config:%s): include cycle %s", r.name, strings.Join(append(t.stack, target), " → "))
			return
		}
	}
	child, err := t.l.readAndParse(target)
	if err != nil {
		t.errorf(file, n, "$(config:%s): %v", r.name, err)
		return
	}
	t.stack = append(t.stack, target)
	t.walk(child, target)
	t.stack = t.stack[:len(t.stack)-1]

	*n = *child // splice the included document's root at this position
	if t.tainted.Has(child) {
		t.tainted.Add(n) // a tainted scalar root keeps its taint through the copy
	}
}
