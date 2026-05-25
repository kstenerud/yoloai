//go:build never

// ABOUTME: W-L8a design checkpoint — the full proposed yoloai.Client surface
// ABOUTME: every CLI command will call after Phase 3 lands. Not built.

// Package yoloai_apidesign holds the W-L8a design checkpoint for the
// `yoloai.Client` surface that the CLI will consume after Phase 3 (W-L8b/c/d/e)
// of the layering refactor lands. Build tag `never` keeps the file out of
// every build — it's read like a header, not compiled.
//
// **Structural shape (Shape B, resolved 2026-05-24 / Q-G).** Resource-bound
// handles: `client.Sandbox(ctx, name)` returns a *Sandbox handle bound to
// (client, name), validated at construction (errors with
// ErrSandboxNotFound for missing names, *UsageError for syntactic
// problems). Sub-groupings (Workdir, Files, Network) are nested
// synchronous handles off the sandbox — pure namespace expansion, no IO,
// no error. Admin sits on a separate `*SystemClient` reachable via
// `client.System()`. Cross-sandbox operations (List, Run, Clone, the
// bug-report primitives) stay on `*Client` directly. The motivation: drop
// the artificial method prefixes (FilesPut/FilesGet/...) in favor of
// structural namespacing (Files().Put / Files().Get), and let the type
// hierarchy match the conceptual hierarchy.
//
// (Worth noting we considered the GCS-style lazy-handle pattern, where
// Sandbox(name) returns immediately and the name is "validated" by the
// first method call. Rejected because GCS's motivation — defer a network
// round-trip — doesn't apply locally, and the lazy pattern lands errors
// downstream of where the name was typed. Strict resource validation
// fits "parse, don't validate" §4 better.)
//
// **Scope.** Every public CLI command in `internal/cli/` is mapped to
// either a handle method (Sandbox / Workdir / Files / Network /
// SystemClient) or a Client method, or a documented exception. Once each
// is implemented on real types in W-L8b this file is deleted.
//
// **Acceptance criteria from layering-refactor.md:**
//   1. Every CLI command has either a method on a handle/sub-client or an
//      explicit exception comment.
//   2. Streaming / interactive operations take an explicit IOStreams struct
//      so TTY handling is visible in the signature.
//   3. Reviewer approval gates W-L8b.
//
// **Reviewer audit trail.** All seven W-L8a open questions resolved (Q-A
// through Q-G). Resolutions live in the "Open questions" section at the
// tail with **RESOLVED <date>** markers.

package yoloai

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// =============================================================================
// I/O primitives
// =============================================================================

// IOStreams bundles caller-provided stdio for streaming / interactive Client
// methods. Modeled on kubectl's `genericiooptions.IOStreams` — exposing TTY
// and stream wiring in the method signature instead of pulling it from
// globals lets embedders (HTTP server, MCP server, tests) plug in their own
// streams without monkey-patching.
//
// For non-interactive methods, IOStreams is not a parameter; output is
// returned in the result type or written to a single `io.Writer` arg.
type IOStreams struct {
	In  io.Reader // stdin (typically nil for non-interactive ops)
	Out io.Writer // stdout
	Err io.Writer // stderr

	// TTY signals that In/Out are a terminal. Backends use it to decide
	// whether to allocate a PTY (docker exec -it, tart exec -t). Set false
	// for piped invocations so the agent doesn't emit ANSI escape sequences
	// into a captured buffer.
	TTY bool

	// Rows and Cols are the terminal dimensions when TTY=true; 0 means
	// "unknown — let the backend pick a default". Provided so backends that
	// allocate a PTY can size it correctly at start (no later WINCH dance).
	Rows, Cols int
}

// SandboxNameFromEnv returns the YOLOAI_SANDBOX env-var value (or "") so
// embedders can match the CLI's name-from-env fallback without
// re-implementing internal/cli/envname.go (D9).
func SandboxNameFromEnv() string { panic("design-only") }

// =============================================================================
// Shared enums (referenced by Options structs throughout)
// =============================================================================

// IsolationMode selects the OCI / VM isolation level for a sandbox.
// Required field where used as a "choice" (RunOptions.Isolation) —
// empty is rejected with *UsageError. Optional where used as a "filter"
// or "override" (DoctorOptions.Isolation, RestartOptions.IsolationOverride)
// — empty there has its own meaning, documented on each field.
//
// Embedders who want the current default without writing it out can use
// DefaultRunOptions() (and the other Default*() factories); see Q-H
// resolution.
type IsolationMode string

const (
	IsolationContainer           IsolationMode = "container"            // runc; current default
	IsolationContainerEnhanced   IsolationMode = "container-enhanced"   // gVisor
	IsolationContainerPrivileged IsolationMode = "container-privileged" // runc + --privileged
	IsolationVM                  IsolationMode = "vm"                   // Kata + QEMU
	IsolationVMEnhanced          IsolationMode = "vm-enhanced"          // Kata + Firecracker
)

// HostOS selects the operating-system environment the agent runs in.
// Required where used as a "choice" (RunOptions.OS). Empty is rejected
// with *UsageError.
type HostOS string

const (
	OSLinux HostOS = "linux" // Linux container or VM (current default)
	OSMac   HostOS = "mac"   // macOS-native sandbox (Seatbelt or Tart)
)

// NetworkMode selects a sandbox's outbound network policy. Modeled as one
// enum field rather than two booleans — the invalid "isolated AND none"
// combination is unrepresentable. Required where used as a "choice"
// (RunOptions.Network). Empty is rejected with *UsageError.
type NetworkMode string

const (
	NetworkOpen     NetworkMode = "open"     // full outbound access (current default)
	NetworkIsolated NetworkMode = "isolated" // iptables + ipset domain allowlist
	NetworkNone     NetworkMode = "none"     // no outbound traffic
)

// TmuxConfMode selects how yoloai writes the per-sandbox tmux config.
// Setup requires a non-empty value; no canonicalization.
type TmuxConfMode string

const (
	TmuxConfDefault     TmuxConfMode = "default"      // baked-in defaults only
	TmuxConfDefaultHost TmuxConfMode = "default+host" // defaults + user's ~/.tmux.conf
	TmuxConfHost        TmuxConfMode = "host"         // user's ~/.tmux.conf only
	TmuxConfNone        TmuxConfMode = "none"         // no config (raw tmux)
)

// ApplyMode selects an override to Apply's default behavior. The zero
// value means "no override — do the normal per-directory apply"
// (git format-patch + am for :copy directories; in-container diff-and-apply
// for :overlay directories). The two named constants are overrides that
// change the behavior away from that default; there is no named "default"
// constant because that would just be the absence of a choice.
type ApplyMode string

const (
	ApplySquash ApplyMode = "squash" // flatten everything into one unstaged patch
	ApplyExport ApplyMode = "export" // write *.patch files to ExportDir; don't apply
)

// MountMode selects how a directory is exposed inside the sandbox.
// Used by RunOptions / DirSpec when declaring directories, and reported
// by DiffResult.Mode when listing the per-dir diffs. Required where a
// directory is declared; empty rejected with *UsageError.
type MountMode string

const (
	// MountCopy makes a fresh copy of the directory at sandbox-create
	// time. The agent's changes are isolated; review with diff and
	// land with apply. Default for the workdir.
	MountCopy MountMode = "copy"

	// MountOverlay mounts the directory via Linux overlayfs inside the
	// sandbox. Instant setup; changes accumulate in an upper layer.
	// Diff/apply work for the changes. Requires CAP_SYS_ADMIN and a
	// container backend that supports overlayfs.
	MountOverlay MountMode = "overlay"

	// MountRW bind-mounts the directory read-write into the sandbox.
	// Changes are live on the host; no diff/apply step. Use for
	// directories you want the agent to modify in place.
	MountRW MountMode = "rw"
)

// LogFormat selects which log stream to emit. Required field. Empty is
// rejected with *UsageError.
type LogFormat string

