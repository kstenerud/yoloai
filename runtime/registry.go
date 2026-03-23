package runtime

// ABOUTME: Backend registry for platform-specific runtime availability.
// Backends register themselves at init() time on supported platforms.

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Factory creates a new Runtime instance.
type Factory func(context.Context) (Runtime, error)

var (
	mu       sync.RWMutex
	backends = make(map[string]Factory)
)

// Register adds a backend factory to the registry.
// Called by each backend's init() function on supported platforms.
func Register(name string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := backends[name]; exists {
		panic(fmt.Sprintf("runtime backend %q registered twice", name))
	}
	backends[name] = factory
}

// New creates a Runtime for the given backend name.
// Returns an error if the backend is not registered (unavailable on this platform).
func New(ctx context.Context, name string) (Runtime, error) {
	mu.RLock()
	factory, ok := backends[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not available on this platform (available: %v)", name, Available())
	}
	return factory(ctx)
}

// IsAvailable returns true if the named backend is registered.
func IsAvailable(name string) bool {
	mu.RLock()
	_, ok := backends[name]
	mu.RUnlock()
	return ok
}

// Available returns a sorted list of registered backend names.
func Available() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
