//go:build never

// ABOUTME: W-L8a design checkpoint — the full proposed yoloai.Client surface
// ABOUTME: every CLI command will call after Phase 3 lands. Not built.

// Package yoloai_apidesign holds the W-L8a design checkpoint for the
// `yoloai.Client` surface that the CLI will consume after Phase 3 (W-L8b/c/d/e)
// of the layering refactor lands. Build tag `never` keeps the file out of
// every build — it's read like a header, not compiled.
//
// **Structural shape (Shape B, resolved 2026-05-24 / Q-G).** Resource-bound
// handles, GCS-style: `client.Sandbox(name)` returns a cheap `*Sandbox`
// handle that wraps `(client, name)`. Sandbox-scoped operations are methods
// on the handle; sub-groupings (Workdir, Files, Network) are nested handles
// off the sandbox. Admin sits on a separate `*SystemClient` reachable via
// `client.System()`. Cross-sandbox operations (List, Run, Clone, the
// bug-report primitives) stay on `*Client` directly. The motivation: drop
// the artificial method prefixes (FilesPut/FilesGet/...) in favor of
// structural namespacing (Files().Put / Files().Get), and let the type
// hierarchy match the conceptual hierarchy.
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

// IsolationMode selects the OCI / VM isolation level for a sandbox. The
// zero value means "unspecified" — Client methods canonicalize it to
// IsolationContainer at the boundary. Every named constant is a real
// string so debug output, JSON, and stored metadata are always
// self-describing (no "" → mystery default mapping).
type IsolationMode string

const (
	IsolationContainer           IsolationMode = "container"            // runc; current default
	IsolationContainerEnhanced   IsolationMode = "container-enhanced"   // gVisor
	IsolationContainerPrivileged IsolationMode = "container-privileged" // runc + --privileged
	IsolationVM                  IsolationMode = "vm"                   // Kata + QEMU
	IsolationVMEnhanced          IsolationMode = "vm-enhanced"          // Kata + Firecracker
)

// HostOS selects the operating-system environment the agent runs in. Zero
// value means "unspecified" — Client methods canonicalize to OSLinux.
type HostOS string

const (
	OSLinux HostOS = "linux" // Linux container or VM (current default)
	OSMac   HostOS = "mac"   // macOS-native sandbox (Seatbelt or Tart)
)

// NetworkMode selects a sandbox's outbound network policy. Modeled as one
// enum field rather than two booleans — the invalid "isolated AND none"
// combination is unrepresentable. Zero value means "unspecified" — Client
// methods canonicalize to NetworkOpen.
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

// LogFormat selects which log stream to emit. Zero value means
// "unspecified" — canonicalized to LogStructured.
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
//     handle in scope; StartBugReporter is name-less).
type Client struct{}

// Options configures a Client.
//
// No Input or Output field by design. The Client is non-interactive (Q-F)
// so it never reads stdin; embedders that need to relay user-facing
// progress events use the OnProgress callback on each method's Options
// struct (Q-? cleanup, 2026-05-24). Streaming methods (Attach, Exec,
// ProxyMCP) take IOStreams per-call; that's the only place an input
// stream appears in the API.
type Options struct {
	Backend string // explicit backend; "" = read from config, auto-detect
	Logger  any    // *slog.Logger; nil = slog.Default(). `any` here to avoid pulling slog into design
}

func New(ctx context.Context) (*Client, error)                          { panic("design-only") }
func NewWithOptions(ctx context.Context, opts Options) (*Client, error) { panic("design-only") }
func (*Client) Close() error                                            { panic("design-only") }

// Sandbox returns a name-bound handle for sandbox-scoped operations. Cheap
// — no IO, never errors. The sandbox isn't looked up until you call a
// method on the handle (Inspect, Diff, etc.), which is where
// ErrSandboxNotFound surfaces if the name doesn't exist. Matches GCS's
// Bucket/Object handle convention.
func (*Client) Sandbox(name string) *Sandbox { panic("design-only") }

// System returns the admin sub-client (`yoloai system …` commands).
func (*Client) System() *SystemClient { panic("design-only") }

// List returns sandboxes matching opts. Cross-sandbox; lives directly on
// Client (a Sandbox handle wouldn't make sense — there's no "name" yet).
type ListOptions struct {
	Statuses []string // "active", "idle", "done", ...; empty = all
	Agents   []string // filter by agent name
	Profiles []string // filter by profile ("" for unprofiled)
	Changes  bool     // only sandboxes with unapplied changes
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

	Isolation IsolationMode // empty = canonicalized to IsolationContainer
	OS        HostOS        // empty = canonicalized to OSLinux

	Network      NetworkMode // outbound policy; empty = canonicalized to NetworkOpen
	AllowDomains []string    // initial allowlist; only meaningful with NetworkIsolated

	Env       map[string]string
	Mounts    []string
	Ports     []string
	Secrets   []string
	BuildArgs map[string]string

	Replace bool // destroy any existing sandbox with the same name first
	Yes     bool // skip confirmation prompts (dirty repo warnings etc.)
	NoStart bool // create state but don't start the container yet

	Runtimes []string // tart-only: pre-built Apple simulator runtime base

	Wait       bool             // block until terminal status
	OnProgress func(msg string) // called with human-readable progress lines; nil = silent
}

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

