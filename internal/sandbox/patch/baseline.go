// ABOUTME: CAS-guarded baseline mutation (advance/set) and the baseline-log
// ABOUTME: read-model. The lock + compare-and-swap live here so two clients
// ABOUTME: can't silently clobber each other's baseline moves.
package patch

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// BaselineChange reports the outcome of a baseline mutation: the new baseline
// commit and its subject line. The prior baseline equals the caller's
// expectedCurrentSHA on success (the CAS guarantees it), so it isn't repeated.
type BaselineChange struct {
	NewSHA  string
	Subject string
}

// BaselineLogEntry is one commit in a sandbox work copy's history from inception
// to HEAD. IsBaseline marks the commit the diff baseline currently points at.
type BaselineLogEntry struct {
	SHA        string
	Subject    string
	IsBaseline bool
}

// BaselineConflictError is returned when a baseline compare-and-swap fails: the
// current baseline did not match the caller's expected value. It carries both
// SHAs so a defeated caller can recover — re-read and retry, or set the SHA it
// intended explicitly. An empty SHA renders as "(none)".
type BaselineConflictError struct {
	Expected string
	Actual   string
}

func (e *BaselineConflictError) Error() string {
	return fmt.Sprintf("baseline changed since read: expected %s, found %s",
		shaForError(e.Expected), shaForError(e.Actual))
}

func shaForError(sha string) string {
	if sha == "" {
		return "(none)"
	}
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// AdvanceBaselineCAS moves the diff baseline to the current HEAD of the sandbox
// work copy, but only if the stored baseline still equals expectedCurrentSHA
// (compare-and-swap). On mismatch it returns *BaselineConflictError without
// writing. expectedCurrentSHA == "" means "expect no baseline yet" and is valid
// only when none is set. :rw and :overlay workdirs are refused with a
// *UsageError (their baselines aren't host-tracked).
func AdvanceBaselineCAS(ctx context.Context, layout config.Layout, rt runtime.Runtime, name, expectedCurrentSHA string) (*BaselineChange, error) {
	return mutateBaseline(ctx, layout, rt, name, expectedCurrentSHA, func(workDir string) (string, error) {
		out, err := runtime.GitExecFor(ctx, rt, name, workDir, "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("resolve HEAD: %w", err)
		}
		return strings.TrimSpace(out), nil
	})
}

// SetBaselineCAS moves the diff baseline to the commit named by ref (a short
// SHA, full SHA, or any rev git can resolve), guarded by the same
// compare-and-swap as AdvanceBaselineCAS.
func SetBaselineCAS(ctx context.Context, layout config.Layout, rt runtime.Runtime, name, expectedCurrentSHA, ref string) (*BaselineChange, error) {
	return mutateBaseline(ctx, layout, rt, name, expectedCurrentSHA, func(workDir string) (string, error) {
		out, err := runtime.GitExecFor(ctx, rt, name, workDir, "rev-parse", ref)
		if err != nil {
			return "", fmt.Errorf("resolve sha %q: %w", ref, err)
		}
		return strings.TrimSpace(out), nil
	})
}

// mutateBaseline performs the lock + CAS + write shared by advance and set. The
// per-sandbox lock makes the read-compare-write atomic against concurrent
// operations on the same host; commitBaseline runs the CAS + resolve + write
// under it, so a conflict short-circuits before any git work.
func mutateBaseline(ctx context.Context, layout config.Layout, rt runtime.Runtime, name, expectedCurrentSHA string, resolve func(workDir string) (string, error)) (*BaselineChange, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	unlock, err := store.AcquireLock(layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}
	if err := checkBaselineMode(meta.Workdir.Mode); err != nil {
		return nil, err
	}

	sha, err := commitBaseline(sandboxDir, meta, &expectedCurrentSHA, resolve)
	if err != nil {
		return nil, err
	}

	// Subject is cosmetic — a failed lookup must not fail the move.
	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	subject := ""
	if out, subjErr := runtime.GitExecFor(ctx, rt, name, workDir, "log", "--format=%s", "-1", sha); subjErr == nil {
		subject = strings.TrimSpace(out)
	}
	return &BaselineChange{NewSHA: sha, Subject: subject}, nil
}

// commitBaseline is the CAS + resolve + write core shared by the lock-free
// apply-path advance and the CAS-guarded user verbs. The caller has already
// loaded meta (under whatever lock it holds) and validated the workdir mode.
// When expected is non-nil it requires the stored baseline to equal *expected
// (compare-and-swap), returning *BaselineConflictError without writing on
// mismatch; resolve computes the target SHA from the work dir and runs only
// after the CAS passes. Returns the written SHA.
func commitBaseline(sandboxDir string, meta *store.Environment, expected *string, resolve func(workDir string) (string, error)) (string, error) {
	if expected != nil && meta.Workdir.BaselineSHA != *expected {
		return "", &BaselineConflictError{Expected: *expected, Actual: meta.Workdir.BaselineSHA}
	}
	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	sha, err := resolve(workDir)
	if err != nil {
		return "", err
	}
	meta.Workdir.BaselineSHA = sha
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return "", err
	}
	return sha, nil
}

