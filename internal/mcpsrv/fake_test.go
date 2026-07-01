// ABOUTME: fakeService is a test double for SandboxService — each method delegates
// ABOUTME: to a per-method func field; nil fields return sensible zero values.
package mcpsrv

import (
	"context"

	"github.com/kstenerud/yoloai"
)

// fakeService implements SandboxService for unit tests. Set the *Fn field for
// any method the test cares about; nil fields return zero values and nil errors.
type fakeService struct {
	EnsureSetupFn   func(ctx context.Context) error
	CreateSandboxFn func(ctx context.Context, opts yoloai.SandboxCreateOptions) error
	StartFn         func(ctx context.Context, name string) error
	ListSandboxesFn func(ctx context.Context) ([]*yoloai.SandboxInfo, error)
	InspectFn       func(ctx context.Context, name string) (*yoloai.SandboxInfo, error)
	WaitFn          func(ctx context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error)
	ResetFn         func(ctx context.Context, name string, opts yoloai.SandboxResetOptions) error
	HasActiveWorkFn func(ctx context.Context, name string) (bool, string, error)
	DestroyFn       func(ctx context.Context, name string, opts yoloai.SandboxDestroyOptions) error
	DiffFn          func(ctx context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error)
	TerminalLogFn   func(ctx context.Context, name string, lines int) (string, error)
	SendInputFn     func(ctx context.Context, name, text string) error
	ListFilesFn     func(ctx context.Context, name string) ([]string, error)
	ReadFileFn      func(ctx context.Context, name, rel string) ([]byte, error)
	WriteFileFn     func(ctx context.Context, name, rel string, data []byte) error
	CacheDirFn      func(ctx context.Context, name string) (string, error)
	ExecFn          func(ctx context.Context, name string, opts yoloai.SandboxExecOptions, io yoloai.IOStreams) error
	FilesDirFn      func(ctx context.Context, name string) (string, error)
}

var _ SandboxService = (*fakeService)(nil)

func (f *fakeService) EnsureSetup(ctx context.Context) error {
	if f.EnsureSetupFn != nil {
		return f.EnsureSetupFn(ctx)
	}
	return nil
}

func (f *fakeService) CreateSandbox(ctx context.Context, opts yoloai.SandboxCreateOptions) error {
	if f.CreateSandboxFn != nil {
		return f.CreateSandboxFn(ctx, opts)
	}
	return nil
}

func (f *fakeService) Start(ctx context.Context, name string) error {
	if f.StartFn != nil {
		return f.StartFn(ctx, name)
	}
	return nil
}

func (f *fakeService) ListSandboxes(ctx context.Context) ([]*yoloai.SandboxInfo, error) {
	if f.ListSandboxesFn != nil {
		return f.ListSandboxesFn(ctx)
	}
	return nil, nil
}

func (f *fakeService) Inspect(ctx context.Context, name string) (*yoloai.SandboxInfo, error) {
	if f.InspectFn != nil {
		return f.InspectFn(ctx, name)
	}
	return nil, nil
}

func (f *fakeService) Wait(ctx context.Context, name string, opts yoloai.SandboxWaitOptions) (*yoloai.SandboxInfo, error) {
	if f.WaitFn != nil {
		return f.WaitFn(ctx, name, opts)
	}
	return nil, nil
}

func (f *fakeService) Reset(ctx context.Context, name string, opts yoloai.SandboxResetOptions) error {
	if f.ResetFn != nil {
		return f.ResetFn(ctx, name, opts)
	}
	return nil
}

func (f *fakeService) HasActiveWork(ctx context.Context, name string) (bool, string, error) {
	if f.HasActiveWorkFn != nil {
		return f.HasActiveWorkFn(ctx, name)
	}
	return false, "", nil
}

func (f *fakeService) Destroy(ctx context.Context, name string, opts yoloai.SandboxDestroyOptions) error {
	if f.DestroyFn != nil {
		return f.DestroyFn(ctx, name, opts)
	}
	return nil
}

func (f *fakeService) Diff(ctx context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
	if f.DiffFn != nil {
		return f.DiffFn(ctx, name, opts)
	}
	return "", nil
}

func (f *fakeService) TerminalLog(ctx context.Context, name string, lines int) (string, error) {
	if f.TerminalLogFn != nil {
		return f.TerminalLogFn(ctx, name, lines)
	}
	return "", nil
}

func (f *fakeService) SendInput(ctx context.Context, name, text string) error {
	if f.SendInputFn != nil {
		return f.SendInputFn(ctx, name, text)
	}
	return nil
}

func (f *fakeService) ListFiles(ctx context.Context, name string) ([]string, error) {
	if f.ListFilesFn != nil {
		return f.ListFilesFn(ctx, name)
	}
	return nil, nil
}

func (f *fakeService) ReadFile(ctx context.Context, name, rel string) ([]byte, error) {
	if f.ReadFileFn != nil {
		return f.ReadFileFn(ctx, name, rel)
	}
	return nil, nil
}

func (f *fakeService) WriteFile(ctx context.Context, name, rel string, data []byte) error {
	if f.WriteFileFn != nil {
		return f.WriteFileFn(ctx, name, rel, data)
	}
	return nil
}

func (f *fakeService) CacheDir(ctx context.Context, name string) (string, error) {
	if f.CacheDirFn != nil {
		return f.CacheDirFn(ctx, name)
	}
	return "", nil
}

func (f *fakeService) Exec(ctx context.Context, name string, opts yoloai.SandboxExecOptions, io yoloai.IOStreams) error {
	if f.ExecFn != nil {
		return f.ExecFn(ctx, name, opts, io)
	}
	return nil
}

func (f *fakeService) FilesDir(ctx context.Context, name string) (string, error) {
	if f.FilesDirFn != nil {
		return f.FilesDirFn(ctx, name)
	}
	return "", nil
}
