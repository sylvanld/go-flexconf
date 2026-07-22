// Package variant implements the generic variant-family matching engine
// shared by flexconf (which re-exports it publicly) and flexvault (for the
// vault registry). A Registry[V] holds a family's registered variants
// (discriminator value → factory) and every configured instance, indexed by
// selectors for exactly-one resolution.
package variant

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// Sentinel errors, usable with errors.Is.
var (
	// ErrVariantNotFound reports that no configured instance matches the
	// requested selectors.
	ErrVariantNotFound = errors.New("flexconf: no variant matches selectors")

	// ErrVariantAmbiguous reports that more than one configured instance
	// matches the requested selectors.
	ErrVariantAmbiguous = errors.New("flexconf: selectors match more than one variant")

	// ErrDuplicateVariant reports two registered instances with identical
	// full selector sets — such a pair could never be resolved unambiguously.
	ErrDuplicateVariant = errors.New("flexconf: two variant instances have identical selectors")
)

// Selector is one key=value constraint identifying a configured instance.
type Selector struct{ Key, Value string }

// Select builds a Selector.
func Select(key, value string) Selector { return Selector{Key: key, Value: value} }

// Registry holds one family's registered variants and configured instances.
// V is the family interface type. Registration is expected during
// init/startup; resolution is safe for concurrent use afterwards.
type Registry[V any] struct {
	mu            sync.RWMutex
	discriminator string
	familyType    reflect.Type
	variants      map[string]func() V
	instances     []instance[V]
}

// instance is one configured variant instance and its selector set.
type instance[V any] struct {
	value     V
	selectors map[string]string
	location  string // human-readable config location, for error messages
}

// RegistryOption tunes a Registry.
type RegistryOption func(*registryConfig)

type registryConfig struct{ discriminator string }

// WithDiscriminator overrides the family's discriminator key (default "type";
// the vault family uses "driver").
func WithDiscriminator(key string) RegistryOption {
	return func(c *registryConfig) { c.discriminator = key }
}

// NewRegistry returns an empty family registry.
func NewRegistry[V any](opts ...RegistryOption) *Registry[V] {
	cfg := registryConfig{discriminator: "type"}
	for _, o := range opts {
		o(&cfg)
	}
	return &Registry[V]{
		discriminator: cfg.discriminator,
		variants:      map[string]func() V{},
		familyType:    reflect.TypeOf((*V)(nil)).Elem(),
	}
}

// Binder is the non-generic view of a Registry that config binders use to
// route variant locations without knowing V statically.
type Binder interface {
	Discriminator() string
	FamilyType() reflect.Type
	Variants() []string
	NewAny(name string) (any, error)
	AddInstance(value any, selectors map[string]string, location string) error
	InstanceCount() int
	TruncateInstances(n int)
}

// FamilyType returns the reflect.Type of the family interface V.
func (r *Registry[V]) FamilyType() reflect.Type { return r.familyType }

// NewAny is New with an any result, for non-generic callers (Binder).
func (r *Registry[V]) NewAny(name string) (any, error) { return r.New(name) }

// TruncateInstances drops instances registered after the first n (used to
// roll back a failed Load without touching earlier successful registrations).
func (r *Registry[V]) TruncateInstances(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= 0 && n < len(r.instances) {
		r.instances = r.instances[:n]
	}
}

// Discriminator returns the family's discriminator key.
func (r *Registry[V]) Discriminator() string { return r.discriminator }

// RegisterVariant registers a variant: name is the discriminator value,
// factory returns a pre-populated instance (its per-variant defaults).
// Re-registering a name is a programming error and panics.
func (r *Registry[V]) RegisterVariant(name string, factory func() V) {
	if name == "" || factory == nil {
		panic("flexconf: RegisterVariant requires a name and a factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.variants[name]; dup {
		panic(fmt.Sprintf("flexconf: variant %q already registered in this family", name))
	}
	r.variants[name] = factory
}

// Variants returns the sorted registered discriminator values.
func (r *Registry[V]) Variants() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.variants))
	for n := range r.variants {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// New instantiates the variant registered under the discriminator value name
// via its factory (defaults pre-populated). An unknown name is an error
// listing the known variants.
func (r *Registry[V]) New(name string) (V, error) {
	r.mu.RLock()
	factory, ok := r.variants[name]
	r.mu.RUnlock()
	if !ok {
		var zero V
		return zero, fmt.Errorf("flexconf: unknown %s %q (known: %s)",
			r.discriminator, name, strings.Join(r.Variants(), ", "))
	}
	return factory(), nil
}

// AddInstance records a configured instance with its full selector set.
// location names the config position, for error messages. Two instances with
// identical selector sets are rejected (ErrDuplicateVariant).
func (r *Registry[V]) AddInstance(v any, selectors map[string]string, location string) error {
	value, ok := v.(V)
	if !ok {
		return fmt.Errorf("flexconf: instance %T is not a member of family %s", v, r.familyType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.instances {
		if equalSelectors(existing.selectors, selectors) {
			return fmt.Errorf("%w: %s and %s both have %s",
				ErrDuplicateVariant, existing.location, location, formatSelectors(selectors))
		}
	}
	r.instances = append(r.instances, instance[V]{value: value, selectors: selectors, location: location})
	return nil
}

// ClearInstances drops every configured instance (used to discard a partially
// populated registry when a Load fails — all-or-nothing).
func (r *Registry[V]) ClearInstances() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances = nil
}

// InstanceCount reports how many configured instances are registered.
func (r *Registry[V]) InstanceCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.instances)
}

// Resolve returns the single configured instance matching all the given
// selectors (subset match). No match → ErrVariantNotFound; more than one →
// ErrVariantAmbiguous listing the matches.
func (r *Registry[V]) Resolve(sel ...Selector) (V, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var zero V
	var matches []instance[V]
	for _, inst := range r.instances {
		if matchesAll(inst.selectors, sel) {
			matches = append(matches, inst)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].value, nil
	case 0:
		return zero, fmt.Errorf("%w: %s", ErrVariantNotFound, formatQuery(sel))
	default:
		descs := make([]string, len(matches))
		for i, m := range matches {
			descs[i] = formatSelectors(m.selectors)
		}
		return zero, fmt.Errorf("%w: %s matches %s",
			ErrVariantAmbiguous, formatQuery(sel), strings.Join(descs, "; "))
	}
}

func matchesAll(have map[string]string, want []Selector) bool {
	for _, s := range want {
		if have[s.Key] != s.Value {
			return false
		}
	}
	return true
}

func equalSelectors(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func formatSelectors(m map[string]string) string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ",") + "}"
}

func formatQuery(sel []Selector) string {
	if len(sel) == 0 {
		return "{}"
	}
	pairs := make([]string, len(sel))
	for i, s := range sel {
		pairs[i] = s.Key + "=" + s.Value
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ",") + "}"
}
