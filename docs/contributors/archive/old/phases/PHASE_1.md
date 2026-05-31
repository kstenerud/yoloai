# Phase 1: Core Domain Types and Path Encoding

## Goal

Pure Go domain types and utility functions — fully unit-testable without Docker. Establishes the testing infrastructure (testify) and the core data model used by all subsequent phases.

## Prerequisites

- Phase 0 complete (compilable project, Cobra CLI)
- `go get github.com/stretchr/testify` (test assertions)

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/paths.go` | Caret encoding (full spec), directory layout helpers |
| `internal/sandbox/paths_test.go` | Table-driven encoding round-trip tests |
| `internal/sandbox/meta.go` | MVP meta.json struct and I/O |
| `internal/sandbox/meta_test.go` | Serialization round-trip tests |
| `internal/sandbox/parse.go` | Directory argument parsing (`:copy`/`:rw`/`:force`) |
| `internal/sandbox/parse_test.go` | Parsing tests including suffix combinations |
| `internal/agent/agent.go` | Agent definitions (Claude, test) |
| `internal/agent/agent_test.go` | Agent lookup tests |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/root.go` | Move `UsageError`/`ConfigError` to `internal/sandbox/errors.go` — **no, defer this.** Phase 4a creates `errors.go`. For now the types stay in `root.go` and Phase 4a will move them. No changes to existing files in this phase. |

## Types and Signatures

### `internal/sandbox/paths.go`

```go
package sandbox

// EncodePath encodes a host path using the caret encoding spec
// (https://github.com/kstenerud/caret-encoding) for use as a
// filesystem-safe directory name.
func EncodePath(hostPath string) string

// DecodePath reverses caret encoding back to the original path.
func DecodePath(encoded string) (string, error)

// SandboxDir returns the host-side state directory for a sandbox.
//   ~/.yoloai/sandboxes/<name>/
func SandboxDir(name string) string

// WorkDir returns the host-side work directory for a specific
// copy-mode mount within a sandbox.
//   ~/.yoloai/sandboxes/<name>/work/<caret-encoded-path>/
func WorkDir(name string, hostPath string) string
```

**Caret encoding rules** (full spec):

Characters that MUST be encoded (replaced with `^XX` where XX is the uppercase hex of the codepoint):

- All control characters: U+0000–U+001F, U+007F
- The caret itself: `^` (U+005E)
- URI-reserved and filesystem-unsafe characters: `! " # $ % & ' ( ) * + , / : ; < = > ? @ [ \ ] | ~`
- Space: U+0020
- Period: `.` (U+002E)

Characters that are safe (NOT encoded):
- Alphanumeric: `0-9`, `A-Z`, `a-z`
- Hyphen: `-` (U+002D)
- Underscore: `_` (U+005F)
- Backtick: `` ` `` (U+0060)
- Braces: `{` `}` (U+007B, U+007D)

Encoding format by codepoint range (use the shortest representation):

| Codepoint Range | Format | Example |
|-----------------|--------|---------|
| U+0000–U+00FF | `^XX` (2 hex digits) | `/` → `^2F` |
| U+0100–U+0FFF | `^gXXX` (3 hex digits) | `Ł` → `^g141` |
| U+1000–U+FFFF | `^hXXXX` (4 hex digits) | `‡` → `^h2021` |
| U+10000–U+FFFFF | `^iXXXXX` (5 hex digits) | `𝄠` → `^i1D120` |
| U+100000–U+10FFFF | `^jXXXXXX` (6 hex digits) | |

Hex digits in output are uppercase. Decoding is case-insensitive for both modifiers and hex digits.

**Decoding rules:**
1. Scan for `^`
2. Check next character: if `g`/`G`/`h`/`H`/`i`/`I`/`j`/`J`, it's a modifier — consume the corresponding number of hex digits (3/4/5/6)
3. Otherwise, no modifier — consume 2 hex digits
4. Convert hex to Unicode codepoint, emit the character
5. Return error on malformed sequences (truncated hex, invalid codepoint)

**`SandboxDir` and `WorkDir`** use `os.UserHomeDir()` to resolve `~`.

### `internal/sandbox/meta.go`

```go
package sandbox

import "time"

