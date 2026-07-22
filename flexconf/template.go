package flexconf

import (
	"errors"
	"fmt"
	"strings"
)

// Templating sentinel errors, usable with errors.Is.
var (
	// ErrBadToken reports a malformed template token (no ":", unterminated "$(").
	ErrBadToken = errors.New("flexconf: malformed template token")

	// ErrIncludeEmbedded reports a $(config:…) token combined with other text.
	ErrIncludeEmbedded = errors.New("flexconf: $(config:…) must be a whole value")

	// ErrIncludeExtension reports an include path without a .yaml/.yml extension.
	ErrIncludeExtension = errors.New("flexconf: include path must end in .yaml/.yml")

	// ErrIncludeEscape reports an include path escaping the config directory.
	ErrIncludeEscape = errors.New("flexconf: include path escapes the config directory")

	// ErrIncludeCycle reports an include cycle (the message names the chain).
	ErrIncludeCycle = errors.New("flexconf: include cycle detected")

	// ErrIncludeTooDeep reports include nesting beyond maxIncludeDepth.
	ErrIncludeTooDeep = errors.New("flexconf: include depth exceeds maxIncludeDepth")

	// ErrUnknownScheme reports a token whose scheme has no registered resolver.
	ErrUnknownScheme = errors.New("flexconf: no resolver registered for scheme")

	// ErrEnvNotSet reports an $(env:NAME) token naming an unset variable.
	ErrEnvNotSet = errors.New("flexconf: environment variable not set")

	// ErrFileNotFound reports a missing/unreadable $(file:…) target.
	ErrFileNotFound = errors.New("flexconf: file not found for file: token")
)

// maxIncludeDepth caps $(config:…) include nesting.
const maxIncludeDepth = 16

// segment is one piece of a scanned scalar: either literal text or a token.
type segment struct {
	literal string // literal text (escapes already applied); empty for tokens
	scheme  string // token scheme; empty for literals
	path    string // token path
}

func (s segment) isToken() bool { return s.scheme != "" }

// scanTokens splits a scalar into literal and token segments, applying the
// `$$(` → `$(` escape. A lone `$` is literal. Returns ErrBadToken for an
// unterminated `$(` or a token with no `:` before its closing `)`.
func scanTokens(s string) ([]segment, error) {
	var segs []segment
	var lit strings.Builder
	for i := 0; i < len(s); {
		// Escape: `$$(` emits a literal `$(`.
		if strings.HasPrefix(s[i:], "$$(") {
			lit.WriteString("$(")
			i += 3
			continue
		}
		if strings.HasPrefix(s[i:], "$(") {
			end := strings.IndexByte(s[i:], ')')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated %q", ErrBadToken, s[i:])
			}
			body := s[i+2 : i+end] // between "$(" and the first ")"
			scheme, path, found := strings.Cut(body, ":")
			if !found || !validScheme(scheme) {
				return nil, fmt.Errorf("%w: %q", ErrBadToken, s[i:i+end+1])
			}
			if lit.Len() > 0 {
				segs = append(segs, segment{literal: lit.String()})
				lit.Reset()
			}
			segs = append(segs, segment{scheme: scheme, path: path})
			i += end + 1
			continue
		}
		lit.WriteByte(s[i])
		i++
	}
	if lit.Len() > 0 {
		segs = append(segs, segment{literal: lit.String()})
	}
	return segs, nil
}

// validScheme reports whether s is a lowercase ASCII scheme name starting
// with a letter.
func validScheme(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// hasTokens reports whether any segment is a token.
func hasTokens(segs []segment) bool {
	for _, s := range segs {
		if s.isToken() {
			return true
		}
	}
	return false
}

// resolveTree expands $(scheme:path) tokens in every scalar value of the
// merged tree. A static Loader (empty resolver set) admits no token at all.
func (l *Loader) resolveTree(tree *node) error {
	return l.walkResolve(tree, "")
}

func (l *Loader) walkResolve(n *node, path string) error {
	switch n.kind {
	case kindMap:
		for _, key := range n.keys {
			if err := l.walkResolve(n.children[key], joinPath(path, key)); err != nil {
				return err
			}
		}
	case kindSeq:
		for i, item := range n.items {
			if err := l.walkResolve(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		return l.resolveScalar(n, path)
	}
	return nil
}

func (l *Loader) resolveScalar(n *node, path string) error {
	if !strings.Contains(n.value, "$(") {
		return nil
	}
	segs, err := scanTokens(n.value)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", n.origin(), displayPath(path), err)
	}
	if !hasTokens(segs) {
		// Escapes only: apply them even so.
		n.value = segs[0].literal
		return nil
	}
	if l.opts.static() {
		// A static Loader does no token processing: any token is an error.
		return fmt.Errorf("%s: %s: %w (static loader admits no $(...) tokens)",
			n.origin(), displayPath(path), ErrUnknownScheme)
	}

	secret := false
	resolved := make([]string, len(segs))
	for i, seg := range segs {
		if !seg.isToken() {
			resolved[i] = seg.literal
			continue
		}
		if seg.scheme == "config" {
			// Whole-value includes were expanded at read; anything left here
			// is a config token embedded in literal text.
			return fmt.Errorf("%s: %s: %w", n.origin(), displayPath(path), ErrIncludeEmbedded)
		}
		value, err := l.dispatch(seg.scheme, seg.path, n)
		if err != nil {
			return fmt.Errorf("%s: %s: $(%s:...): %w", n.origin(), displayPath(path), seg.scheme, err)
		}
		resolved[i] = value
		if seg.scheme == "secret" {
			secret = true // flag for redaction in later error messages
		}
	}

	if len(segs) == 1 && segs[0].isToken() {
		// Whole-value token: untagged, so the type is inferred from the
		// resolved content exactly as a literal would be.
		n.value, n.tag = resolved[0], ""
	} else {
		// Mixed with literal text (or multiple tokens): always a string.
		n.value, n.tag = strings.Join(resolved, ""), "!!str"
	}
	n.secret = n.secret || secret
	n.substituted = true
	return nil
}
