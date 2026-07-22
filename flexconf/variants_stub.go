package flexconf

import "reflect"

// bindVariant routes a variant-family interface location to its registry.
// The variant binding layer lands with the variants feature; until then no
// interface is a variant location.
func (b *binder) bindVariant(n *node, v reflect.Value, path string) (bool, error) {
	return false, nil
}
