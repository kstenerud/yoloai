package config

// ABOUTME: Name validation for sandboxes and profiles, plus the parsed
// ABOUTME: SandboxName / PrincipalSegment boundary types (containerd-conformant).

import (
	"regexp"

	"github.com/kstenerud/yoloai/yoerrors"
)

// MaxNameLength is the maximum allowed sandbox name length. The binding
// ceiling is containerd's identifier maxLength (76) minus the "yoloai-"
// prefix (7) and the largest principal segment plus its separator
// (MaxPrincipalLength + 1 = 9): 76 - 7 - 9 = 60, rounded down to 56 to keep
// a margin. The default (no-principal) instance name "yoloai-<name>" then
// tops out at 7 + 56 = 63, and a fully-namespaced
// "yoloai-<principal>-<name>" at 7 + 8 + 1 + 56 = 72 ≤ 76. See D62.
const MaxNameLength = 56

// MaxPrincipalLength is the maximum allowed principal-segment length. Kept
// small (8) so the joined instance name stays within containerd's ceiling
// regardless of sandbox-name length. See D62.
const MaxPrincipalLength = 8

// ValidNameRe matches profile names: starts with a letter or digit, then
// letters, digits, underscores, dots, or hyphens. Looser than the sandbox
// grammar below because profile names are only filesystem directory names,
// never runtime container ids.
var ValidNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// sandboxNameRe is the containerd identifier grammar (pkg/identifiers):
// alphanumeric runs separated by single '.', '_', or '-', with every
// separator surrounded by alphanumerics. Stricter than ValidNameRe — it
// rejects trailing/leading/doubled separators ("my-app-", "a..b", "x__y")
// that containerd's container-id validation would reject at create time.
var sandboxNameRe = regexp.MustCompile(`^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$`)

// principalSegmentRe restricts a principal segment to a single alphanumeric
// run (no separators), keeping the joined "yoloai-<principal>-<name>"
// unambiguous to split back into its parts.
var principalSegmentRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// SandboxName is a parsed, validated sandbox name. Its only constructor is
// ParseSandboxName, so holding a SandboxName is proof the value satisfies the
// containerd identifier grammar and length bound (parse-don't-validate). This
// is the single source of grammar truth; validate-style entry points delegate
// here. See D62 / DF16.
type SandboxName string

// ParseSandboxName validates name and returns it as a SandboxName, or a usage
// error explaining the violation.
func ParseSandboxName(name string) (SandboxName, error) {
	if name == "" {
		return "", yoerrors.NewUsageError("sandbox name is required")
	}
	if name[0] == '/' || name[0] == '\\' {
		return "", yoerrors.NewUsageError("invalid sandbox name %q: looks like a path (did you swap the arguments?)", name)
	}
	if len(name) > MaxNameLength {
		return "", yoerrors.NewUsageError("invalid sandbox name: must be at most %d characters (got %d)", MaxNameLength, len(name))
	}
	if !sandboxNameRe.MatchString(name) {
		return "", yoerrors.NewUsageError("invalid sandbox name %q: must be alphanumeric segments joined by single '.', '_', or '-' (no leading, trailing, or repeated separators)", name)
	}
	return SandboxName(name), nil
}

// PrincipalSegment is a parsed, validated principal identifier used to namespace
// runtime instance names across multiple principals (tenants). The empty value
// is the reserved default ("no principal"): it elides from the instance name so
// a single-principal embedder (the CLI) behaves exactly as before. A non-empty
// segment is a single alphanumeric run of at most MaxPrincipalLength. See D62.
type PrincipalSegment string

// InstancePrefix returns the runtime instance-name prefix for the given principal.
// With an empty principal it returns "yoloai-" (the historical default); with a
// non-empty principal it returns "yoloai-<principal>-". This is the single source
// of truth for the prefix used by both store.InstanceName and the backend orphan
// sweeps — every prune predicate calls this so principal-scoping is centrally
// maintained. See D62 / DF19.
func InstancePrefix(p PrincipalSegment) string {
	if p == "" {
		return "yoloai-"
	}
	return "yoloai-" + string(p) + "-"
}

// ParsePrincipalSegment validates a principal identifier and returns it as a
// PrincipalSegment. The empty string is accepted as the default sentinel.
func ParsePrincipalSegment(principal string) (PrincipalSegment, error) {
	if principal == "" {
		return "", nil
	}
	if len(principal) > MaxPrincipalLength {
		return "", yoerrors.NewUsageError("invalid principal %q: must be at most %d characters (got %d)", principal, MaxPrincipalLength, len(principal))
	}
	if !principalSegmentRe.MatchString(principal) {
		return "", yoerrors.NewUsageError("invalid principal %q: must be a single run of letters and digits", principal)
	}
	return PrincipalSegment(principal), nil
}
