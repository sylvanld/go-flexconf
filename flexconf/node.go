package flexconf

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// nodeKind is the shape of a value-tree node.
type nodeKind int

const (
	kindScalar nodeKind = iota
	kindMap
	kindSeq
)

func (k nodeKind) String() string {
	switch k {
	case kindMap:
		return "map"
	case kindSeq:
		return "sequence"
	default:
		return "scalar"
	}
}

// node is flexconf's format-neutral value tree. Templating and binding
// operate on it, never on raw file bytes or format-specific decoders.
type node struct {
	kind nodeKind

	// Scalar state. value is the raw scalar text; tag is the YAML schema tag
	// ("!!int", "!!str", …; empty means untagged → inferred), used when binding
	// into an untyped (any) target. secret marks a value that originated from a
	// secret: token — it must never appear in error messages.
	value  string
	tag    string
	secret bool

	// Map state: insertion-ordered keys plus the children.
	keys     []string
	children map[string]*node

	// Sequence state.
	items []*node

	// Origin, for diagnostics: the file the node came from and its line.
	file string
	line int
}

// fromYAML converts a parsed yaml.Node into a node tree. file is recorded on
// every node for diagnostics.
func fromYAML(y *yaml.Node, file string) (*node, error) {
	switch y.Kind {
	case yaml.DocumentNode:
		if len(y.Content) == 0 {
			// Empty document: contributes an empty map (an empty file counts
			// as present with no keys).
			return &node{kind: kindMap, children: map[string]*node{}, file: file}, nil
		}
		return fromYAML(y.Content[0], file)
	case yaml.AliasNode:
		return fromYAML(y.Alias, file)
	case yaml.MappingNode:
		n := &node{kind: kindMap, children: map[string]*node{}, file: file, line: y.Line}
		for i := 0; i+1 < len(y.Content); i += 2 {
			keyNode, valNode := y.Content[i], y.Content[i+1]
			key := keyNode.Value // keys are never templated or interpreted
			if _, dup := n.children[key]; dup {
				return nil, fmt.Errorf("%s:%d: duplicate key %q", file, keyNode.Line, key)
			}
			child, err := fromYAML(valNode, file)
			if err != nil {
				return nil, err
			}
			n.keys = append(n.keys, key)
			n.children[key] = child
		}
		return n, nil
	case yaml.SequenceNode:
		n := &node{kind: kindSeq, file: file, line: y.Line}
		for _, item := range y.Content {
			child, err := fromYAML(item, file)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, child)
		}
		return n, nil
	case yaml.ScalarNode:
		tag := y.Tag
		if y.Style == yaml.SingleQuotedStyle || y.Style == yaml.DoubleQuotedStyle ||
			y.Style == yaml.LiteralStyle || y.Style == yaml.FoldedStyle {
			tag = "!!str" // quoted scalars are strings, never re-inferred
		}
		return &node{kind: kindScalar, value: y.Value, tag: tag, file: file, line: y.Line}, nil
	default:
		return nil, fmt.Errorf("%s:%d: unsupported YAML node kind", file, y.Line)
	}
}

// parseYAML parses one config file's bytes into a node tree. An empty file
// yields an empty map (present, contributing no keys).
func parseYAML(data []byte, file string) (*node, error) {
	var y yaml.Node
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	if y.Kind == 0 {
		return &node{kind: kindMap, children: map[string]*node{}, file: file}, nil
	}
	return fromYAML(&y, file)
}

// origin renders the node's source position for error messages.
func (n *node) origin() string {
	if n.file == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", n.file, n.line)
}