// Meta holds sandbox configuration captured at creation time.
// Simplified for MVP: single workdir only, no directories array, no resources.
type Meta struct {
	YoloaiVersion string    `json:"yoloai_version"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`

	Agent string `json:"agent"`
	Model string `json:"model,omitempty"` // omit if agent default was used

	Workdir WorkdirMeta `json:"workdir"`

	HasPrompt   bool     `json:"has_prompt"`
	NetworkMode string   `json:"network_mode,omitempty"` // "" = default, "none" = --network-none
	Ports       []string `json:"ports,omitempty"`        // e.g. ["3000:3000"]
}

// WorkdirMeta stores the resolved workdir state at creation time.
type WorkdirMeta struct {
	HostPath    string `json:"host_path"`
	MountPath   string `json:"mount_path"`
	Mode        string `json:"mode"` // "copy" or "rw"
	BaselineSHA string `json:"baseline_sha,omitempty"`
}

// SaveMeta writes meta.json to the given directory path.
func SaveMeta(dir string, meta *Meta) error

// LoadMeta reads meta.json from the given directory path.
func LoadMeta(dir string) (*Meta, error)
```

`SaveMeta` writes JSON with `json.MarshalIndent` (2-space indent) and `0644` permissions. `LoadMeta` reads and unmarshals.

### `internal/agent/agent.go`

```go
package agent

import "time"

// PromptMode determines how the agent receives its initial prompt.
type PromptMode string

const (
	// PromptModeInteractive feeds prompt via tmux send-keys after startup.
	PromptModeInteractive PromptMode = "interactive"
	// PromptModeHeadless passes prompt as a CLI argument in the launch command.
	PromptModeHeadless PromptMode = "headless"
)

// Definition describes an agent's install, launch, and behavioral characteristics.
type Definition struct {
	Name           string
	InteractiveCmd string            // command for interactive mode (e.g. "claude --dangerously-skip-permissions")
	HeadlessCmd    string            // command template for headless mode; PROMPT is replaced with the actual prompt
	PromptMode     PromptMode        // default prompt delivery mode
	APIKeyEnvVars  []string          // env vars to inject (e.g. ["ANTHROPIC_API_KEY"])
	StateDir       string            // agent state directory inside container (e.g. "/home/yoloai/.claude/"), empty if none
	SubmitSequence string            // tmux send-keys sequence for interactive prompt delivery (e.g. "Enter Enter")
	StartupDelay   time.Duration     // wait before sending prompt in interactive mode
	ModelFlag      string            // flag name for model selection (e.g. "--model"), empty if not supported
	ModelAliases   map[string]string // short alias → full model name (e.g. "sonnet" → "claude-sonnet-4-latest")
}

// GetAgent returns the agent definition for the given name.
// Returns nil if the agent is not known.
func GetAgent(name string) *Definition
```

**Agent definitions:**

Claude:
- `Name`: `"claude"`
- `InteractiveCmd`: `"claude --dangerously-skip-permissions"`
- `HeadlessCmd`: `"claude -p \"PROMPT\" --dangerously-skip-permissions"`
- `PromptMode`: `PromptModeInteractive`
- `APIKeyEnvVars`: `["ANTHROPIC_API_KEY"]`
- `StateDir`: `"/home/yoloai/.claude/"`
- `SubmitSequence`: `"Enter Enter"`
- `StartupDelay`: `3 * time.Second`
- `ModelFlag`: `"--model"`
- `ModelAliases`: `{"sonnet": "claude-sonnet-4-latest", "opus": "claude-opus-4-latest", "haiku": "claude-haiku-4-latest"}`

Test:
- `Name`: `"test"`
- `InteractiveCmd`: `"bash"`
- `HeadlessCmd`: `"sh -c \"PROMPT\""`
- `PromptMode`: `PromptModeHeadless`
- `APIKeyEnvVars`: `[]string{}` (empty, not nil)
- `StateDir`: `""` (none)
- `SubmitSequence`: `"Enter"`
- `StartupDelay`: `0`
- `ModelFlag`: `""` (ignored)
- `ModelAliases`: `nil`

`GetAgent` does a simple map lookup on the agent name. Returns `nil` for unknown agents.

### `internal/sandbox/parse.go`

```go
package sandbox

