package loader

import "os"

// Env resolves environment variables. Injected so tests run against a fixed
// map, never the real process environment.
type Env interface {
	Lookup(name string) (value string, ok bool)
}

// OSEnv is the production Env: the process environment.
type OSEnv struct{}

// Lookup reports the process environment variable named name.
func (OSEnv) Lookup(name string) (string, bool) { return os.LookupEnv(name) }

// MapEnv is a fixed-map Env for tests.
type MapEnv map[string]string

// Lookup reports the value stored under name in the map.
func (m MapEnv) Lookup(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}
