// ABOUTME: Apple-only Dockerfile preflight. Apple's builder rejects a Dockerfile
// ABOUTME: above an effective size ceiling with an opaque stream error and no
// ABOUTME: output, so the materialized context's Dockerfile has its prose
// ABOUTME: comments blanked out and its size checked before the build (DF229).

package apple

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// maxDockerfileBytes is the largest Dockerfile we hand to `container build`.
//
// apple/container#735 documents a 16384-byte cap, and at 16384 the CLI does
// report it cleanly (`invalidArgument: Dockerfile size (N bytes) exceeds …`).
// That is not the operative limit. Below 16384 and above an *effective* ceiling
// the build dies instantly with `unavailable: "Stream unexpectedly closed."`
// and no output whatsoever — no step list, no error line, nothing to search for.
//
// The effective ceiling is content-dependent, and nothing simple predicts it.
// Measured 2026-08-19 on container CLI 1.0.0:
//
//	shipped base Dockerfile (21 instructions, 16 non-ASCII)   ~15070
//	same instructions, em-dash padding                        ~15238
//	same instructions, all-ASCII padding                      ~15967
//	synthetic ASCII,  20 / 60 / 120 instructions       15016 / 15102 / 15330
//
// So instruction count barely moves it, non-ASCII characters lower it (they cost
// several times their bytes), and the whole band spans ~1 KB with no clean model.
// This constant is therefore a deliberately conservative proxy, set two band-
// widths below the lowest ceiling ever measured (15016) rather than just under
// it — the last gate here was calibrated to a number that looked safe and was
// not. Stripping prose takes the base Dockerfile to ~8.4 KB, so the margin costs
// nothing anyone is using. Raising it is fine, with fresh measurements.
const maxDockerfileBytes = 13000

// dockerfileDirectives are the comment forms that are *metadata*, not prose, and
// must survive stripping. Parser directives (`syntax`, `escape`, `check`) change
// how the file is built — dropping `# syntax=` silently swaps the frontend — and
// the linter pragmas are load-bearing for anything that lints a materialized
// context. Everything else in a `#` line is commentary.
var dockerfileDirectives = regexp.MustCompile(`^\s*#\s*(syntax|escape|check)\s*=|^\s*#\s*(hadolint|shellcheck)\s`)

// heredocStart matches a heredoc redirection (`<<EOF`, `<<-'EOF'`, `<<"EOF"`) and
// captures the delimiter word.
var heredocStart = regexp.MustCompile(`<<-?\s*["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

// prepareDockerfile rewrites the Dockerfile in a materialized build context so
// Apple's builder will accept it, then refuses the build outright if it is still
// too large. Apple-scoped on purpose: docker, podman and containerd have no such
// limit and get the file as written. When apple/container fixes #735 and the
// underlying ceiling, this whole file is what gets deleted.
//
// A context without a Dockerfile is left alone — `container build` reports that
// far better than a preflight guess could.
func prepareDockerfile(dir string) error {
	path := filepath.Join(dir, "Dockerfile")
	src, err := os.ReadFile(path) //nolint:gosec // G304: path is a directory we just materialized
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read build context Dockerfile: %w", err)
	}

	stripped := stripDockerfileProse(src)
	if len(stripped) > maxDockerfileBytes {
		return fmt.Errorf("build context Dockerfile is %d bytes (%d after stripping comments), over the %d-byte limit the apple builder accepts: "+
			"above it `container build` fails instantly with `unavailable: \"Stream unexpectedly closed.\"` and no output at all. "+
			"shorten the Dockerfile — fewer or shorter instructions, and prefer ASCII, which this builder charges far more for as multi-byte characters",
			len(src), len(stripped), maxDockerfileBytes)
	}
	if bytes.Equal(src, stripped) {
		return nil
	}
	return fileutil.WriteFile(path, stripped, 0o644)
}

// stripDockerfileProse blanks every prose comment line, leaving the line itself
// in place. Blanking rather than deleting is the point: BuildKit reports errors
// by line number against the file it was handed, so removing lines would make
// every diagnostic point at the wrong line of the Dockerfile in the repo. An
// empty line costs one byte against a comment's seventy, so nothing is lost by
// keeping it.
//
// Two kinds of `#` line are not comments and are left exactly as written:
//
//   - a line inside a `\` continuation, which is shell text in the middle of a
//     RUN, not a Dockerfile comment;
//   - a line inside a heredoc body, same reason.
//
// Neither appears in the base Dockerfile today, which is a fact about today's
// file and not about the next one — a profile's Dockerfile is user-authored and
// goes through here too.
func stripDockerfileProse(src []byte) []byte {
	lines := strings.Split(string(src), "\n")
	out := make([]string, 0, len(lines))

	continuation := false
	var open []string    // heredoc delimiters still to be closed, in order
	var pending []string // delimiters declared on the instruction still being read

	for _, line := range lines {
		switch {
		case len(open) > 0:
			// Inside a heredoc body: every byte is the author's script.
			out = append(out, line)
			if strings.TrimSpace(line) == open[0] {
				open = open[1:]
			}
			continue
		case isProseComment(line) && !continuation:
			out = append(out, "")
		default:
			out = append(out, line)
			for _, m := range heredocStart.FindAllStringSubmatch(line, -1) {
				pending = append(pending, m[1])
			}
		}

		// A heredoc's body starts after the whole logical line, so a `<<EOF`
		// declared before a `\` waits for the continuation to finish.
		continuation = strings.HasSuffix(strings.TrimRight(line, " \t"), `\`)
		if !continuation {
			open, pending = pending, nil
		}
	}
	return []byte(strings.Join(out, "\n"))
}

// isProseComment reports whether a top-level line is a comment carrying prose
// rather than a directive. Docker allows leading whitespace before the `#`.
func isProseComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#") && !dockerfileDirectives.MatchString(line)
}
