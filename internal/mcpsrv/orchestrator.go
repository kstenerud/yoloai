// ABOUTME: SandboxService is the testability seam between tool handlers and the
// ABOUTME: concrete yoloai.Client; clientService wraps the real client as the adapter.
package mcpsrv

import (
	"context"

	"github.com/kstenerud/yoloai"
)

// SandboxService abstracts all sandbox operations used by the MCP server's tool
// handlers and proxy. The seam decouples them from the concrete *yoloai.Client
// chain so handlers can be tested with a fake implementation.
type SandboxService interface {
	EnsureSetup(ctx context.Context) error
	CreateSandbox(ctx context.Context, opts yoloai.SandboxCreateOptions) error
	Start(ctx context.Context, name string) error
	ListSandboxes(ctx context.Context) ([]*yoloai.SandboxInfo, error)
	Inspect(ctx context.Context, name string) (*yoloai.SandboxInfo, error)
	Wait(ctx context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error)
	Reset(ctx context.Context, name string, opts yoloai.SandboxResetOptions) error
	HasActiveWork(ctx context.Context, name string) (active bool, reason string, err error)
	Destroy(ctx context.Context, name string, opts yoloai.SandboxDestroyOptions) error
	Diff(ctx context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error)
	TerminalLog(ctx context.Context, name string, lines int) (string, error)
	SendInput(ctx context.Context, name, text string) error
	ListFiles(ctx context.Context, name string) ([]string, error)
	ReadFile(ctx context.Context, name, rel string) ([]byte, error)
	WriteFile(ctx context.Context, name, rel string, data []byte) error
	CacheDir(ctx context.Context, name string) (string, error)
	Exec(ctx context.Context, name string, opts yoloai.SandboxExecOptions, io yoloai.IOStreams) error
	// FilesDir returns the host-side path to the sandbox's file-exchange
	// directory. Used by the proxy for {files} placeholder expansion; tool
	// handlers use the content-oriented ListFiles/ReadFile/WriteFile instead.
	FilesDir(ctx context.Context, name string) (string, error)
}

// clientService implements SandboxService by delegating to a *yoloai.Client.
type clientService struct{ client *yoloai.Client }

func newClientService(c *yoloai.Client) *clientService { return &clientService{client: c} }

var _ SandboxService = (*clientService)(nil)

func (cs *clientService) EnsureSetup(ctx context.Context) error {
	return cs.client.EnsureSetup(ctx)
}

func (cs *clientService) CreateSandbox(ctx context.Context, opts yoloai.SandboxCreateOptions) error {
	_, err := cs.client.CreateSandbox(ctx, opts)
	return err
}

func (cs *clientService) Start(ctx context.Context, name string) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	_, err = sb.Start(ctx, yoloai.SandboxStartOptions{})
	return err
}

func (cs *clientService) ListSandboxes(ctx context.Context) ([]*yoloai.SandboxInfo, error) {
	return cs.client.ListSandboxes(ctx)
}

func (cs *clientService) Inspect(ctx context.Context, name string) (*yoloai.SandboxInfo, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return nil, err
	}
	return sb.Inspect(ctx)
}

func (cs *clientService) Wait(ctx context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return nil, err
	}
	return sb.Wait(ctx, opts)
}

func (cs *clientService) Reset(ctx context.Context, name string, opts yoloai.SandboxResetOptions) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	_, err = sb.Reset(ctx, opts)
	return err
}

func (cs *clientService) HasActiveWork(ctx context.Context, name string) (bool, string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return false, "", err
	}
	active, reason := sb.HasActiveWork(ctx)
	return active, reason, nil
}

func (cs *clientService) Destroy(ctx context.Context, name string, opts yoloai.SandboxDestroyOptions) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	_, err = sb.Destroy(ctx, opts)
	return err
}

func (cs *clientService) Diff(ctx context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return "", err
	}
	return sb.Workdir().Diff(ctx, opts)
}

// TerminalLog reads the last lines of the agent's terminal log. The ctx param
// satisfies the interface but is not forwarded — Agent.TerminalLog takes no ctx.
func (cs *clientService) TerminalLog(_ context.Context, name string, lines int) (string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return "", err
	}
	return sb.Agent().TerminalLog(lines)
}

func (cs *clientService) SendInput(ctx context.Context, name, text string) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	return sb.Agent().SendInput(ctx, text)
}

// ListFiles lists all files in the sandbox's file-exchange directory. The
// ".*" pattern is included alongside "*" so hidden (dot-prefixed) files are
// listed too — "*" alone does not match them.
func (cs *clientService) ListFiles(_ context.Context, name string) ([]string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return nil, err
	}
	return sb.Files().List([]string{"*", ".*"})
}

// ReadFile returns the bytes of a file in the sandbox's file-exchange directory.
// Path containment is enforced by the library.
func (cs *clientService) ReadFile(_ context.Context, name, rel string) ([]byte, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return nil, err
	}
	return sb.Files().ReadFile(rel)
}

// WriteFile writes data to a file in the sandbox's file-exchange directory.
// Path containment is enforced by the library.
func (cs *clientService) WriteFile(_ context.Context, name, rel string, data []byte) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	return sb.Files().WriteFile(rel, data)
}

// CacheDir returns the host-side path to the sandbox's cache directory.
func (cs *clientService) CacheDir(_ context.Context, name string) (string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return "", err
	}
	return sb.CacheDir(), nil
}

func (cs *clientService) Exec(ctx context.Context, name string, opts yoloai.SandboxExecOptions, io yoloai.IOStreams) error {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return err
	}
	return sb.Exec(ctx, opts, io)
}

// FilesDir returns the host-side path to the sandbox's file-exchange directory.
func (cs *clientService) FilesDir(_ context.Context, name string) (string, error) {
	sb, err := cs.client.Sandbox(name)
	if err != nil {
		return "", err
	}
	return sb.Files().Path(), nil
}
