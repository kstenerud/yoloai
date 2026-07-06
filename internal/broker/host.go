// ABOUTME: InjectorHost owns a sandbox's key-injector lifetime. SidecarHost is the
// ABOUTME: CLI implementation: spawn a detached out-of-process injector, track + reap it.
package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// InjectorHost owns the lifetime of one sandbox's key-injector (D106). The CLI
// uses a SidecarHost (out-of-process, survives the CLI, re-derives the key on
// respawn); an embedder uses an in-process host (holds the live CredentialSource)
// — added when Phase 2's refreshing/minting sources land.
type InjectorHost interface {
	// Ensure starts the injector for the spec's sandbox if it is not already
	// running, and returns the address the container reaches it at. If a live
	// injector is already recorded it is reused (the running session keeps the
	// key it started with); a recorded-but-dead one is respawned. This is both
	// the start path and the lazy-reconcile path (D106).
	Ensure(ctx context.Context, spec InjectorSpec) (addr string, err error)

	// Stop terminates the injector recorded under sandboxDir and clears its
	// record. It is a no-op when nothing is recorded.
	Stop(ctx context.Context, sandboxDir string) error
}

// InjectorSpec is everything needed to (re)spawn a sandbox's injector. Its
// Bindings carry secrets and are never persisted; the host re-receives the spec
// (re-derived from the ambient env) on each Ensure, so a respawn needs no
// on-disk secret.
type InjectorSpec struct {
	// SandboxDir holds the injector record (injector.json) and log (injector.log).
	SandboxDir string
	// BindHost is the container-reachable host interface to bind (the bridge
	// gateway, e.g. "172.17.0.1") — never "0.0.0.0", which would expose the
	// injector on the LAN (D106). The port is chosen ephemerally.
	BindHost     string
	UpstreamURL  string
	StripHeaders []string
	// ExpectedToken is the per-sandbox placeholder secret the injector verifies on
	// inbound requests before injecting the real credential (see Injector).
	ExpectedToken string
	Bindings      []BindingConfig
}

// InjectorRecord is the persisted handle to a running sidecar (injector.json). It
// holds no secret — only the PID (for liveness/reaping) and the reachable addr.
type InjectorRecord struct {
	PID  int    `json:"pid"`
	Addr string `json:"addr"`
}

const (
	injectorRecordFile = "injector.json"
	injectorLogFile    = "injector.log"
	handshakeTimeout   = 5 * time.Second
	termGrace          = 1 * time.Second
)

// SidecarHost is the CLI InjectorHost: it spawns the injector as a detached child
// process (the running binary's `__inject` subcommand) that outlives the CLI.
type SidecarHost struct {
	// command resolves the executable and args to spawn. Defaults to the running
	// binary's `__inject` subcommand; overridable in tests.
	command func() (exe string, args []string, err error)
	// env is the child's environment. Empty by default (the sidecar needs none
	// and we keep zero ambient leak, D63/§12); tests may set a minimal PATH.
	env []string
}

var _ InjectorHost = (*SidecarHost)(nil)

// HasRecord reports whether an injector record exists for the sandbox — i.e. the
// sandbox was launched with brokering. Reconcile uses this to skip non-brokered
// sandboxes cheaply, before any credential re-derivation or backend call.
func HasRecord(sandboxDir string) bool {
	rec, err := loadRecord(sandboxDir)
	return err == nil && rec != nil
}

// InjectorAlive reports whether the sandbox's recorded injector process is
// running. False when there is no record or the process is dead — the latter is
// the reconcile respawn case.
func InjectorAlive(sandboxDir string) bool {
	rec, err := loadRecord(sandboxDir)
	return err == nil && rec != nil && processAlive(rec.PID)
}

// NewSidecarHost returns a SidecarHost that spawns `<this-binary> __inject` with
// an empty environment.
func NewSidecarHost() *SidecarHost {
	return &SidecarHost{command: defaultSidecarCommand, env: []string{}}
}

func defaultSidecarCommand() (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, fmt.Errorf("broker: resolve own executable: %w", err)
	}
	return exe, []string{InjectVerb}, nil
}

// Ensure implements InjectorHost.
func (h *SidecarHost) Ensure(ctx context.Context, spec InjectorSpec) (string, error) {
	rec, _ := loadRecord(spec.SandboxDir)
	if rec != nil && processAlive(rec.PID) {
		return rec.Addr, nil
	}
	// Respawn (reconcile, D106): if a dead record exists, rebind its port so the
	// container's base_url — pinned to this address at first launch — keeps
	// reaching the injector. A fresh injector (no record) takes an ephemeral port.
	return h.spawn(spec, respawnBindPort(rec))
}

// respawnBindPort returns the port to rebind for a respawn: the dead record's
// port (so base_url stays valid), or "0" (ephemeral) when there is no record.
func respawnBindPort(rec *InjectorRecord) string {
	if rec == nil {
		return "0"
	}
	if _, port, err := net.SplitHostPort(rec.Addr); err == nil && port != "" {
		return port
	}
	return "0"
}

// Stop implements InjectorHost.
func (h *SidecarHost) Stop(_ context.Context, sandboxDir string) error {
	rec, err := loadRecord(sandboxDir)
	if err != nil {
		return err
	}
	if rec != nil {
		killProcess(rec.PID)
	}
	return removeRecord(sandboxDir)
}

