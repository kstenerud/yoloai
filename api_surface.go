//go:build never

// ABOUTME: W-L8a design checkpoint — the full proposed yoloai.Client surface
// ABOUTME: every CLI command will call after Phase 3 lands. Not built.

// Package yoloai_apidesign holds the W-L8a design checkpoint for the
// `yoloai.Client` surface that the CLI will consume after Phase 3 (W-L8b/c/d/e)
// of the layering refactor lands. Build tag `never` keeps the file out of
// every build — it's read like a header, not compiled. Once each method on
// `clientAPI` below is implemented on the real `yoloai.Client` (in W-L8b)
// this file is deleted.
//
// **Scope.** Every public CLI command in `internal/cli/` is mapped to either:
//   (a) a method on `clientAPI`, or
//   (b) a documented exception (presentation, config-file, vscode launcher).
//
// **Reviewer's checklist (W-L8a acceptance criteria from layering-refactor.md):**
//   1. Every CLI command has either a clientAPI method + Options struct or an
//      explicit exception comment.
//   2. Streaming / interactive operations take an explicit IOStreams struct so
//      TTY handling is visible in the signature (kubectl's lesson: don't try
//      to hide stdio).
//   3. Reviewer approval gates W-L8b.
//
// Open questions for reviewer attention are tagged **OPEN-Q** in comments.

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
// re-implementing internal/cli/envname.go. **OPEN-Q (D9):** keep as a
// package-level helper as designed, or move to a `yoloai.Config{}` value?
// Recommended: keep as the standalone helper — embedders that don't want
// env semantics ignore it, and it stays useful even before Client
// construction.
func SandboxNameFromEnv() string { panic("design-only") }

// =============================================================================
// Sandbox lifecycle  (CLI: new, clone, start, stop, restart, destroy, reset, wait)
// =============================================================================

// NetworkMode selects a sandbox's outbound network policy. The three modes
// are mutually exclusive — modeled as one enum field rather than two
// booleans (the previous --network-isolated + --network-none flag pair) so
// the invalid "isolated AND none" combination is unrepresentable. The CLI's
// flags marshal to these values: no flag → NetworkOpen, --network-isolated
// → NetworkIsolated, --network-none → NetworkNone.
type NetworkMode string

const (
	// NetworkOpen leaves the sandbox's network unchanged: full outbound
	// access via the backend's default networking. This is the zero value
	// so callers who don't care about networking get sensible defaults.
	NetworkOpen NetworkMode = ""

	// NetworkIsolated enforces an iptables + ipset domain allowlist inside
	// the sandbox. The agent's default allowlist plus any AllowDomains
	// added via RunOptions are permitted; everything else is blocked.
	// Requires a backend that supports network isolation
	// (BackendCaps.NetworkIsolation = true).
	NetworkIsolated NetworkMode = "isolated"

	// NetworkNone disables outbound traffic entirely. Stronger than
	// NetworkIsolated — the agent cannot reach the LLM API either, so
	// only suitable for fully offline workflows.
	NetworkNone NetworkMode = "none"
)

// RunOptions configures Run / Create. Field set unifies the current
// `yoloai.Client.Run`'s subset with the full surface of `sandbox.CreateOptions`
// (the CLI's `yoloai new` flag set). Fields below match the CLI flag names
// 1:1 unless noted; field tags are advisory for future YAML/JSON marshaling.
type RunOptions struct {
	Name    string  // required; sandbox identifier
	Workdir DirSpec // primary work directory; required (set Path + Mode)

	AuxDirs []DirSpec // additional `-d <dir>` mounts (read-only by default)

	Agent   string // "claude", "gemini", "codex", "opencode", "aider", "test"; default: config or "claude"
	Model   string // agent-specific model id or alias; default: agent default
	Profile string // profile name; default: none
	Prompt  string // initial prompt sent to the agent; default: empty (interactive)

	Isolation string // "container", "container-enhanced", "container-privileged", "vm", "vm-enhanced"
	OS        string // "linux" (default) or "mac" (routes to seatbelt/tart)

	Network      NetworkMode // network policy; zero value = open (default)
	AllowDomains []string    // initial allowlist; only meaningful when Network == NetworkIsolated

	Env       map[string]string // YOLOAI_BUILD_* and YOLOAI_RUNTIME_* vars merged into the agent env
	Mounts    []string          // raw `--mount` strings, parsed and validated by the runtime
	Ports     []string          // raw `--port <host:container>` mappings
	Secrets   []string          // build-time secrets (`id=…,src=…`); validated and tilde-expanded
	BuildArgs map[string]string // --build-arg key=value pairs for the image build

	Replace bool // destroy any existing sandbox with the same name first
	Yes     bool // skip interactive confirmation prompts (dirty repos, replace warnings, ...)
	NoStart bool // create state but do not start the container yet (today: --no-start)

	Runtimes []string // tart-only: pre-built Apple simulator runtime base ("ios:26.4", "tvos")

	// Wait, if true, blocks until the agent reaches a terminal status
	// (StatusDone, StatusFailed, StatusStopped). Mirrors the existing
	// Client.Run(opts.Wait) semantic. OnProgress receives status updates.
	Wait       bool
	OnProgress func(name, msg string)
}