// Sandbox is a cheap name-bound handle. Construct via Client.Sandbox(name).
// Never errors at construction time; the name is "validated" by the first
// method call (Inspect, etc.), which returns ErrSandboxNotFound if the
// sandbox doesn't exist. Matches GCS's BucketHandle convention.
type Sandbox struct{}

// Name returns the bound sandbox name.
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

// StartOptions configures Start.
type StartOptions struct {
	Attach            bool          // also attach to tmux after start
	Prompt            string        // optional prompt to inject after relaunch
	IsolationOverride IsolationMode // change isolation on restart; rebuilds container
}

func (*Sandbox) Start(ctx context.Context, opts StartOptions) error { panic("design-only") }

// StopOptions configures Stop. Reserved for future use (e.g. --timeout).
type StopOptions struct{}

func (*Sandbox) Stop(ctx context.Context, opts StopOptions) error { panic("design-only") }

// RestartOptions configures Restart.
type RestartOptions struct {
	Attach bool
}

func (*Sandbox) Restart(ctx context.Context, opts RestartOptions) error { panic("design-only") }

// DestroyOptions configures Destroy.
type DestroyOptions struct {
	Force bool // destroy even when the sandbox has unapplied changes
}

func (*Sandbox) Destroy(ctx context.Context, opts DestroyOptions) error { panic("design-only") }

// ResetOptions configures Reset.
type ResetOptions struct {
	Restart    bool
	ClearState bool
	KeepCache  bool
	KeepFiles  bool
	Attach     bool
	Prompt     string
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
	WorkDir string
	User    string
}

// ExecResult is returned for non-streaming Exec. When io.TTY=true, output
// went straight to io.Out/Err and the fields here are empty.
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
	Format   LogFormat     // empty = canonicalized to LogStructured
	Sources  []string      // for LogStructured / LogStructuredRaw: cli, sandbox, monitor, hooks
	MinLevel string        // debug | info | warn | error
	Since    time.Duration // 0 = no filter
	Follow   bool          // tail live; returns when sandbox is done
}

// Logs streams the requested log to w.
func (*Sandbox) Logs(ctx context.Context, opts LogOptions, w io.Writer) error {
	panic("design-only")
}

// ProxyMCP bridges the caller's stdio to an inner MCP server running
// inside the sandbox. Requires the backend to implement
// runtime.StdioExecer; returns *UsageError otherwise.
func (*Sandbox) ProxyMCP(ctx context.Context, io IOStreams) error { panic("design-only") }

// --- sub-handles ---

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

// DiffResult mirrors patch.DiffResult.
type DiffResult struct {
	Dir    string
	Mode   string // "copy" or "overlay"
	Patch  string
	Stat   string
	IsBase bool
}

func (*Workdir) Diff(ctx context.Context, opts DiffOptions) ([]*DiffResult, error) {
	panic("design-only")
}

// ApplyOptions configures Apply.
type ApplyOptions struct {
	Mode       ApplyMode // empty = default behavior; ApplySquash / ApplyExport override
	ExportDir  string    // required when Mode == ApplyExport
	Refs       []string  // specific commits or ranges
	Paths      []string  // pathspec filter
	IncludeWIP bool
	DryRun     bool // invalid with ApplyExport
	WithTags   bool // invalid with ApplySquash
	Yes        bool // skip confirmation prompts
}

// ApplyResult bundles per-directory outcomes plus a top-level rollup.
type ApplyResult struct {
	Per     []*PerDirApplyResult
	Patches []string // populated only when Mode == ApplyExport
	Skipped []string
}

// PerDirApplyResult lifts patch.ApplyResult.
type PerDirApplyResult struct {
	Dir        string
	Applied    bool
	Conflicts  bool
	Patch      string // dry-run only
	ErrMessage string
}

func (*Workdir) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	panic("design-only")
}

// Baseline operations. CLI: `yoloai baseline {advance|set|log}`. Baselines
// are part of the work-tree concept (the baseline SHA marks where the
// agent's accumulated changes start), so they live here rather than as a
// separate sub-handle.

type BaselineEntry struct {
	When time.Time
	SHA  string
	Note string
}

func (*Workdir) AdvanceBaseline(ctx context.Context) error         { panic("design-only") }
func (*Workdir) SetBaseline(ctx context.Context, sha string) error { panic("design-only") }
func (*Workdir) BaselineLog(ctx context.Context) ([]BaselineEntry, error) {
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
	Sources []string
	Force   bool
}

