// ABOUTME: CLI app state at TOP/cli/state.yaml — app-owned setup ceremony the
// ABOUTME: library has no business knowing (e.g. the one-shot first-run tip).

package cliutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// CLIState is the CLI app's own on-disk state, persisted at CLIStatePath()
// (TOP/cli/state.yaml). It records app-side setup ceremony — the kind of
// "has the wizard run" bookkeeping the library deliberately stopped owning
// (the library just-works from declarative defaults; the app owns ceremony).
// Today it carries only the first-run onboarding-tip flag.
type CLIState struct {
	// FirstRunTipShown records that the one-time "enable shell completions"
	// onboarding tip has already been printed, so it fires exactly once.
	FirstRunTipShown bool `yaml:"first_run_tip_shown"`
}

// LoadCLIState reads CLIStatePath(). A missing file is not an error — it
// returns a zero-value CLIState (the first-run state).
func LoadCLIState() (*CLIState, error) {
	data, err := os.ReadFile(CLIStatePath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &CLIState{}, nil
		}
		return nil, fmt.Errorf("read cli state: %w", err)
	}
	var s CLIState
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse cli state: %w", err)
	}
	return &s, nil
}

// SaveCLIState writes state to CLIStatePath(), creating TOP/cli if needed.
func SaveCLIState(s *CLIState) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal cli state: %w", err)
	}
	if err := fileutil.MkdirAll(CLIDir(), 0750); err != nil {
		return fmt.Errorf("create cli namespace: %w", err)
	}
	if err := fileutil.WriteFile(CLIStatePath(), data, 0600); err != nil {
		return fmt.Errorf("write cli state: %w", err)
	}
	return nil
}

// firstRunTip is the one-time onboarding nudge shown after the CLI first does
// real work. It is presentation/ceremony, so it lives in the app, keyed off
// CLIState.FirstRunTipShown.
const firstRunTip = "Tip: enable shell completions with 'yoloai system completion --help'"

// MaybeShowFirstRunTip prints the one-time onboarding tip the first time the
// CLI does real work, then records that it has been shown so it never repeats.
// Best-effort: state errors are swallowed because the tip is a nicety, not
// correctness — a failure to persist the flag at worst shows the tip twice.
func MaybeShowFirstRunTip(w io.Writer) {
	s, err := LoadCLIState()
	if err != nil || s.FirstRunTipShown {
		return
	}
	fmt.Fprintln(w, firstRunTip) //nolint:errcheck // best-effort onboarding output
	s.FirstRunTipShown = true
	_ = SaveCLIState(s)
}
