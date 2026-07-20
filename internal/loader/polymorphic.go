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
// A registry may also name a default variant with SetDefault. The factory a
// variant registers returns a *fresh* target, so a factory that returns a
// pre-populated value declares that variant's defaults: Decode decodes the
// block's keys over it, leaving whatever the block does not mention.
type PolymorphicSettings[I any] struct {
	discriminator string
	factories     map[string]func() I
	fallback      string // discriminator value used when the block names none
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

// SetDefault names the variant used when a block omits the discriminator, or is
// absent entirely. It panics if name was never registered — a default naming a
// variant that does not exist is a wiring bug, caught at startup rather than at
// the first load. Returns the registry so it can chain after Register.
func (p *PolymorphicSettings[I]) SetDefault(name string) *PolymorphicSettings[I] {
	if _, ok := p.factories[name]; !ok {
		panic(fmt.Sprintf("flexconf: default %s %q is not registered (known: %v)", p.discriminator, name, p.known()))
	}
	p.fallback = name
	return p
}

// Default returns a fresh instance of the default variant, fully populated by
// its factory — the variant's declared defaults, with nothing decoded over
// them. It reports an error if no default variant has been set.
func (p *PolymorphicSettings[I]) Default() (I, error) {
	var zero I
	if p.fallback == "" {
		return zero, fmt.Errorf("flexconf: no default %s set (known: %v)", p.discriminator, p.known())
	}
	return p.factories[p.fallback](), nil
}

// DefaultSettings renders the default variant as a Settings whose Default tree
// is the variant's values plus the discriminator naming it. That is the block a
// `settings init` writes for a polymorphic field: a complete, re-loadable
// starting point rather than an empty stub.
func (p *PolymorphicSettings[I]) DefaultSettings() (Settings, error) {
	v, err := p.Default()
	if err != nil {
		return Settings{}, err
	}
	s := Defaults(v)
	if s.Default.Kind != yaml.MappingNode {
		return Settings{}, fmt.Errorf("flexconf: default %s %q must marshal to a mapping", p.discriminator, p.fallback)
	}
	s.Default = withDiscriminator(s.Default, p.discriminator, p.fallback)
	return s, nil
}

// Decode reads the discriminator from s, selects the registered factory, and
// strictly decodes the block's remaining fields into the concrete value it
// yields. The discriminator field is not passed to the variant. Because the
// factory's value is decoded *onto*, a pre-populated factory supplies that
// variant's defaults for every key the block leaves out.
//
// A block that omits the discriminator — or a Settings with nothing loaded at
// all — resolves to the variant named by SetDefault; without one, that is an
// error. An unregistered discriminator value or an unknown field is always an
// error.
func (p *PolymorphicSettings[I]) Decode(s Settings) (I, error) {
	var zero I
	tree := s.Tree
	if tree == nil {
		tree = s.Default
	}
	if tree == nil {
		return p.Default()
	}
	if tree.Kind != yaml.MappingNode {
		return zero, fmt.Errorf("flexconf: polymorphic block must be a mapping with a %q field", p.discriminator)
	}

	name := p.fallback
	if disc := mappingValue(tree, p.discriminator); disc != nil && disc.Value != "" {
		name = disc.Value
	}
	if name == "" {
		return zero, fmt.Errorf("flexconf: missing %q to select a variant (known: %v)", p.discriminator, p.known())
	}
	factory, ok := p.factories[name]
	if !ok {
		return zero, fmt.Errorf("flexconf: unknown %s %q (known: %v)", p.discriminator, name, p.known())
	}

	target := factory()
	// A default tree under a loaded one decodes first, so the block overrides
	// only the keys it names — matching Settings.Decode's merge semantics.
	if s.Tree != nil && s.Default != nil && s.Default.Kind == yaml.MappingNode {
		if err := strictDecode(withoutKey(s.Default, p.discriminator), target); err != nil {
			return zero, fmt.Errorf("flexconf: decoding default %s %q: %w", p.discriminator, name, err)
		}
	}
	if err := strictDecode(withoutKey(tree, p.discriminator), target); err != nil {
		return zero, fmt.Errorf("flexconf: decoding %s %q: %w", p.discriminator, name, err)
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

// withDiscriminator returns a copy of a mapping node carrying key: value as its
// first entry — the discriminator a variant's own struct never declares, put
// back so the rendered block names the variant it decodes to.
func withDiscriminator(n *yaml.Node, key, value string) *yaml.Node {
	out := *withoutKey(n, key)
	kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	vn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	out.Content = append([]*yaml.Node{kn, vn}, out.Content...)
	return &out
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