// DirArg holds the parsed components of a directory argument.
type DirArg struct {
	Path  string // resolved absolute path
	Mode  string // "copy", "rw", or "ro"
	Force bool   // :force was specified
}

// ParseDirArg parses a directory argument with optional suffixes.
// Suffixes (:copy, :rw, :force) can be combined in any order.
// Default mode (no :copy or :rw) is determined by the caller
// (workdir defaults to "copy", aux dirs default to "ro").
//
// Examples:
//   "./my-app"            → {Path: "/abs/my-app", Mode: "", Force: false}
//   "./my-app:copy"       → {Path: "/abs/my-app", Mode: "copy", Force: false}
//   "./my-app:rw"         → {Path: "/abs/my-app", Mode: "rw", Force: false}
//   "./my-app:rw:force"   → {Path: "/abs/my-app", Mode: "rw", Force: true}
//   "./my-app:force"      → {Path: "/abs/my-app", Mode: "", Force: true}
//   "$HOME:force"         → {Path: "/home/user", Mode: "", Force: true}
func ParseDirArg(arg string) (*DirArg, error)
```

**Parsing rules:**
1. Split on `:` from the right, recognizing only known suffixes (`copy`, `rw`, `force`). Unknown suffixes are treated as part of the path (paths can contain colons on Linux, though rare).
2. Error if both `:copy` and `:rw` are specified.
3. Resolve path to absolute via `filepath.Abs`.
4. Mode is `""` (empty) when neither `:copy` nor `:rw` is specified — the caller applies the default.

## Implementation Steps

1. **Add testify dependency:**
   ```
   go get github.com/stretchr/testify
   ```

2. **Create `internal/sandbox/paths.go`:**
   - Build the `mustEncode` character set (use a `[256]bool` lookup table for ASCII fast-path, plus check for non-ASCII codepoints > U+007F that need encoding)
   - `EncodePath`: iterate runes, encode unsafe characters using shortest caret representation
   - `DecodePath`: scan for `^`, parse modifier + hex digits, convert to rune
   - `SandboxDir`, `WorkDir`: compose paths using `os.UserHomeDir()` and `EncodePath`

3. **Create `internal/sandbox/paths_test.go`:**
   - Table-driven round-trip tests covering: plain paths (`/home/user/project`), paths with spaces, paths with special characters, the caret character itself, non-ASCII paths (e.g. Unicode directory names), empty string edge case
   - Direct tests for `SandboxDir` and `WorkDir` output format

4. **Create `internal/sandbox/meta.go`:**
   - `SaveMeta`: marshal with `json.MarshalIndent`, write with `os.WriteFile`
   - `LoadMeta`: read with `os.ReadFile`, unmarshal

5. **Create `internal/sandbox/meta_test.go`:**
   - Round-trip test: create Meta, save to temp dir, load back, assert equal
   - Test omitempty behavior: Model empty → field absent in JSON
   - Test with ports and network mode set

6. **Create `internal/agent/agent.go`:**
   - Define `agents` map with Claude and test definitions
   - `GetAgent` looks up by name

7. **Create `internal/agent/agent_test.go`:**
   - Test `GetAgent("claude")` returns expected fields
   - Test `GetAgent("test")` returns expected fields (no API keys, no state dir, 0 delay)
   - Test `GetAgent("unknown")` returns nil

8. **Create `internal/sandbox/parse.go`:**
   - Parse suffixes from right, collecting known suffixes
   - Validate no `:copy` + `:rw` conflict
   - Resolve to absolute path

9. **Create `internal/sandbox/parse_test.go`:**
   - Table-driven tests: bare path, `:copy`, `:rw`, `:force`, `:rw:force`, `:copy:force`, `:force:rw` (order doesn't matter), both `:copy:rw` (error), path with colons in name, relative path resolution

10. **Run `go mod tidy`.**

## Tests

### `internal/sandbox/paths_test.go`

```go
func TestEncodePath_BasicPaths(t *testing.T)
// Table: {input, expected}
// - "/home/user/project" → "^2Fhome^2Fuser^2Fproject"
// - "/tmp/test" → "^2Ftmp^2Ftest"
// - "simple" → "simple" (no unsafe chars)

