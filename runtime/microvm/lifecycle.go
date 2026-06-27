//go:build linux

package microvm

// ABOUTME: QEMU -M microvm instance lifecycle — Create/Start/Stop/Remove/Inspect/Prune/Exec.
// ABOUTME: Per-instance qcow2 overlay over the golden rootfs, a virtiofsd for the workdir,
// ABOUTME: a daemonized QEMU process, and readiness/exec over the guest agent (QGA).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
)

const (
	instancesSubdir   = "instances"
	diskName          = "disk.qcow2"
	qemuPIDName       = "qemu.pid"
	vfsPIDName        = "virtiofsd.pid"
	qgaSockName       = "qga.sock"
	serialSockName    = "serial.sock"
	vfsSockName       = "vfs.sock"
	serialLogName     = "serial.log"
	instanceCfgName   = "instance.json"
	workdirFSTag      = "workdir"
	defaultMemoryMB   = 2048
	defaultCPUs       = 2
	readyTimeout      = 90 * time.Second
	stopGraceDeadline = 5 * time.Second
)

// instanceDir is the per-instance state directory (overlay disk, PID files, sockets).
func (r *Runtime) instanceDir(name string) string {
	return filepath.Join(r.microvmDir(), instancesSubdir, name)
}

func (r *Runtime) diskPath(name string) string { return filepath.Join(r.instanceDir(name), diskName) }
func (r *Runtime) qemuPIDPath(name string) string {
	return filepath.Join(r.instanceDir(name), qemuPIDName)
}
func (r *Runtime) vfsPIDPath(name string) string {
	return filepath.Join(r.instanceDir(name), vfsPIDName)
}
func (r *Runtime) qgaSockPath(name string) string {
	return filepath.Join(r.instanceDir(name), qgaSockName)
}
func (r *Runtime) serialSock(name string) string {
	return filepath.Join(r.instanceDir(name), serialSockName)
}
func (r *Runtime) vfsSockPath(name string) string {
	return filepath.Join(r.instanceDir(name), vfsSockName)
}
func (r *Runtime) serialLogPath(name string) string {
	return filepath.Join(r.instanceDir(name), serialLogName)
}
func (r *Runtime) instanceCfgPath(name string) string {
	return filepath.Join(r.instanceDir(name), instanceCfgName)
}