func (*Files) Put(ctx context.Context, opts PutOptions) error { panic("design-only") }

// GetOptions configures Get. Patterns match inside the sandbox exchange
// dir; Output is a host destination (dir or file).
type GetOptions struct {
	Patterns []string
	Output   string
	Force    bool
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
// the sandbox was created with NetworkIsolated; otherwise methods return
// *UsageError.
type Network struct{}

func (*Network) Allow(ctx context.Context, domains []string) error { panic("design-only") }
func (*Network) Deny(ctx context.Context, domains []string) error  { panic("design-only") }
func (*Network) Allowed(ctx context.Context) ([]string, error)     { panic("design-only") }

// =============================================================================
// Client — bug-report primitives
// =============================================================================

// BugReportOptions configures BugReport and StartBugReporter.
type BugReportOptions struct {
	Mode      BugReportMode // empty = canonicalized to BugReportSafe
	OutputDir string        // directory to write the report; "" = CWD
}

// BugReport captures a one-shot snapshot of a sandbox plus relevant
// system state to a markdown file. Returns the absolute path of the
// written file. Distinct from StartBugReporter — BugReport gathers state
// AT the call moment without buffering events. CLI: `yoloai sandbox
// <name> bugreport [safe|unsafe]`.
//
// Lives on Client (taking name explicitly) rather than Sandbox so it's
// usable from error paths where you don't have a handle in scope (e.g.
// inside a defer after Run failed).
func (*Client) BugReport(ctx context.Context, name string, opts BugReportOptions) (path string, err error) {
	panic("design-only")
}

// BugReportSession is the handle returned by StartBugReporter. Buffers
// runtime events until Stop or Discard.
type BugReportSession interface {
	Stop() (path string, err error) // flush + write report
	Discard()                       // throw away buffered events
}

// StartBugReporter begins buffering runtime events. Embedders scope the
// session however they want (single risky call, fixed duration, program
// lifetime). The CLI's top-level `--bugreport` flag is a thin wrapper.
func (*Client) StartBugReporter(ctx context.Context, opts BugReportOptions) BugReportSession {
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
	Name        string
	Description string
	Platforms   []string
	Requires    string
	InstallHint string
	Available   bool
	Note        string // probe failure reason; "" when available
	Version     string // VersionString() output; "" when unavailable
}

// AgentInfo is the public face of `agent.Definition`.
type AgentInfo struct {
	Name          string
	Description   string
	PromptMode    PromptMode
	APIKeyEnvVars []string
	ModelAliases  map[string]string
}

// SystemInfo bundles paths + disk usage + version metadata.
type SystemInfo struct {
	Version           string
	Commit            string
	Date              string
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
	Profile    string
	Backend    string
	All        bool // build across every available backend (exclusive with Backend)
	Force      bool
	Secrets    []string
	OnProgress func(msg string) // human-readable progress (image build steps); nil = silent
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
	Backend      string
	Mode         IsolationMode // "" when IsBaseMode
	IsBaseMode   bool
	Availability Availability
	InitErr      error
	MissingCaps  []string
}

func (*SystemClient) Doctor(ctx context.Context, opts DoctorOptions) ([]DoctorReport, error) {
	panic("design-only")
}

// PruneOptions configures Prune.
type PruneOptions struct {
	Backend      string // "" = all
	DryRun       bool
	Cache        bool // also prune backend caches (forces base rebuild)
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
	HasChanges  bool   // unapplied agent commits or WIP edits

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
	Mode string // "copy" (default), "overlay", "rw"
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
	// (or WIP edits) to apply.
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
//         Client.StartBugReporter(ctx, opts) BugReportSession — session
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
//       sub-client (GCS-style cheap handles). Five conceptual groups
//       become five structural homes:
//         Sandbox     (lifecycle + interaction)        client.Sandbox(name)
//         Workdir     (diff / apply / baseline)        sandbox.Workdir()
//         Files       (exchange dir)                   sandbox.Files()
//         Network     (allowlist)                      sandbox.Network()
//         System      (admin)                          client.System()
//       Cross-sandbox ops (List, Run, Clone, BugReport, StartBugReporter)
//       stay directly on Client. ExchangeDir + Prompt collapse into
//       *Info fields (no separate methods). Drops artificial method
//       prefixes (FilesPut → Files().Put, AllowDomains → Network().Allow)
//       in favor of structural namespacing.
//       Research: aligns with cloud.google.com/go/storage's
//       BucketHandle/ObjectHandle convention. kubernetes-client-go uses
//       a similar pattern (CoreV1().Pods(ns).Get). go-github and stripe
//       use sub-clients with IDs-per-call — less idiomatic for yoloai's
//       repeated-name workflow.