func TestEncodePath_SpecialCharacters(t *testing.T)
// Table: {input, expected}
// - path with spaces: "/my dir" → "^2Fmy^20dir"
// - path with caret: "/foo^bar" → "^2Ffoo^5Ebar"
// - path with colons: "/foo:bar" → "^2Ffoo^3Abar"
// - path with dots: "/foo.bar" → "^2Ffoo^2Ebar"

func TestEncodePath_NonASCII(t *testing.T)
// - "/home/user/données" → uses ^gXXX or ^hXXXX for non-ASCII runes

func TestDecodePath_RoundTrip(t *testing.T)
// For each test path: assert DecodePath(EncodePath(path)) == path

func TestDecodePath_CaseInsensitive(t *testing.T)
// "^2f" decodes same as "^2F"
// "^G141" decodes same as "^g141"

func TestDecodePath_Errors(t *testing.T)
// Truncated: "^2" → error
// Invalid hex: "^ZZ" → error
// Truncated with modifier: "^g14" → error

func TestSandboxDir(t *testing.T)
// Returns ~/.yoloai/sandboxes/<name>/

func TestWorkDir(t *testing.T)
// Returns ~/.yoloai/sandboxes/<name>/work/<encoded-path>/
```

### `internal/sandbox/meta_test.go`

```go
func TestMeta_SaveLoadRoundTrip(t *testing.T)
// Create full Meta, save to t.TempDir(), load back, assert.Equal

func TestMeta_OmitEmptyFields(t *testing.T)
// Save Meta with empty Model, empty NetworkMode, nil Ports
// Read raw JSON, verify fields are absent

func TestMeta_WithPortsAndNetwork(t *testing.T)
// Save Meta with Ports: ["3000:3000", "8080:8080"], NetworkMode: "none"
// Load back, assert values match
```

### `internal/agent/agent_test.go`

```go
func TestGetAgent_Claude(t *testing.T)
// Verify all Claude definition fields

func TestGetAgent_Test(t *testing.T)
// Verify: no API keys, no state dir, 0 startup delay, headless prompt mode

func TestGetAgent_Unknown(t *testing.T)
// Returns nil
```

### `internal/sandbox/parse_test.go`

```go
func TestParseDirArg_Modes(t *testing.T)
// Table: {input, expectedMode, expectedForce}
// - "./app" → Mode:"", Force:false
// - "./app:copy" → Mode:"copy", Force:false
// - "./app:rw" → Mode:"rw", Force:false
// - "./app:force" → Mode:"", Force:true
// - "./app:rw:force" → Mode:"rw", Force:true
// - "./app:force:copy" → Mode:"copy", Force:true

func TestParseDirArg_ConflictingModes(t *testing.T)
// "./app:copy:rw" → error

func TestParseDirArg_AbsolutePath(t *testing.T)
// Relative path is resolved to absolute
// Absolute path is preserved

func TestParseDirArg_PathWithColons(t *testing.T)
// "/path/to/file:with:colons" — colons not matching known suffixes stay in path
```

## Verification

```bash
# All tests pass
go test ./internal/sandbox/... ./internal/agent/...

# Full project still compiles
go build ./...

# Linter passes
make lint

# Full test suite
make test
```

## Concerns

### 1. Error types location (minor)

`UsageError` and `ConfigError` currently live in `internal/cli/root.go`. Phase 4a will create `internal/sandbox/errors.go` and move them. This means Phase 1's `parse.go` cannot return a `UsageError` directly — it returns plain errors, and the CLI layer wraps them. This is fine and matches the "commands are thin wrappers" pattern.

### 2. `DecodePath` return signature

[PLAN.md](../PLAN.md) shows `DecodePath(encoded string) string` but decoding can fail on malformed input. The phase plan uses `(string, error)` instead. This is the right call — malformed caret sequences should be caught, not silently produce garbage. [PLAN.md](../PLAN.md) should be updated if we want strict consistency.

### 3. Caret encoding scope for MVP paths

In practice, MVP paths are Unix absolute paths — they'll only contain ASCII characters and the main encoding target is `/` → `^2F`. The full spec implementation handles Unicode and all edge cases, which is correct per the resolved open question, but the test emphasis should be on the common case (ASCII paths with slashes) with a few non-ASCII tests for completeness.