// Create writes the instance config and builds the per-instance writable disk as
// a qcow2 overlay over the read-only golden rootfs (instant, like a VM clone).
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	dir := r.instanceDir(cfg.Name)
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create instance dir: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal instance config: %w", err)
	}
	if err := fileutil.WriteFile(r.instanceCfgPath(cfg.Name), data, 0600); err != nil {
		return fmt.Errorf("write instance config: %w", err)
	}

	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("qemu-img not found (install qemu-utils): %w", err)
	}
	// No explicit size: the overlay inherits the backing file's virtual size.
	c := sysexec.CommandContext(ctx, r.execEnv, qemuImg,
		"create", "-q", "-f", "qcow2", "-b", r.goldenRootfsPath(), "-F", "raw", r.diskPath(cfg.Name))
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("create overlay disk: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Start boots the instance: a virtiofsd for the workdir, then a daemonized QEMU,
// then waits for the guest agent and mounts the workdir inside the guest.
func (r *Runtime) Start(ctx context.Context, name string) error {
	if r.running(name) {
		return nil
	}
	cfg, err := r.loadInstanceConfig(name)
	if err != nil {
		return err
	}
	memMB, cpus := resourcesOf(cfg)

	if err := r.startVirtiofsd(name, workdirHostPath(cfg)); err != nil {
		return err
	}

	logFile, err := fileutil.OpenFile(r.serialLogPath(name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		r.stopVirtiofsd(name)
		return fmt.Errorf("open serial log: %w", err)
	}
	// Long-lived: use Command (not CommandContext) + a new process group so QEMU
	// survives after Start returns and isn't killed with the caller's context.
	qemuCmd := sysexec.Command(r.execEnv, qemuBin, r.qemuArgs(name, memMB, cpus)...)
	qemuCmd.Stdout = logFile
	qemuCmd.Stderr = logFile
	qemuCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := qemuCmd.Start(); err != nil {
		_ = logFile.Close()
		r.stopVirtiofsd(name)
		return fmt.Errorf("start QEMU: %w", err)
	}
	if err := writePID(r.qemuPIDPath(name), qemuCmd.Process.Pid); err != nil {
		_ = qemuCmd.Process.Kill()
		_ = logFile.Close()
		r.stopVirtiofsd(name)
		return fmt.Errorf("write QEMU pid: %w", err)
	}

	procDone := make(chan error, 1)
	go func() { procDone <- qemuCmd.Wait(); _ = logFile.Close() }()

	if err := r.waitForReady(ctx, name, procDone); err != nil {
		_ = r.Stop(context.WithoutCancel(ctx), name) // best-effort cleanup
		detail := ""
		if b, rerr := os.ReadFile(r.serialLogPath(name)); rerr == nil && len(b) > 0 { //nolint:gosec // path within instance dir
			detail = "\nserial log:\n" + strings.TrimSpace(string(b))
		}
		return fmt.Errorf("wait for guest agent: %w%s", err, detail)
	}

	// Mount the workdir share inside the guest at its in-sandbox path.
	if cfg.WorkingDir != "" {
		mount := fmt.Sprintf("mkdir -p %q && mount -t virtiofs %s %q", cfg.WorkingDir, workdirFSTag, cfg.WorkingDir)
		if _, code, err := qgaExec(ctx, r.qgaSockPath(name), []string{"/bin/sh", "-c", mount}); err != nil || code != 0 {
			return fmt.Errorf("mount workdir in guest (exit %d): %w", code, err)
		}
	}
	return nil
}

// Stop terminates QEMU (SIGTERM then SIGKILL) and the virtiofsd helper.
func (r *Runtime) Stop(_ context.Context, name string) error {
	if pid, err := readPID(r.qemuPIDPath(name)); err == nil {
		terminate(pid)
	}
	_ = os.Remove(r.qemuPIDPath(name))
	_ = os.Remove(r.qgaSockPath(name))
	_ = os.Remove(r.serialSock(name))
	r.stopVirtiofsd(name)
	return nil
}

// Remove stops the instance and deletes its state directory.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	_ = r.Stop(ctx, name)
	if err := os.RemoveAll(r.instanceDir(name)); err != nil {
		return fmt.Errorf("remove instance dir: %w", err)
	}
	return nil
}

// Inspect reports liveness from the QEMU PID. ErrNotFound when the instance dir
// doesn't exist.
func (r *Runtime) Inspect(_ context.Context, name string) (runtime.InstanceInfo, error) {
	if _, err := os.Stat(r.instanceDir(name)); err != nil {
		if os.IsNotExist(err) {
			return runtime.InstanceInfo{}, runtime.ErrNotFound
		}
		return runtime.InstanceInfo{}, err
	}
	return runtime.InstanceInfo{Running: r.running(name)}, nil
}

// Exec runs a command in the guest via the guest agent. user is ignored (QGA
// runs as root); per-user exec is a later refinement.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	if !r.running(name) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}
	if len(cmd) == 0 {
		return runtime.ExecResult{}, fmt.Errorf("empty command")
	}
	out, code, err := qgaExec(ctx, r.qgaSockPath(name), cmd)
	if err != nil {
		return runtime.ExecResult{}, err
	}
	return runtime.ExecResult{Stdout: strings.TrimSpace(out), ExitCode: code}, nil
}

// Prune removes orphaned instance dirs (named instances not in knownInstances).
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	var result runtime.PruneResult
	known := make(map[string]bool, len(knownInstances))
	for _, n := range knownInstances {
		known[n] = true
	}
	entries, err := os.ReadDir(filepath.Join(r.microvmDir(), instancesSubdir))
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		result.Items = append(result.Items, runtime.PruneItem{Kind: "vm", Name: e.Name()})
		fmt.Fprintf(output, "orphaned microvm instance: %s\n", e.Name()) //nolint:errcheck // best-effort
		if !dryRun {
			_ = r.Remove(ctx, e.Name())
		}
	}
	return result, nil
}

// --- helpers ---

func (r *Runtime) loadInstanceConfig(name string) (runtime.InstanceConfig, error) {
	var cfg runtime.InstanceConfig
	data, err := os.ReadFile(r.instanceCfgPath(name)) //nolint:gosec // path within instance dir
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, runtime.ErrNotFound
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse instance config: %w", err)
	}
	return cfg, nil
}

// running reports whether the instance's QEMU process is alive.
func (r *Runtime) running(name string) bool {
	pid, err := readPID(r.qemuPIDPath(name))
	if err != nil {
		return false
	}
	return pidAlive(pid)
}