// CloneOptions configures Clone. Mirrors `sandbox.CloneOptions`.
type CloneOptions struct {
	Source string // existing sandbox name
	Dest   string // new sandbox name (must not exist; not subject to Replace)
}

// StartOptions configures Start. Mirrors today's `sandbox.StartOptions`.
type StartOptions struct {
	Attach            bool   // also attach to the tmux session after start
	Prompt            string // optional prompt to inject after agent relaunch
	IsolationOverride string // change isolation mode on restart; rebuilds container
}

// StopOptions configures Stop. Currently empty — the CLI flags (`--all`,
// wildcards) are CLI-side argument parsing, not options to the underlying
// per-sandbox operation. Reserved for future use (e.g., --timeout).
type StopOptions struct{}

// RestartOptions configures Restart (CLI `restart` command). Today the CLI
// composes Stop+Start; surfacing Restart as a single method lets future
// optimizations land without a CLI rewrite.
type RestartOptions struct {
	Attach bool
}

// DestroyOptions configures Destroy. `Force` replaces the existing
// `force bool` arg; richer struct lets us add dry-run, timeout, etc. later.
type DestroyOptions struct {
	Force bool // destroy even when the sandbox has unapplied changes
}

// ResetOptions mirrors today's `sandbox.ResetOptions`. The CLI flags
// `--restart`, `--clear-state`, `--keep-cache`, `--keep-files`, `--attach`,
// `--prompt` all map 1:1.
type ResetOptions struct {
	Name       string
	Restart    bool
	ClearState bool
	KeepCache  bool
	KeepFiles  bool
	Attach     bool
	Prompt     string
}

// WaitOptions configures Wait — the new method that yields the agent's exit
// code (D17 / OPEN_QUESTIONS Q77).
type WaitOptions struct {
	// Timeout caps how long Wait blocks; 0 = no timeout. On expiry, Wait
	// returns ctx.Err() (DeadlineExceeded) with exitCode=-1.
	Timeout time.Duration

	// PollInterval overrides the 5 s default poll cadence. Embedders running
	// many concurrent waits may want a longer interval to reduce load.
	PollInterval time.Duration

	// OnStatus, if set, is invoked once per poll with the current status.
	// Safe to call concurrently from multiple goroutines (per-Wait call
	// site only).
	OnStatus func(status string)
}

func (clientAPI) Run(ctx context.Context, opts RunOptions) (*Info, error)     { panic("design-only") }
func (clientAPI) Clone(ctx context.Context, opts CloneOptions) (*Info, error) { panic("design-only") }
func (clientAPI) Start(ctx context.Context, name string, opts StartOptions) error {
	panic("design-only")
}
func (clientAPI) Stop(ctx context.Context, name string, opts StopOptions) error { panic("design-only") }
func (clientAPI) Restart(ctx context.Context, name string, opts RestartOptions) error {
	panic("design-only")
}
func (clientAPI) Destroy(ctx context.Context, name string, opts DestroyOptions) error {
	panic("design-only")
}
func (clientAPI) Reset(ctx context.Context, opts ResetOptions) error { panic("design-only") }

// Wait blocks until the named sandbox reaches a terminal status
// (StatusDone, StatusFailed, StatusStopped) or ctx is cancelled. Returns
// the agent's exit code (0 for StatusDone with a clean agent, non-zero
// otherwise) and a wrapped error on cancel / timeout / inspect failure.
// CLI: new `yoloai wait <name>` command lands in W-L8b.
func (clientAPI) Wait(ctx context.Context, name string, opts WaitOptions) (exitCode int, err error) {
	panic("design-only")
}

