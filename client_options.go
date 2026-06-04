// ABOUTME: ClientCreateOptions — the construction-time configuration for a
// ABOUTME: Client (data/home dirs, optional backend, IO, env snapshot, principal).
// ABOUTME: Passed to NewClient; the library never reads ambient process state.

package yoloai

import (
	"io"
	"log/slog"
)

// ClientCreateOptions configures a Client.
type ClientCreateOptions struct {
	// DataDir is the root yoloai data directory; all per-Client state
	// lives below it (sandboxes/, profiles/, config.yaml, state.yaml,
	// credentials/). REQUIRED — empty is rejected at construction.
	//
	// No implicit default. yoloai library code never reads $HOME or any
	// other ambient process state. The CLI fills this from $HOME/.yoloai/
	// at startup (its single licensed os.UserHomeDir() call). HTTP
	// servers, daemons, multi-tenant processes, and tests pass an
	// explicit path. See development-principles.md §12.
	DataDir string

	// HomeDir is the host user's home directory. REQUIRED — empty is
	// rejected at construction with a *UsageError. It is where ~-expansion
	// in user-supplied paths, seed-file lookups (~/.claude, ~/.codex), and
	// auth-file discovery resolve.
	//
	// There is no implicit filepath.Dir(DataDir) derivation: under the D60
	// data-dir bifurcation DataDir is $HOME/.yoloai/library, so its parent
	// is $HOME/.yoloai — not $HOME. Silently deriving it there sent every
	// seed/credential lookup to the wrong home and launched agents
	// unconfigured, so the boundary now demands an explicit value. The CLI
	// passes cliutil.Layout().HomeDir (its single licensed os.UserHomeDir()
	// site); embedders pass the host user's home.
	HomeDir string

	// BackendType selects the runtime backend (yoloai.BackendDocker,
	// yoloai.BackendTart, etc.). OPTIONAL — empty constructs a backend-less
	// Client that serves host-only reads and, via System(), cross-backend
	// admin without ever opening a connection. A backend-bound
	// operation (Exec, Attach, Start, lifecycle, Create, List, Clone, …) on a
	// backend-less Client returns ErrBackendRequired.
	//
	// No implicit default. Backend selection is inherently ambient (it
	// probes which container daemons are installed), so it belongs at the
	// outermost boundary, not silently inside Client construction (§4 /
	// §12). The CLI resolves it from its --backend / --isolation / --os
	// flags via runtime.SelectBackend and passes the concrete result here.
	// Embedders that want that same auto-detection call the public
	// yoloai.SelectBackend helper and pass its result. When set, the backend
	// is opened lazily on the first backend-bound op, not at construction.
	BackendType BackendType

	// Logger receives structured log output. Default: slog.Default().
	Logger *slog.Logger

	// Output receives human-readable progress messages. Default: io.Discard.
	Output io.Writer

	// Input provides interactive input. Default: an empty reader (immediate
	// EOF) — the library never reads the embedding process's os.Stdin (§12: no
	// ambient configuration). Embedders that want interactive input pass it
	// explicitly; the CLI passes cmd.InOrStdin() at its boundary.
	Input io.Reader

	// Version is the yoloAI version string stamped into each created
	// sandbox's environment.json. The CLI fills it from build info; embedders may
	// leave it empty. Not a per-create input — it lives here so Create
	// callers don't repeat it.
	Version string

	// Env is the authorized host-environment snapshot for this Client. It is
	// the ONLY source from which the library resolves user-declared ${VAR}
	// references in config/profile values AND the agent's API-key / auth-hint
	// credential values injected into the sandbox — the library never reads
	// the live process environment for them (§12). Optional; nil/empty means
	// no ${VAR} resolution and no env-sourced credentials.
	//
	// The CLI fills this from its single licensed os.Environ() snapshot (plus
	// sudo-stripped-credential recovery). A multi-principal embedder MUST pass
	// each principal's own environment here — never the daemon's process env —
	// so credentials stay principal-scoped (D58/D59).
	//
	// Env is also where the selected backend reads its daemon-connection
	// settings (the library never reads them from the process env, §12). Include
	// whichever apply to your BackendType:
	//   - docker:  DOCKER_HOST, DOCKER_CERT_PATH, DOCKER_TLS_VERIFY,
	//              DOCKER_API_VERSION. All optional — absent/blank means the
	//              default local socket with no TLS (same as the docker CLI).
	//   - podman:  CONTAINER_HOST, DOCKER_HOST, XDG_RUNTIME_DIR for socket
	//              discovery. Absent falls back to the well-known socket paths.
	//   - seatbelt: locale/terminal vars (PATH, HOME, TERM, LANG, LC_*) are
	//              forwarded to the on-host agent from this snapshot.
	Env map[string]string

	// Principal namespaces this Client's sandboxes under an owning principal
	// (tenant/user), so two principals can each own a sandbox of the same name
	// without colliding on the runtime backend. Client-scoped, not per-call —
	// the Client is the principal-scoped handle (D58/D59).
	//
	// Empty ("") is the default no-principal sentinel: instance names elide the
	// segment (yoloai-<name>) and behavior is identical to today. Non-empty
	// must be ≤8 alphanumeric chars (parsed at construction; invalid is
	// rejected with a *UsageError). See D62.
	Principal string

	// SecretsStagingDir is the host directory under which the library stages a
	// per-sandbox temp dir of plaintext agent credentials before bind-mounting
	// it in. Optional; empty ("") means the OS default temp dir (os.TempDir()),
	// which is what the single-principal CLI uses.
	//
	// The library decides WHAT to stage and WHEN to delete it; the embedder
	// supplies WHERE (D59 refinement). A multi-principal daemon points each
	// principal's Client at that principal's own tmpfs so plaintext
	// credentials never share a staging root across principals.
	SecretsStagingDir string
}
