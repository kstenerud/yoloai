package runtime

// ABOUTME: Backend registry for platform-specific runtime availability.
// Backends register themselves at init() time on supported platforms.

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/kstenerud/yoloai/internal/config"
)

// Factory creates a new Runtime instance given a Layout.
// The Layout provides DataDir-rooted paths so backends never read ambient HOME.
type Factory func(context.Context, config.Layout) (Runtime, error)

// entry pairs a factory with the backend's static descriptor so callers can
// look up either side independently — descriptors without paying the cost of
// instantiating a runtime.
type entry struct {
	factory    Factory
	descriptor BackendDescriptor
}

var (
	mu       sync.RWMutex
	backends = make(map[BackendType]entry)
)

// Register adds a backend factory and its static descriptor to the registry,
// keyed on descriptor.Type. Called by each backend's init() function on
// supported platforms. Panics if the backend is already registered.
func Register(factory Factory, descriptor BackendDescriptor) {
	name := descriptor.Type
	mu.Lock()
	defer mu.Unlock()
	if _, exists := backends[name]; exists {
		panic(fmt.Sprintf("runtime backend %q registered twice", name))
	}
	backends[name] = entry{factory: factory, descriptor: descriptor}
}

// New creates a Runtime for the given backend name and layout.
// Returns an error if the backend is not registered (unavailable on this platform).
func New(ctx context.Context, name BackendType, layout config.Layout) (Runtime, error) {
	mu.RLock()
	e, ok := backends[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend %q not available on this platform (available: %v)", name, Available())
	}
	return e.factory(ctx, layout)
}

// IsAvailable returns true if the named backend is registered.
func IsAvailable(name BackendType) bool {
	mu.RLock()
	_, ok := backends[name]
	mu.RUnlock()
	return ok
}

// Available returns a sorted list of registered backend names.
func Available() []BackendType {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]BackendType, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Descriptor returns the static descriptor for a registered backend without
// instantiating it. Returns the zero descriptor and ok=false if the backend
// is not registered (unavailable on this platform).
func Descriptor(name BackendType) (BackendDescriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := backends[name]
	if !ok {
		return BackendDescriptor{}, false
	}
	return e.descriptor, true
}

// Descriptors returns descriptors for all registered backends, sorted by name.
// Enables callers to enumerate available backends' static facts without
// instantiating any of them.
func Descriptors() []BackendDescriptor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]BackendDescriptor, 0, len(backends))
	for _, e := range backends {
		out = append(out, e.descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}