// =============================================================================
// Read / inspect  (CLI: list, sandbox <name> info, sandbox <name> prompt)
// =============================================================================

// ListOptions filters the List output. The CLI's `yoloai ls --active`,
// `--idle`, `--agent claude`, `--profile foo`, etc., map to fields here.
type ListOptions struct {
	Statuses []string // "active", "idle", "done", "stopped", "failed", "broken"; empty = all
	Agents   []string // filter by agent name
	Profiles []string // filter by profile (use "" for unprofiled)
	Changes  bool     // only sandboxes with unapplied changes
}

func (clientAPI) List(ctx context.Context, opts ListOptions) ([]*Info, error) { panic("design-only") }
func (clientAPI) Inspect(ctx context.Context, name string) (*Info, error)     { panic("design-only") }
func (clientAPI) Status(ctx context.Context, name string) (Status, error)     { panic("design-only") }

// Prompt returns the original prompt the sandbox was created with. CLI:
// `yoloai sandbox <name> prompt`.
func (clientAPI) Prompt(ctx context.Context, name string) (string, error) { panic("design-only") }

// =============================================================================
// Workflow: diff / apply / baseline  (CLI: diff, apply, baseline …)
// =============================================================================

// DiffOptions configures Diff. The CLI's `yoloai diff <name> [<ref>] [-- <path>...]`
// translates `<ref>` into Ref and the path tail into Paths.
type DiffOptions struct {
	Ref      string   // single ref or "A..B" range; "" = full agent diff
	Paths    []string // pathspec filters; "" = all paths
	Stat     bool     // --stat: per-file insertion/deletion summary
	NameOnly bool     // --name-only: just the file list, no hunks
}

// DiffResult mirrors `patch.DiffResult` (which itself aliases workspace.DiffResult).
// Re-exported here so embedders don't have to import the patch package.
type DiffResult struct {
	Dir    string // sandbox dir relative path
	Mode   string // "copy" or "overlay"
	Patch  string // unified diff text
	Stat   string // populated when DiffOptions.Stat = true
	IsBase bool   // workdir vs auxiliary dir
}

func (clientAPI) Diff(ctx context.Context, name string, opts DiffOptions) ([]*DiffResult, error) {
	panic("design-only")
}

// ApplyMode selects how Apply emits its output. The three modes were
// previously represented by overlapping booleans (Squash, PatchesDir) with
// MarkFlagsMutuallyExclusive guarding the invalid pairs at the CLI layer;
// here the mutex moves into the type.
type ApplyMode string

const (
	// ApplyDefault commits the agent's changes back to the host repo using
	// git format-patch + git am (or overlay-aware exec for :overlay dirs).
	// This is the zero value so callers who don't care get the right default.
	ApplyDefault ApplyMode = ""

	// ApplySquash flattens all in-scope changes into a single unstaged
	// patch on the host. Mutex with ApplyExport. Mutex with WithTags
	// (squashing collapses commit boundaries; tags don't survive).
	ApplySquash ApplyMode = "squash"

	// ApplyExport writes one *.patch file per commit to ExportDir and does
	// not touch the host repo. Mutex with ApplySquash and DryRun (export
	// already doesn't apply anything, so DryRun is meaningless).
	ApplyExport ApplyMode = "export"
)

// ApplyOptions configures Apply.
//
// **Gap closed by this design:** today `yoloai.Client.Apply` only handles
// the basic commits-only path. ApplyOptions surfaces the full CLI surface
// (audit findings: overlay apply, format-patch apply, selective apply).
//
// Validation: Mode-incompatible combinations (e.g. ApplyExport + DryRun,
// ApplySquash + WithTags) return a UsageError from the Client method; the
// CLI no longer needs MarkFlagsMutuallyExclusive once it routes through
// this type.
type ApplyOptions struct {
	Mode       ApplyMode // ApplyDefault, ApplySquash, or ApplyExport
	ExportDir  string    // required when Mode == ApplyExport; ignored otherwise
	Refs       []string  // specific commits or ranges; empty = all agent commits
	Paths      []string  // pathspec filter; empty = all paths
	IncludeWIP bool      // also apply uncommitted edits as unstaged changes
	DryRun     bool      // print what would be applied; invalid with ApplyExport
	WithTags   bool      // also transfer git tags created by the agent; invalid with ApplySquash
	Yes        bool      // skip interactive confirmation prompts
}

