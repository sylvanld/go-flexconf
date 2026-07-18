package loader

import "gopkg.in/yaml.v3"

// Redacted is what a secret-sourced scalar renders as in any config dump.
const Redacted = "«redacted»"

// Dump renders the templated tree back to YAML with every tainted scalar
// replaced by «redacted». Every path that echoes config (a --print-config
// dump, a debug log, an error that quotes a block) should go through here —
// redaction is structural (the taint set), not a per-field allowlist someone
// can forget to extend.
func (ld *Loaded) Dump() ([]byte, error) {
	return yaml.Marshal(redactedCopy(ld.Tree, ld.Taint))
}

// redactedCopy deep-copies a node tree, masking tainted scalars.
func redactedCopy(n *yaml.Node, taint NodeSet) *yaml.Node {
	out := *n
	if n.Kind == yaml.ScalarNode && taint.Has(n) {
		out.Value = Redacted
		out.Tag, out.Style = "!!str", yaml.DoubleQuotedStyle
		return &out
	}
	if len(n.Content) > 0 {
		out.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			out.Content[i] = redactedCopy(c, taint)
		}
	}
	return &out
}
