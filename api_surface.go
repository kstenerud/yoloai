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
// **Reviewer audit trail.** Twenty-five W-L8a design questions
// resolved (Q-A through Q-Y) across two critique rounds. Resolutions
// live in the "Open questions" section at the tail with
// **RESOLVED <date>** markers; later refinements to earlier
// resolutions are recorded as follow-up entries in the relevant
// Q-block.
//
// **Threading model.** Methods on *Client, *Sandbox, the sub-handles
// (*Workdir, *Files, *Network), and *SystemClient are synchronous and
// block the calling goroutine until the operation completes. The
// concurrency boundary sits at the call site, not inside the library:
//
//   - Use `context.Context` for cancellation and deadlines.
//   - Use `go client.Op(...)` if you want the operation to run
//     concurrently with other work in your program.
//   - `OnProgress` callbacks are invoked synchronously from the
//     calling goroutine. They must not block — long work in a
//     progress callback stalls the operation that produced it.
//
// This matches the dominant Go idiom (docker, containerd, aws-sdk-go-v2,
// go-git, go-getter, and the standard library's io.Copy / http.Client.Do
// / net.Dial / sql.DB.Query). Channel-returning APIs would leak goroutine
// lifecycle across the API boundary; embedders who want pub/sub
// semantics build that on top with their own goroutine + channel. See
// Q-N for the research.

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

// ApplyMode selects how Apply lands changes.
//
// IMPLEMENTATION DIVERGENCE (D26/D27, 2026-05-28). This sketch had a zero-value
// "no override = default workdir apply" with named overrides (no named default
// "because that would just be the absence of a choice"). That's exactly the
// movable-default footgun §4 rejects — and it bit us (a default flip silently
// changed apply behavior). The landed design makes Mode **required**: the zero
// value is a *UsageError, and the two real modes are named explicitly —
// ApplyModeCommits (replay the commit series) and ApplyModeNoCommit (net diff,
// unstaged; formerly "squash"). The CLI (policy) picks the mode for the user.
//
// The sketch's earlier "ApplyExport" mode is GONE (4e, §12). Export doesn't land
// changes — folding it into Apply gave us "an Apply mode that doesn't apply",
// straining the required-Mode contract (which is about *how to land*, not
// *whether to land*). It is its own verb: Workdir().Export (see below).
type ApplyMode string

const (
	ApplyModeCommits  ApplyMode = "commits"   // replay the commit series (git format-patch → git am)
	ApplyModeNoCommit ApplyMode = "no-commit" // flatten to one unstaged net diff in the working tree
)

// MountMode selects how a directory is exposed inside the sandbox.
// Required where a directory is declared; empty rejected with
// *UsageError.
//
// **Per-position restrictions (Q-U):**
//   - Workdir (RunOptions.Workdir): MountCopy, MountOverlay, or MountRW.
//   - Auxiliary dirs (RunOptions.AuxDirs): MountRW or MountRO only.
//     MountCopy and MountOverlay on aux dirs are rejected with
//     *UsageError.
//
// The diff/apply workflow is workdir-only; auxiliary dirs are either
// live-edit (:rw) or read-only reference (:ro).
type MountMode string

const (
	// MountCopy makes a fresh copy of the directory at sandbox-create
	// time. The agent's changes are isolated; review with diff and
	// land with apply. Workdir-only after Q-U.
	MountCopy MountMode = "copy"

	// MountOverlay mounts the directory via Linux overlayfs inside the
	// sandbox. Instant setup; changes accumulate in an upper layer.
	// Diff/apply work for the changes. Requires CAP_SYS_ADMIN and a
	// container backend that supports overlayfs. Workdir-only after Q-U.
	MountOverlay MountMode = "overlay"

	// MountRW bind-mounts the directory read-write into the sandbox.
	// Changes are live on the host; no diff/apply step. Allowed for
	// workdir and aux dirs.
	MountRW MountMode = "rw"

	// MountRO bind-mounts the directory read-only into the sandbox.
	// Aux-dir reference material (libraries, docs, etc.) the agent
	// can read but not modify. Aux-only.
	MountRO MountMode = "ro"
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

// LogLevel filters structured log entries by minimum severity (entries
// at this level and above are emitted). Empty means "no level filter."
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
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

// BackendName names a runtime backend. Open-set typed string — the
// constants document the shipped backends; future backends register
// their own name (the runtime registry is the source of truth).
type BackendName string

const (
	BackendDocker     BackendName = "docker"
	BackendPodman     BackendName = "podman"
	BackendTart       BackendName = "tart"
	BackendSeatbelt   BackendName = "seatbelt"
	BackendContainerd BackendName = "containerd"
)

// AgentName names a coding agent. Open-set typed string — the constants
// document the shipped agents; user-defined or future agents add their
// own. Empty in Options means "use the configured default."
type AgentName string

const (
	AgentClaude   AgentName = "claude"
	AgentCodex    AgentName = "codex"
	AgentGemini   AgentName = "gemini"
	AgentOpenCode AgentName = "opencode"
	AgentAider    AgentName = "aider"
	AgentTest     AgentName = "test" // dev/test helper agent
)

// LogSource names a structured-log stream. Closed set — adding a new
// source requires both a constant here and a producer in the
// implementation.
type LogSource string

const (
	LogSourceCLI     LogSource = "cli"
	LogSourceSandbox LogSource = "sandbox"
	LogSourceMonitor LogSource = "monitor"
	LogSourceHooks   LogSource = "hooks"
)

// MountSpec describes a host-to-container bind mount declared in
// RunOptions.Mounts. Replaces the prior []string of "host:container[:ro]"
// tokens.
//
// Field names carry the "Path" suffix in symmetry with PortMapping's
// "Port" suffix below. Go doesn't surface types at the call site —
// `for _, m := range mounts { ... m.HostPath ... }` is self-documenting
// where bare `m.Host` would leave a reader guessing (hostname? IP?
// port?). Direction in the prefix (Host vs Container), kind in the
// suffix (Path vs Port).
type MountSpec struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// PortMapping describes a host-to-container port mapping declared in
// RunOptions.Ports. Replaces the prior []string of "8080:8080[/tcp]"
// tokens.
//
// Field names carry the "Port" suffix in symmetry with MountSpec's
// "Path" suffix above. Same rationale: an int field named "Host"
// reads ambiguously at every call site; "HostPort" is self-documenting.
// Direction in the prefix, kind in the suffix.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // empty = "tcp"
}

// PruneItemKind categorises one removed item in PruneResult. Open-set
// typed string — additional categories can be added without breaking
// embedders.
type PruneItemKind string

const (
	PruneItemContainer PruneItemKind = "container" // a sandbox container
	PruneItemImage     PruneItemKind = "image"     // a backend image / VM image
	PruneItemTempDir   PruneItemKind = "tempdir"   // a stale yoloai temp directory
	PruneItemVolume    PruneItemKind = "volume"    // a backend-side volume
)

// PruneItem describes one removed item in PruneResult. Replaces the
// prior []string of opaque identifiers.
type PruneItem struct {
	Kind  PruneItemKind
	Name  string // identifier (container name, image ref, path, etc.)
	Bytes int64  // bytes reclaimed by removing this item
}

// SimulatorPlatform names the Apple simulator platform family.
// Open-set typed string — named constants document the known platforms;
// future platforms add their own constants without breaking embedders.
type SimulatorPlatform string

const (
	SimulatorIOS      SimulatorPlatform = "ios"
	SimulatorTVOS     SimulatorPlatform = "tvos"
	SimulatorWatchOS  SimulatorPlatform = "watchos"
	SimulatorVisionOS SimulatorPlatform = "visionos"
)

// SimulatorRuntime identifies one Apple simulator runtime to pre-stage
// inside a tart VM. Used by RunOptions.SimulatorRuntimes. Backend-
// specific (tart-only); RunOptions carries it inline rather than in a
// per-backend sub-struct while it's the only such option (Q-B4 audit;
// revisit when a second backend-specific option appears).
type SimulatorRuntime struct {
	Platform SimulatorPlatform
	Version  string // optional; empty = latest available
}

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
	// DataDir is the root yoloai data directory; all per-Client state
	// lives below it (sandboxes/, profiles/, config.yaml, state.yaml,
	// credentials/). REQUIRED — empty is rejected with *UsageError at
	// Client construction.
	//
	// No implicit default. yoloai library code never reads $HOME or
	// any other ambient process state. The CLI fills DataDir from
	// $HOME/.yoloai/ at startup — that one os.UserHomeDir() call site
	// is the entire allowlist for the home-dir lookup. HTTP servers,
	// daemons, multi-tenant processes, and tests must pass an
	// explicit path. See development-principles.md §12 (No ambient
	// configuration) for the rationale and the linter scope.
	DataDir string

	Backend BackendName  // explicit backend; empty = read from DataDir/config.yaml, auto-detect
	Logger  *slog.Logger // structured-event sink; nil = slog.Default()
}

