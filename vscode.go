// ABOUTME: SystemClient.VscodeAttach — resolve the VS Code attach-to-container
// ABOUTME: details (container name, workdir, folder URI) for a sandbox.
package yoloai

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// VscodeAttach describes how to open a sandbox in VS Code via its
// attach-to-running-container support. Supported reports whether the sandbox's
// backend exposes a docker-compatible container surface; when false, the
// container fields and FolderURI are empty and the caller should fall back to a
// VS Code Remote Tunnel.
type VscodeAttach struct {
	Backend       BackendName
	Supported     bool
	ContainerName string
	WorkdirPath   string
	FolderURI     string // vscode-remote://attached-container+<hex>... ; empty when unsupported
}

// VscodeAttach resolves the VS Code attach details for a sandbox. It reads the
// sandbox metadata and the backend's declared capabilities — no running backend
// is required. A missing sandbox yields ErrSandboxNotFound.
func (s *SystemClient) VscodeAttach(name string) (*VscodeAttach, error) {
	sandboxDir := s.layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, sandbox.ErrSandboxNotFound
	}
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load sandbox metadata: %w", err)
	}

	res := &VscodeAttach{Backend: meta.Backend}

	desc, ok := runtime.Descriptor(meta.Backend)
	if !ok || !desc.Capabilities.ContainerAttach {
		return res, nil
	}

	res.Supported = true
	res.ContainerName = store.InstanceName(meta.Principal, meta.Name)
	res.WorkdirPath = meta.Workdir.MountPath

	payload, err := json.Marshal(map[string]string{"containerName": res.ContainerName})
	if err != nil {
		return nil, fmt.Errorf("marshal container payload: %w", err)
	}
	res.FolderURI = fmt.Sprintf("vscode-remote://attached-container+%s%s",
		hex.EncodeToString(payload), res.WorkdirPath)
	return res, nil
}
