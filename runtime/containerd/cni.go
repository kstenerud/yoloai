//go:build linux

package containerdrt

// ABOUTME: CNI network setup and teardown for containerd-managed containers.
// Creates network namespaces, runs CNI ADD/DEL, and persists state for idempotent teardown.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cni "github.com/containerd/go-cni"
	"github.com/vishvananda/netns"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// cniStateFileName is the filename for per-sandbox CNI state.
const cniStateFileName = "cni-state.json"

// errFirewallRulesMissing is returned by runCNIAdd when n.Setup reports
// success but the CNI firewall plugin did not actually install CNI-FORWARD
// ACCEPT rules for the allocated IP. See DF9 in discovered-findings.md and
// the "Firewall plugin: silent no-op when result.IPs is empty" entry in
// backend-idiosyncrasies.md. setupCNI uses errors.Is to detect this and
// retry once with a fresh netns + IPAM allocation.
var errFirewallRulesMissing = errors.New("CNI firewall rules missing after ADD")

// cniConflistTemplate is the CNI configuration for the yoloai network.
// Written once to ~/.yoloai/cni/yoloai.conflist on first use.
const cniConflistTemplate = `{
  "cniVersion": "1.0.0",
  "name": "yoloai",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "yoloai0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.89.0.0/16",
        "routes": [{"dst": "0.0.0.0/0"}]
      }
    },
    {"type": "portmap", "capabilities": {"portMappings": true}},
    {"type": "firewall"}
  ]
}
`

// cniState holds the persisted network state for a sandbox.
type cniState struct {
	NetnsName string `json:"netns_name"`
	NetnsPath string `json:"netns_path"`
	Interface string `json:"interface"`
	IP        string `json:"ip"`
}

// cniConfDir returns the path to the yoloai CNI config directory.
func cniConfDir(layout config.Layout) string {
	return layout.CniDir()
}

// cniStatePath returns the path to the CNI state file for a sandbox.
func cniStatePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, config.BackendDirName, cniStateFileName)
}

// ensureCNIConflist writes the yoloai CNI conflist if it does not already exist
// or if the existing file does not match the current template. Overwriting on
// mismatch ensures subnet changes (e.g. moving from 10.88 to 10.89 to avoid
// conflicts with Podman) take effect without manual intervention.
func ensureCNIConflist(layout config.Layout) error {
	dir := cniConfDir(layout)
	if err := fileutil.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G301: 0750 is appropriate for CNI config dir
		return fmt.Errorf("create CNI config dir: %w", err)
	}
	path := filepath.Join(dir, "yoloai.conflist")
	existing, err := os.ReadFile(path) //nolint:gosec // G304: path is always a trusted yoloai config subpath
	if err == nil && string(existing) == cniConflistTemplate {
		return nil // up to date
	}
	if err := fileutil.WriteFile(path, []byte(cniConflistTemplate), 0o644); err != nil { //nolint:gosec // G306: world-readable config is correct
		return fmt.Errorf("write CNI conflist: %w", err)
	}
	return nil
}

// createNetNS creates a named network namespace and returns its path.
// The namespace is created at /var/run/netns/<name> (standard Linux path).
//
// netns.NewNamed calls unshare(CLONE_NEWNET) which switches the calling OS
// thread into the new namespace and never restores it. We must save and
// restore the original namespace ourselves, with the OS thread locked, so
// that subsequent CNI plugin execs inherit the host namespace — not the newly
// created one (which would cause the bridge plugin to reject CNI_NETNS as
// "same as current netns").
func createNetNS(name string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return "", fmt.Errorf("get current netns: %w", err)
	}
	defer origNS.Close() //nolint:errcheck // G104: best-effort restore; OS thread is locked and will exit

	ns, err := netns.NewNamed(name)
	if err != nil {
		return "", fmt.Errorf("create netns %s: %w", name, err)
	}
	_ = ns.Close() //nolint:gosec // G104: fd close only — namespace persists at /var/run/netns/<name>

	if err := netns.Set(origNS); err != nil {
		return "", fmt.Errorf("restore original netns: %w", err)
	}

	return fmt.Sprintf("/var/run/netns/%s", name), nil
}

// deleteNetNS deletes a named network namespace.
func deleteNetNS(name string) error {
	if err := netns.DeleteNamed(name); err != nil {
		// Ignore "no such file" — namespace may already be gone.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("delete netns %s: %w", name, err)
	}
	return nil
}

// cleanupStaleCNIResultsCache deletes the libcni results cache file for
// containerName. The cache lives at /var/lib/cni/results/yoloai-<containerName>-eth0.
// Without this cleanup, a subsequent CNI ADD would leave a stale cache entry
// that a NEXT creation's pre-flight would use to incorrectly delete iptables rules.
// Errors are silently ignored — this is best-effort pre-flight cleanup.
func cleanupStaleCNIResultsCache(containerName string) {
	path := fmt.Sprintf("/var/lib/cni/results/yoloai-%s-eth0", containerName)
	_ = os.Remove(path)
}