// New is removed — every caller must supply DataDir explicitly via
// NewWithOptions. The zero-argument constructor was a convenience that
// would have to silently default DataDir (the exact anti-pattern §12
// rules out). Tests use NewWithOptions(ctx, Options{DataDir: t.TempDir()}).

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
	Statuses        []Status    // filter to these statuses; empty = all
	Agents          []AgentName // filter by agent name; empty = all
	Profiles        []string    // filter by profile name; nil = all; []string{""} = only unprofiled sandboxes
	OnlyWithChanges bool        // filter to sandboxes with unapplied changes
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

	Agent   AgentName // empty = read from DataDir/config.yaml or profile
	Model   string    // agent-specific model id or alias
	Profile string    // profile name
	Prompt  string    // initial prompt; empty = interactive

	Isolation IsolationMode // REQUIRED; empty rejected. Use DefaultRunOptions() for the current default.
	OS        HostOS        // REQUIRED; empty rejected
	Network   NetworkMode   // REQUIRED; empty rejected

	AllowDomains []string // initial allowlist; only meaningful with NetworkIsolated

	Env       map[string]string
	Mounts    []MountSpec
	Ports     []PortMapping
	Secrets   []string
	BuildArgs map[string]string

	Replace            bool // destroy any existing sandbox with the same name first
	SkipDirtyRepoCheck bool // proceed despite uncommitted changes in the host workdir
	NoStart            bool // create state but don't start the container yet

	SimulatorRuntimes []SimulatorRuntime // tart-only: pre-built Apple simulator runtime bases to stage in the VM

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
//
// IMPLEMENTATION DIVERGENCE (F2 re-rooting, 2026-05-28). This section is the
// aspiration; the landed handle reconciled it to the facts (see
// feedback "api_surface aspirational" / working-notes D-entries):
//   - Status() — DEFERRED. There is no cheap status-only path in the manager
//     (Inspect does the git forks); a "cheap polling companion" can't be
//     delivered honestly yet, so it wasn't added.
//   - Restart — SIMPLIFIED. Implemented as stop+start taking StartOptions
//     (isolation override already rides StartOptions.Isolation). The elaborate
//     RestartOptions isolation-transition policy below (cross-backend refusal,
//     gVisor+overlay AcceptOverlayDataLoss) has no internal basis and is
//     deferred to its own finding.
//   - Destroy — DestroyOptions{Force} (not SkipApplyCheck); Force false returns
//     a typed *ActiveWorkError. NeedsConfirmation is NOT deleted outright: the
//     batch CLI destroy needs a side-effect-free pre-check, exposed as
//     Sandbox.HasActiveWork(ctx) (bool, reason).
//   - Option types — StartOptions/Info/Status are re-exported aliases;
//     ResetOptions/DestroyOptions/ExecOptions are hand-written public structs.
//     ResetOptions drops Name (the handle supplies it); Restart→RestartContainer.
//   - Exec — Exec(ExecOptions{Command, PTY}, IOStreams); PTY=false folds the old
//     StdioExec (pipes via IOStreams.In/Out/Err). No ExecResult yet (returns error).

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
//
// **Concurrency.** Safe for concurrent use by multiple goroutines (and
// across process boundaries — two `yoloai` invocations cooperate via
// the same file locks). Write methods (Start, Stop, Restart, Destroy,
// Reset, Apply, AdvanceBaseline, SetBaseline, Files.Put, Files.Rm,
// Network.Allow, Network.Remove) acquire a per-sandbox file lock at
// method entry; concurrent writes serialize. Read methods (Inspect,
// Status, Diff, Logs, BaselineLog, Files.Ls, Files.Get, Network.Allowed)
// don't take the lock and run in parallel.
//
// Reads concurrent with writes may observe intermediate state — e.g.,
// a Diff running during an Apply may see the working tree mid-stash-
// pop. State doesn't corrupt and methods don't crash, but reads
// aren't transactional. Embedders that need transactional consistency
// should serialize reads against writes themselves.
type Sandbox struct{}

// Name returns the bound sandbox name. Always valid: Client.Sandbox
// validated it at construction.
func (*Sandbox) Name() string { panic("design-only") }

// --- lifecycle + introspection ---

// Inspect returns the full sandbox snapshot — Meta (creation-time facts),
// lifecycle status, agent status, HasChanges, exchange-dir path, and
// original prompt. Cost is dominated by one `git` invocation per
// :copy/:overlay directory (to compute HasChanges); plus one backend
// RPC for status detection and one meta.json read. Use Status for the
// polling case if you only need the lifecycle enum — it skips both the
// git forks and the metadata read.
func (*Sandbox) Inspect(ctx context.Context) (*Info, error) { panic("design-only") }

// Status returns just the lifecycle enum. One backend RPC + one status
// file read; the cheap polling companion to Inspect.
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

func (*Sandbox) Stop(ctx context.Context) error { panic("design-only") }

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
//	    be lost). AcceptOverlayDataLoss=true overrides after the user
//	    acknowledges.
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
// StatusFailed, StatusStopped) or ctx is cancelled. Returns the terminal
// Status and a wrapped error on cancel / timeout / inspect failure.
//
// If the sandbox is already in a terminal status at call time, Wait
// returns immediately without polling.
//
// Status, not exit code: yoloai classifies agent outcomes into the
// Status enum at the agent-status.json layer; the raw process exit
// code from the agent itself isn't a portable success signal across
// agents (Claude, Codex, Gemini, OpenCode, Aider all use different
// conventions — some return non-zero for "no findings" or "warnings,"
// some return zero on clean shutdown after a crash). Use Status to
// branch programmatically. The CLI maps StatusDone → exit 0 and
// anything else → exit 1, but that mapping is the CLI's contract,
// not the Client's.
//
// The raw agent exit code (when the agent reported one) is available
// via Info.AgentExitCode for debug logging and bug reports — it must
// NOT be used as a success signal.
func (*Sandbox) Wait(ctx context.Context, opts WaitOptions) (Status, error) {
	panic("design-only")
}

// --- streaming / interactive ---

// Attach blocks until the user detaches or the agent exits. Requires
// IOStreams.TTY=true; non-TTY attach returns a *UsageError.
func (*Sandbox) Attach(ctx context.Context, io IOStreams) error {
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
	Format   LogFormat   // REQUIRED; empty rejected. Use DefaultLogOptions() for LogStructured.
	Sources  []LogSource // for LogStructured / LogStructuredRaw; empty = all known sources
	MinLevel LogLevel
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

// Unlock force-clears the per-sandbox file lock. Recovery API for the
// rare cases where the lock genuinely is stale (sudden power loss,
// filesystem went read-only / offline mid-operation, kernel wedge).
//
// Refuses with *UsageError when the holder PID is alive — the right
// recovery is to wait or kill the holder, not to silently break
// another running operation's invariants.
//
// CLI: `yoloai sandbox <name> unlock`. See Q-Z for the rationale.
func (*Sandbox) Unlock(ctx context.Context) error { panic("design-only") }

// =============================================================================
// Workdir — diff / apply / baseline (sub-handle off Sandbox)
// =============================================================================

// Workdir scopes diff / apply / baseline operations on a single sandbox.
// Constructed via Sandbox.Workdir(); never errors. Inherits its parent
// *Sandbox's concurrency contract (see that type's docstring).
type Workdir struct{}

// DiffOptions configures Diff.
type DiffOptions struct {
	Ref      string   // single ref or "A..B" range; "" = full agent diff
	Paths    []string // pathspec filters; empty = all
	Stat     bool     // --stat
	NameOnly bool     // --name-only
}

// Diff returns the workdir's diff against its baseline as a single
// string. Empty string means no changes. Output form depends on
// DiffOptions (raw patch by default, stat summary when Stat=true,
// names only when NameOnly=true).
//
// Aux dirs are not diffed: :rw dirs have no diff (changes are live)
// and :copy/:overlay aux dirs are not supported (Q-U).
func (*Workdir) Diff(ctx context.Context, opts DiffOptions) (string, error) {
	panic("design-only")
}

// ApplyOptions configures Apply.
//
// No confirmation-skip field: Apply has no state-bearing refusal to
// acknowledge. The "are you sure you want to apply N commits?" prompt
// in today's CLI is pure UX with no underlying Client state; the CLI
// handles it before calling Apply. Embedders just call Apply; if they
// want to confirm with their user first, that's their concern.
type ApplyOptions struct {
	Mode               ApplyMode // REQUIRED — ApplyModeCommits or ApplyModeNoCommit; zero is a *UsageError
	Refs               []string  // specific commits or ranges (ApplyModeCommits only)
	Paths              []string  // pathspec filter
	IncludeUncommitted bool      // also apply the agent's uncommitted edits (staged + unstaged + untracked) as unstaged changes on the host; default is committed changes only
	IncludeTags        bool      // also transfer git tags created by the agent; invalid with ApplyModeNoCommit
	DryRun             bool      // preview without applying or advancing the baseline
}

// ApplyResult reports the outcome of a single Apply call. Workdir-only
// after Q-U — there are no per-dir slices because aux dirs don't
// participate in diff/apply.
type ApplyResult struct {
	Status ApplyStatus // see ApplyStatus constants
	Patch  string      // populated only when Status == ApplyStatusDryRun
	Err    error       // non-nil when an unexpected error occurred (runtime / IO failure; distinct from ApplyStatusConflict which is the normal "git refused" path)
}

// ApplyStatus is the typed outcome of an Apply call. Embedders switch
// on Status to know what happened.
type ApplyStatus string

const (
	// ApplyStatusApplied: clean apply. Commits landed on the host repo
	// (or unstaged diff was written for squash mode).
	ApplyStatusApplied ApplyStatus = "applied"

	// ApplyStatusEmpty: the workdir had no changes to apply.
	ApplyStatusEmpty ApplyStatus = "empty"

	// ApplyStatusConflict: git am (or the analogous overlay path)
	// failed to apply cleanly. The repo was rolled back to its
	// pre-apply state via `git am --abort`; any host-side stash was
	// popped back. The user needs to inspect the diff and either fix
	// it or skip this dir.
	ApplyStatusConflict ApplyStatus = "conflict"

	// ApplyStatusAppliedStashConflict: git am succeeded — the agent's
	// commits ARE on the host repo — but the subsequent `git stash
	// pop` (restoring the user's pre-apply uncommitted edits) hit a
	// conflict against the newly-applied commits. The user has the
	// commits PLUS unresolved merge markers in their working tree.
	ApplyStatusAppliedStashConflict ApplyStatus = "applied-stash-conflict"

	// ApplyStatusDryRun: DryRun=true; the Patch field holds what
	// would have been applied. Nothing changed on the host.
	ApplyStatusDryRun ApplyStatus = "dry-run"
)

func (*Workdir) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	panic("design-only")
}