const (
	LogStructured    LogFormat = "structured" // pretty-printed merge-sorted JSONL (current default)
	LogStructuredRaw LogFormat = "raw"        // raw JSONL lines
	LogAgent         LogFormat = "agent"      // agent terminal, ANSI stripped
	LogAgentRaw      LogFormat = "agent-raw"  // raw agent terminal stream
)

// BugReportMode selects the redaction level for BugReport.
type BugReportMode string

const (
	BugReportSafe   BugReportMode = "safe"   // redacted; default
	BugReportUnsafe BugReportMode = "unsafe" // full unredacted content
)

// Availability is the verdict of a Doctor check for one backend+mode pair.
type Availability string

const (
	AvailabilityReady       Availability = "ready"
	AvailabilityWarning     Availability = "warning"
	AvailabilityUnavailable Availability = "unavailable"
)

// ExecUser identifies the run-as user for Exec. Open-set typed string:
// the named constants below document yoloai's stock-image conventions
// for discoverability, but any other value is passed through to the
// backend untouched as a Unix username or uid[:gid] spec — useful for
// profile-customized images that define their own users.
//
// Empty means "use the container's default user" (typically the yoloai
// user, uid 1001, in yoloai's stock images).
type ExecUser string

const (
	ExecUserRoot   ExecUser = "root"   // run as root inside the container (uid 0)
	ExecUserYoloai ExecUser = "yoloai" // run as the yoloai user (uid 1001 in stock images; the agent's default)
)

// PromptMode mirrors `agent.PromptMode` (the existing typed string in the
// agent package). Synthetic here; W-L8b will replace with a type alias
// (`type PromptMode = agent.PromptMode`).
type PromptMode string

const (
	PromptModeInteractive PromptMode = "interactive"
	PromptModeHeadless    PromptMode = "headless"
)

// =============================================================================
// Client — top-level entry point
// =============================================================================

// Client is the entry point for every yoloai operation. Construct via
// yoloai.New(ctx) or yoloai.NewWithOptions(ctx, Options{...}). Safe for
// concurrent use.
//
// Methods split by surface:
//   - Creation methods (Run, Clone) return *Info; the embedder builds a
//     handle for follow-up via client.Sandbox(name).
//   - Cross-sandbox queries (List) live directly on Client.
//   - Per-sandbox operations live on the *Sandbox handle from
//     client.Sandbox(name).
//   - Admin operations live on the *SystemClient from client.System().
//   - Bug-report primitives live on Client (BugReport takes a name
//     explicitly, since it's often called from error paths without a
//     handle in scope; StartBugReportSession is name-less).
type Client struct{}

// Options configures a Client.
//
// No Input or Output field by design. The Client is non-interactive
// (resolved Q-F: Client is orchestration, CLI is the UI layer) so it
// never reads stdin; embedders that need to relay user-facing progress
// events use the OnProgress callback on each method's Options struct.
// Streaming methods (Attach, Exec, ProxyMCP) take IOStreams per-call;
// that's the only place an input stream appears in the API.
//
// Logger is *slog.Logger directly rather than an interface — yoloai is
// internal-grade per D3 and uses slog throughout its own implementation.
// Embedders on logr / zap / zerolog bridge via the slog handler adapters
// each of those libraries provides.
type Options struct {
	Backend string       // explicit backend; "" = read from config, auto-detect
	Logger  *slog.Logger // structured-event sink; nil = slog.Default()
}

func New(ctx context.Context) (*Client, error)                          { panic("design-only") }
func NewWithOptions(ctx context.Context, opts Options) (*Client, error) { panic("design-only") }
func (*Client) Close() error                                            { panic("design-only") }

// Sandbox returns a name-bound handle for sandbox-scoped operations.
// Validates name resolution at construction — returns ErrSandboxNotFound
// when the sandbox doesn't exist on this host, or *UsageError when name
// is syntactically invalid (empty, contains illegal characters, etc.).
//
// Strict validation places errors at the line where the name is typed
// rather than in the middle of a downstream workflow. The handle then
// "proves" the sandbox existed at construction time (parse-don't-validate,
// §4). Methods on the handle still surface ErrSandboxNotFound when their
// own filesystem ops fail — the handle isn't a permanent existence
// guarantee, just a construction-time check (concurrent destroys can
// invalidate it).
//
// (We don't follow GCS's lazy-handle convention here because the
// motivation doesn't apply: GCS handles defer a network round-trip;
// yoloai's existence check is a local os.Stat.)
func (*Client) Sandbox(ctx context.Context, name string) (*Sandbox, error) {
	panic("design-only")
}

// System returns the admin sub-client (`yoloai system …` commands).
func (*Client) System() *SystemClient { panic("design-only") }

// List returns sandboxes matching opts. Cross-sandbox; lives directly on
// Client (a Sandbox handle wouldn't make sense — there's no "name" yet).
type ListOptions struct {
	Statuses        []string // "active", "idle", "done", ...; empty = all
	Agents          []string // filter by agent name
	Profiles        []string // filter by profile ("" for unprofiled)
	OnlyWithChanges bool     // filter to sandboxes with unapplied changes
}

func (*Client) List(ctx context.Context, opts ListOptions) ([]*Info, error) {
	panic("design-only")
}

// =============================================================================
// Client.Run / Client.Clone — creation methods
// =============================================================================
//
// Run and Clone are on Client (not Sandbox) because they CREATE a sandbox;
// there's no name-bound handle until after they return. The embedder
// constructs a Sandbox handle from info.Meta.Name for follow-up
// operations. CLI: `yoloai new`, `yoloai clone`.

// RunOptions configures Run. Field set unifies the existing Client.Run
// subset with sandbox.CreateOptions (the full `yoloai new` flag set).
type RunOptions struct {
	Name    string  // required; sandbox identifier
	Workdir DirSpec // primary work directory; required (Path + Mode)

	AuxDirs []DirSpec // additional `-d <dir>` mounts; read-only by default

	Agent   string // "claude", "gemini", "codex", "opencode", "aider", "test"
	Model   string // agent-specific model id or alias
	Profile string // profile name
	Prompt  string // initial prompt; empty = interactive

	Isolation IsolationMode // REQUIRED; empty rejected. Use DefaultRunOptions() for the current default.
	OS        HostOS        // REQUIRED; empty rejected
	Network   NetworkMode   // REQUIRED; empty rejected

	AllowDomains []string // initial allowlist; only meaningful with NetworkIsolated

	Env       map[string]string
	Mounts    []string
	Ports     []string
	Secrets   []string
	BuildArgs map[string]string

	Replace            bool // destroy any existing sandbox with the same name first
	SkipDirtyRepoCheck bool // proceed despite uncommitted changes in the host workdir
	NoStart            bool // create state but don't start the container yet

	SimulatorRuntimes []string // tart-only: pre-built Apple simulator runtime base ("ios:26.4", "tvos")

	Wait       bool             // block until terminal status
	OnProgress func(msg string) // called with human-readable progress lines; nil = silent
}

// DefaultRunOptions returns a RunOptions value with the current default
// choices populated for the required enum fields (Isolation = container,
// OS = linux, Network = open). Embedders who want the lazy ergonomic
// without writing those out can use this as a starting point and override
// what they care about:
//
//	opts := yoloai.DefaultRunOptions()
//	opts.Name = "myproj"
//	opts.Workdir = yoloai.DirSpec{Path: "/path"}
//	client.Run(ctx, opts)
//
// Tests should NOT use this helper — they should construct RunOptions{}
// literals with every field explicit, so that a future change to the
// default isolation/OS/network doesn't silently shift test behavior.
func DefaultRunOptions() RunOptions { panic("design-only") }

func (*Client) Run(ctx context.Context, opts RunOptions) (*Info, error) { panic("design-only") }

// CloneOptions configures Clone.
type CloneOptions struct {
	Source     string           // existing sandbox name
	Dest       string           // new sandbox name; must not exist
	OnProgress func(msg string) // human-readable progress; nil = silent
}

func (*Client) Clone(ctx context.Context, opts CloneOptions) (*Info, error) {
	panic("design-only")
}