// advanceBaselineUnlocked is the lock-free baseline write used by the apply
// path, which already holds the per-sandbox lock. It treats :rw and :overlay as
// a no-op — their baselines aren't host-tracked (live mount / overlay upper
// layer) — matching the apply flow, which only reaches here in copy mode
// anyway. resolve computes the target SHA from the loaded work dir.
func advanceBaselineUnlocked(layout config.Layout, name string, resolve func(workDir string) (string, error)) error {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return err
	}
	if meta.Workdir.Mode == store.DirModeRW || meta.Workdir.Mode == store.DirModeOverlay {
		return nil
	}
	_, err = commitBaseline(sandboxDir, meta, nil, resolve)
	return err
}

// checkBaselineMode rejects the workdir modes that don't carry a host-tracked
// baseline. copy / ro / "" fall through (nil).
func checkBaselineMode(mode store.DirMode) error {
	switch mode {
	case store.DirModeRW:
		return yoerrors.NewUsageError("baseline is not tracked for :rw directories")
	case store.DirModeOverlay:
		return yoerrors.NewUsageError("use git commands inside the container to manage overlay baselines")
	case store.DirModeCopy, store.DirModeRO, "":
		return nil
	}
	return nil
}

// BaselineLog returns the sandbox work copy's commit history from the sandbox
// inception commit to HEAD, marking the current baseline, so the lineage stays
// useful for recovery even after an accidental baseline advance. Entries are
// newest-first (the agent's commits), followed by the inception commit.
//
// Inception detection priority:
//  1. meta.Workdir.InceptionSHA (written at sandbox creation for new sandboxes)
//  2. first commit authored by yoloai@localhost (legacy fresh-repo sandboxes)
//  3. full log fallback (old sandboxes on existing repos with no marker)
func BaselineLog(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string) ([]BaselineLogEntry, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}
	if err := checkBaselineMode(meta.Workdir.Mode); err != nil {
		return nil, err
	}

	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	baselineSHA := meta.Workdir.BaselineSHA

	inceptionSHA := meta.Workdir.InceptionSHA
	if inceptionSHA == "" {
		if out, gitErr := runtime.GitExecFor(ctx, rt, name, workDir,
			"log", "--format=%H", "--author=yoloai@localhost", "--reverse", "--max-count=1",
		); gitErr == nil {
			inceptionSHA = strings.TrimSpace(out)
		}
	}

	if inceptionSHA == "" {
		out, logErr := runtime.GitExecFor(ctx, rt, name, workDir, "log", "--format=%H %s")
		if logErr != nil {
			return nil, fmt.Errorf("git log: %w", logErr)
		}
		return parseBaselineLog(out, baselineSHA), nil
	}

	out, err := runtime.GitExecFor(ctx, rt, name, workDir, "log", "--format=%H %s", inceptionSHA+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	entries := parseBaselineLog(out, baselineSHA)

	inceptionLine, err := runtime.GitExecFor(ctx, rt, name, workDir, "log", "--format=%H %s", "-1", inceptionSHA)
	if err != nil {
		return nil, fmt.Errorf("git log inception: %w", err)
	}
	return append(entries, parseBaselineLog(inceptionLine, baselineSHA)...), nil
}

// parseBaselineLog turns "%H %s" log output (one commit per line) into entries,
// marking the commit whose full SHA equals baselineSHA.
func parseBaselineLog(output, baselineSHA string) []BaselineLogEntry {
	entries := make([]BaselineLogEntry, 0)
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fullSHA, subject, _ := strings.Cut(line, " ")
		entries = append(entries, BaselineLogEntry{
			SHA:        fullSHA,
			Subject:    subject,
			IsBaseline: baselineSHA != "" && fullSHA == baselineSHA,
		})
	}
	return entries
}