// ExportOptions configures Export. Dir is required.
type ExportOptions struct {
	Dir                string   // destination directory for patch files (created if absent); required
	Refs               []string // specific commits or ranges (copy-mode only); empty = whole beyond-baseline range
	Paths              []string // pathspec filter
	IncludeUncommitted bool     // also write uncommitted.diff (copy-mode only)
}

// ExportResult reports what Export wrote.
type ExportResult struct {
	Dir                 string   // destination directory
	Files               []string // patch/diff files written (absolute paths)
	UncommittedExported bool     // uncommitted.diff was written
}

// Export writes the agent's changes as patch files under opts.Dir instead of
// applying them — the `apply --patches` flow. It is its own verb (NOT an Apply
// mode): export serializes without landing, so the required-Mode Apply contract
// doesn't apply. Resolves mount mode internally (copy → format-patch files +
// optional uncommitted.diff; overlay → upper-layer diffs, container must be
// running). Dir is required (*UsageError if empty); Refs on overlay is refused
// (*UsageError). Never advances the baseline.
func (*Workdir) Export(ctx context.Context, opts ExportOptions) (*ExportResult, error) {
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
// namespacing. Inherits its parent *Sandbox's concurrency contract.
//
// The host path to the exchange dir lives on *Info (returned by
// Sandbox.Inspect) as Info.HostExchangeDir — no separate FilesPath
// method.
//
// **Partial-completion contract.** Put / Rm / Network.Allow / Network.Remove
// process a list of items and return just `error`. They commit each
// item independently; on the first failure they return the error
// without rolling back the items already processed. Get is similar but
// returns `written []string` so the caller can tell exactly which
// destination files exist on disk after the call.
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
// return *UsageError. Inherits its parent *Sandbox's concurrency
// contract.
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
//   - Allowed()       — return the current allowlist with per-entry
//     provenance (AllowedDomain.Source)
//
// The allowlist returned by Allowed() is the merged set of (a) the
// agent's required defaults (agentDef.NetworkAllowlist —
// e.g. api.anthropic.com for Claude — without which the agent itself
// can't function) and (b) user additions made at create-time
// (RunOptions.AllowDomains) or at runtime (Network.Allow). The
// AllowedDomain.Source field discriminates (a) from (b). Create-time
// vs runtime user additions aren't distinguished today; the storage
// flattens them. Add a third Source constant if a use case ever
// requires that split.
type Network struct{}

// AllowedDomainSource categorises where an allowlist entry came from.
// Open-set typed string: future use cases (e.g. distinguishing
// create-time from runtime user additions) add new constants without
// breaking embedders.
type AllowedDomainSource string

const (
	// AllowedFromAgentRequirement: the domain is in the bound agent's
	// default allowlist — i.e. removing it would break the agent
	// itself (Claude → api.anthropic.com, Gemini → cloudcode-pa
	// endpoints, etc.).
	AllowedFromAgentRequirement AllowedDomainSource = "agent-requirement"

	// AllowedFromUser: the domain was added by the user, either at
	// create time (RunOptions.AllowDomains) or at runtime (Network.Allow).
	// Today's storage flattens these two; a future Source constant
	// can split them if needed.
	AllowedFromUser AllowedDomainSource = "user"
)

// AllowedDomain pairs a domain with its provenance in the merged
// allowlist. Returned by Network.Allowed.
type AllowedDomain struct {
	Domain string
	Source AllowedDomainSource
}

func (*Network) Allow(ctx context.Context, domains []string) error    { panic("design-only") }
func (*Network) Remove(ctx context.Context, domains []string) error   { panic("design-only") }
func (*Network) Allowed(ctx context.Context) ([]AllowedDomain, error) { panic("design-only") }

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
type BugReportSession struct{}

// Stop flushes the buffered events to a report file and returns the
// absolute path of the written report.
func (*BugReportSession) Stop() (path string, err error) { panic("design-only") }

// Discard throws away buffered events without writing a report.
func (*BugReportSession) Discard() { panic("design-only") }

// StartBugReportSession begins buffering runtime events. Embedders scope the
// session however they want (single risky call, fixed duration, program
// lifetime). The CLI's top-level `--bugreport` flag is a thin wrapper.
func (*Client) StartBugReportSession(ctx context.Context, opts BugReportOptions) *BugReportSession {
	panic("design-only")
}

// =============================================================================
// SystemClient — admin sub-client (off Client)
// =============================================================================

// SystemClient scopes `yoloai system …` operations. Constructed via
// Client.System(); never errors at construction.
//
// **Concurrency.** Safe for concurrent use by multiple goroutines. The
// read-only methods (Info, Backends, Backend, Agents, Agent, Doctor,
// DiskUsage, Check) run in parallel. Write methods (Build, Prune,
// Setup) acquire global locks (per-profile for Build; cross-backend
// for Prune; on the config file for Setup) and serialize concurrent
// callers.
type SystemClient struct{}

// BackendInfo bundles a BackendDescriptor with its current Probe verdict.
type BackendInfo struct {
	Name              BackendName
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
	Name             AgentName
	Description      string
	PromptMode       PromptMode
	APIKeyEnvVars    []string
	ModelAliases     map[string]string
	NetworkAllowlist []string // domains the agent requires for normal operation under NetworkIsolated; surfaced by Network.Allowed as AllowedFromAgentRequirement entries
}

// BuildInfo carries metadata about the yoloai binary itself (from
// compile-time -ldflags), grouped together since these three fields
// only make sense as a set.
type BuildInfo struct {
	Version string    // semver tag or "dev" (yoloai version string)
	Commit  string    // git short SHA the binary was built from
	Date    time.Time // build timestamp
}

// SystemInfo bundles paths + build metadata + per-backend info.
// Embedders that need total disk usage call SystemClient.DiskUsage()
// for the structured shape; SystemInfo doesn't carry a rendered total.
type SystemInfo struct {
	Build             BuildInfo
	ConfigPath        string
	ProfileConfigPath string
	DataDir           string
	SandboxesDir      string
	Backends          []BackendInfo
}

func (*SystemClient) Info(ctx context.Context) (*SystemInfo, error)       { panic("design-only") }
func (*SystemClient) Backends(ctx context.Context) ([]BackendInfo, error) { panic("design-only") }
func (*SystemClient) Backend(ctx context.Context, name BackendName) (*BackendInfo, error) {
	panic("design-only")
}
func (*SystemClient) Agents() []AgentInfo                      { panic("design-only") }
func (*SystemClient) Agent(name AgentName) (*AgentInfo, error) { panic("design-only") }

// ProfileInfo is the public face of a yoloai profile. Returned by
// SystemClient.Profile / Profiles.
type ProfileInfo struct {
	Name          string
	Description   string // optional human-readable description (from profile config.yaml)
	HasDockerfile bool   // true if the profile carries a custom Dockerfile
	IsBase        bool   // true for the reserved "base" profile
}

// CreateProfileOptions configures CreateProfile. Either supply
// Dockerfile/Config inline, OR set From to copy from an existing
// profile as a template. From is mutually exclusive with
// Dockerfile/Config.
type CreateProfileOptions struct {
	Name       string // required; profile name (validated; "base" reserved)
	Dockerfile []byte // optional Dockerfile content
	Config     []byte // optional config.yaml content
	From       string // optional template profile name; mutually exclusive with Dockerfile/Config
}

// Profiles returns every profile in DataDir/profiles/ plus the
// reserved "base" entry. CLI: `yoloai profile list`.
func (*SystemClient) Profiles(ctx context.Context) ([]ProfileInfo, error) { panic("design-only") }

// Profile returns one profile's info, or ErrProfileNotFound if the
// name doesn't resolve. CLI: `yoloai profile info <name>`.
func (*SystemClient) Profile(ctx context.Context, name string) (*ProfileInfo, error) {
	panic("design-only")
}

// CreateProfile creates a new profile directory under
// DataDir/profiles/<name>/. Returns *UsageError if the name already
// exists or if From + (Dockerfile or Config) are both set. CLI:
// `yoloai profile create <name>`.
func (*SystemClient) CreateProfile(ctx context.Context, opts CreateProfileOptions) error {
	panic("design-only")
}

// DeleteProfile removes a profile directory. Refuses to delete the
// reserved "base" profile. CLI: `yoloai profile delete <name>`.
func (*SystemClient) DeleteProfile(ctx context.Context, name string) error { panic("design-only") }

// ConfigEntry is one key-value pair from the yoloai config file.
type ConfigEntry struct {
	Key   string // dotted key path (e.g. "agent", "tmux_conf")
	Value string // string representation of the value
	Scope ConfigScope
}

// ConfigScope identifies which config file an entry lives in.
type ConfigScope string

const (
	ConfigScopeGlobal  ConfigScope = "global"  // DataDir/config.yaml
	ConfigScopeProfile ConfigScope = "profile" // DataDir/profiles/<active>/config.yaml
)

// Config returns every config entry across the global and active
// profile config files. CLI: `yoloai config list`.
func (*SystemClient) Config(ctx context.Context) ([]ConfigEntry, error) { panic("design-only") }

// GetConfig returns the value for one config key, or *UsageError if
// the key is unknown. CLI: `yoloai config get <key>`.
func (*SystemClient) GetConfig(ctx context.Context, key string) (*ConfigEntry, error) {
	panic("design-only")
}

// SetConfig sets a config key to a value, writing it to the file
// determined by IsGlobalKey(key). CLI: `yoloai config set <key> <value>`.
func (*SystemClient) SetConfig(ctx context.Context, key, value string) error {
	panic("design-only")
}

// ResetConfig removes a key from its config file (returning behavior
// to defaults). CLI: `yoloai config reset <key>`.
func (*SystemClient) ResetConfig(ctx context.Context, key string) error { panic("design-only") }

// BuildOptions configures Build.
type BuildOptions struct {
	Profile     string
	Backend     BackendName // empty = current default; ignored when AllBackends == true
	AllBackends bool        // build across every available backend; mutually exclusive with a non-empty Backend
	Rebuild     bool        // build even when the checksum says the existing image is current
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
	Detail string // backend-specific extra info (e.g. cache vs. image breakdown); free-form text
	Err    error  // non-nil when the backend's disk-usage query failed; Bytes is 0 in that case
}

func (*SystemClient) DiskUsage(ctx context.Context) (*DiskUsage, error) { panic("design-only") }

// DoctorOptions configures Doctor.
type DoctorOptions struct {
	BackendFilter   BackendName
	IsolationFilter IsolationMode
	OnProgress      func(msg string) // human-readable progress (per-check); nil = silent
}

// DoctorReport is the verdict for one backend+mode pair. Mode is the
// concrete isolation mode being checked; the "is this the backend's
// default mode?" question is derivable by comparing Mode to
// BackendInfo's default — no separate IsDefaultMode flag needed.
type DoctorReport struct {
	Backend             BackendName
	Mode                IsolationMode
	Availability        Availability
	InitErr             error
	MissingCapabilities []string
}

func (*SystemClient) Doctor(ctx context.Context, opts DoctorOptions) ([]DoctorReport, error) {
	panic("design-only")
}

// PruneOptions configures Prune. Always operates across all installed
// backends — per-backend pruning was dropped (Q-L) as having no real-world
// use case.
type PruneOptions struct {
	DryRun           bool
	IncludeBaseImage bool             // also remove base image (forces rebuild on next sandbox)
	OnProgress       func(msg string) // human-readable progress (per-item); nil = silent
}

type PruneResult struct {
	RemovedItems []PruneItem
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
	TmuxConf TmuxConfMode // required
	Backend  BackendName  // initial default container_backend; empty = no default set
	Agent    AgentName    // initial default agent name; empty = no default set
}

func (*SystemClient) Setup(ctx context.Context, opts SetupOptions) error { panic("design-only") }

// =============================================================================
// Re-exported types
// =============================================================================
//
// W-L8b lands real aliases or re-exports. Listed here so the design
// type-checks without pulling internal packages.

// Status re-exports sandbox.Status — the lifecycle classification
// yoloai assigns to a sandbox based on container state + agent
// observations. The full constant set ships with the implementation;
// named here for the design checkpoint.
//
// **String-value overlap with AgentStatus.** StatusActive, StatusIdle,
// and StatusDone share string values ("active", "idle", "done") with
// AgentStatusActive, AgentStatusIdle, AgentStatusDone respectively.
// The two enums are distinct types — Status describes the *combined*
// sandbox state (container + agent), AgentStatus describes the *agent
// process* state alone — and embedders should compare each value
// against its own type, not cross-compare via string. (Go's type
// system enforces this; the overlap is only visible when serialised.)
type Status string

const (
	StatusActive      Status = "active"      // container running, agent actively working
	StatusIdle        Status = "idle"        // container running, agent alive, awaiting input
	StatusDone        Status = "done"        // container running, agent exited cleanly (exit 0)
	StatusFailed      Status = "failed"      // container running, agent exited with error (non-zero)
	StatusStopped     Status = "stopped"     // container stopped
	StatusRemoved     Status = "removed"     // container removed but sandbox dir exists
	StatusBroken      Status = "broken"      // sandbox dir exists but meta.json missing/invalid
	StatusUnavailable Status = "unavailable" // backend not running (container state unknown)
)

// AgentStatus is the agent process's self-reported state, separate from
// the lifecycle Status (which describes the combined sandbox state).
// Re-exports sandbox.AgentStatus. See Status's docstring for the
// shared-string-values note.
type AgentStatus string

const (
	AgentStatusUnknown AgentStatus = ""       // status not yet determined
	AgentStatusActive  AgentStatus = "active" // agent is actively working
	AgentStatusIdle    AgentStatus = "idle"   // agent is idle, awaiting input
	AgentStatusDone    AgentStatus = "done"   // agent has completed its task
)

// SandboxMeta is the on-disk sandbox metadata captured at creation time.
// In the W-L8b implementation this is a Go type alias:
//
//	type SandboxMeta = store.Meta
//
// pointing at sandbox/store.Meta (or internal/sandbox/store.Meta after
// W-L12 — type aliases work across internal/ boundaries because the
// public yoloai package can import from internal/ of the same module).
// The full field list (~25 fields covering identity, backend, agent/
// model, workdir + aux dirs, network mode, resource limits, isolation,
// etc.) lives in sandbox/store/meta.go with stable JSON tags.
//
// Sketched here as a placeholder struct for the design checkpoint; the
// real definition is the alias.
type SandboxMeta struct { /* alias to sandbox/store.Meta — see meta.go */
}

// Info is the snapshot returned by Sandbox.Inspect: creation-time facts
// (Meta) plus live state computed at inspection time. Fields that also
// live on Meta (Backend, ImageRef, Isolation, Workdir.BaselineSHA) are
// not duplicated at the top level — read them via info.Meta.* (Q-O).
type Info struct {
	// Creation-time facts (read from meta.json).
	Meta SandboxMeta

	// Live state — computed at Inspect time, not stored in Meta.
	Status        Status
	AgentStatus   AgentStatus
	HasChanges    bool // workdir has unapplied agent commits or uncommitted edits
	AgentExitCode *int // raw process exit code reported by the agent (nil = not reported). For debug / bug-report logging only; NOT a portable success signal across agents — branch on Status instead.

	// Convenience fields not present in Meta.
	HostExchangeDir string
	OriginalPrompt  string
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
	// unapplied changes and DestroyOptions.SkipApplyCheck is false.
	ErrUnappliedChanges error

	// ErrBackendUnavailable is returned by NewWithOptions when the
	// requested or auto-selected backend is not usable on this host.
	ErrBackendUnavailable error

	// ErrProfileNotFound is returned by SystemClient.Profile when the
	// named profile does not exist in DataDir/profiles/. Also returned
	// from Run / Clone if RunOptions.Profile names a missing profile.
	ErrProfileNotFound error
)

// SandboxLockedError indicates a write method on a sandbox couldn't
// acquire the per-sandbox file lock within the brief retry window
// because another holder is currently using it. Returned as
// (nil, *SandboxLockedError) — use errors.As to match.
//
// Holder identification: the lock-acquire path writes the acquiring
// process's PID into the lock file content (separate from the flock
// semantic — the file's bytes are informational, the lock itself is
// the flock(2) advisory lock). Contention readers parse the PID and
// classify the holder.
//
// HolderAlive=true means another yoloai-shaped process is genuinely
// using the sandbox; the right user action is "wait, or cancel the
// other operation." HolderAlive=false means the lock is stale (rare
// — flock auto-releases on graceful exit AND on crash; staleness
// requires sudden power loss, the filesystem going read-only or
// offline mid-operation, or a kernel-level wedge). The right user
// action is "run `yoloai sandbox <name> unlock` to clear it."
type SandboxLockedError struct {
	Name        string // sandbox name
	HolderPID   int    // PID recorded in the lock file; 0 if unreadable
	HolderAlive bool   // true if HolderPID names a live process; false if dead or unknown
	LockPath    string // host path to the lock file; included so the error message can name it
}

func (*SandboxLockedError) Error() string { panic("design-only") }

// UsageError indicates the caller passed something the Client refused
// before doing any work. CLI maps to exit code 2 with a "Run 'yoloai
// <cmd> -h' for help" hint. Re-exports internal/yoerrors.UsageError.
type UsageError struct {
	Msg  string
	Hint string
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
//   config get|set|reset          Promoted to SystemClient methods
//                                 (Q-W). Operates on DataDir/config.yaml.
//                                 No longer a CLI-only exception now that
//                                 DataDir is an explicit Client parameter
//                                 — HTTP/MCP embedders need this too.
//
//   profile create|list|info|delete   Promoted to SystemClient methods
//                                 (Q-W). Operates on DataDir/profiles/.
//                                 Same rationale as config — HTTP/MCP
//                                 embedders need to manipulate profiles
//                                 too. See SystemClient.Profiles /
//                                 CreateProfile / DeleteProfile.
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
//             ErrUnappliedChanges, ErrBackendUnavailable, ErrProfileNotFound.
//             (ErrNoChanges dropped in Q-P as redundant with ApplyResult.
//             ErrProfileNotFound added in Q-W when profile management
//             joined the API.) Promotion rule: add only when a real
//             call site needs to branch on it AND the state isn't
//             recoverable from a returned result.
//         (2) *UsageError — caller did something refused before any work.
//             Re-exports internal/yoerrors. Detected with errors.As.
//         (3) *UnrecoverableError — Client started, hit something it
//             couldn't recover from, gave up. Carries UnrecoverableCode +
//             Message + Detail + wrapped Cause. Detected with errors.As.
//       No "other" — every Client error fits one of these three.
//
// Q-C.  Streaming-vs-buffered for Diff / Apply?
//       **RESOLVED 2026-05-24:** Defer. Workdir.Diff returns
//       []*DiffResult, Workdir.Apply returns *ApplyResult. Observed-
//       workflow ceiling (~50MB across 10 commits × 100 files × 500
//       lines) is well within slice-result budget. Adding streaming
//       methods later is non-breaking; pre-empting the surface now
//       carries dead weight.
//
//       Later refinement (Q-U, 2026-05-25): with aux :copy/:overlay
//       removed, Workdir.Diff returns just `(string, error)` (one
//       workdir, one output) and Workdir.Apply returns the simplified
//       flat *ApplyResult. The Q-C "buffered is fine" verdict still
//       holds — the new shapes are even further inside the
//       slice-result budget.
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
//           layer would be lost). RestartOptions.AcceptOverlayDataLoss
//           = true overrides after the user acknowledges.
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
//
//       Later refinement (2026-05-25 follow-up #4):
//         "Name carries the meaning" applied at the field level.
//       Audit each commented field: is the comment doing the name's
//       job? If yes, rename; if no (invariant, side effect, zero-value
//       semantic, cross-reference), keep. Applied to:
//
//         Info.Prompt              → Info.OriginalPrompt
//         Info.ExchangeDir         → Info.HostExchangeDir
//         Info.BaselineSHA         (comment dropped; name self-describes)
//         LogOptions.MinLevel string → MinLevel LogLevel (new typed enum)
//         DoctorOptions.Backend    → DoctorOptions.BackendFilter
//         DoctorOptions.Isolation  → DoctorOptions.IsolationFilter
//         UsageError.Hint          (comment dropped; name self-describes)
//
//       Principle codified in docs/dev/standards/GO.md's "Clarity over
//       brevity" section with the keep-vs-delete heuristic for field
//       comments.
//
// Q-L.  PruneOptions surface — how many knobs does prune really need?
//
//       **RESOLVED 2026-05-25:** Project owner identified that the
//       only modes that matter in practice are (a) "prune everything
//       except current base image" (common) and (b) "prune everything
//       including base image" (rare). Per-backend pruning was never
//       wanted; even the hypothetical "wipe docker but not tart" case
//       isn't worth the surface area because rebuilding all bases
//       isn't a big deal.
//
//       Old shape (5 fields):
//         Backend       string  // "" = all
//         DryRun        bool
//         PruneCache    bool    // also prune backend caches
//         IncludeStale  bool
//         OnProgress    func(string)
//
//       New shape (3 fields):
//         DryRun           bool
//         IncludeBaseImage bool             // also remove base image
//         OnProgress       func(string)
//
//       Reasoning per field:
//         Backend          → DROPPED. No use case. Always all backends.
//         PruneCache       → RENAMED IncludeBaseImage. Names the user-
//                            visible decision ("preserve my base or not")
//                            instead of the implementation detail ("which
//                            caches"). Retained comment documents the
//                            side effect — keep-vs-delete heuristic case.
//         IncludeStale     → DROPPED. The CLI never exposed a toggle for
//                            this; stale yoloai temp dirs are always
//                            pruned. No real opt-out use case.
//         DryRun           → KEPT. Universal pattern for destructive ops.
//         OnProgress       → KEPT. Embedders need progress for a
//                            potentially long-running operation.
//
//       CLI breaking changes (W-L8b/d, add to BREAKING-CHANGES.md):
//         --backend  removed (always all backends)
//         --all      removed (always all backends; flag is redundant)
//         --cache    renamed to --images (plain prune now also reclaims
//                    the no-rebuild cache; --images adds base-image removal)
//
//       Codified principle: when option fields accumulate, audit each
//       against actual use cases — not hypothetical ones. YAGNI applies
//       to API surface as much as to implementation; a 30-second future
//       feature is preferable to a permanently confusing option set.
//
// Q-M.  OnProgress audit — does every Options struct really need one?
//
//       **RESOLVED 2026-05-25:** Audited all six OnProgress callbacks.
//       OnProgress earns its place when the operation has discrete,
//       observable events AND/OR can run long enough that the caller
//       benefits from a liveness signal during the blocking call.
//       Clarification on what OnProgress IS NOT: it does not make the
//       call async. The call still blocks the caller's goroutine.
//       Embedders who want concurrency wrap the call in `go func()`.
//       OnProgress exists for in-call liveness and per-step rendering.
//
//         RunOptions       KEEP — multi-minute, many discrete events
//                                 (image build, container start, mounts,
//                                  agent launch).
//         CloneOptions     KEEP — single CopyDir call, but sandboxes
//                                 routinely reach 1–2 GB per `yoloai ls`;
//                                 30s+ of silence feels like a hang.
//                                 Liveness signal, not multi-event.
//                                 CopyDir may later emit per-file events.
//         BuildOptions     KEEP — image build, many discrete layer/
//                                 step events.
//         DoctorOptions    KEEP — multiple backend×isolation probes;
//                                 per-check events worth surfacing.
//         PruneOptions     KEEP — per-item events across potentially
//                                 many containers/VMs/images.
//         SetupOptions     DROP — three filesystem writes, milliseconds.
//                                 No events worth surfacing; CLI handles
//                                 user-visible output via its pre-Setup
//                                 wizard.
//
//       Codified test for future OnProgress additions: ask "does this
//       op take long enough that the caller benefits from knowing it's
//       alive, AND/OR are there ≥2 discrete events worth observing?"
//       If no to both, drop it. The cost of a missing OnProgress is
//       low (add it back when needed); the cost of one present without
//       justification is API noise and embedder confusion.
//
// Q-N.  Async support — channels vs blocking + callback?
//
//       **RESOLVED 2026-05-25:** Stay with synchronous methods +
//       OnProgress callback. Do NOT return channels / futures / handles
//       from operation methods.
//
//       Research summary (full survey in commit message + working
//       notes). Every major Go SDK uses synchronous APIs for bounded
//       operations:
//
//         docker/docker          Sync. ImagePull/Build return io.ReadCloser
//                                streaming JSON events the caller drains.
//         containerd/containerd  Sync. Pull blocks; progress polled
//                                out-of-band by the caller.
//         aws-sdk-go-v2 S3       Sync. Upload progress via wrapping the
//                                input io.Reader (counting reader).
//         go-git                 Sync. CloneOptions.Progress io.Writer
//                                receives human-readable lines.
//         hashicorp/go-getter    Sync. ProgressListener interface
//                                (callback-shaped).
//         kubernetes client-go   Sync for bounded ops. watch.Interface
//                                with channel is reserved for unbounded
//                                streams (the ONLY mainstream channel-
//                                returning API found).
//         stdlib                 io.Copy, http.Client.Do, net.Dial,
//                                sql.DB.Query — all sync. Concurrency
//                                is the caller's job via `go`.
//
//       Why not channels:
//         1. Channel-returning APIs leak goroutine ownership across the
//            API boundary. The library spawns; the caller must reason
//            about its lifetime, leak risk on abandonment, cancellation
//            plumbing. Dave Cheney's explicit warning ("Channels are
//            not enough"): "if you return a channel, you've also
//            implicitly created a goroutine the caller has to reason
//            about."
//         2. Terminal events become awkward. Progress + completion +
//            error all multiplex onto one channel via tagged events,
//            or you need a separate error channel, or close-semantics
//            for completion — all inventions the caller learns.
//         3. Backpressure hazards. Unbuffered = caller stalls producer
//            silently. Buffered = unbounded memory risk.
//         4. The caller can already achieve async with `go client.Op(...)
//            + ctx cancellation`. No value added by moving the goroutine
//            inside the library.
//
//       What we DO commit to:
//         - Methods are synchronous; context.Context handles cancellation
//           and deadlines.
//         - OnProgress callbacks are invoked synchronously and must not
//           block (documented in the top-of-file threading-model note).
//         - Concurrency is the caller's job — `go client.Op(...)`.
//
//       Possible future refinement (not in scope for W-L8a/b):
//         BuildOptions could optionally accept an io.Writer for streamed
//         build output, matching go-git's CloneOptions.Progress pattern.
//         Defer until we have a concrete embedder need; OnProgress is
//         sufficient today.
//
//       If we ever need genuine pub/sub semantics (multiple subscribers,
//       unbounded duration like log streaming or a watch loop), introduce
//       a Watch-style handle then — not before. Logs() already returns
//       (io.ReadCloser, error) which is the right shape for that kind of
//       stream.
//
// Q-O.  Info.Meta `any` → typed re-export; drop field duplicates; Inspect
//       cost honesty.
//
//       **RESOLVED 2026-05-25:** Three related fixes on the Info struct,
//       plus a docstring correction on Inspect.
//
//       1. Info.Meta was typed `any` — same anti-pattern as the earlier
//          Logger `any`. Replaced with SandboxMeta, a type alias to
//          sandbox/store.Meta (or internal/sandbox/store.Meta after
//          W-L12). Type aliases work across internal/ boundaries
//          because the public yoloai package can import from internal/
//          of the same module. D3 defers external stability — there's
//          no API-contract reason to hide a concrete type. Embedders
//          shouldn't need a type assertion to read sandbox metadata.
//
//       2. Four Info fields duplicated fields already in Meta:
//
//            Info.BaselineSHA  → Meta.Workdir.BaselineSHA
//            Info.Backend      → Meta.Backend
//            Info.Image        → Meta.ImageRef
//            Info.Isolation    → Meta.Isolation
//
//          Dropped. Single source of truth; embedders read these via
//          info.Meta.*. Drops 4 fields of maintenance overlap.
//
//       3. `type Info = struct {...}` (alias to an anonymous struct
//          literal) → `type Info struct {...}` (named struct). The
//          alias form was non-idiomatic and would prevent method
//          definition on Info.
//
//       Inspect-docstring correction: original wording claimed
//       "multiple backend round-trips; not cheap" — imprecise. Actual
//       cost breakdown:
//
//         Status alone:    ~10–50 ms (1 backend RPC + 1 status file read)
//         Inspect:         ~50–200 ms (adds meta.json read + N git forks
//                          for HasChanges, where N = :copy/:overlay dirs)
//
//       The dominant Inspect cost is the per-:copy/:overlay git
//       invocation for HasChanges, not the backend RPC. (DirSize would
//       have been the truly expensive bit — recursive walk on GB-scale
//       sandbox dirs — but it's not in the proposed Info; if embedders
//       want it, a separate method that owns its cost is the right
//       shape.)
//
//       The Status / Inspect split is still justified — Status skips
//       both the git forks and the metadata read — just for a less
//       dramatic reason than the original docstring implied. Updated
//       the docstring to name the actual expensive thing.
//
// Q-P.  ErrNoChanges sentinel — redundant with ApplyResult?
//
//       **RESOLVED 2026-05-25:** Dropped. The five sentinels become four.
//
//       The state ErrNoChanges signalled is fully encoded in
//       ApplyResult.PerDir — every directory processed with no changes
//       is reported as a PerDirApplyResult with Status ==
//       ApplyStatusEmpty. The aggregate "nothing applied overall" is
//       "no PerDir entry has Status == ApplyStatusApplied." So the
//       sentinel was duplicate signalling: callers had to handle both
//       errors.Is(err, ErrNoChanges) AND inspect the result, and the
//       two could in principle disagree.
//
//       Beyond redundancy, the pattern is awkward: "no changes to
//       apply" is a successful no-op, not a failure. Returning an
//       error for it tangles success with error-path branching:
//
//         result, err := wd.Apply(ctx, opts)
//         if errors.Is(err, ErrNoChanges) {
//             // success path #1 — masquerading as an error
//         } else if err != nil {
//             return err  // real failure
//         }
//         // success path #2 — applied something
//
//       Without the sentinel, error means failure and the result
//       describes what happened. Clean.
//
//       Audited the other four sentinels — each survives because its
//       call returns no result struct (Run rejected before producing
//       Info; Destroy is err-only; New returns nil client on backend
//       failure). ErrNoChanges was uniquely redundant because Apply
//       does return a result that already encodes the state.
//
//       Codified principle: don't promote a sentinel when the state
//       is recoverable from the returned result. Sentinels are for
//       cases where the result is unavailable or absent. Promotion
//       rule in Q-B updated.
//
//       CLI behavior unchanged: the "Nothing to apply" message keys
//       off inspecting result.PerDir rather than errors.Is.
//
// Q-Q.  Stringly-typed CLI-UI leak sweep.
//
//       **RESOLVED 2026-05-25:** Audited every string field against the
//       principle "fields should not be strings unless they actually
//       represent string values." Five CLI-UI leaks fixed:
//
//         BuildInfo.Date        string → time.Time
//             Was pre-rendered as "ISO-8601 build timestamp." Embedders
//             format dates however they want; the API surfaces a
//             time.Time and lets renderers decide.
//
//         SystemInfo.DiskUsage  string → DROPPED
//             Was a human-rendered total ("1.2 GB"). Collided in name
//             with the structured DiskUsage type. Embedders who want
//             total bytes call SystemClient.DiskUsage() for the typed
//             result.
//
//         PerDirApplyResult.Recovery  string → DROPPED
//             Was "human-readable next-step instructions." The
//             recovery action is fully derivable from Status —
//             ApplyStatusConflict means "resolve the conflict,"
//             ApplyStatusAppliedStashConflict means "resolve stash
//             merge markers." Embedders switch on Status to render
//             their own text. Same logic as Q-P (drop redundant
//             sentinel). HTTP/MCP embedders can localize; programmatic
//             callers branch on the enum.
//
//         PerDirApplyResult.ErrMessage  string → Err error
//             Flattening a Go error to a string loses errors.Is /
//             errors.As. Use the typed error.
//
//         BackendDiskUsage.Error  string → Err error
//             Same reason.
//
//       Codified principle: API field types name the actual data
//       shape. Pre-rendered humanizations (dates, byte counts,
//       descriptions) belong in the embedder's render layer, not in
//       the Client's snapshot types. Errors stay typed all the way
//       through. The principle is now part of the GO.md "Clarity over
//       brevity" section — every field type answers "what is this?"
//       not "how does the CLI render it?".
//
// Q-R.  Critique B-series — consistency, empty options, batch results.
//
//       **RESOLVED 2026-05-25:** Four smaller fixes applied together.
//
//       B2 (partial-completion shape consistency): accept the
//         asymmetry between Workdir.Apply (rich *ApplyResult) and the
//         batch list-mutation ops (Files.Put/Get/Rm,
//         Network.Allow/Remove returning error or []string only).
//         Apply's per-directory conflict semantics are too rich for
//         the caller to reconstruct from a single error; the batch
//         ops are retry-on-error workflows where rich per-item
//         results would be over-engineered. Documented on the Files
//         type with the explicit contract.
//
//       B3 (empty Options structs): dropped. StopOptions struct{} and
//         AttachOptions struct{} were placeholders "reserved for
//         future use." Adding option structs later is a breaking
//         change, but a small and easy one — and the file gets
//         cleaner now. Same YAGNI logic as Q-L (drop IncludeStale)
//         and Q-O (drop duplicated Info fields).
//
//       B4 (SimulatorRuntimes typed): []string → []SimulatorRuntime.
//         The string form "ios:26.4" was a CLI-shaped opaque token
//         (Q-Q applied: pre-parsed structured value, not data).
//         Replaced with a struct of (Platform SimulatorPlatform,
//         Version string). Added the SimulatorPlatform open-set
//         typed enum (ios / tvos / watchos / visionos).
//         Backend-specificity: this is the only tart-only field on
//         the universal RunOptions today. Carrying it inline is
//         pragmatic; revisit if more backend-specific options
//         appear and the pattern starts to weigh.
//
//       B5 (BugReportSession concrete): interface → struct. The
//         interface had no plausible alternate implementer and broke
//         consistency with the rest of the file (every other handle
//         is a concrete *struct). No reason for the abstraction.
//         StartBugReportSession now returns *BugReportSession.
//
// Q-S.  Wait return type — exit code or Status?
//
//       **RESOLVED 2026-05-25:** Wait now returns (Status, error)
//       instead of (exitCode int, error).
//
//       The previous signature promised "the agent's exit code (0 for
//       clean StatusDone, non-zero otherwise)." That conflated three
//       distinct things:
//
//         1. Whether the wait completed (cancel / timeout / inspect
//            failure) — the err return.
//         2. Whether the agent finished cleanly — yoloai's Status
//            classification at the agent-status.json layer.
//         3. The actual numeric exit code the agent's process
//            reported — agent-specific.
//
//       The killer: across the agents we already ship (Claude, Codex,
//       Gemini, OpenCode, Aider), there is no portable contract for
//       "0 = success, non-zero = failure." Some return non-zero for
//       "no findings" (linter-style tools), some return zero after a
//       clean shutdown that followed a crash. Promising "the agent's
//       exit code" gives embedders a number that LOOKS like a success
//       signal but isn't.
//
//       What yoloai reliably knows is its own Status classification:
//       StatusDone (clean) vs StatusFailed (anything else). That's
//       what callers should branch on.
//
//       Terminal-state-at-call-time behavior also documented: Wait
//       returns immediately if the sandbox is already terminal at
//       call time, no polling.
//
//       Exit code preserved for debug / bug reports: added
//       Info.AgentExitCode *int. Nil when not reported. The pointer
//       form makes "unknown" representable; the doc explicitly warns
//       it must NOT be used as a success signal. Bug reports already
//       capture the raw agent-status.json (which includes ExitCode)
//       so the debug data path is intact; the Info field surfaces it
//       to programmatic embedders that want to log or display it.
//
//       CLI behavior unchanged: it maps StatusDone → exit 0 and
//       anything else → exit 1. That mapping lives at the CLI layer,
//       not in the Client.
//
//       Codified principle: APIs surface honest classifications, not
//       opaque numbers that LOOK actionable but aren't portable.
//       Numeric values from external processes (exit codes, signals)
//       belong in debug surfaces, not in success/fail return values.
//
// Q-T.  Concurrency safety contract for handles.
//
//       **RESOLVED 2026-05-25:** Promote the partial existing safety
//       (Client + per-sandbox file locks) into an explicit, documented
//       guarantee on every handle.
//
//       The committed contract:
//         - Client and SystemClient are safe for concurrent use by
//           multiple goroutines.
//         - *Sandbox (and its sub-handles *Workdir, *Files, *Network)
//           are safe for concurrent use across goroutines AND across
//           process boundaries — concurrent `yoloai` invocations
//           cooperate through the same per-sandbox file locks.
//         - Write methods on *Sandbox / sub-handles acquire the
//           per-sandbox file lock at method entry. Concurrent writes
//           on the same sandbox serialize.
//         - Read methods on *Sandbox / sub-handles don't take the
//           lock and run in parallel with each other.
//         - Reads concurrent with writes may observe intermediate
//           state but do not crash or corrupt state.
//
//       This matches the database/sql and http.Client conventions:
//       share the handle freely; concurrent writes serialize; reads
//       are non-blocking but not transactional.
//
//       Implementation discipline for W-L8b:
//         - Every write method on *Sandbox acquires the per-sandbox
//           lock at the public entry point, BEFORE any backend RPC
//           or filesystem op. The lock acquisition is part of the
//           method's signature, not pushed down into internals.
//         - The existing acquireMultiLock used by Clone is the
//           reference implementation; extend it (or compose it)
//           rather than inventing a parallel locking model.
//         - Per-sandbox locks must release on all error paths —
//           defer-unlock at method entry.
//
//       Possible follow-on (NOT in scope for W-L8a/b): explicit
//       sandbox.Lock() / Unlock() APIs for embedders that want to
//       batch reads with the snapshot of a single write window.
//       Defer until a concrete use case appears.
//
// Q-U.  Aux :copy / :overlay — remove?
//
//       **RESOLVED 2026-05-25:** Remove. After this refactor,
//       auxiliary directories support :rw and :ro only. The diff/apply
//       workflow is workdir-only.
//
//       Background. The existing implementation does support multi-dir
//       diff/apply across the workdir and every :copy / :overlay aux
//       directory (sandbox/patch/apply.go's GenerateMultiPatch;
//       internal/cli/apply_overlay.go for the overlay path). It's real
//       working code with tests (TestApplyFormatPatch_Multiple,
//       TestGenerateFormatPatch_Multiple, etc.). But the user-visible
//       workflow is intentionally limited:
//
//         - All-or-nothing apply (first failure halts the chain)
//         - No per-dir selection
//         - No cross-dir conflict resolution
//         - No "apply :overlay but not :copy" filtering
//
//       The API surface this WOULD support if we kept it (per-dir
//       status matrices, filter options, recovery branching, partial
//       resume) is significantly more complex than what the
//       implementation actually does today. We were projecting
//       sophistication onto a barely-used feature.
//
//       Use cases that would justify the projected complexity (monorepo
//       with sibling repos; project + docs in separate repo; project +
//       tools repo) are real but rare, and have clean workarounds:
//         - Make a parent directory the workdir
//         - Use :rw for the secondary dir (live edit)
//         - Run separate sandboxes
//
//       Project owner verdict: remove the aux :copy / :overlay surface
//       now while we're in beta and have an active refactor to land
//       it on. See who complains. If a real use case emerges, restore
//       with a cleaner API informed by the actual need.
//
//       API simplifications cascading from this:
//
//         - Sandbox.Workdir() / *Workdir handle: name now accurately
//           scoped to the single workdir (no aux dirs in this handle).
//           No rename needed (the C4 misnomer disappears with the
//           scope shrink).
//         - Workdir.Diff returns (string, error) instead of
//           ([]*DiffResult, error). Empty string = no changes.
//         - DiffResult struct DELETED — was only useful for multi-dir.
//         - ApplyResult flat: Status, Patch, ExportedPath, Err. No
//           PerDir slice, no SkippedDirs.
//         - PerDirApplyResult DELETED — folded into ApplyResult.
//         - SkippedDir, SkipReason, SkipReasonReadWrite,
//           SkipReasonOverlayStopped DELETED — nothing to skip when
//           only the workdir is in scope.
//         - MountMode: added explicit MountRO ("ro") for aux read-only
//           reference dirs (was implicit default). Doc updated with
//           workdir-vs-aux mode restrictions.
//
//       Implementation work (W-L8b/c/d):
//
//         - parse.go: reject :copy and :overlay suffixes on the -d
//           flag with *UsageError pointing at the workdir or :rw / :ro
//           alternatives.
//         - create_prepare.go: drop the aux :copy/:overlay code paths.
//         - sandbox/patch/apply.go: delete GenerateMultiPatch and
//           collapse ApplyAll to a single-dir helper.
//         - internal/cli/apply_overlay.go: drop the multi-dir
//           iteration.
//         - inspect.go: HasChanges check no longer iterates aux dirs.
//         - Delete multi-dir tests.
//         - BREAKING-CHANGES.md entry: "Auxiliary :copy and :overlay
//           are no longer supported. Migration: make the directory the
//           workdir; or mount as :rw if you want live edits; or run a
//           separate sandbox. The diff/apply workflow only ever
//           applied to the workdir + uncommitted aux changes in
//           practice; this codifies what was already true in
//           operations."
//
//       Codified principle (general): API complexity should match
//       complexity actually exercised, not anticipated future use.
//       Removing rarely-used features in beta is far easier than
//       removing them after they accrete sophistication or pick up
//       hidden dependencies. When in doubt about a barely-used
//       feature, cut it; restore from the real-use feedback when
//       someone shows up with a concrete need.
//
// Q-V.  Network.Allowed provenance — flat []string or typed?
//
//       **RESOLVED 2026-05-25:** Typed. Allowed() returns
//       []AllowedDomain with a Source enum.
//
//       The previous shape returned []string and flattened away the
//       provenance of each entry. Two real use cases benefit from
//       knowing where an entry came from:
//
//         1. "Don't silently nuke an agent-required domain." If a UI
//            or automation calls Network.Remove("api.anthropic.com")
//            on a Claude sandbox, the embedder should be able to
//            detect that removal will break the agent itself, not
//            just enforce a user policy decision.
//
//         2. "Show me my additions vs baked-in defaults." A
//            management UI rendering "Agent requires: X / Your
//            additions: Y" is a real UX win.
//
//       Implementation reality check (sandbox/create_prepare.go:679):
//       the agent's default allowlist (agentDef.NetworkAllowlist) and
//       the user's RunOptions.AllowDomains are concatenated into
//       meta.NetworkAllow at create time. The on-disk storage flattens
//       them. However, the agent's default list is shipped data
//       reachable by sandbox.Agent → agentDef.NetworkAllowlist, so
//       provenance is RECOVERABLE at read time:
//
//         agent-required = meta.NetworkAllow ∩ agentDef.NetworkAllowlist
//         user-added     = meta.NetworkAllow \ agentDef.NetworkAllowlist
//
//       Create-time vs runtime user additions can't be distinguished
//       today — the storage doesn't separate them. We could add that
//       split later by introducing a third AllowedDomainSource
//       constant and a storage change; no use case justifies it now.
//
//       Naming: AllowedFromAgentRequirement (not just AllowedFromAgent)
//       names the actual claim — these domains exist because the
//       agent REQUIRES them to function, not just because the agent's
//       definition lists them. The name signals to embedders that
//       removal has consequences for the agent.
//
//       Codified principle: API result types preserve the provenance
//       distinctions the implementation can answer for, even when
//       storage flattens them — derivation at read time is fine.
//       Throwing away derivable information at the API boundary is
//       the same anti-pattern as the Q-Q CLI-UI leak sweep, applied
//       to data-shape rather than rendering.
//
// Q-W.  DataDir as an explicit Client parameter; profiles/config join
//       the Client API; no-ambient-configuration principle codified.
//
//       **RESOLVED 2026-05-25:** Three coupled changes.
//
//       1. Options.DataDir is REQUIRED. Empty = *UsageError at
//          construction. No implicit $HOME-based default in the
//          library. The CLI fills DataDir from $HOME/.yoloai/ at
//          startup; HTTP/MCP/daemon/test embedders pass an explicit
//          path.
//
//          Why required (not "default-with-warning"): WOMM ("works on
//          my machine") failures from ambient state are precisely
//          what this prevents. A library that silently lands HTTP-
//          server state in /root/.yoloai/ because the daemon happened
//          to run as root, or trashes a developer's real ~/.yoloai/
//          from a test that forgot t.TempDir(), is the failure mode.
//          The cost of forcing one extra parameter is small; the
//          cost of the failure mode is large.
//
//          The zero-argument New(ctx) constructor is REMOVED. Every
//          caller goes through NewWithOptions with DataDir set.
//
//       2. SystemClient gains profile and config management methods —
//          Profiles / Profile / CreateProfile / DeleteProfile and
//          Config / GetConfig / SetConfig / ResetConfig. They operate
//          on DataDir/profiles/ and DataDir/config.yaml respectively.
//          Previously these were listed in the exception block as
//          "CLI calls config/ and os.RemoveAll directly" — that was
//          reasoning from the CLI's perspective. HTTP / MCP / SaaS
//          embedders need the same capabilities, and DataDir being
//          explicit lets them target the right location. The
//          exception entries dissolve.
//
//          New sentinel: ErrProfileNotFound. Same shape as
//          ErrSandboxNotFound.
//
//       3. Codified principle in development-principles.md §12 (No
//          ambient configuration): library boundaries take all
//          configuration as explicit arguments; environment, $HOME,
//          and other ambient process state are resolved at the
//          outermost layer (CLI startup, server bootstrap, test
//          setup) and passed down.
//
//          Concrete rules:
//            - os.UserHomeDir() is BANNED outside one designated CLI
//              entry point (allowlisted in the W-L10 linter).
//            - os.Getenv() for yoloai's own config is BANNED in
//              library code. CLI startup may read env; everything
//              below takes parameters.
//            - os.Getwd() as a silent default is BANNED.
//            - Agent API keys (ANTHROPIC_API_KEY etc.) are an
//              exception, because the agent's published contract IS
//              "I read this env var" — that's part of agent.Definition.
//
//          Enforcement: W-L10's scope expands to include the
//          os.UserHomeDir / os.Getenv / os.Getwd bans (one allowlist
//          per call). Adding a new env-read requires a justification
//          comment AND a W-L10 allowlist entry, audited at PR time.
//
//       Implementation work (W-L8b/c):
//         - Plumb DataDir through config.LoadConfig, store.* path
//           helpers, sandbox.* lookups. Every function that today
//           resolves ~/.yoloai/ via os.UserHomeDir() takes DataDir
//           (or a derived path) as a parameter.
//         - CLI startup: one os.UserHomeDir() call site
//           (cmd/yoloai/main.go or internal/cli/root.go) computes the
//           default DataDir; can be overridden with --data-dir flag.
//         - W-L10 linter rule: forbid os.UserHomeDir/Getenv/Getwd
//           outside allowlist.
//         - All tests construct Client with t.TempDir() based DataDir.
//
//       Codified principle (general): explicit configuration beats
//       ambient state. The library boundary is the contract; if
//       configuration isn't in the contract, it isn't configurable —
//       and silent defaults that depend on process environment are
//       configuration the contract pretends doesn't exist.
//
// Q-X.  AgentInfo.NetworkAllowlist exposed; Unrecoverable escape
//       hatches removed.
//
//       **RESOLVED 2026-05-25:** Two unrelated cleanups bundled.
//
//       1. AgentInfo gains NetworkAllowlist []string. Embedders building
//          UIs around network policy need to see what defaults a given
//          agent brings (per Q-V's AllowedFromAgentRequirement source).
//          Was an obvious omission once Q-V landed.
//
//       2. UnrecoverableCode loses two values:
//            UnrecoverableNotImplemented  → DROPPED
//            UnrecoverableInternal        → DROPPED
//
//          Audited the actual codebase for sites that would surface
//          either:
//
//            - "Unknown agent" (lifecycle.go, 5 places) classifies
//              cleanly as state_corrupted: a saved sandbox references
//              an agent definition that no longer exists.
//            - "Internal error: tart backend type mismatch"
//              (system_tart.go:169) is a programming-bug type
//              assertion. Bugs panic; the CLI's existing recover()
//              at internal/cli/root.go:54 turns panics into graceful
//              "yoloai crashed; please file a bug" output.
//            - Registry init-time panics (runtime/registry.go:34,39)
//              are correct as panics — invariant-violation at startup,
//              not an operational outcome.
//
//          No remaining site genuinely needs an "internal" or
//          "not implemented" classification. The five remaining
//          codes (agent_crash, backend_failure, build_failure,
//          state_corrupted, vm_boot_failure) exhaustively cover
//          operational failures.
//
//          Why drop instead of rename to UnrecoverableBug: keeping a
//          generic escape hatch creates the temptation to use it
//          lazily — "I don't know what this is, ship it as Bug" —
//          which defeats the §7 discipline (act on every return value;
//          classify every failure). Without the escape hatch, the
//          only path forward is correct classification (or fix the
//          bug structurally). Embedders that need to wrap panics into
//          typed errors (e.g., an HTTP daemon that doesn't want a
//          panic crashing the process) recover at their own goroutine
//          boundary — standard Go pattern, not the library's job.
//
//       Codified principle (general): API taxonomies stay
//       exhaustive by removing escape hatches that invite incorrect
//       classification. If a failure mode can't be named in the
//       taxonomy, that's a taxonomy gap to fix (add a real code) or
//       a programming bug (panic + caller's recover boundary), not
//       an "Other / Internal / Bug" bucket.
//
// Q-Y.  Round-2 critique sweep — typed-name enums, structured tokens,
//       small redundancies, stale references.
//
//       **RESOLVED 2026-05-25:** Round-2 fresh-eyes pass found one
//       backlog item (typed-name sweep we'd implicitly endorsed but
//       not applied), a handful of stale references from earlier
//       renames, two small structural redundancies, and a few doc
//       tightenings.
//
//       1. Typed-name enum sweep (the backlog item). Added open-set
//          typed enums BackendName, AgentName, LogSource and applied
//          them consistently:
//
//            Options.Backend          string → BackendName
//            BuildOptions.Backend     string → BackendName
//            SetupOptions.Backend     string → BackendName
//            DoctorOptions.BackendFilter string → BackendName
//            DoctorReport.Backend     string → BackendName
//            BackendInfo.Name         string → BackendName
//            SystemClient.Backend(name string) → (name BackendName)
//
//            RunOptions.Agent         string → AgentName
//            SetupOptions.Agent       string → AgentName
//            ListOptions.Agents       []string → []AgentName
//            AgentInfo.Name           string → AgentName
//            SystemClient.Agent(name string) → (name AgentName)
//
//            LogOptions.Sources       []string → []LogSource
//
//          Consistent with IsolationMode / HostOS / NetworkMode /
//          SimulatorPlatform etc. Closes the loop on the §4
//          parse-don't-validate discipline.
//
//       2. Structured tokens — CLI-shape leaks (same shape as the
//          Q-R SimulatorRuntime fix). Two more []string fields
//          replaced with structured types:
//
//            RunOptions.Mounts  []string → []MountSpec
//                where MountSpec is { HostPath, ContainerPath string;
//                                     ReadOnly bool }.
//            RunOptions.Ports   []string → []PortMapping
//                where PortMapping is { HostPort, ContainerPort int;
//                                       Protocol string }.
//
//            Direction in the prefix (Host / Container); kind in the
//            suffix (Path / Port). Without the type-carrying suffix
//            an int field named "Host" reads ambiguously at every
//            call site (Go doesn't surface types) — same hazard for
//            a string field named "Host" that could be a hostname or
//            a path. The suffix anchors the meaning unconditionally.
//            (Originally documented with bare Host/Container in
//            both types; corrected during the landing sweep — see
//            names.go for the call-site reading argument.)
//
//          Embedders no longer format/parse "host:container[:ro]"
//          and "8080:8080/tcp" tokens; the CLI parses these from
//          flag values into the typed structs before calling Run.
//
//          PruneResult.RemovedItems []string → []PruneItem (with
//          PruneItemKind open-set typed enum). Tells embedders WHAT
//          got removed, not just opaque names.
//
//       3. Small redundancies fixed:
//
//          - DoctorReport: dropped IsDefaultMode bool. Mode == "" was
//            the empty-as-meaningful pattern Q-H ruled out; the
//            "is this the default mode?" question is derivable by
//            comparing Mode to BackendInfo's default. No separate
//            flag needed.
//
//          (BuildOptions.Backend + AllBackends interaction left as-is
//          — the two-field shape with documented exclusivity matches
//          the CLI's --backend / --all-backends flag pair and the
//          mutual-exclusion is checked at the call.)
//
//       4. Stale references fixed:
//
//          - RestartOptions doc said "Force=true overrides" — pre
//            Q-J name. Updated to "AcceptOverlayDataLoss=true."
//          - Q-I body had the same stale reference. Updated.
//          - Q-B sentinel list said "Four stable sentinels" — Q-W
//            added ErrProfileNotFound bringing total to five.
//            Updated.
//
//       5. Doc tightenings:
//
//          - Info.HasChanges now says "workdir has unapplied agent
//            commits or uncommitted edits" (post Q-U workdir-only).
//          - Status and AgentStatus types document the shared-string-
//            values overlap ("active", "idle", "done") so embedders
//            don't accidentally cross-compare via string.
//
//       Net effect: same architectural shape as after Q-X, but the
//       file now applies its own typed-enum discipline uniformly
//       (no stringly Backend/Agent/Source leftovers) and the CLI-
//       shape token strings are gone from the universal options. The
//       previous Q-rounds caught the structural questions; this one
//       was polish.
//
// Q-Z.  Stale lock UX — don't leave the user hanging.
//
//       **RESOLVED 2026-05-26:** Lock-contention path identifies the
//       holder and gives the user actionable recovery.
//
//       Context. yoloai's per-sandbox lock uses flock(2)
//       (sandbox/lock_unix.go), which the kernel auto-releases when
//       the holding process exits — graceful or not. In the common
//       case (process crash, panic, SIGKILL, OOM) flock cannot become
//       stale. Q-T builds on this for the concurrency contract.
//
//       But: sudden power loss, the filesystem going read-only or
//       offline mid-operation, or a kernel-level process wedge CAN
//       leave a lock effectively unrecoverable without manual
//       intervention. Plus the existing AcquireLock blocks
//       indefinitely (unix.LOCK_EX, no timeout), so even legitimate
//       contention surfaces to the user as a silent hang. Either
//       failure mode leaves the user without information or recourse.
//
//       The fix:
//
//       1. Lock acquisition becomes a brief non-blocking retry,
//          not an infinite block. The W-L8b implementation uses
//          LOCK_EX|LOCK_NB with a few-second retry budget; on
//          continued failure it returns *SandboxLockedError.
//
//       2. The lock file now carries the acquiring process's PID
//          as its content (previously zero-byte). This is
//          informational only — the flock(2) advisory lock is still
//          the source of truth for mutual exclusion; the PID bytes
//          let contention readers identify and classify the holder.
//
//       3. *SandboxLockedError carries Name, HolderPID, HolderAlive,
//          LockPath. Embedders match with errors.As; the CLI formats
//          a recovery message:
//
//            HolderAlive=true:
//              "Sandbox \"foo\" is in use by another yoloai process
//               (PID 12345). Wait for it to finish, or cancel that
//               process before retrying."
//
//            HolderAlive=false:
//              "Sandbox \"foo\" has a stale lock (PID 12345 no
//               longer exists). This is rare — usually caused by
//               sudden power loss or a filesystem failure during a
//               previous operation. Clear it with:
//                 yoloai sandbox foo unlock
//               or manually:
//                 rm ~/.yoloai/sandboxes/foo.lock"
//
//       4. Sandbox.Unlock(ctx) is the API for manual recovery; the
//          CLI surfaces it as `yoloai sandbox <name> unlock`. Unlock
//          refuses with *UsageError when HolderAlive=true (the right
//          recovery for an alive holder is to wait or kill, not to
//          silently break invariants of a running op). For dead
//          holders, Unlock removes the lock file and returns nil.
//
//       5. PID-aliveness check is `syscall.Kill(pid, 0)` on Unix —
//          ESRCH means dead, EPERM means alive (but not ours), no
//          error means alive. Not foolproof (PIDs are reused on
//          long-uptime systems) but combined with "Unlock refuses
//          alive holders" the failure mode (refuse to unlock a
//          reused PID) is safe — user can still rm the file.
//
//       Windows. The current sandbox/lock_windows.go is a no-op (the
//       file's header notes Windows isn't fully supported and that
//       concurrent operations may error but won't corrupt state).
//       Q-Z doesn't ship Windows lock support; that's a separate
//       workstream when Windows-as-host becomes a target. The
//       *SandboxLockedError surface can be reused if/when Windows
//       gets real locks.
//
//       Codified principle: long-running coordination primitives
//       (locks, pidfiles, semaphores) need an explicit "what does
//       the user do when it gets stuck" answer baked into the API
//       and the CLI. Blocking-forever is not a user-friendly
//       default; an actionable error with a recovery path is.