// ApplyResult mirrors `patch.ApplyResult` plus a top-level rollup for
// callers that don't want to iterate per-directory.
type ApplyResult struct {
	Per     []*PerDirApplyResult
	Patches []string // populated only when Mode == ApplyExport: written *.patch files
	Skipped []string // dirs skipped (overlay-and-running-required, :rw, …)
}

// PerDirApplyResult is the existing `patch.ApplyResult` shape lifted here.
type PerDirApplyResult struct {
	Dir        string
	Applied    bool
	Conflicts  bool
	Patch      string // dry-run only
	ErrMessage string // non-empty if this dir errored without aborting the run
}

func (clientAPI) Apply(ctx context.Context, name string, opts ApplyOptions) (*ApplyResult, error) {
	panic("design-only")
}

// Baseline operations. CLI: `yoloai baseline {advance|set|log} <name>`.

type BaselineEntry struct {
	When time.Time
	SHA  string
	Note string
}

func (clientAPI) AdvanceBaseline(ctx context.Context, name string) error { panic("design-only") }
func (clientAPI) SetBaseline(ctx context.Context, name, sha string) error {
	panic("design-only")
}
func (clientAPI) BaselineLog(ctx context.Context, name string) ([]BaselineEntry, error) {
	panic("design-only")
}

// =============================================================================
// Streaming / interactive  (CLI: attach, exec, log, mcp proxy)
// =============================================================================

// AttachOptions configures Attach. Modeled on `docker attach`; today's CLI
// flag set is minimal. **OPEN-Q:** add a DetachKeys field for callers that
// want to override `Ctrl-b d`? Defer until a real consumer asks.
type AttachOptions struct{}

// Attach blocks until the user detaches (or the agent exits). Requires
// IOStreams.TTY=true; non-TTY attach returns a UsageError.
// Backend must register a fixed tmux socket via `runtime-config.json`.
func (clientAPI) Attach(ctx context.Context, name string, opts AttachOptions, io IOStreams) error {
	panic("design-only")
}

// ExecOptions configures Exec. The split between interactive and
// non-interactive exec is signalled by IOStreams.TTY at call time, not by
// a flag — same shape as `kubectl exec`.
type ExecOptions struct {
	Command []string          // the command to run inside the sandbox
	Env     map[string]string // extra env vars for the exec'd process
	WorkDir string            // working dir inside the sandbox; empty = container default
	User    string            // override the run-as user; empty = container default
}

// ExecResult is returned for non-streaming Exec calls. When IOStreams is
// provided and TTY=true, the result fields are unused — output went
// straight to IOStreams.Out / Err.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs cmd inside the sandbox. When io.TTY is true the call streams
// stdio through io and blocks until the command exits; the returned
// ExecResult.Stdout/Stderr are empty in that case. Non-TTY callers leave
// io.In nil and read Stdout/Stderr from the result.
func (clientAPI) Exec(ctx context.Context, name string, opts ExecOptions, io IOStreams) (*ExecResult, error) {
	panic("design-only")
}

// LogFormat selects which log stream to emit. Replaces the previous
// (Agent, AgentRaw, Raw) boolean triplet — at most one was meant to be
// true, modeled here as one field.
type LogFormat string

const (
	// LogStructured is the default: pretty-printed merge-sorted stream of
	// all four structured JSONL logs (cli, sandbox, monitor, hooks). Maps
	// to today's `yoloai log <name>` with no flag.
	LogStructured LogFormat = ""

	// LogStructuredRaw emits the same structured log as raw JSONL lines.
	// Maps to `--raw`.
	LogStructuredRaw LogFormat = "raw"

	// LogAgent emits the agent's terminal output with ANSI escape
	// sequences stripped. Maps to `--agent`.
	LogAgent LogFormat = "agent"

	// LogAgentRaw emits the raw agent terminal stream (ANSI preserved).
	// Maps to `--agent-raw`.
	LogAgentRaw LogFormat = "agent-raw"
)

// LogOptions configures Logs. Maps directly to the CLI's `yoloai log <name>`
// flag set after the JSONL redesign (see BREAKING-CHANGES "sandbox <name> log
// redesigned").
type LogOptions struct {
	Format   LogFormat     // which stream to emit; default = LogStructured
	Sources  []string      // --source: filter to cli, sandbox, monitor, hooks (only meaningful for LogStructured / LogStructuredRaw)
	MinLevel string        // --level: debug|info|warn|error
	Since    time.Duration // --since: time window (0 = no filter)
	Follow   bool          // --follow / -f: tail live; returns when sandbox is done
}