func (r *Runtime) qemuArgs(name string, memMB, cpus int) []string {
	return []string{
		"-machine", "microvm,acpi=on,memory-backend=mem",
		"-enable-kvm", "-cpu", "host",
		"-m", strconv.Itoa(memMB), "-smp", strconv.Itoa(cpus),
		"-kernel", r.kernelPath(), "-initrd", r.initrdPath(),
		"-append", "console=ttyS0 root=/dev/vda rw rootfstype=ext4 quiet",
		"-drive", "id=root,file=" + r.diskPath(name) + ",format=qcow2,if=none",
		"-device", "virtio-blk-device,drive=root",
		"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dM,share=on", memMB),
		"-chardev", "socket,id=qga,path=" + r.qgaSockPath(name) + ",server=on,wait=off",
		"-device", "virtio-serial-device",
		"-device", "virtserialport,chardev=qga,name=org.qemu.guest_agent.0",
		"-chardev", "socket,id=ser,path=" + r.serialSock(name) + ",server=on,wait=off,logfile=" + r.serialLogPath(name),
		"-serial", "chardev:ser",
		"-chardev", "socket,id=vfs,path=" + r.vfsSockPath(name),
		"-device", "vhost-user-fs-device,chardev=vfs,tag=" + workdirFSTag,
		"-nodefaults", "-no-reboot", "-nographic",
	}
}

// startVirtiofsd launches the per-VM virtiofs daemon sharing the host workdir.
func (r *Runtime) startVirtiofsd(name, shareDir string) error {
	bin := findVirtiofsd()
	if bin == "" {
		return fmt.Errorf("virtiofsd not found (install virtiofsd)")
	}
	if shareDir == "" {
		shareDir = r.instanceDir(name) // nothing to share; point at a harmless dir
	}
	_ = os.Remove(r.vfsSockPath(name))
	cmd := sysexec.Command(r.execEnv, bin, "--socket-path="+r.vfsSockPath(name), "--shared-dir="+shareDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start virtiofsd: %w", err)
	}
	if err := writePID(r.vfsPIDPath(name), cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	// QEMU needs the vhost-user socket to exist before it connects.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(r.vfsSockPath(name)); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("virtiofsd socket did not appear")
}

func (r *Runtime) stopVirtiofsd(name string) {
	if pid, err := readPID(r.vfsPIDPath(name)); err == nil {
		terminate(pid)
	}
	_ = os.Remove(r.vfsPIDPath(name))
	_ = os.Remove(r.vfsSockPath(name))
}

// waitForReady polls the guest agent until it answers, the deadline passes, or
// the QEMU process exits early (procDone).
func (r *Runtime) waitForReady(ctx context.Context, name string, procDone <-chan error) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		select {
		case err := <-procDone:
			return fmt.Errorf("QEMU exited before the guest agent came up: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := qgaPing(pingCtx, r.qgaSockPath(name))
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest agent not ready within %s", readyTimeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// workdirHostPath returns the host directory backing the in-guest workdir — the
// mount whose container path is the working dir, else the working dir itself.
func workdirHostPath(cfg runtime.InstanceConfig) string {
	for _, m := range cfg.Mounts {
		if m.ContainerPath == cfg.WorkingDir {
			return m.HostPath
		}
	}
	return cfg.WorkingDir
}

// resourcesOf resolves memory (MB) and vCPU count from the instance config
// (Memory is bytes, NanoCPUs is cpus*1e9), falling back to defaults.
func resourcesOf(cfg runtime.InstanceConfig) (memMB, cpus int) {
	memMB, cpus = defaultMemoryMB, defaultCPUs
	if cfg.Resources == nil {
		return memMB, cpus
	}
	if mb := int(cfg.Resources.Memory / (1024 * 1024)); mb > 0 {
		memMB = mb
	}
	if n := int(cfg.Resources.NanoCPUs / 1_000_000_000); n > 0 {
		cpus = n
	}
	return memMB, cpus
}

func writePID(path string, pid int) error {
	return fileutil.WriteFile(path, []byte(strconv.Itoa(pid)), 0600)
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path within instance dir
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// terminate sends SIGTERM, then SIGKILL if the process is still alive after a grace period.
func terminate(pid int) {
	if !pidAlive(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(stopGraceDeadline)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
