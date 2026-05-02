package dns

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a configured Provider from the per-credential YAML
// section. The map keys are exactly the YAML field names that lived
// under the `credentials.<ref>` block.
type Factory func(cfg map[string]any) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register associates a Factory with a provider name. It MUST be
// called from a package-level init() function inside each provider
// package, e.g.:
//
//	//go:build cloudflare || all
//	package cloudflare
//	func init() { dns.Register("cloudflare", New) }
//
// Re-registration of the same name panics; this is a programming
// error caught at process startup.
func Register(name string, f Factory) {
	if name == "" {
		panic("dns.Register: empty provider name")
	}
	if f == nil {
		panic("dns.Register: nil factory for " + name)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("dns.Register: duplicate provider name " + name)
	}
	registry[name] = f
}

// Lookup returns the Factory for the named provider, or
// (nil, false) if it has not been compiled into this binary.
func Lookup(name string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// Build constructs a Provider by name with the given credential
// configuration. Returns a typed error when the name is unknown so
// that the CLI can produce a helpful "did you forget a build tag?"
// message.
func Build(name string, cfg map[string]any) (Provider, error) {
	f, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("dns: provider %q not registered (compiled-in providers: %v)", name, Registered())
	}
	return f(cfg)
}

// Registered returns the sorted names of all providers compiled into
// this binary. Useful for diagnostics.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
