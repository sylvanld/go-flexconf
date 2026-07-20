package loader

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// PolymorphicSettings resolves a lazily-loaded Settings block into one of
// several concrete types, chosen at decode time by a discriminator field in the
// data itself. It generalises the pattern the private secrets block uses (a
// `driver:` field selecting the driver's own option schema) into a reusable,
// typed mechanism: a config block whose shape depends on one of its own fields.
//
// I is the common interface every variant satisfies. The discriminator is the
// name of the field that names the variant (e.g. "type", or something more
// domain-specific like "engine" or "channel") — there is no default, each block
// declares its own. Register the variants, then Decode a captured Settings:
//
//	var vaults = NewPolymorphicSettings[Vault]("type")
//	vaults.Register("keepass", func() Vault { return &KeepassVault{} })
//	vaults.Register("env",     func() Vault { return &EnvVault{} })
//	v, err := vaults.Decode(cfg.Vault) // *KeepassVault | *EnvVault
//
// The discriminator field is stripped before the remaining keys decode
// (strictly — an unknown key is an error), so variant structs declare only their
// own fields, never the discriminator.
type PolymorphicSettings[I any] struct {
	discriminator string
	factories     map[string]func() I
}

// NewPolymorphicSettings builds a registry keyed by the discriminator field.
// discriminator must not be empty — each polymorphic block names its own
// selector field.
func NewPolymorphicSettings[I any](discriminator string) *PolymorphicSettings[I] {
	if discriminator == "" {
		panic("flexconf: polymorphic discriminator must not be empty")
	}
	return &PolymorphicSettings[I]{discriminator: discriminator, factories: map[string]func() I{}}
}

// Register maps a discriminator value to a factory that yields a fresh,
// decodable target for that variant. It panics on a duplicate name — the same
// fail-loud registry discipline as the rest of the toolkit. Returns the registry
// so registrations can chain.
func (p *PolymorphicSettings[I]) Register(name string, factory func() I) *PolymorphicSettings[I] {
	if _, dup := p.factories[name]; dup {
		panic(fmt.Sprintf("flexconf: duplicate %s variant %q", p.discriminator, name))
	}
	p.factories[name] = factory
	return p
}

// Decode reads the discriminator from s, selects the registered factory, and
// strictly decodes the block's remaining fields into the concrete value it
// yields. The discriminator field is not passed to the variant. A missing
// discriminator, an unregistered value, or an unknown field is an error.
func (p *PolymorphicSettings[I]) Decode(s Settings) (I, error) {
	var zero I
	if s.Tree == nil || s.Tree.Kind != yaml.MappingNode {
		return zero, fmt.Errorf("flexconf: polymorphic block must be a mapping with a %q field", p.discriminator)
	}
	disc := mappingValue(s.Tree, p.discriminator)
	if disc == nil || disc.Value == "" {
		return zero, fmt.Errorf("flexconf: missing %q to select a variant (known: %v)", p.discriminator, p.known())
	}
	factory, ok := p.factories[disc.Value]
	if !ok {
		return zero, fmt.Errorf("flexconf: unknown %s %q (known: %v)", p.discriminator, disc.Value, p.known())
	}
	target := factory()
	if err := strictDecode(withoutKey(s.Tree, p.discriminator), target); err != nil {
		return zero, fmt.Errorf("flexconf: decoding %s %q: %w", p.discriminator, disc.Value, err)
	}
	return target, nil
}

// known returns the registered variant names, sorted, for error messages.
func (p *PolymorphicSettings[I]) known() []string {
	names := make([]string, 0, len(p.factories))
	for n := range p.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// withoutKey returns a shallow copy of a mapping node with key removed, leaving
// the original (which the caller may still Dump) untouched.
func withoutKey(n *yaml.Node, key string) *yaml.Node {
	out := *n
	out.Content = make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			continue
		}
		out.Content = append(out.Content, n.Content[i], n.Content[i+1])
	}
	return &out
}
