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
// a margin. A fully-namespaced instance name "yoloai-<principal>-<name>" then
// tops out at 7 + 8 + 1 + 56 = 72 ≤ 76; the CLI's "yoloai-cli-<name>" is
// 7 + 3 + 1 + 56 = 67. Every instance name is principal-scoped (D126). See D62.
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
		return "", yoerrors.NewUsageError("invalid sandbox name %q: must be alphanumeric segments joined by single '.', '_', or '-' (no leading, trailing, or repeated separators), e.g. \"my-box\" or \"feature_2\"", name)
	}
	return SandboxName(name), nil
}

// PrincipalSegment is a parsed, validated principal identifier used to namespace
// runtime instance names across multiple principals (tenants). Every embedder
// names itself; there is no default or elision (D126, superseding D62's empty
// sentinel). A segment is a single alphanumeric run of at most
// MaxPrincipalLength. The empty string is not a valid segment — it is the
// "unset" value that ParsePrincipalSegment rejects and InstancePrefix panics on.
// See D62 / D126.
type PrincipalSegment string

// CLIPrincipal is the principal the yoloai CLI operates under. The CLI is an
// embedder like any other and names itself (D126): its runtime instances are
// "yoloai-cli-<name>". It already owns TOP/cli on the filesystem axis (D59);
// this carries that identity onto the runtime-namespace axis D62 exists to fix.
const CLIPrincipal PrincipalSegment = "cli"

// InstancePrefix returns the runtime instance-name prefix for the given
// principal: "yoloai-<principal>-", unconditionally. Every principal is
// non-empty (D126), so this cannot emit a bare "yoloai-" for anyone to hardcode
// — the poka-yoke that makes DF98's prefix-stripping class unwritable. This is
// the single source of truth for the prefix used by both store.InstanceName and
// the backend orphan sweeps, so principal-scoping stays centrally maintained.
//
// Panics on an empty principal. Like config.NewLayout panicking on an empty
// dataDir, the empty value is "unset", not a legal input: public boundaries
// (yoloai.NewClient) validate the principal and return *UsageError via
// ParsePrincipalSegment before any Layout is constructed, so a "" reaching here
// is a programming bug — an unprincipaled Layout — and it fails loudly at the
// point of use rather than silently producing the malformed "yoloai--". See
// D62 / D126 / DF19.
func InstancePrefix(p PrincipalSegment) string {
	if p == "" {
		panic("config.InstancePrefix: principal is required (empty is invalid; every embedder names itself per D126, and boundaries validate via ParsePrincipalSegment before a Layout is built)")
	}
	return "yoloai-" + string(p) + "-"
}

// ParsePrincipalSegment validates a principal identifier and returns it as a
// PrincipalSegment. The empty string is rejected (D126): there is no default
// principal, so an embedder that omits one gets a *UsageError rather than a
// silent sentinel.
func ParsePrincipalSegment(principal string) (PrincipalSegment, error) {
	if principal == "" {
		return "", yoerrors.NewUsageError("a principal is required: pass a non-empty ClientCreateOptions.Principal (the CLI uses %q)", CLIPrincipal)
	}
	if len(principal) > MaxPrincipalLength {
		return "", yoerrors.NewUsageError("invalid principal %q: must be at most %d characters (got %d)", principal, MaxPrincipalLength, len(principal))
	}
	if !principalSegmentRe.MatchString(principal) {
		return "", yoerrors.NewUsageError("invalid principal %q: must be a single run of letters and digits", principal)
	}
	return PrincipalSegment(principal), nil
}