// Logs streams the requested log to w. Returns nil when the source is
// exhausted (or, with Follow=true, when the sandbox reaches a terminal
// status). The caller is responsible for w being a TTY-suitable writer if
// they expect color / pagination.
func (clientAPI) Logs(ctx context.Context, name string, opts LogOptions, w io.Writer) error {
	panic("design-only")
}

// ProxyMCP bridges an outer agent's stdio (via io.In / io.Out) to an inner
// MCP server running inside the sandbox. Requires the backend to implement
// `runtime.StdioExecer`; returns a UsageError when it doesn't. CLI:
// `yoloai mcp proxy <name>`.
func (clientAPI) ProxyMCP(ctx context.Context, name string, io IOStreams) error {
	panic("design-only")
}

// =============================================================================
// Files exchange  (CLI: files <name> {put|get|ls|rm|path})
// =============================================================================

// FilesPutOptions configures FilesPut. Source globs are expanded by the
// caller (CLI) before invocation; Client doesn't do host glob resolution.
type FilesPutOptions struct {
	Sources []string // host paths (already glob-expanded by caller)
	Force   bool     // overwrite existing files
}

// FilesGetOptions configures FilesGet. Patterns are matched inside the
// sandbox exchange dir; Output is a host directory (or file path for a
// single-file get).
type FilesGetOptions struct {
	Patterns []string // glob patterns evaluated inside the sandbox
	Output   string   // host destination dir/file
	Force    bool     // overwrite existing destination paths
}

// FileEntry describes a single file in the exchange directory.
type FileEntry struct {
	Path string // path relative to the exchange dir
	Size int64  // bytes
	Mode uint32 // unix file mode
}

func (clientAPI) FilesPut(ctx context.Context, name string, opts FilesPutOptions) error {
	panic("design-only")
}
func (clientAPI) FilesGet(ctx context.Context, name string, opts FilesGetOptions) (written []string, err error) {
	panic("design-only")
}
func (clientAPI) FilesLs(ctx context.Context, name string, patterns []string) ([]FileEntry, error) {
	panic("design-only")
}
func (clientAPI) FilesRm(ctx context.Context, name string, patterns []string) error {
	panic("design-only")
}

// FilesPath returns the host path to a sandbox's exchange directory. Pure
// path lookup, no Client state needed — exposed as a Client method anyway
// so CLI code paths stay uniform (always "ask the Client").
func (clientAPI) FilesPath(name string) string { panic("design-only") }

// =============================================================================
// Network allowlist  (CLI: sandbox <name> {allow|allowed|deny})
// =============================================================================

func (clientAPI) AllowDomains(ctx context.Context, name string, domains []string) error {
	panic("design-only")
}
func (clientAPI) AllowedDomains(ctx context.Context, name string) ([]string, error) {
	panic("design-only")
}
func (clientAPI) DenyDomains(ctx context.Context, name string, domains []string) error {
	panic("design-only")
}

// =============================================================================
// Bug report  (CLI: sandbox <name> bugreport [safe|unsafe], top-level --bugreport)
// =============================================================================

// BugReportMode selects redaction level for BugReport. Today the CLI
// takes a positional "safe" / "unsafe" arg — typed enum here keeps
// invalid third values out of the type.
type BugReportMode string

const (
	// BugReportSafe redacts credentials, API keys, and other sensitive
	// content from logs before inclusion. Default and recommended for
	// reports filed publicly.
	BugReportSafe BugReportMode = "safe"

	// BugReportUnsafe includes full unredacted content. Use only for
	// private debugging where the report won't be shared.
	BugReportUnsafe BugReportMode = "unsafe"
)

// BugReportOptions configures BugReport.
type BugReportOptions struct {
	Mode BugReportMode // zero value is BugReportSafe via Client method default
}

// BugReport collects sandbox + system + log diagnostics and writes them to
// a single markdown file in CWD. Returns the absolute path of the written
// file. The top-level `--bugreport` flag (flight recorder for any CLI
// invocation) is implemented as middleware on the CLI side that calls this
// method on panic / error — it remains a CLI concern.
func (clientAPI) BugReport(ctx context.Context, name string, opts BugReportOptions) (path string, err error) {
	panic("design-only")
}

