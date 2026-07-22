package flexconf

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/sylvanld/go-flexconf/internal/variant"
)

// VariantType constrains a variant family's interface type. It is `any` for
// now.
type VariantType = any

// Registry holds one variant family's registered variants and configured
// instances (re-exported from the internal engine via a generic type alias).
type Registry[V VariantType] = variant.Registry[V]

// Selector is one key=value constraint identifying a configured instance.
type Selector = variant.Selector

// RegistryOption tunes a Registry.
type RegistryOption = variant.RegistryOption

// Variant sentinel errors, usable with errors.Is.
var (
	ErrVariantNotFound  = variant.ErrVariantNotFound
	ErrVariantAmbiguous = variant.ErrVariantAmbiguous
	ErrDuplicateVariant = variant.ErrDuplicateVariant
)

// NewRegistry returns an empty family registry for V.
func NewRegistry[V VariantType](opts ...RegistryOption) *Registry[V] {
	return variant.NewRegistry[V](opts...)
}

// Select builds a Selector.
func Select(key, value string) Selector { return variant.Select(key, value) }

// WithDiscriminator overrides a family's discriminator key (default "type").
func WithDiscriminator(key string) RegistryOption { return variant.WithDiscriminator(key) }

// Resolve returns the single configured instance of family V matching the
// selectors, or an error if none or more than one matches.
func Resolve[V VariantType](r *Registry[V], sel ...Selector) (V, error) {
	return r.Resolve(sel...)
}

// --- process-wide registry -------------------------------------------------

var (
	processRegistriesMu sync.RWMutex
	processRegistries   = map[reflect.Type]variant.Binder{}
)

// RegisterVariant registers a variant of family V into the process-wide
// registry (creating the family's registry on first use, with the default
// "type" discriminator). The Loader routes variant locations to the
// process-wide registry unless WithRegistry overrides it.
func RegisterVariant[V VariantType](name string, factory func() V) {
	t := reflect.TypeOf((*V)(nil)).Elem()
	processRegistriesMu.Lock()
	b, ok := processRegistries[t]
	if !ok {
		b = variant.NewRegistry[V]()
		processRegistries[t] = b
	}
	processRegistriesMu.Unlock()
	b.(*variant.Registry[V]).RegisterVariant(name, factory)
}

// Get resolves an instance of family V from the process-wide registry by
// selectors (exactly-one-or-fail).
func Get[V VariantType](sel ...Selector) (V, error) {
	t := reflect.TypeOf((*V)(nil)).Elem()
	processRegistriesMu.RLock()
	b, ok := processRegistries[t]
	processRegistriesMu.RUnlock()
	if !ok {
		var zero V
		return zero, fmt.Errorf("%w: family %s has no registered variants", ErrVariantNotFound, t)
	}
	return b.(*variant.Registry[V]).Resolve(sel...)
}

// processRegistryFor returns the process-wide registry for a family type.
func processRegistryFor(t reflect.Type) (variant.Binder, bool) {
	processRegistriesMu.RLock()
	defer processRegistriesMu.RUnlock()
	b, ok := processRegistries[t]
	return b, ok
}

// WithRegistry routes this Loader's variant locations to an explicit registry
// (a *Registry[V]) instead of the process-wide one — for isolation and tests.
func (l *Loader) WithRegistry(r any) *Loader {
	b, ok := r.(variant.Binder)
	if !ok {
		panic(fmt.Sprintf("flexconf: WithRegistry requires a *flexconf.Registry[V], got %T", r))
	}
	nl := *l
	nl.registries = append(append([]variant.Binder{}, l.registries...), b)
	return &nl
}

// --- binder integration ----------------------------------------------------

// registryFor finds the registry handling a family interface type: an
// explicit WithRegistry registry first, else the process-wide one.
func (l *Loader) registryFor(t reflect.Type) (variant.Binder, bool) {
	for _, b := range l.registries {
		if b.FamilyType() == t {
			return b, true
		}
	}
	return processRegistryFor(t)
}