// spawn launches a fresh detached sidecar, hands it the config (with secrets) on
// stdin, reads back its listen address, and records its PID+addr.
func (h *SidecarHost) spawn(spec InjectorSpec, bindPort string) (string, error) {
	exe, args, err := h.command()
	if err != nil {
		return "", err
	}

	// sysexec.Command (not CommandContext): the sidecar must outlive both any ctx
	// and this process (the CLI returns while the agent keeps running). Setsid
	// detaches it into its own session so it is reparented to init, not killed
	// with the CLI. The env is explicit (empty in production — the sidecar needs
	// none — per DEV §12 / D63).
	cmd := sysexec.Command(h.env, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	inR, inW, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("broker: stdin pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return "", fmt.Errorf("broker: stdout pipe: %w", err)
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	if logf, lerr := fileutil.OpenFile(filepath.Join(spec.SandboxDir, injectorLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); lerr == nil {
		cmd.Stderr = logf
		defer func() { _ = logf.Close() }()
	}

	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW)
		return "", fmt.Errorf("broker: start sidecar: %w", err)
	}
	// The child now owns its dups of inR/outW; the parent drops them.
	_ = inR.Close()
	_ = outW.Close()

	// Hand the config (with the secret) to the child on stdin, then EOF it.
	encErr := json.NewEncoder(inW).Encode(sidecarConfig(spec, bindPort))
	_ = inW.Close()
	if encErr != nil {
		_ = outR.Close()
		killProcess(cmd.Process.Pid)
		return "", fmt.Errorf("broker: send sidecar config: %w", encErr)
	}

	// Read the handshake addr, bounded so a child that dies before binding can't
	// hang us.
	_ = outR.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var hs Handshake
	decErr := json.NewDecoder(outR).Decode(&hs)
	_ = outR.Close()
	if decErr != nil || hs.Addr == "" {
		killProcess(cmd.Process.Pid)
		return "", fmt.Errorf("broker: sidecar handshake failed: %w", errors.Join(decErr, errEmptyAddr(hs.Addr)))
	}

	rec := &InjectorRecord{PID: cmd.Process.Pid, Addr: hs.Addr}
	if err := saveRecord(spec.SandboxDir, rec); err != nil {
		killProcess(cmd.Process.Pid)
		return "", err
	}

	// Reap in the background rather than Release: the sidecar is Setsid-detached,
	// so it outlives this process (when the CLI exits the goroutine dies and init
	// adopts the sidecar). But while this process *is* alive, a dying sidecar must
	// be waited on — an unreaped child lingers as a zombie that `kill(pid,0)`
	// still reports as alive, defeating the liveness check.
	go func() { _ = cmd.Wait() }()
	return hs.Addr, nil
}

func errEmptyAddr(addr string) error {
	if addr == "" {
		return errors.New("empty handshake address")
	}
	return nil
}

// sidecarConfig builds the wire config from a spec, binding an ephemeral port on
// the spec's container-reachable host.
func sidecarConfig(spec InjectorSpec, bindPort string) SidecarConfig {
	return SidecarConfig{
		UpstreamURL:   spec.UpstreamURL,
		BindAddr:      net.JoinHostPort(spec.BindHost, bindPort),
		StripHeaders:  spec.StripHeaders,
		ExpectedToken: spec.ExpectedToken,
		Bindings:      spec.Bindings,
	}
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// --- record persistence (injector.json) --------------------------------------

func recordPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, injectorRecordFile)
}

const placeholderTokenFile = "injector-token"

// PlaceholderToken returns the sandbox's per-sandbox injector placeholder token,
// generating and persisting a fresh random one on first call (get-or-create).
// The launch path delivers it into the container as the agent's placeholder
// credential and passes it to the injector as ExpectedToken; the reconcile path
// recovers the same value so a respawned injector keeps accepting the running
// agent's requests. The token lives host-side (0600, never bind-mounted), so a
// co-resident container cannot learn another sandbox's token. It is not a real
// credential — only a per-sandbox capability secret — so persisting it is safe.
func PlaceholderToken(sandboxDir string) (string, error) {
	path := filepath.Join(sandboxDir, placeholderTokenFile)
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // path from sandbox dir
		if tok := string(data); tok != "" {
			return tok, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("broker: read %s: %w", placeholderTokenFile, err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("broker: generate placeholder token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := fileutil.WriteFile(path, []byte(tok), 0600); err != nil {
		return "", fmt.Errorf("broker: persist %s: %w", placeholderTokenFile, err)
	}
	return tok, nil
}

// LoadRecord returns the injector record (injector.json) recorded under
// sandboxDir, or nil if none is recorded. The host-orphan sweep reads it from
// every live sandbox to build the set of injector PIDs to keep (DF71).
func LoadRecord(sandboxDir string) (*InjectorRecord, error) {
	return loadRecord(sandboxDir)
}

func loadRecord(sandboxDir string) (*InjectorRecord, error) {
	data, err := os.ReadFile(recordPath(sandboxDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("broker: read %s: %w", injectorRecordFile, err)
	}
	var rec InjectorRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("broker: parse %s: %w", injectorRecordFile, err)
	}
	return &rec, nil
}

func saveRecord(sandboxDir string, rec *InjectorRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("broker: marshal %s: %w", injectorRecordFile, err)
	}
	if err := fileutil.WriteFile(recordPath(sandboxDir), data, 0600); err != nil {
		return fmt.Errorf("broker: write %s: %w", injectorRecordFile, err)
	}
	return nil
}

func removeRecord(sandboxDir string) error {
	if err := os.Remove(recordPath(sandboxDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("broker: remove %s: %w", injectorRecordFile, err)
	}
	return nil
}

// --- process liveness/teardown ------------------------------------------------

// processAlive reports whether pid names a live process this user can signal.
// Signal 0 probes existence without delivering a signal. (PID reuse can yield a
// false positive; the symptom is a failing API call → reconcile/restart — D106.)
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killProcess asks pid to terminate (SIGTERM), then forces it (SIGKILL) if it is
// still alive after a short grace period.
func killProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(termGrace)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