// =============================================================================
// Sandbox handle — name-bound; methods for one sandbox
// =============================================================================

// Sandbox is a name-bound handle for one sandbox. Construct via
// Client.Sandbox(ctx, name), which validates that the name resolves
// before returning the handle — see that method for details.
//
// Once you have a *Sandbox, name lookup has succeeded. Methods on the
// handle do their own per-op IO (Inspect reads metadata, Diff runs git,
// etc.) but don't re-validate the name. A concurrent destroy can
// invalidate a handle between method calls; those methods then surface
// ErrSandboxNotFound from their own IO, just like any other Go object
// that holds a reference to mutable shared state.
type Sandbox struct{}

// Name returns the bound sandbox name. Always valid: Client.Sandbox
// validated it at construction.
func (*Sandbox) Name() string { panic("design-only") }

// --- lifecycle + introspection ---

// Inspect returns the full sandbox snapshot — metadata, lifecycle state,
// agent status, exchange-dir path, original prompt, baseline SHA, etc.
// Multiple backend round-trips; not cheap. Use Status for the polling
// case.
func (*Sandbox) Inspect(ctx context.Context) (*Info, error) { panic("design-only") }

// Status returns just the lifecycle enum. Single cheap check; the polling
// companion to Inspect.
func (*Sandbox) Status(ctx context.Context) (Status, error) { panic("design-only") }

// StartOptions configures Start. No Attach field: "start and attach"
// is composition, not a primitive — callers (CLI and embedders alike)
// call Sandbox.Attach with explicit IOStreams after Start returns. This
// keeps the os.Stdin/Stdout/Stderr presumption out of the lifecycle
// surface (same principle as Q-F: Client provides primitives; CLI
// composes them).
//
// No isolation-override field either: Start just starts a stopped
// sandbox with the isolation it was created with. Changing isolation
// requires a container recreation — that's on Restart (see
// RestartOptions.IsolationOverride).
type StartOptions struct {
	NewPrompt string // optional prompt to inject after relaunch; distinct from RunOptions.Prompt (the original)
}

func (*Sandbox) Start(ctx context.Context, opts StartOptions) error { panic("design-only") }

// StopOptions configures Stop. Reserved for future use (e.g. --timeout).
type StopOptions struct{}

func (*Sandbox) Stop(ctx context.Context, opts StopOptions) error { panic("design-only") }

// RestartOptions configures Restart. Restart is "stop, optionally change
// isolation, recreate the container, start" — the natural home for an
// isolation override since changing isolation requires container
// recreation.
//
// No Attach field; same rationale as StartOptions.
//
// **Isolation-change policy.** A transition is permitted only when the
// target mode is in the active backend's SupportedIsolationModes set —
// i.e., a within-backend transition that recreates the container with a
// different runtime configuration but keeps the same image, network
// model, and host-mounted state. Concretely:
//
//	container ↔ container-privileged     (docker / podman): allowed
//	container ↔ container-enhanced       (docker / podman): allowed, but
//	    refused with *UsageError when :overlay directories hold uncommitted
//	    state (gVisor doesn't support overlayfs in-container; data would
//	    be lost). Force=true overrides after the user acknowledges.
//	vm ↔ vm-enhanced                     (containerd): allowed
//	container* family ↔ vm* family       — REFUSED with *UsageError
//	    pointing at the destroy + recreate sequence. The backend changes
//	    (docker ↔ containerd); the image lives in a different store; the
//	    network model is different. This is a new sandbox, not a restart.
type RestartOptions struct {
	IsolationOverride     IsolationMode // empty = keep current isolation. Cross-backend transitions refused.
	AcceptOverlayDataLoss bool          // acknowledge that :overlay state will be destroyed on container ↔ container-enhanced transitions. Required when overlay dirs hold uncommitted state for that transition.
}

func (*Sandbox) Restart(ctx context.Context, opts RestartOptions) error { panic("design-only") }

// DestroyOptions configures Destroy.
type DestroyOptions struct {
	SkipApplyCheck bool // proceed despite the ErrUnappliedChanges refusal (unapplied agent commits / uncommitted edits)
}

func (*Sandbox) Destroy(ctx context.Context, opts DestroyOptions) error { panic("design-only") }

// ResetOptions configures Reset. No Attach field; same rationale as
// StartOptions.
type ResetOptions struct {
	RestartContainer bool // also stop+start the container after resetting state (in-place by default)
	ClearState       bool
	KeepCache        bool
	KeepFiles        bool
	NewPrompt        string // optional prompt to inject after reset; distinct from RunOptions.Prompt
}

func (*Sandbox) Reset(ctx context.Context, opts ResetOptions) error { panic("design-only") }

// WaitOptions configures Wait — block until terminal status.
type WaitOptions struct {
	Timeout      time.Duration       // 0 = no timeout
	PollInterval time.Duration       // 0 = default 5 s
	OnStatus     func(status Status) // called once per poll
}

// Wait blocks until the sandbox reaches a terminal status (StatusDone,
// StatusFailed, StatusStopped) or ctx is cancelled. Returns the agent's
// exit code (0 for clean StatusDone, non-zero otherwise) and a wrapped
// error on cancel / timeout / inspect failure.
func (*Sandbox) Wait(ctx context.Context, opts WaitOptions) (exitCode int, err error) {
	panic("design-only")
}

// --- streaming / interactive ---

// AttachOptions configures Attach.
type AttachOptions struct{}

// Attach blocks until the user detaches or the agent exits. Requires
// IOStreams.TTY=true; non-TTY attach returns a *UsageError.
func (*Sandbox) Attach(ctx context.Context, opts AttachOptions, io IOStreams) error {
	panic("design-only")
}

// ExecOptions configures Exec.
type ExecOptions struct {
	Command []string
	Env     map[string]string
	Dir     string   // current working directory for the exec'd process (NOT the sandbox's primary workdir); empty = container's default WORKDIR
	User    ExecUser // run-as user; empty = container default. See ExecUser for known values.
}

// ExecResult is returned by Exec. Always non-nil when the underlying
// process at least started (even on partial completion or cancellation).
// Fields are populated as far as the run got:
//
//   - Process ran to completion (any exit code):
//     Stdout / Stderr fully captured; ExitCode is the real exit code.
//     Returned with error == nil.
//
//   - Process killed (ctx cancelled, SIGKILL):
//     Stdout / Stderr contain bytes received before the kill;
//     ExitCode reflects the kill (-1 or 128+signal depending on backend).
//     Returned with error wrapping ctx.Err() (or the underlying kill cause).
//
//   - IO error mid-stream (broken pipe, read failure):
//     Stdout / Stderr contain bytes up to the failure; ExitCode may not
//     be set. Returned with the io error.
//
//   - Process never started (binary not found, permission denied, etc.):
//     Exec returns (nil, err); there is no result to populate.
//
//   - IOStreams.TTY=true (streaming): output went straight to io.Out /
//     io.Err; ExecResult fields are empty by design. ExitCode is still
//     set when the process completes normally.
//
// **Non-zero ExitCode is NOT a Go error.** Exec returns error == nil for
// any clean process completion regardless of exit code. Tooling-style
// usage where exit codes carry meaning (linters returning 1 for findings,
// tests returning 2 for compile failure, grep returning 1 for no match)
// works without forcing callers to decide "is this an error or just
// non-zero?". Callers branch on ExitCode explicitly when they care.
//
// This contract differs from exec.Cmd.Run(), which returns *ExitError on
// non-zero exit. The trade-off favors tooling embedders over consumers
// who treat any non-zero exit as fatal.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs cmd inside the sandbox. TTY semantics chosen at the call site
// via IOStreams (kubectl pattern).
func (*Sandbox) Exec(ctx context.Context, opts ExecOptions, io IOStreams) (*ExecResult, error) {
	panic("design-only")
}