// cleanupStaleIPAMLeases removes any host-local IPAM lease files for
// containerName left over from a previous failed or replaced sandbox.
// Lease files live at /var/lib/cni/networks/yoloai/<IP> and contain
// "<containerName>\n<interface>" as their content.
// Errors are silently ignored — this is best-effort pre-flight cleanup.
func cleanupStaleIPAMLeases(containerName string) {
	dir := "/var/lib/cni/networks/yoloai"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path) //nolint:gosec // G304: trusted dir
		if err != nil {
			continue
		}
		// Lease format: "<containerID>\n<interface>" in modern plugins,
		// or just "<containerID>" in older ones. Match the first line.
		firstLine, _, _ := strings.Cut(strings.TrimRight(string(data), "\r\n"), "\n")
		if strings.TrimSpace(firstLine) == containerName {
			_ = os.Remove(path)
		}
	}
}

// setupCNI creates a network namespace, runs CNI ADD, and persists state.
// Returns the netns path.
func setupCNI(ctx context.Context, layout config.Layout, sandboxDir, containerName string) (string, error) {
	if err := ensureCNIConflist(layout); err != nil {
		return "", err
	}

	// Remove any stale IPAM leases from a previous failed or replaced run.
	cleanupStaleIPAMLeases(containerName)

	nsName := "yoloai-" + containerName
	// Delete any stale netns from a previous failed run before creating a fresh one.
	_ = deleteNetNS(nsName)
	netnsPath, err := createNetNS(nsName)
	if err != nil {
		return "", err
	}

	// If CNI ADD fails, clean up both the netns and any partial IPAM allocation.
	if err := runCNIAdd(ctx, layout, netnsPath, sandboxDir, containerName); err != nil {
		_ = deleteNetNS(nsName)
		cleanupStaleIPAMLeases(containerName)

		// DF9: the firewall plugin can silently no-op, leaving the netns wired
		// at the link layer but with no CNI-FORWARD ACCEPT rules. Self-heal by
		// retrying ADD once with a fresh netns + IPAM allocation. Any other
		// failure mode is surfaced directly to the caller.
		if errors.Is(err, errFirewallRulesMissing) {
			slog.Warn("CNI firewall rules missing after ADD; retrying once",
				"event", "sandbox.network.firewall_retry",
				"container", containerName,
				"first_err", err.Error())
			netnsPath, err = createNetNS(nsName)
			if err != nil {
				return "", err
			}
			if retryErr := runCNIAdd(ctx, layout, netnsPath, sandboxDir, containerName); retryErr != nil {
				_ = deleteNetNS(nsName)
				cleanupStaleIPAMLeases(containerName)
				return "", fmt.Errorf("CNI setup (retry after firewall no-op): %w", retryErr)
			}
			return netnsPath, nil
		}

		return "", fmt.Errorf("CNI setup: %w", err)
	}

	return netnsPath, nil
}

// runCNIAdd runs CNI ADD for the given netns and persists cni-state.json.
func runCNIAdd(ctx context.Context, layout config.Layout, netnsPath, sandboxDir, containerName string) error {
	n, err := cni.New(
		cni.WithPluginConfDir(cniConfDir(layout)),
		cni.WithPluginDir([]string{"/opt/cni/bin"}),
	)
	if err != nil {
		return fmt.Errorf("create CNI client: %w", err)
	}

	// WithPluginConfDir only sets the dir; WithConfListFile actually loads the file.
	if err := n.Load(cni.WithConfListFile(filepath.Join(cniConfDir(layout), "yoloai.conflist"))); err != nil {
		return fmt.Errorf("load CNI config: %w", err)
	}

	// Remove any stale libcni results cache so that teardown later reads the
	// result from THIS ADD, not a leftover from a prior run. IPAM leases are
	// already cleaned up by cleanupStaleIPAMLeases before this function runs.
	// We do NOT call n.Remove here because that would also invoke the firewall
	// plugin's DEL handler, which would delete CNI-FORWARD rules — potentially
	// for an already-running container that shares the same IP.
	cleanupStaleCNIResultsCache(containerName)

	result, err := n.Setup(ctx, containerName, netnsPath)
	if err != nil {
		return fmt.Errorf("CNI ADD: %w", err)
	}

	// Extract IP from result for state persistence and the post-ADD firewall
	// rule check. The /32 host form is what the firewall plugin writes into
	// CNI-FORWARD; the /16 subnet form is what setupCNI persists.
	ip := ""
	if iface, ok := result.Interfaces["eth0"]; ok && len(iface.IPConfigs) > 0 {
		ip = iface.IPConfigs[0].IP.String()
	}

	// DF9 verify: n.Setup can return success while the firewall plugin
	// silently no-ops. Two observed signatures lead to the same retry:
	//
	//   1. Empty IPConfigs in the returned result — the same empty-result
	//      pathology that makes the firewall plugin no-op also makes the
	//      bridge IP unrecoverable from the Go result. Bridge still added
	//      POSTROUTING + IPAM lease (we can see them in iptables / lease
	//      files), but they're unreachable from here, so the only safe
	//      thing is to undo and retry.
	//   2. IPConfigs populated but CNI-FORWARD lacks ACCEPT rules for
	//      <ip>/32 — the documented silent no-op variant.
	//
	// Both surface as errFirewallRulesMissing so setupCNI's retry kicks in.
	// On verify failure we run n.Remove to undo bridge's POSTROUTING + IPAM
	// allocation; without this the retry leaks an orphan POSTROUTING entry.
	var verifyErr error
	if ip == "" {
		verifyErr = fmt.Errorf("%w: CNI result has no IPConfigs for eth0 (suspected silent firewall no-op)", errFirewallRulesMissing)
	} else if err := verifyCNIForwardRules(ctx, ip); err != nil {
		verifyErr = err
	}
	if verifyErr != nil {
		if removeErr := n.Remove(ctx, containerName, netnsPath); removeErr != nil {
			slog.Warn("CNI rollback after verify failure also failed; possible POSTROUTING leak",
				"event", "sandbox.network.firewall_rollback_failed",
				"container", containerName,
				"remove_err", removeErr.Error())
		}
		return verifyErr
	}

	stateIP := ip + "/16"
	state := cniState{
		NetnsName: "yoloai-" + containerName,
		NetnsPath: netnsPath,
		Interface: "eth0",
		IP:        stateIP,
	}

	stateDir := filepath.Join(sandboxDir, config.BackendDirName)
	if err := fileutil.MkdirAll(stateDir, 0o750); err != nil {
		return fmt.Errorf("create backend dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal CNI state: %w", err)
	}

	if err := fileutil.WriteFile(cniStatePath(sandboxDir), data, 0o600); err != nil {
		return fmt.Errorf("write CNI state: %w", err)
	}

	return nil
}