// =============================================================================
// Admin: backends + agents + system info  (CLI: system info|backends|agents)
// =============================================================================

// BackendInfo bundles a BackendDescriptor with its current Probe verdict so
// the CLI can render the table without re-probing.
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

// AgentInfo is the public face of `agent.Definition` (which lives in
// internal/ after Phase 5). Reexported so the CLI doesn't have to import
// internal/agent.
type AgentInfo struct {
	Name          string
	Description   string
	PromptMode    string
	APIKeyEnvVars []string
	ModelAliases  map[string]string
}

func (clientAPI) Backends(ctx context.Context) ([]BackendInfo, error) { panic("design-only") }
func (clientAPI) Backend(ctx context.Context, name string) (*BackendInfo, error) {
	panic("design-only")
}
func (clientAPI) Agents() []AgentInfo                   { panic("design-only") }
func (clientAPI) Agent(name string) (*AgentInfo, error) { panic("design-only") }

// SystemInfo bundles paths + disk usage + version metadata for the
// `yoloai system info` command.
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

func (clientAPI) SystemInfo(ctx context.Context) (*SystemInfo, error) { panic("design-only") }

// =============================================================================
// Admin: build / check / disk / doctor / prune / setup
// =============================================================================

type BuildOptions struct {
	Profile string   // build a profile image; "" = base image only
	Backend string   // explicit backend to build for; "" = config default
	All     bool     // build across every available backend (mutually exclusive with Backend)
	Force   bool     // rebuild even when checksum says it's current
	Secrets []string // --secret id=…,src=… ; validated by the caller
}

type DiskUsage struct {
	Sandboxes  int64
	PerBackend []BackendDiskUsage
}

type BackendDiskUsage struct {
	Name   string
	Bytes  int64  // -1 when unknown
	Detail string // human-readable hint
	Error  string // non-empty on probe failure
}

type DoctorOptions struct {
	Backend   string // filter to one backend
	Isolation string // filter to one isolation mode
}

type DoctorReport struct {
	Backend      string
	Mode         string
	IsBaseMode   bool
	Availability string // "ready" | "unavailable" | "warning"
	InitErr      error
	MissingCaps  []string // names of failed RequiredCapabilities checks
}

type PruneOptions struct {
	Backend      string // limit pruning to one backend; "" = all available
	DryRun       bool
	Cache        bool // also prune backend image/snapshot cache (forces base rebuild)
	IncludeStale bool // include stale yoloai temp dirs older than --keep-newer
}

type PruneResult struct {
	RemovedItems []string
	FreedBytes   int64
}

// SetupOptions mirrors `sandbox.SetupOptions` (TmuxConf, Backend, Agent).
type SetupOptions struct {
	TmuxConf string // "default", "default+host", "host", "none"
	Backend  string
	Agent    string
}

func (clientAPI) Build(ctx context.Context, opts BuildOptions) error { panic("design-only") }
func (clientAPI) Check(ctx context.Context) error                    { panic("design-only") }
func (clientAPI) DiskUsage(ctx context.Context) (*DiskUsage, error)  { panic("design-only") }
func (clientAPI) Doctor(ctx context.Context, opts DoctorOptions) ([]DoctorReport, error) {
	panic("design-only")
}
func (clientAPI) Prune(ctx context.Context, opts PruneOptions) (*PruneResult, error) {
	panic("design-only")
}
func (clientAPI) Setup(ctx context.Context, opts SetupOptions) error { panic("design-only") }

// =============================================================================
// Exceptions — CLI commands that do NOT go through Client
// =============================================================================

// The following CLI commands are intentionally not on Client. Each
// exception is justified.
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
//                                 `internal/config` directly. `config/` is a
//                                 leaf utility package, not an orchestration
//                                 dependency — W-L8e's import ban targets
//                                 `internal/sandbox` and `internal/runtime`.
//
//   profile create|list|info|delete   Same rationale as `config`:
//                                 file-system ops on `~/.yoloai/profiles/`
//                                 with no sandbox state. CLI uses `config/`
//                                 and `os.RemoveAll` directly; the
//                                 cleanup-hint iteration over descriptors
//                                 (W-L5) already goes through `runtime/`.
//
//   sandbox <name> vscode         Spawns external `code` binary with a
//                                 sandbox-targeted attach URL. CLI calls
//                                 Client.Inspect to get container name +
//                                 image, then exec.Command("code", …)
//                                 directly. The external-process launch is
//                                 CLI work, not orchestration.
//
//   mcp serve                     The MCP server itself CONSUMES Client (it
//                                 is a peer to the CLI surface). The
//                                 `serve` command's body is `mcpsrv.New
//                                 (yoloai.Client).Run(ctx)` after Phase 3.
//
//   system tart                   Backend-scoped per Pattern B (W-L2).
//                                 Imports `runtime/tart` directly. The
//                                 W-L10 enforcement linter explicitly
//                                 allowlists `system_tart.go`.
//
//   x [extension]                 Extension dispatcher (yoloai x <ext>
//                                 forwards to an executable in
//                                 `~/.yoloai/extensions/`). Pure CLI shell
//                                 invocation; no sandbox state.