// LogOptions configures Logs.
type LogOptions struct {
	Format   LogFormat     // REQUIRED; empty rejected. Use DefaultLogOptions() for LogStructured.
	Sources  []string      // for LogStructured / LogStructuredRaw: cli, sandbox, monitor, hooks
	MinLevel string        // debug | info | warn | error
	Since    time.Duration // 0 = no filter
	Follow   bool          // tail live; returns when sandbox is done
}

// DefaultLogOptions returns a LogOptions value populated with the current
// default Format (LogStructured). Same opt-in pattern as
// DefaultRunOptions — see that function's doc.
func DefaultLogOptions() LogOptions { panic("design-only") }

// Logs streams the requested log to w. Behavior depends on
// LogOptions.Follow:
//
//   - Follow=false (default): emit the log up to "now" and return.
//     Suitable for one-shot snapshots and bug-report inclusion.
//
//   - Follow=true: same as above, then keep writing as new entries
//     arrive. Returns when one of three things happens:
//     1. ctx is cancelled — caller-driven stop. Returns ctx.Err().
//     2. The sandbox reaches a terminal status (StatusDone,
//     StatusFailed, StatusStopped) — no more entries can arrive.
//     Returns nil.
//     3. w returns a non-nil error from Write (broken pipe, HTTP
//     client disconnected, etc.). The wrapped write error is
//     returned.
//
// Non-CLI callers (HTTP server streaming to a client, websocket relay,
// test harness) typically pass a request-scoped ctx so client
// disconnect cancels the follow loop:
//
//	func handleLogStream(w http.ResponseWriter, r *http.Request) {
//	    sb, err := client.Sandbox(r.Context(), name)
//	    if err != nil { http.Error(w, err.Error(), 404); return }
//	    _ = sb.Logs(r.Context(), yoloai.LogOptions{
//	        Format: yoloai.LogStructuredRaw, // JSONL — client parses
//	        Follow: true,
//	    }, w)
//	}
//
// The method never panics on a cancelled ctx; it observes the
// cancellation and returns.
func (*Sandbox) Logs(ctx context.Context, opts LogOptions, w io.Writer) error {
	panic("design-only")
}

// ProxyMCP bridges the caller's stdio to an inner MCP server running
// inside the sandbox. Requires the backend to implement
// runtime.StdioExecer; returns *UsageError otherwise.
func (*Sandbox) ProxyMCP(ctx context.Context, io IOStreams) error { panic("design-only") }

// --- sub-handles ---
//
// Workdir / Files / Network are cheap synchronous wrappers — pure
// namespace expansion off an already-validated *Sandbox. No IO, no
// errors; their methods do all the work.

// Workdir returns a handle for diff/apply/baseline operations on the
// sandbox's work tree.
func (*Sandbox) Workdir() *Workdir { panic("design-only") }

// Files returns a handle for the sandbox's exchange directory (the
// host-shared `/yoloai/files/` location).
func (*Sandbox) Files() *Files { panic("design-only") }

// Network returns a handle for the sandbox's network allowlist (when
// created with NetworkIsolated).
func (*Sandbox) Network() *Network { panic("design-only") }

// =============================================================================
// Workdir — diff / apply / baseline (sub-handle off Sandbox)
// =============================================================================

// Workdir scopes diff / apply / baseline operations on a single sandbox.
// Constructed via Sandbox.Workdir(); never errors.
type Workdir struct{}

// DiffOptions configures Diff.
type DiffOptions struct {
	Ref      string   // single ref or "A..B" range; "" = full agent diff
	Paths    []string // pathspec filters; empty = all
	Stat     bool     // --stat
	NameOnly bool     // --name-only
}

// DiffResult mirrors workspace.DiffResult (re-exported through
// sandbox/patch). One entry per diffable directory in the sandbox —
// the workdir plus every aux directory mounted in :copy or :overlay
// mode. :rw aux dirs do not produce diffs (changes are already live
// on the host).
type DiffResult struct {
	Dir       string    // host path to the directory that was diffed
	Mode      MountMode // MountCopy or MountOverlay (MountRW dirs don't produce diffs)
	Output    string    // diff text, OR stat summary when DiffOptions.Stat = true. Single field — only one form is populated per call, depending on opts.
	Empty     bool      // true when the dir had no changes from baseline; useful for skipping/hiding empty entries in UI
	IsWorkdir bool      // true for the primary workdir; false for aux (-d) dirs. Precomputed convenience for UI rendering — saves the caller a second Inspect call.
}

func (*Workdir) Diff(ctx context.Context, opts DiffOptions) ([]*DiffResult, error) {
	panic("design-only")
}

// ApplyOptions configures Apply.
// ApplyOptions configures Apply.
//
// No confirmation-skip field: Apply has no state-bearing refusal to
// acknowledge. The "are you sure you want to apply N commits?" prompt
// in today's CLI is pure UX with no underlying Client state; the CLI
// handles it before calling Apply. Embedders just call Apply; if they
// want to confirm with their user first, that's their concern.
type ApplyOptions struct {
	Mode               ApplyMode // empty = default behavior; ApplySquash / ApplyExport override
	ExportDir          string    // required when Mode == ApplyExport
	Refs               []string  // specific commits or ranges
	Paths              []string  // pathspec filter
	IncludeUncommitted bool      // also apply the agent's uncommitted edits (staged + unstaged + untracked) as unstaged changes on the host; default is committed changes only
	IncludeTags        bool      // also transfer git tags created by the agent; invalid with ApplySquash
	DryRun             bool      // invalid with ApplyExport
}

// ApplyResult bundles per-directory outcomes plus a top-level rollup.
//
// Distinction between PerDir and SkippedDirs:
//   - PerDir lists every directory Apply actually processed. Entries
//     where the directory had no changes from baseline are reported
//     here with Applied=false (the diff was empty; nothing to do).
//   - SkippedDirs lists directories Apply declined to process at all
//     because of the directory's mount mode or container state. Each
//     entry carries a Reason so embedders can render or branch without
//     remembering the dir's mount mode out of band.
type ApplyResult struct {
	PerDir      []*PerDirApplyResult
	Patches     []string // populated only when Mode == ApplyExport
	SkippedDirs []SkippedDir
}

// SkippedDir reports one directory that Apply declined to act on,
// together with the reason. See SkipReason for the closed-set of
// reasons known today.
type SkippedDir struct {
	Dir    string // host path to the skipped directory
	Reason SkipReason
}

// SkipReason categorizes why a directory was skipped by Apply.
// Open-set typed string (same shape as ExecUser): the named constants
// document the cases known today; future skip cases add their own
// constants without breaking existing embedders.
type SkipReason string

const (
	// SkipReasonReadWrite: :rw directories don't need applying — the
	// agent's changes are already live on the host bind-mount.
	SkipReasonReadWrite SkipReason = "rw"

	// SkipReasonOverlayStopped: :overlay directories need a running
	// container to compute and apply the in-container diff. When the
	// container is stopped, Apply reports this rather than failing.
	SkipReasonOverlayStopped SkipReason = "overlay-stopped"
)

// ApplyStatus is the typed outcome of one per-directory apply. Replaces
// the Applied + Conflicts boolean pair that confused four mutually-
// exclusive states into two flags. Embedders switch on Status to know
// what happened and (via Recovery) what — if anything — the user
// should do next.
type ApplyStatus string

const (
	// ApplyStatusApplied: clean apply. Commits landed on the host repo
	// (or unstaged diff was written for squash mode).
	ApplyStatusApplied ApplyStatus = "applied"

	// ApplyStatusEmpty: the dir had no changes to apply. PerDir entry
	// reports the dir was processed; nothing happened.
	ApplyStatusEmpty ApplyStatus = "empty"

	// ApplyStatusConflict: git am (or the analogous overlay path)
	// failed to apply cleanly. The repo was rolled back to its
	// pre-apply state via `git am --abort`; any host-side stash was
	// popped back. The user needs to inspect the diff and either fix
	// it or skip this dir. Recovery field carries the next-step text.
	ApplyStatusConflict ApplyStatus = "conflict"

	// ApplyStatusAppliedStashConflict: git am succeeded — the agent's
	// commits ARE on the host repo — but the subsequent `git stash
	// pop` (restoring the user's pre-apply uncommitted edits) hit a
	// conflict against the newly-applied commits. The user has the
	// commits PLUS unresolved merge markers in their working tree.
	// Recovery carries resolution instructions.
	ApplyStatusAppliedStashConflict ApplyStatus = "applied-stash-conflict"

	// ApplyStatusDryRun: DryRun=true; the Patch field holds what
	// would have been applied. Nothing changed on the host.
	ApplyStatusDryRun ApplyStatus = "dry-run"
)

