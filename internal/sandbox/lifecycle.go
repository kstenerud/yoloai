// ABOUTME: Façade aliases re-exporting lifecycle types and functions so
// ABOUTME: existing callers in package sandbox continue to compile unmodified.
package sandbox

import "github.com/kstenerud/yoloai/internal/sandbox/lifecycle"

// StartOptions configures Start/Restart. See lifecycle.StartOptions.
type StartOptions = lifecycle.StartOptions

// ResetOptions configures Reset. See lifecycle.ResetOptions.
type ResetOptions = lifecycle.ResetOptions

// PatchConfigAllowedDomains rewrites a sandbox's allowed-domains list. See lifecycle.PatchConfigAllowedDomains.
var PatchConfigAllowedDomains = lifecycle.PatchConfigAllowedDomains
