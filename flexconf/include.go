package flexconf

import (
	"fmt"
	"path/filepath"
	"strings"
)

// expandIncludes recursively expands $(config:path) include tokens in a
// layer's tree, before merge and before scalar resolution. layerDir is the
// config directory the tree was read from — includes must not escape it.
// chain tracks the include path for cycle reporting.
func (l *Loader) expandIncludes(n *node, layerDir string, chain []string) (*node, error) {
	switch n.kind {
	case kindMap:
		for _, key := range n.keys {
			child, err := l.expandIncludes(n.children[key], layerDir, chain)
			if err != nil {
				return nil, err
			}
			n.children[key] = child
		}
		return n, nil
	case kindSeq:
		for i, item := range n.items {
			child, err := l.expandIncludes(item, layerDir, chain)
			if err != nil {
				return nil, err
			}
			n.items[i] = child
		}
		return n, nil
	default:
		return l.expandScalarInclude(n, layerDir, chain)
	}
}

// expandScalarInclude splices the referenced file's tree when the scalar is a
// whole-value $(config:…) token; other scalars pass through untouched (their
// tokens resolve later).
func (l *Loader) expandScalarInclude(n *node, layerDir string, chain []string) (*node, error) {
	if !strings.Contains(n.value, "$(config:") {
		return n, nil
	}
	segs, err := scanTokens(n.value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", n.origin(), err)
	}
	hasConfig := false
	for _, seg := range segs {
		if seg.scheme == "config" {
			hasConfig = true
		}
	}
	if !hasConfig {
		return n, nil
	}
	if len(segs) != 1 || !segs[0].isToken() {
		return nil, fmt.Errorf("%s: %w", n.origin(), ErrIncludeEmbedded)
	}
	incPath := segs[0].path

	// Extension and containment checks.
	switch filepath.Ext(incPath) {
	case ".yaml", ".yml":
	default:
		return nil, fmt.Errorf("%s: %w: %q", n.origin(), ErrIncludeExtension, incPath)
	}
	target := incPath
	if !filepath.IsAbs(target) {
		// Relative to the directory of the file containing the token.
		target = filepath.Join(filepath.Dir(n.file), target)
	}
	rel, err := filepath.Rel(layerDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s: %w: %q", n.origin(), ErrIncludeEscape, incPath)
	}

	// Depth cap and cycle detection over the whole chain.
	if len(chain) >= maxIncludeDepth {
		return nil, fmt.Errorf("%s: %w (%d)", n.origin(), ErrIncludeTooDeep, maxIncludeDepth)
	}
	for _, seen := range chain {
		if seen == target {
			return nil, fmt.Errorf("%s: %w: %s → %s", n.origin(), ErrIncludeCycle,
				strings.Join(chain, " → "), target)
		}
	}

	data, err := l.readFile(target)
	if err != nil {
		return nil, fmt.Errorf("%s: $(config:%s): %w", n.origin(), incPath, err)
	}
	tree, err := parseYAML(data, target)
	if err != nil {
		return nil, fmt.Errorf("%s: $(config:%s): %w", n.origin(), incPath, err)
	}
	// Included subtrees are indistinguishable from inline content; expand
	// their own includes recursively.
	return l.expandIncludes(tree, layerDir, append(chain, target))
}