// teardownCNI reads cni-state.json and runs CNI DEL to release resources.
// Idempotent: no-op if cni-state.json does not exist.
func teardownCNI(ctx context.Context, layout config.Layout, sandboxDir string) error {
	statePath := cniStatePath(sandboxDir)
	data, err := os.ReadFile(statePath) //nolint:gosec // G304: path is always a trusted sandbox subpath
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already torn down or never set up
		}
		return fmt.Errorf("read CNI state: %w", err)
	}

	var state cniState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse CNI state: %w", err)
	}

	if err := runCNIDel(ctx, layout, state); err != nil {
		// Log but don't fail — best-effort teardown.
		_ = err
	}

	// Derive container name from netns name to clean up IPAM leases.
	containerName := strings.TrimPrefix(state.NetnsName, "yoloai-")
	cleanupStaleIPAMLeases(containerName)

	if err := deleteNetNS(state.NetnsName); err != nil {
		_ = err // best-effort
	}

	// Remove state file so teardown is idempotent.
	_ = os.Remove(statePath)

	return nil
}

// runCNIDel runs CNI DEL for the given state.
func runCNIDel(ctx context.Context, layout config.Layout, state cniState) error {
	n, err := cni.New(
		cni.WithPluginConfDir(cniConfDir(layout)),
		cni.WithPluginDir([]string{"/opt/cni/bin"}),
	)
	if err != nil {
		return fmt.Errorf("create CNI client: %w", err)
	}

	if err := n.Load(cni.WithConfListFile(filepath.Join(cniConfDir(layout), "yoloai.conflist"))); err != nil {
		return fmt.Errorf("load CNI config: %w", err)
	}

	// Derive container name from netns name (strip "yoloai-" prefix).
	containerName := state.NetnsName
	if len(containerName) > 7 {
		containerName = containerName[7:] // strip "yoloai-"
	}

	return n.Remove(ctx, containerName, state.NetnsPath)
}

// verifyCNIForwardRules confirms the CNI firewall plugin actually installed
// ACCEPT rules for ip in the CNI-FORWARD chain. Returns errFirewallRulesMissing
// when iptables succeeds but no matching rule is present (the DF9 signature) —
// callers retry on this. Other failures (iptables binary missing, exec error)
// are returned wrapped, since they indicate something different.
//
// ip must be the bare dotted form (e.g. "10.89.1.90"); the firewall plugin
// writes rules with the /32 host mask.
func verifyCNIForwardRules(ctx context.Context, ip string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "iptables", "-t", "filter", "-S", "CNI-FORWARD").CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables -S CNI-FORWARD for %s: %w (output: %s)", ip, err, strings.TrimSpace(string(out)))
	}
	if !cniForwardHasIP(string(out), ip) {
		return fmt.Errorf("%w: ip %s", errFirewallRulesMissing, ip)
	}
	return nil
}

// cniForwardHasIP returns true if the iptables -S CNI-FORWARD dump contains
// at least one ACCEPT rule referencing ip with the /32 host mask. The CNI
// firewall plugin writes two rules per container (RELATED,ESTABLISHED inbound
// and outbound ACCEPT); either being present is sufficient to confirm the
// plugin ran. Pure function; tested independently of iptables.
func cniForwardHasIP(chainDump, ip string) bool {
	needle := ip + "/32"
	for _, line := range strings.Split(chainDump, "\n") {
		if strings.Contains(line, needle) && strings.Contains(line, "ACCEPT") {
			return true
		}
	}
	return false
}