// PerDirApplyResult reports one directory's apply outcome.
type PerDirApplyResult struct {
	Dir        string      // host path of the directory
	Status     ApplyStatus // see ApplyStatus constants
	Patch      string      // populated only when Status == ApplyStatusDryRun
	Recovery   string      // human-readable next-step instructions; populated when Status requires user action (Conflict or AppliedStashConflict); empty otherwise
	ErrMessage string      // non-empty when an unexpected error occurred (not the same as a conflict; for runtime / IO failures)
}

func (*Workdir) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	panic("design-only")
}

// Baseline operations. CLI: `yoloai baseline {advance|set|log}`. Baselines
// are part of the work-tree concept (the baseline SHA marks where the
// agent's accumulated changes start), so they live here rather than as a
// separate sub-handle.

// BaselineLogEntry is one commit in the sandbox's work-copy history, as
// reported by Workdir.BaselineLog. The list runs from the sandbox's
// inception commit (immutable creation-time SHA stored in
// meta.Workdir.InceptionSHA) to HEAD; exactly one entry has
// IsBaseline=true — the commit that meta.Workdir.BaselineSHA currently
// points at.
//
// Used for recovery / debugging — "where am I relative to baseline?"
// and "I accidentally advanced too far; pick a commit to SetBaseline
// to." Common workflows don't need this: Apply advances baseline
// automatically (see Workdir.AdvanceBaseline), and Diff computes
// against the baseline internally. The CLI surfaces this via
// `yoloai baseline log <name>`.
type BaselineLogEntry struct {
	SHA        string // full git commit hash
	Subject    string // first line of the commit message
	IsBaseline bool   // true for the entry the current baseline points at
}

// AdvanceBaseline moves the sandbox baseline to the work copy's current
// HEAD. Use after an out-of-band apply (raw `git am`, CI tool, etc.)
// so subsequent Diff / Apply don't re-process commits that are already
// on the host.
func (*Workdir) AdvanceBaseline(ctx context.Context) error { panic("design-only") }

// SetBaseline pins the baseline to a specific commit SHA. Recovery
// tool — primarily for fixing baselines that got stuck (e.g. after a
// git stash pop conflict during apply that left the baseline behind
// the actually-applied commits) or were advanced too far by mistake.
func (*Workdir) SetBaseline(ctx context.Context, sha string) error { panic("design-only") }

// BaselineLog returns the sandbox work copy's commit history from
// inception to HEAD with the current baseline marked. Debug / recovery
// tool; not part of the common Diff/Apply workflow.
func (*Workdir) BaselineLog(ctx context.Context) ([]BaselineLogEntry, error) {
	panic("design-only")
}

// =============================================================================
// Files — exchange-dir operations (sub-handle off Sandbox)
// =============================================================================

// Files scopes exchange-directory operations on a single sandbox.
// Constructed via Sandbox.Files(); never errors. Replaces the
// FilesPut/FilesGet/FilesLs/FilesRm prefix dance with structural
// namespacing.
//
// The host path to the exchange dir lives on *Info (returned by
// Sandbox.Inspect) as Info.ExchangeDir — no separate FilesPath method.
type Files struct{}

// PutOptions configures Put. Host glob expansion happens at the caller
// (the CLI does shell globbing); Sources are already-resolved host paths.
type PutOptions struct {
	Sources   []string
	Overwrite bool // overwrite existing destination files
}

func (*Files) Put(ctx context.Context, opts PutOptions) error { panic("design-only") }

// GetOptions configures Get. Patterns are evaluated inside the sandbox
// exchange dir and may expand to multiple files; OutputPath is the host
// destination.
//
// **OutputPath rules** (enforced by Get; violations return *UsageError):
//
//   - Patterns expand to multiple files → OutputPath must exist and be a
//     directory. Each matched file is written into it using its basename.
//
//   - Patterns expand to a single file → OutputPath may be either:
//     (a) an existing directory (file is written into it with basename), or
//     (b) a file path (created or overwritten per Overwrite).
//
//   - OutputPath is REQUIRED — empty is rejected. The CLI's `-o` flag
//     defaults to "."; embedders that want CWD pass "." explicitly.
//
//   - Without Overwrite=true, an existing destination file at the resolved
//     final path returns *UsageError. Overwrite=true replaces it.
type GetOptions struct {
	Patterns   []string
	OutputPath string // REQUIRED; empty rejected. See type doc for the multi-file vs single-file rules.
	Overwrite  bool   // overwrite existing destination files
}

// FileEntry describes one file in the exchange directory.
type FileEntry struct {
	Path string
	Size int64
	Mode uint32
}

func (*Files) Get(ctx context.Context, opts GetOptions) (written []string, err error) {
	panic("design-only")
}
func (*Files) Ls(ctx context.Context, patterns []string) ([]FileEntry, error) {
	panic("design-only")
}
func (*Files) Rm(ctx context.Context, patterns []string) error { panic("design-only") }

// =============================================================================
// Network — allowlist operations (sub-handle off Sandbox)
// =============================================================================

// Network scopes network-allowlist operations on a single sandbox.
// Constructed via Sandbox.Network(); never errors. Only meaningful when
// the sandbox was created with NetworkIsolated; otherwise methods
// return *UsageError.
//
// **Allowlist-only model.** The sandbox holds ONE list of permitted
// outbound domains. Anything not on the list is blocked by iptables +
// ipset rules inside the sandbox. There is no separate denylist data
// structure — "denying" a domain means removing it from the allowlist
// so the implicit drop rule applies.
//
//   - Allow(domains)  — add domains to the allowlist
//   - Remove(domains) — remove domains from the allowlist (the policy
//     effect is that the domain becomes denied)
//   - Allowed()       — return the current allowlist
//
// The allowlist returned by Allowed() is the merged set of (a) the
// agent's built-in default allowlist (agentDef.NetworkAllowlist —
// e.g. api.anthropic.com for Claude), (b) the initial
// RunOptions.AllowDomains supplied at creation, and (c) any runtime
// Allow() additions. Callers can't currently distinguish the three
// sources from each other; if a future use case needs to (e.g. a
// recovery UI that warns "removing an agent-default would break the
// agent"), a richer Allowed() variant can be added without breaking
// the current shape.
type Network struct{}

func (*Network) Allow(ctx context.Context, domains []string) error  { panic("design-only") }
func (*Network) Remove(ctx context.Context, domains []string) error { panic("design-only") }
func (*Network) Allowed(ctx context.Context) ([]string, error)      { panic("design-only") }

// =============================================================================
// Client — bug-report primitives
// =============================================================================

// BugReportOptions configures BugReport and StartBugReportSession.
type BugReportOptions struct {
	Mode      BugReportMode // REQUIRED; empty rejected. Use DefaultBugReportOptions() for BugReportSafe.
	OutputDir string        // directory to write the report; "" = CWD
}

// DefaultBugReportOptions returns a BugReportOptions value with Mode
// populated as BugReportSafe. Same opt-in pattern as DefaultRunOptions.
func DefaultBugReportOptions() BugReportOptions { panic("design-only") }

// BugReport captures a one-shot snapshot of a sandbox plus relevant
// system state to a markdown file. Returns the absolute path of the
// written file. Distinct from StartBugReportSession — BugReport gathers state
// AT the call moment without buffering events. CLI: `yoloai sandbox
// <name> bugreport [safe|unsafe]`.
//
// Lives on Client (taking name explicitly) rather than Sandbox so it's
// usable from error paths where you don't have a handle in scope (e.g.
// inside a defer after Run failed).
func (*Client) BugReport(ctx context.Context, name string, opts BugReportOptions) (path string, err error) {
	panic("design-only")
}

