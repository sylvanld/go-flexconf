package flexconf

// mergeTrees merges an ordered list of per-layer trees (lowest to highest
// precedence) into one tree: maps deep-merge by key, scalars and sequences
// replace wholesale. Inputs are not mutated.
func mergeTrees(layers []*node) *node {
	var out *node
	for _, layer := range layers {
		out = mergeNode(out, layer)
	}
	return out
}

func mergeNode(base, override *node) *node {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}
	// Only two maps deep-merge; any other combination replaces. (A shape
	// conflict between non-polymorphic fields is caught earlier by per-file
	// shape validation; polymorphic fields legitimately replace.)
	if base.kind != kindMap || override.kind != kindMap {
		return override
	}
	merged := &node{
		kind:     kindMap,
		children: map[string]*node{},
		file:     override.file,
		line:     override.line,
	}
	for _, key := range base.keys {
		merged.keys = append(merged.keys, key)
		merged.children[key] = base.children[key]
	}
	for _, key := range override.keys {
		if existing, ok := merged.children[key]; ok {
			merged.children[key] = mergeNode(existing, override.children[key])
		} else {
			merged.keys = append(merged.keys, key)
			merged.children[key] = override.children[key]
		}
	}
	return merged
}
