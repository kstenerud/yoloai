package containerdrt

// ABOUTME: CNI network setup and teardown for containerd-managed containers.
// Creates network namespaces, runs CNI ADD/DEL, and persists state for idempotent teardown.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cni "github.com/containerd/go-cni"
	"github.com/vishvananda/netns"

	"github.com/kstenerud/yoloai/config"
)

// cniStateFileName is the filename for per-sandbox CNI state.
const cniStateFileName = "cni-state.json"

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
        "subnet": "10.88.0.0/16",
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
func cniConfDir() string {
	return filepath.Join(config.YoloaiDir(), "cni")
}

// cniStatePath returns the path to the CNI state file for a sandbox.
func cniStatePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, config.BackendDirName, cniStateFileName)
}

// ensureCNIConflist writes the yoloai CNI conflist if it does not already exist.
func ensureCNIConflist() error {
	dir := cniConfDir()
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G301: 0750 is appropriate for CNI config dir
		return fmt.Errorf("create CNI config dir: %w", err)
	}
	path := filepath.Join(dir, "yoloai.conflist")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	if err := os.WriteFile(path, []byte(cniConflistTemplate), 0o644); err != nil { //nolint:gosec // G306: world-readable config is correct
		return fmt.Errorf("write CNI conflist: %w", err)
	}
	return nil
}

// createNetNS creates a named network namespace and returns its path.
// The namespace is created at /var/run/netns/<name> (standard Linux path).
func createNetNS(name string) (string, error) {
	ns, err := netns.NewNamed(name)
	if err != nil {
		return "", fmt.Errorf("create netns %s: %w", name, err)
	}
	_ = ns.Close() //nolint:gosec // G104: fd close only — namespace persists at /var/run/netns/<name>
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

// setupCNI creates a network namespace, runs CNI ADD, and persists state.
// Returns the netns path.
func setupCNI(ctx context.Context, sandboxDir, containerName string) (string, error) {
	if err := ensureCNIConflist(); err != nil {
		return "", err
	}

	nsName := "yoloai-" + containerName
	netnsPath, err := createNetNS(nsName)
	if err != nil {
		return "", err
	}

	// If CNI ADD fails, clean up the netns to avoid leaking it.
	if err := runCNIAdd(ctx, netnsPath, sandboxDir, containerName); err != nil {
		_ = deleteNetNS(nsName)
		return "", fmt.Errorf("CNI setup: %w", err)
	}

	return netnsPath, nil
}

// runCNIAdd runs CNI ADD for the given netns and persists cni-state.json.
func runCNIAdd(ctx context.Context, netnsPath, sandboxDir, containerName string) error {
	n, err := cni.New(
		cni.WithPluginConfDir(cniConfDir()),
		cni.WithPluginDir([]string{"/opt/cni/bin"}),
	)
	if err != nil {
		return fmt.Errorf("create CNI client: %w", err)
	}

	// WithPluginConfDir only sets the dir; WithConfListFile actually loads the file.
	if err := n.Load(cni.WithConfListFile(filepath.Join(cniConfDir(), "yoloai.conflist"))); err != nil {
		return fmt.Errorf("load CNI config: %w", err)
	}

	result, err := n.Setup(ctx, containerName, netnsPath)
	if err != nil {
		return fmt.Errorf("CNI ADD: %w", err)
	}

	// Extract IP from result for state persistence.
	ip := ""
	if iface, ok := result.Interfaces["eth0"]; ok && len(iface.IPConfigs) > 0 {
		ip = iface.IPConfigs[0].IP.String() + "/16"
	}

	state := cniState{
		NetnsName: "yoloai-" + containerName,
		NetnsPath: netnsPath,
		Interface: "eth0",
		IP:        ip,
	}

	stateDir := filepath.Join(sandboxDir, config.BackendDirName)
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return fmt.Errorf("create backend dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal CNI state: %w", err)
	}

	if err := os.WriteFile(cniStatePath(sandboxDir), data, 0o600); err != nil {
		return fmt.Errorf("write CNI state: %w", err)
	}

	return nil
}

// teardownCNI reads cni-state.json and runs CNI DEL to release resources.
// Idempotent: no-op if cni-state.json does not exist.
func teardownCNI(ctx context.Context, sandboxDir string) error {
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

	if err := runCNIDel(ctx, state); err != nil {
		// Log but don't fail — best-effort teardown.
		_ = err
	}

	if err := deleteNetNS(state.NetnsName); err != nil {
		_ = err // best-effort
	}

	// Remove state file so teardown is idempotent.
	_ = os.Remove(statePath)

	return nil
}

// runCNIDel runs CNI DEL for the given state.
func runCNIDel(ctx context.Context, state cniState) error {
	n, err := cni.New(
		cni.WithPluginConfDir(cniConfDir()),
		cni.WithPluginDir([]string{"/opt/cni/bin"}),
	)
	if err != nil {
		return fmt.Errorf("create CNI client: %w", err)
	}

	if err := n.Load(cni.WithConfListFile(filepath.Join(cniConfDir(), "yoloai.conflist"))); err != nil {
		return fmt.Errorf("load CNI config: %w", err)
	}

	// Derive container name from netns name (strip "yoloai-" prefix).
	containerName := state.NetnsName
	if len(containerName) > 7 {
		containerName = containerName[7:] // strip "yoloai-"
	}

	return n.Remove(ctx, containerName, state.NetnsPath)
}