// BugReportSession is the handle returned by StartBugReportSession. Buffers
// runtime events until Stop or Discard.
type BugReportSession interface {
	Stop() (path string, err error) // flush + write report
	Discard()                       // throw away buffered events
}

// StartBugReportSession begins buffering runtime events. Embedders scope the
// session however they want (single risky call, fixed duration, program
// lifetime). The CLI's top-level `--bugreport` flag is a thin wrapper.
func (*Client) StartBugReportSession(ctx context.Context, opts BugReportOptions) BugReportSession {
	panic("design-only")
}

// =============================================================================
// SystemClient — admin sub-client (off Client)
// =============================================================================

// SystemClient scopes `yoloai system …` operations. Constructed via
// Client.System(); never errors at construction.
type SystemClient struct{}

// BackendInfo bundles a BackendDescriptor with its current Probe verdict.
type BackendInfo struct {
	Name              string
	Description       string
	Platforms         []string
	Requires          string
	InstallHint       string
	Available         bool
	UnavailableReason string // human-readable probe failure reason; empty when Available=true
	Version           string // VersionString() output; empty when Available=false
}

// AgentInfo is the public face of `agent.Definition`.
type AgentInfo struct {
	Name          string
	Description   string
	PromptMode    PromptMode
	APIKeyEnvVars []string
	ModelAliases  map[string]string
}

// BuildInfo carries metadata about the yoloai binary itself (from
// compile-time -ldflags), grouped together since these three fields
// only make sense as a set.
type BuildInfo struct {
	Version string // semver tag or "dev" (yoloai version string)
	Commit  string // git short SHA the binary was built from
	Date    string // ISO-8601 build timestamp
}

// SystemInfo bundles paths + disk usage + build metadata.
type SystemInfo struct {
	Build             BuildInfo
	ConfigPath        string
	ProfileConfigPath string
	DataDir           string
	SandboxesDir      string
	DiskUsage         string
	Backends          []BackendInfo
}

func (*SystemClient) Info(ctx context.Context) (*SystemInfo, error)       { panic("design-only") }
func (*SystemClient) Backends(ctx context.Context) ([]BackendInfo, error) { panic("design-only") }
func (*SystemClient) Backend(ctx context.Context, name string) (*BackendInfo, error) {
	panic("design-only")
}
func (*SystemClient) Agents() []AgentInfo                   { panic("design-only") }
func (*SystemClient) Agent(name string) (*AgentInfo, error) { panic("design-only") }

// BuildOptions configures Build.
type BuildOptions struct {
	Profile     string
	Backend     string
	AllBackends bool // build across every available backend (exclusive with Backend)
	Rebuild     bool // build even when the checksum says the existing image is current
	Secrets     []string
	OnProgress  func(msg string) // human-readable progress (image build steps); nil = silent
}

func (*SystemClient) Build(ctx context.Context, opts BuildOptions) error { panic("design-only") }

// Check is a short summary of backend availability. Distinct from Doctor
// (which is the full capability report).
func (*SystemClient) Check(ctx context.Context) error { panic("design-only") }

// DiskUsage reports per-backend disk consumption.
type DiskUsage struct {
	Sandboxes  int64
	PerBackend []BackendDiskUsage
}

type BackendDiskUsage struct {
	Name   string
	Bytes  int64
	Detail string
	Error  string
}

func (*SystemClient) DiskUsage(ctx context.Context) (*DiskUsage, error) { panic("design-only") }

// DoctorOptions configures Doctor.
type DoctorOptions struct {
	Backend    string           // filter to one backend
	Isolation  IsolationMode    // filter to one isolation mode
	OnProgress func(msg string) // human-readable progress (per-check); nil = silent
}

// DoctorReport is the verdict for one backend+mode pair.
type DoctorReport struct {
	Backend             string
	Mode                IsolationMode // "" when IsDefaultMode
	IsDefaultMode       bool
	Availability        Availability
	InitErr             error
	MissingCapabilities []string
}

func (*SystemClient) Doctor(ctx context.Context, opts DoctorOptions) ([]DoctorReport, error) {
	panic("design-only")
}

// PruneOptions configures Prune.
type PruneOptions struct {
	Backend      string // "" = all
	DryRun       bool
	PruneCache   bool // also prune backend caches (forces base rebuild)
	IncludeStale bool
	OnProgress   func(msg string) // human-readable progress (per-item); nil = silent
}

type PruneResult struct {
	RemovedItems []string
	FreedBytes   int64
}

func (*SystemClient) Prune(ctx context.Context, opts PruneOptions) (*PruneResult, error) {
	panic("design-only")
}

// SetupOptions carries every answer the first-run setup wizard would have
// collected. The CLI's `yoloai system setup` is an interactive wizard
// that fills these in by prompting the user (or accepting flag overrides),
// then calls Setup with the populated struct. Embedders set these
// directly. Setup never prompts — every answer must be supplied here.
type SetupOptions struct {
	TmuxConf   TmuxConfMode     // required
	Backend    string           // initial default container_backend; "" = no default set
	Agent      string           // initial default agent name; "" = no default set
	OnProgress func(msg string) // human-readable progress (per-step); nil = silent
}

func (*SystemClient) Setup(ctx context.Context, opts SetupOptions) error { panic("design-only") }

// =============================================================================
// Re-exported types
// =============================================================================
//
// W-L8b lands real aliases or re-exports. Listed here so the design
// type-checks without pulling internal packages.

// Status re-exports sandbox.Status.
type Status = string

// Info bundles sandbox metadata + lifecycle state. Returned by
// Sandbox.Inspect. Includes ExchangeDir and Prompt fields so embedders
// don't need separate FilesPath / Prompt methods (collapsed per Q-G
// resolution).
type Info = struct {
	// Identity + metadata
	Meta any // sandbox.Meta — full metadata struct

	// Lifecycle state
	Status      Status
	AgentStatus string // "active", "idle", "done", etc. (separate from lifecycle)
	HasChanges  bool   // unapplied agent commits or uncommitted edits

	// Convenience fields lifted to avoid separate methods
	ExchangeDir string // host path to the sandbox's /yoloai/files/ exchange dir
	Prompt      string // original prompt the sandbox was created with
	BaselineSHA string // current baseline ref

	// Backend-side facts
	Backend   string
	Image     string
	Isolation IsolationMode
}

// DirSpec re-exports sandbox.DirSpec.
type DirSpec struct {
	Path string
	Mode MountMode // REQUIRED; empty rejected. MountCopy is the typical workdir choice.
}

// =============================================================================
// Error taxonomy
// =============================================================================
//
// Every Client method returns nil, one of the five stable sentinels, a
// *UsageError, or an *UnrecoverableError — no "other". Internal callers
// wrap raw errors into one of these categories before returning. The
// taxonomy is exhaustive so embedders never need to string-match.
//
// Naming follows Go stdlib convention: ErrXxx for sentinel values
// (matched with errors.Is), XxxError for struct types (matched with
// errors.As).

// Stable sentinels.
var (
	// ErrSandboxExists is returned by Run when a sandbox with the given
	// name already exists and Replace is false.
	ErrSandboxExists error

	// ErrSandboxNotFound is returned by any name-based method when the
	// named sandbox does not exist. Replaces today's wrapped
	// fs.ErrNotExist.
	ErrSandboxNotFound error

	// ErrUnappliedChanges is returned by Destroy when the sandbox has
	// unapplied changes and DestroyOptions.Force is false.
	ErrUnappliedChanges error

	// ErrNoChanges is returned by Apply when there are no agent commits
	// (or uncommitted edits, with IncludeUncommitted) to apply.
	ErrNoChanges error

	// ErrBackendUnavailable is returned by New when the requested or
	// auto-selected backend is not usable on this host.
	ErrBackendUnavailable error
)