// =============================================================================
// Re-exported types
// =============================================================================
//
// The real implementation will re-export the following types from internal
// packages so embedders don't pull internal/ paths. Listed here for review;
// W-L8b lands the actual aliases.

// Status re-exports sandbox.Status. (TBD: aliases vs typedefs vs duplicate
// constants. Recommended: aliases, like the existing sandbox.UsageError =
// yoerrors.UsageError pattern.)
type Status = string

// Info re-exports sandbox.Info.
type Info = struct{}

// DirSpec re-exports sandbox.DirSpec.
type DirSpec struct {
	Path string
	Mode string // "copy" (default), "overlay", "rw"
}

// =============================================================================
// Open questions for the reviewer
// =============================================================================
//
// Q-A.  ListOptions vs separate filter methods?  Today the CLI parses
//       `--active --idle --agent foo` into runtime filters. Could expose as
//       method options (chosen) or as separate methods (List, ListByAgent).
//       Recommendation: ListOptions (one method, ergonomic for embedders).
//
// Q-B.  Error mapping.  W7 (architecture-remediation) lands typed errors
//       under `internal/yoerrors`. Should the Client surface promise
//       specific sentinels (ErrSandboxNotFound, ErrUnappliedChanges,
//       ErrBackendUnavailable) as part of its stability contract? D3 defers
//       the broader stability question. Recommendation: document the three
//       sentinels already aliased in `yoloai.go` as stable; everything
//       else is internal.
//
// Q-C.  Streaming-vs-buffered ergonomics for `Logs` / `Diff` / `Apply`.
//       `Logs` already takes an `io.Writer`. Should `Diff` / `Apply` also
//       support streaming (large diffs OOM the result type)? Recommended
//       for V1: keep result-typed return (CLI consumers already use this
//       shape; embedders for bulk diffing are rare). Add streaming
//       variants on demand.
//
// Q-D.  Where do `RunOptions.Wait` and `Wait()` overlap?  Run(Wait=true)
//       returns *Info; Wait() returns exit code + error. They serve
//       different consumers (sync `new --wait` users vs scripted
//       `yoloai wait`). Recommendation: keep both. The CLI's `wait`
//       command translates Wait()'s int back into a process exit code.
//
// Q-E.  Should `BugReport` collect the `--bugreport unsafe` flight
//       recorder log automatically?  Today `--bugreport` is a middleware
//       wrapper around every CLI command that captures runtime events.
//       The on-demand `sandbox <name> bugreport` is distinct. Recommendation:
//       expose only the on-demand version on Client; the flight recorder
//       stays CLI middleware.
//
// Q-F.  `Setup()` on Client interactive prompts?  Setup is the only
//       non-streaming method that today reads from stdin (tmux conf prompt,
//       backend choice, agent choice). Options: (1) keep stdin-reading in
//       Client (Manager already does), (2) require all answers in
//       SetupOptions before calling. Recommendation: SetupOptions for
//       embedders + a separate `RunInteractiveSetup(ctx, IOStreams)` for
//       the CLI's wizard.
//
// Q-G.  Should `Setup` / `Build` / `Doctor` move to a `yoloai.Admin{}`
//       sub-Client to keep the main surface focused?  Today's `Client` is
//       sandbox-focused; bolting admin onto it grows the type. Recommended:
//       single Client for V1, split if it gets >40 methods. Currently
//       ~30 (including admin), comfortable.

// clientAPI is a synthetic interface type that exists only so the methods
// above type-check as Go. The real W-L8b implementation declares each
// method on `*Client` directly. Reviewers should read the methods as
// `func (c *Client) X(...)`, not as interface members.
type clientAPI struct{}
