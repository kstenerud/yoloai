//go:build linux

package containerdrt

// ABOUTME: Network diagnostic capture invoked on DF9 probe-timeout —
// dumps in-VM and host-side network state to a file for offline
// root-cause analysis.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// captureNetworkDiagnostics is called when waitForNetworkReady
// exhausts its retry budget. It captures in-task and host-side network
// state to help root-cause why the netns never became reachable.
//
// Writes a human-readable dump to <sandboxDir>/network-diag.txt and
// logs the path with a structured `sandbox.network.diag` event. The
// smoke test preserves the whole sandbox dir on failure, so the dump
// is surfaced automatically.
//
// Best-effort: errors capturing individual sections are recorded in
// the dump rather than failing the whole capture, since the entire
// point is to gather what we can about a broken state. The caller
// supplies its own context; this function will not panic.
func captureNetworkDiagnostics(ctx context.Context, r *Runtime, name string, task client.Task) {
	sandboxDir := r.sandboxDirForName(name)
	netnsName := "yoloai-" + name

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "yoloai network diagnostic capture (DF9 investigation)\n")
	fmt.Fprintf(&buf, "captured: %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&buf, "container: %s\n", name)
	fmt.Fprintf(&buf, "netns:     %s\n\n", netnsName)

	// ---- In-task state (single exec runs the whole script) ----
	fmt.Fprintf(&buf, "============================================================\n")
	fmt.Fprintf(&buf, "IN-VM state (via task.Exec)\n")
	fmt.Fprintf(&buf, "============================================================\n")
	inVMOut, inVMErr := runDiagExec(ctx, task, "diag-invm", inVMDiagScript, 30*time.Second)
	buf.WriteString(inVMOut)
	if inVMErr != "" {
		fmt.Fprintf(&buf, "\n[exec stderr/error: %s]\n", inVMErr)
	}

	// ---- Host-side state ----
	fmt.Fprintf(&buf, "\n============================================================\n")
	fmt.Fprintf(&buf, "HOST-SIDE state\n")
	fmt.Fprintf(&buf, "============================================================\n")

	statePath := cniStatePath(sandboxDir)
	fmt.Fprintf(&buf, "\n== cni-state.json (%s) ==\n", statePath)
	if cniState, err := os.ReadFile(statePath); err != nil { //nolint:gosec // G304: internal path under user's home, fixed layout
		fmt.Fprintf(&buf, "ERROR: %v\n", err)
	} else {
		buf.Write(cniState)
		buf.WriteString("\n")
	}

	appendHostCmd(ctx, r.execEnv, &buf, "ip netns list", "ip", "netns", "list")
	appendHostCmd(ctx, r.execEnv, &buf, "host-side ip addr (yoloai bridge interfaces)",
		"sh", "-c", "ip addr show | grep -A2 -E 'yoloai0|veth' | head -60")
	appendHostCmd(ctx, r.execEnv, &buf, "bridge link",
		"bridge", "link", "show")
	appendHostCmd(ctx, r.execEnv, &buf, "bridge fdb show br yoloai0",
		"bridge", "fdb", "show", "br", "yoloai0")
	appendHostCmd(ctx, r.execEnv, &buf, "ip netns exec "+netnsName+" ip addr",
		"ip", "netns", "exec", netnsName, "ip", "addr")
	appendHostCmd(ctx, r.execEnv, &buf, "ip netns exec "+netnsName+" ip route",
		"ip", "netns", "exec", netnsName, "ip", "route")
	appendHostCmd(ctx, r.execEnv, &buf, "ip netns exec "+netnsName+" ip neighbor",
		"ip", "netns", "exec", netnsName, "ip", "neighbor")
	appendHostCmd(ctx, r.execEnv, &buf, "ip netns exec "+netnsName+" bridge link",
		"ip", "netns", "exec", netnsName, "bridge", "link")
	appendHostCmd(ctx, r.execEnv, &buf, "iptables -t nat -S POSTROUTING",
		"iptables", "-t", "nat", "-S", "POSTROUTING")
	appendHostCmd(ctx, r.execEnv, &buf, "iptables -t filter -L CNI-FORWARD -n -v",
		"iptables", "-t", "filter", "-L", "CNI-FORWARD", "-n", "-v")
	appendHostCmd(ctx, r.execEnv, &buf, "iptables -t filter -L FORWARD -n -v",
		"iptables", "-t", "filter", "-L", "FORWARD", "-n", "-v")
	appendHostCmd(ctx, r.execEnv, &buf, "ls -la /run/cni/networks/yoloai/ (IPAM leases)",
		"ls", "-la", "/run/cni/networks/yoloai/")

	outPath := filepath.Join(sandboxDir, "network-diag.txt")
	if err := fileutil.WriteFile(outPath, buf.Bytes(), 0o644); err != nil { //nolint:gosec // G306: world-readable diag file is intentional
		slog.Warn("failed to write network-diag.txt",
			"event", "sandbox.network.diag.write_error",
			"path", outPath,
			"err", err.Error())
		return
	}
	slog.Info("network diagnostic capture written",
		"event", "sandbox.network.diag",
		"path", outPath,
		"bytes", buf.Len())
}

