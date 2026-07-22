package flexvault

import (
	"fmt"
	"sort"
	"sync"
)

var (
	driversMu sync.RWMutex
	drivers   = map[string]func() VaultDriver{}
)

// Register associates a driver name with a factory. Concrete drivers call it
// from init(), so importing a driver package for its side effect is all that
// is needed to make it selectable by name. Registering a name twice PANICS —
// driver names are a global namespace and silent shadowing is a footgun.
func Register(name string, factory func() VaultDriver) {
	if name == "" || factory == nil {
		panic("flexvault: Register requires a name and a factory")
	}
	driversMu.Lock()
	defer driversMu.Unlock()
	if _, dup := drivers[name]; dup {
		panic(fmt.Sprintf("flexvault: driver %q already registered", name))
	}
	drivers[name] = factory
}

// New constructs a registered driver by name; the caller then Configures and
// Unlocks it (usually via a Manager). An unknown name is an error listing the
// registered drivers.
func New(name string) (VaultDriver, error) {
	driversMu.RLock()
	factory, ok := drivers[name]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("flexvault: unknown driver %q (registered: %v)", name, Drivers())
	}
	return factory(), nil
}

// Drivers returns the sorted names of all registered drivers.
func Drivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	names := make([]string, 0, len(drivers))
	for n := range drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
