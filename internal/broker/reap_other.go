// ABOUTME: Fallback injector enumeration for platforms without a /proc or ps
// ABOUTME: path — yoloai targets linux + darwin, so this just reports none.
//go:build !linux && !darwin

package broker

// platformInjectorPIDs has no portable implementation off linux/darwin; the
// sweep simply finds nothing to reap there.
func platformInjectorPIDs() ([]int, error) { return nil, nil }
