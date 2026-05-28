// ABOUTME: RenderNotices prints the structured Notices returned by orchestration
// ABOUTME: methods (F8) — warnings to stderr, info to stdout (suppressed in JSON
// ABOUTME: mode so they don't corrupt the JSON document on stdout).

package cliutil

import (
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

// RenderNotices writes a method's advisory notices for the user: warnings go to
// stderr (always — they don't corrupt JSON on stdout), info goes to stdout in
// human mode and is suppressed in JSON mode.
func RenderNotices(cmd *cobra.Command, ns []yoloai.Notice) {
	jsonMode := JSONEnabled(cmd)
	for _, n := range ns {
		switch n.Level {
		case yoloai.NoticeWarn:
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", n.Message) //nolint:errcheck // best-effort
		case yoloai.NoticeInfo:
			if !jsonMode {
				fmt.Fprintln(cmd.OutOrStdout(), n.Message) //nolint:errcheck // best-effort
			}
		}
	}
}