// UsageError indicates the caller passed something the Client refused
// before doing any work. CLI maps to exit code 2 with a "Run 'yoloai
// <cmd> -h' for help" hint. Re-exports internal/yoerrors.UsageError.
type UsageError struct {
	Msg  string
	Hint string // optional follow-up text
}

func (*UsageError) Error() string { panic("design-only") }

// UnrecoverableError indicates the Client started an operation, hit a
// state it can't recover from, and gave up. Code categorizes the failure
// for embedder branching and CLI exit-code mapping. Unwrap returns Cause
// so errors.Is/errors.As walks past UnrecoverableError to find any
// sentinel inside.
type UnrecoverableError struct {
	Code    UnrecoverableCode
	Message string
	Detail  string
	Cause   error
}

func (*UnrecoverableError) Error() string { panic("design-only") }
func (*UnrecoverableError) Unwrap() error { panic("design-only") }

// UnrecoverableCode is the typed enum of UnrecoverableError categories.
type UnrecoverableCode string

const (
	UnrecoverableAgentCrash     UnrecoverableCode = "agent_crash"
	UnrecoverableBackendFailure UnrecoverableCode = "backend_failure"
	UnrecoverableBuildFailure   UnrecoverableCode = "build_failure"
	UnrecoverableStateCorrupted UnrecoverableCode = "state_corrupted"
	UnrecoverableVMBootFailure  UnrecoverableCode = "vm_boot_failure"
	UnrecoverableNotImplemented UnrecoverableCode = "not_implemented"
	UnrecoverableInternal       UnrecoverableCode = "internal"
)

// =============================================================================
// Exceptions — CLI commands that do NOT go through Client / Sandbox / System
// =============================================================================
//
//   help [topic]                  Pure presentation. Renders embedded
//                                 markdown topics. No sandbox state.
//
//   completion <shell>            Shell completion script generation.
//                                 Pure cobra/CLI machinery.
//
//   version                       Build-time constants. No state.
//
//   config get|set|reset          User-config file edits. CLI calls
//                                 `internal/config` directly. config/ is a
//                                 leaf utility package, not an orchestration
//                                 dependency — W-L8e's import ban targets
//                                 internal/sandbox and internal/runtime.
//
//   profile create|list|info|delete   Same rationale as `config`:
//                                 filesystem ops on ~/.yoloai/profiles/.
//                                 CLI uses config/ and os.RemoveAll
//                                 directly; the cleanup-hint iteration
//                                 over descriptors (W-L5) already goes
//                                 through runtime/.
//
//   sandbox <name> vscode         Spawns external `code` binary with a
//                                 sandbox-targeted attach URL. CLI calls
//                                 client.Sandbox(name).Inspect for the
//                                 container info, then exec.Command
//                                 directly. The external-process launch
//                                 is CLI work, not orchestration.
//
//   mcp serve                     The MCP server CONSUMES Client (peer to
//                                 the CLI surface). The `serve` command
//                                 body is `mcpsrv.New(client).Run(ctx)`.
//
//   system tart                   Backend-scoped per Pattern B (W-L2).
//                                 Imports runtime/tart directly. The
//                                 W-L10 enforcement linter allowlists
//                                 system_tart.go.
//
//   x [extension]                 Extension dispatcher. Pure CLI shell
//                                 invocation; no sandbox state.