// inVMDiagScript probes the VM's own view of its network. Each step
// has its own short `timeout` so the whole script bounds at ~25s.
const inVMDiagScript = `set +e
echo "== ip addr =="
ip addr 2>&1
echo
echo "== ip route =="
ip route 2>&1
echo
echo "== ip neighbor =="
ip neighbor 2>&1
echo
echo "== /etc/resolv.conf =="
cat /etc/resolv.conf 2>&1
echo
echo "== resolvectl status (if available) =="
timeout 3 resolvectl status 2>&1 | head -50
echo
echo "== getent hosts api.anthropic.com (NSS lookup) =="
out=$(timeout 5 getent hosts api.anthropic.com 2>&1); echo "exit=$?"; echo "$out"
echo
echo "== getent ahostsv4 api.anthropic.com (IPv4 only) =="
out=$(timeout 5 getent ahostsv4 api.anthropic.com 2>&1); echo "exit=$?"; echo "$out"
echo
echo "== nslookup api.anthropic.com 8.8.8.8 (bypass resolv.conf) =="
out=$(timeout 5 nslookup api.anthropic.com 8.8.8.8 2>&1); echo "exit=$?"; echo "$out"
echo
echo "== TCP /dev/tcp/1.1.1.1/443 (raw, no DNS) =="
timeout 3 bash -c '</dev/tcp/1.1.1.1/443' 2>&1; echo "exit=$?"
echo
echo "== TCP /dev/tcp/api.anthropic.com/443 (DNS + routing) =="
timeout 5 bash -c '</dev/tcp/api.anthropic.com/443' 2>&1; echo "exit=$?"
echo
echo "== ip -s link show eth0 (RX/TX counters) =="
ip -s link show eth0 2>&1
echo
echo "== /proc/net/route =="
cat /proc/net/route 2>&1
echo
echo "== /proc/net/arp =="
cat /proc/net/arp 2>&1
echo
gw=$(ip route show default 2>/dev/null | awk '/^default/ {print $3; exit}')
if [ -n "$gw" ]; then
    echo "== arping gateway $gw =="
    timeout 3 arping -c 2 -w 2 "$gw" 2>&1 || echo "(arping missing or failed)"
    echo
    echo "== ping gateway $gw =="
    timeout 3 ping -c 2 -W 1 "$gw" 2>&1 || echo "(ping missing or failed)"
fi
`

// runDiagExec runs a script inside the task and returns (stdout, stderr).
// On any error, returns whatever was captured plus the error in stderr.
func runDiagExec(ctx context.Context, task client.Task, label, script string, timeout time.Duration) (string, string) {
	execID := fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
	spec := &specs.Process{
		Args: []string{"sh", "-c", script},
		Cwd:  "/",
		Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
	}
	var stdout, stderr bytes.Buffer
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	process, err := task.Exec(execCtx, execID, spec, cio.NewCreator(cio.WithStreams(nil, &stdout, &stderr)))
	if err != nil {
		return stdout.String(), fmt.Sprintf("exec create: %v", err)
	}
	defer func() { _, _ = process.Delete(ctx) }()

	exitCh, err := process.Wait(execCtx)
	if err != nil {
		return stdout.String(), fmt.Sprintf("wait: %v", err)
	}
	if err := process.Start(execCtx); err != nil {
		return stdout.String(), fmt.Sprintf("start: %v", err)
	}
	select {
	case <-execCtx.Done():
		return stdout.String(), fmt.Sprintf("context: %v", execCtx.Err())
	case <-exitCh:
		return stdout.String(), stderr.String()
	}
}

// appendHostCmd runs a host-side command with a short per-command
// timeout and writes its output to buf under the given label. Errors
// are recorded in the buffer; this function never blocks the whole
// diag capture if one command hangs.
// env is the explicit subprocess env (DEV §12); pass r.execEnv.
func appendHostCmd(ctx context.Context, env []string, buf *bytes.Buffer, label string, args ...string) {
	fmt.Fprintf(buf, "\n== %s ==\n", label)
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := sysexec.CommandContext(cmdCtx, env, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(buf, "ERROR: %v\n", err)
	}
	buf.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		buf.WriteByte('\n')
	}
}
