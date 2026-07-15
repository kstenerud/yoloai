// ABOUTME: Façade aliases re-exporting lifecycle types and functions so the
// ABOUTME: lifecycle/ leaf carve stays invisible to package orchestrator's callers.
package orchestrator

import "github.com/kstenerud/yoloai/internal/orchestrator/lifecycle"

// StartOptions configures Start/Restart. See lifecycle.StartOptions.
type StartOptions = lifecycle.StartOptions

// ResetOptions configures Reset. See lifecycle.ResetOptions.
type ResetOptions = lifecycle.ResetOptions

// PatchConfigAllowedDomains rewrites a sandbox's allowed-domains list. See lifecycle.PatchConfigAllowedDomains.
var PatchConfigAllowedDomains = lifecycle.PatchConfigAllowedDomains