// =============================================================================
// Open questions for the reviewer  (all resolved)
// =============================================================================
//
// Q-A.  ListOptions vs separate filter methods?
//       **RESOLVED 2026-05-24:** One Client.List(ctx, ListOptions) method.
//       CLI fills the struct from flags; embedders set fields directly.
//       No convenience wrappers in V1.
//
// Q-B.  Error mapping. Which sentinels are stable contract?
//       **RESOLVED 2026-05-24:** Three-category exhaustive taxonomy.
//         (1) Five stable sentinels — ErrSandboxExists, ErrSandboxNotFound,
//             ErrUnappliedChanges, ErrNoChanges, ErrBackendUnavailable.
//             Promotion rule: add only when a real call site needs to
//             branch on it.
//         (2) *UsageError — caller did something refused before any work.
//             Re-exports internal/yoerrors. Detected with errors.As.
//         (3) *UnrecoverableError — Client started, hit something it
//             couldn't recover from, gave up. Carries UnrecoverableCode +
//             Message + Detail + wrapped Cause. Detected with errors.As.
//       No "other" — every Client error fits one of these three.
//
// Q-C.  Streaming-vs-buffered for Diff / Apply?
//       **RESOLVED 2026-05-24:** Defer. Workdir.Diff returns []*DiffResult,
//       Workdir.Apply returns *ApplyResult. Observed-workflow ceiling
//       (~50MB across 10 commits × 100 files × 500 lines) is well within
//       slice-result budget. Adding streaming methods later is non-breaking;
//       pre-empting the surface now carries dead weight.
//
// Q-D.  Run(Wait=true) vs separate Wait()?
//       **RESOLVED 2026-05-24:** Keep both. Client.Run(Wait=true) is the
//       one-call sync-embedder API with rich *Info return.
//       Sandbox.Wait() is the standalone block-until-terminal that the
//       new `yoloai wait <name>` CLI consumes and that embedders
//       operating on an existing sandbox call directly. Internal polling
//       logic is shared.
//
// Q-E.  Flight recorder shape?
//       **RESOLVED 2026-05-24:** Two complementary primitives.
//         Client.BugReport(ctx, name, opts) — one-shot snapshot at call
//             time.
//         Client.StartBugReportSession(ctx, opts) BugReportSession — session
//             that buffers events until Stop / Discard.
//       Reframing credit: the flight recorder is a session primitive
//       (turn on, do stuff, turn off), not a CLI-invocation wrapper.
//       CLI's --bugreport flag becomes a thin wrapper around the session.
//
// Q-F.  Setup interactive prompts?
//       **RESOLVED 2026-05-24:** Client never reads stdin. SystemClient.Setup
//       takes a fully-populated SetupOptions and acts. The wizard moves
//       to internal/cli/.
//       **Broader principle articulated by reviewer:** the Client is the
//       orchestration layer; all user interaction lives in the CLI. Same
//       separation applies (W-L8d) to Run / Apply / Destroy's dirty-repo
//       / replace / unapplied-changes prompts — they migrate out of
//       sandbox.Manager into the CLI, with Client returning a UsageError
//       or sentinel when a gate is closed.
//
// Q-G.  Admin sub-Client / structural grouping?
//       **RESOLVED 2026-05-24:** Shape B — name-bound handles + System
//       sub-client. Five conceptual groups become five structural homes:
//         Sandbox     (lifecycle + interaction)        client.Sandbox(ctx, name)
//         Workdir     (diff / apply / baseline)        sandbox.Workdir()
//         Files       (exchange dir)                   sandbox.Files()
//         Network     (allowlist)                      sandbox.Network()
//         System      (admin)                          client.System()
//       Cross-sandbox ops (List, Run, Clone, BugReport, StartBugReportSession)
//       stay directly on Client. ExchangeDir + Prompt collapse into
//       *Info fields (no separate methods). Drops artificial method
//       prefixes (FilesPut → Files().Put, AllowDomains → Network().Allow)
//       in favor of structural namespacing.
//
//       **Handle-validation policy** (refined 2026-05-25 after reviewer
//       pushback on cheap-handle cargo-culting):
//       Client.Sandbox(ctx, name) is STRICT — validates name resolution
//       at construction; returns ErrSandboxNotFound / *UsageError up
//       front. The handle then proves the sandbox existed at
//       construction. Considered the GCS lazy-handle pattern; rejected
//       because GCS's motivation (defer a network round-trip) doesn't
//       apply locally. Sub-handles (Workdir / Files / Network) are
//       cheap synchronous wrappers — pure namespace expansion off an
//       already-validated *Sandbox; no IO at the wrapper level.
//
// Q-H.  Zero-value semantics for required typed-enum fields?
//       **RESOLVED 2026-05-25:** Strict (Path B). Required enum fields
//       (RunOptions.Isolation, .OS, .Network, LogOptions.Format,
//       BugReportOptions.Mode) reject empty values with *UsageError.
//       Embedders MUST set every required field explicitly OR use the
//       Default*() convenience constructors:
//
//         opts := yoloai.DefaultRunOptions()  // pre-filled defaults
//         opts.Name = "myproj"; opts.Workdir = ...
//         client.Run(ctx, opts)
//
//       Filter / override fields (RestartOptions.IsolationOverride,
//       DoctorOptions.Isolation) keep empty-is-meaningful semantics —
//       they describe absence-of-override, not absence-of-choice.
//
//       Cost paid: ~75 test sites updated to set explicit values.
//       Future-proofing gained: tests and embedders explicitly pin to
//       the values they chose; default changes don't silently shift
//       behavior. The Default*() factories are the documented escape
//       hatch for embedders who want lazy ergonomics; tests must NOT
//       use them (per their godoc) so that intentional behavior
//       remains testable against any default change.
//       Three factories defined: DefaultRunOptions(),
//       DefaultLogOptions(), DefaultBugReportOptions().
//
// Q-I.  IsolationOverride: where does it live, and what transitions are
//       safe?
//       **RESOLVED 2026-05-25:** Move from StartOptions to
//       RestartOptions (where the user-facing `yoloai restart --isolation`
//       flag actually lives). Permitted transitions are constrained to
//       within-backend: target mode must be in the active backend's
//       SupportedIsolationModes set.
//
//         Allowed (clean container recreation, host-mounted state
//         preserved):
//           container ↔ container-privileged        (docker / podman)
//           container ↔ container-enhanced          (docker / podman)*
//           vm ↔ vm-enhanced                        (containerd)
//
//         Refused with *UsageError:
//           container* family ↔ vm* family — backend change, image
//             store change, network model change. Not a restart;
//             requires destroy + recreate. Error message names the
//             specific sequence (`yoloai apply <name> && yoloai
//             destroy <name> && yoloai new --isolation X <name> .`).
//
//         * The container ↔ container-enhanced transition is further
//           refused when :overlay directories hold uncommitted state
//           (gVisor doesn't support in-container overlayfs; the upper
//           layer would be lost). RestartOptions.Force=true overrides
//           after the user acknowledges.
//
//       The within-backend rule is checked against
//       BackendDescriptor.SupportedIsolationModes — already exists; no
//       new metadata needed.
//
//       Reviewer's framing on the underlying use case: this exists for
//       the realistic "oops I need privileged for this" situation
//       (container → container-privileged). The other realistic case
//       is container ↔ container-enhanced for ad-hoc isolation
//       upgrades. Cross-backend transitions were always
//       silently-destructive; refusing them is honesty about the
//       blast radius rather than a feature loss.
//
// Q-J.  "Force" naming audit.
//       **RESOLVED 2026-05-25:** "Force" is a CLI-UX convenience name
//       that doesn't carry its meaning into API code. Five `Force bool`
//       fields renamed to be concern-specific:
//
//         RestartOptions.Force      → AcceptOverlayDataLoss
//         DestroyOptions.Force      → SkipApplyCheck
//         PutOptions.Force          → Overwrite
//         GetOptions.Force          → Overwrite
//         BuildOptions.Force        → Rebuild
//
//       Codified principle: API boolean fields are named for the
//       specific effect, not for the user-facing flag. "Force"
//       remains an acceptable CLI flag name (familiar Unix idiom),
//       but the API never has a field literally called Force. Future
//       overrides each get their own specific name; no field gets to
//       mean two things.
//
//       Reviewer's framing: prevents the at-risk Force fields
//       (Restart, Destroy) from silently accreting concerns as new
//       safety checks are added — under the old name, a future
//       "permit cross-backend transition" or "ignore active agent"
//       override would naturally land in the same Force field; with
//       specific names, growth is explicit (a new field) rather than
//       implicit (broadening an existing field's meaning).
//
// Q-K.  Full name audit — fields whose names don't carry the meaning.
//       **RESOLVED 2026-05-25:** Sweep across the file for unclear
//       names. Renames applied:
//
//         Group 1 — "Yes" → concern-specific (same shape as Q-J):
//           RunOptions.Yes        → SkipDirtyRepoCheck
//           ApplyOptions.Yes      → DROPPED ENTIRELY (see follow-up
//                                   note below)
//
//         Group 2 — vague nouns made specific:
//           RunOptions.Runtimes        → SimulatorRuntimes
//           ListOptions.Changes        → OnlyWithChanges
//           DiffResult.IsBase          → IsWorkdir
//           ApplyResult.Per            → PerDir
//           ApplyResult.Skipped        → SkippedDirs
//           BuildOptions.All           → AllBackends
//           PruneOptions.Cache         → PruneCache
//           DoctorReport.IsBaseMode    → IsDefaultMode
//           DoctorReport.MissingCaps   → MissingCapabilities
//
//         Group 3 — typed MountMode enum:
//           Defined MountMode { MountCopy, MountOverlay, MountRW }.
//           DirSpec.Mode and DiffResult.Mode both use it.
//
//         Group 4 — build metadata nested struct:
//           SystemInfo.Version / Commit / Date → SystemInfo.Build
//             (BuildInfo{Version, Commit, Date}).
//
//         Group 5 — disambiguate Prompt overloading:
//           StartOptions.Prompt   → NewPrompt (distinguishes from
//                                   RunOptions.Prompt = original prompt)
//           ResetOptions.Prompt   → NewPrompt
//
//         Group 6 — explicit verbs:
//           ResetOptions.Restart       → RestartContainer
//           ApplyOptions.WithTags      → IncludeTags
//
//       Principle codified: API field names answer "what does setting
//       this to true do?" with a specific verb or fact, not a CLI
//       flag's UX shorthand. The Default*() factories and the CLI
//       parser absorb any verbosity hit at the call sites.
//
//       Later refinement (2026-05-25 follow-up):
//         ApplyOptions.IncludeWIP    → IncludeUncommitted
//       "WIP" is informal jargon; "uncommitted" matches git's own
//       terminology (uncommitted = staged + unstaged + untracked).
//       Same rename applied to mentions in Info.HasChanges and
//       SkipApplyCheck / ErrNoChanges doc-comments.
//
//       Later refinement (2026-05-25 follow-up #2):
//         ApplyOptions.SkipApplyConfirmation → DROPPED
//       Audit of the existing CLI revealed --yes on Apply only gates
//       sandbox.Confirm() stdin prompts in apply_overlay /
//       apply_selective / apply_squash — there is NO state-bearing
//       refusal inside Manager.Apply that the field would bypass.
//       Per Q-F (Client never prompts; CLI is the UI layer), pure-UX
//       prompts belong in the CLI, not on the API. Embedders just
//       call Apply; if they want to confirm with their user first,
//       that's their concern.
//
//       Asymmetry with RunOptions.SkipDirtyRepoCheck is intentional:
//       the dirty-repo case IS a real state-bearing refusal (host
//       workdir has uncommitted changes — observable, Client-side).
//       Per Q-F, Client.Run returns *UsageError; CLI catches and
//       prompts; retries with SkipDirtyRepoCheck=true if confirmed.
//       Apply has no such state condition — the "confirmation" is
//       pure "are you sure?" UX with no underlying refusal.
//
//       Later refinement (2026-05-25 follow-up #3):
//         Network.Deny → Network.Remove
//       The underlying model is allowlist-only — no separate denylist
//       exists. "Deny" named the policy EFFECT (the domain becomes
//       denied); "Remove" names the actual OPERATION (remove from
//       allowlist). Aligns with the "API names the specific effect"
//       principle from Q-J / Q-K.
//
//       The corresponding CLI breaking change lands in W-L8b/d when
//       the implementation flips: `yoloai sandbox <name> deny` →
//       `yoloai sandbox <name> remove`. Add to docs/BREAKING-CHANGES.md
//       when the implementation lands.