// bindVariant handles a variant location: an interface-typed target whose
// type is a registered family. It reads the discriminator, instantiates the
// variant via its factory, strictly binds the remaining keys, assigns the
// instance to the field, derives selectors, and records the instance.
func (b *binder) bindVariant(n *node, v reflect.Value, path string) (bool, error) {
	if b.loader == nil {
		return false, nil
	}
	reg, ok := b.loader.registryFor(v.Type())
	if !ok {
		return false, nil
	}
	if n.kind != kindMap {
		return true, fmt.Errorf("%s: %s: variant entry must be a map with a %q key",
			n.origin(), displayPath(path), reg.Discriminator())
	}

	// 1. Read the discriminator: present, scalar, and a literal (never a token).
	disc := reg.Discriminator()
	dn, present := n.children[disc]
	if !present {
		return true, fmt.Errorf("%s: %s: %s is required (known: %s)",
			n.origin(), displayPath(path), disc, strings.Join(reg.Variants(), ", "))
	}
	if dn.kind != kindScalar || dn.substituted {
		return true, fmt.Errorf("%s: %s: %s must be a plain string literal, never a token",
			dn.origin(), displayPath(joinPath(path, disc)), disc)
	}

	// 2. Instantiate via the factory (per-variant defaults pre-populated).
	instance, err := reg.NewAny(dn.value)
	if err != nil {
		return true, fmt.Errorf("%s: %s: %w", dn.origin(), displayPath(path), err)
	}
	iv := reflect.ValueOf(instance)
	if iv.Kind() != reflect.Pointer || iv.Elem().Kind() != reflect.Struct {
		return true, fmt.Errorf("%s: variant factory for %q must return a struct pointer, got %T",
			displayPath(path), dn.value, instance)
	}

	// 3. Strictly bind the remaining keys onto the instance's sub-schema.
	sub := &node{kind: kindMap, children: map[string]*node{}, file: n.file, line: n.line}
	for _, key := range n.keys {
		if key == disc {
			continue
		}
		sub.keys = append(sub.keys, key)
		sub.children[key] = n.children[key]
	}
	if err := b.bindStruct(sub, iv.Elem(), path); err != nil {
		return true, err
	}

	// 4. Assign to the field (the primary target).
	v.Set(iv)

	// 5. Derive selectors: discriminator, the nearest naming key (single
	// field or map key — list items have none), and selector-tagged fields.
	selectors := map[string]string{disc: dn.value}
	if name, ok := namingKey(path); ok {
		selectors["name"] = name
	}
	fields, err := structFields(iv.Elem().Type())
	if err != nil {
		return true, err
	}
	for _, info := range fields {
		if !info.selector {
			continue
		}
		fv := iv.Elem().FieldByIndex(info.index)
		selectors[info.key] = fmt.Sprint(fv.Interface())
	}

	// 6. Record the instance (the convenience index).
	if err := reg.AddInstance(instance, selectors, fmt.Sprintf("%s (%s)", displayPath(path), n.origin())); err != nil {
		return true, fmt.Errorf("%s: %w", n.origin(), err)
	}
	b.touched(reg)
	return true, nil
}

// namingKey extracts the nearest naming key from a key path: the last map/
// field segment. A list item ("...[i]") has no name.
func namingKey(path string) (string, bool) {
	if path == "" || strings.HasSuffix(path, "]") {
		return "", false
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		path = path[i+1:]
	}
	return path, path != ""
}

// touched records a registry populated during this Load so a failure can roll
// its instances back (all-or-nothing).
func (b *binder) touched(reg variant.Binder) {
	for _, t := range b.touchedRegs {
		if t.reg == reg {
			return
		}
	}
	// The count BEFORE this Load's first instance was already captured by the
	// caller adding one; store count-1 as the rollback point.
	b.touchedRegs = append(b.touchedRegs, touchedRegistry{reg: reg, before: reg.InstanceCount() - 1})
}

type touchedRegistry struct {
	reg    variant.Binder
	before int
}

// rollback truncates every touched registry to its pre-Load instance count.
func (b *binder) rollback() {
	for _, t := range b.touchedRegs {
		t.reg.TruncateInstances(t.before)
	}
}
