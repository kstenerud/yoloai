package cli

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// RunPager pipes content through $PAGER (or "less -R" fallback) when stdout
// is a TTY. When stdout is not a TTY (piped), copies content directly to stdout.
func RunPager(r io.Reader) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) { //nolint:gosec // fd conversion is safe on all supported platforms
		_, err := io.Copy(os.Stdout, r)
		return err
	}

	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}

	var args []string
	if strings.HasSuffix(pager, "less") || pager == "less" {
		args = []string{"-R"}
	}

	cmd := exec.Command(pager, args...) //nolint:gosec // G204: pager is from $PAGER env or "less" default
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Pager not found or failed — fall back to direct copy
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			_, copyErr := io.Copy(os.Stdout, r)
			return copyErr
		}
		return err
	}

	return nil
}
