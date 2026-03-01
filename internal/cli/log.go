package cli

// ABOUTME: Sandbox log display logic shared by `yoloai sandbox log` and the
// ABOUTME: top-level `yoloai log` shortcut.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log <name>",
		Short: "Show sandbox session log",
		Args:  cobra.ArbitraryArgs,
		RunE:  runLog,
	}
	cmd.Flags().Bool("no-strip", false, "Show raw output with ANSI escape sequences")
	return cmd
}

// runLog is the shared implementation for `sandbox log` and the `log` alias.
func runLog(cmd *cobra.Command, args []string) error {
	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}

	sandboxDir, err := sandbox.RequireSandboxDir(name)
	if err != nil {
		return err
	}

	logPath := filepath.Join(sandboxDir, "log.txt")

	if jsonEnabled(cmd) {
		data, readErr := os.ReadFile(logPath) //nolint:gosec
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{"content": ""})
			}
			return fmt.Errorf("open log file: %w", readErr)
		}
		return writeJSON(cmd.OutOrStdout(), map[string]string{"content": string(data)})
	}

	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "No log output yet") //nolint:errcheck
			return nil
		}
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	noStrip, _ := cmd.Flags().GetBool("no-strip")
	if noStrip {
		_, err = io.Copy(cmd.OutOrStdout(), f)
	} else {
		err = stripANSI(cmd.OutOrStdout(), f)
	}
	return err
}
