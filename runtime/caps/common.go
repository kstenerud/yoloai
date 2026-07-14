package caps

// ABOUTME: Shared HostCapability constructors reused across multiple backends.
// ABOUTME: Each constructor takes injectable function pointers for testability.

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	goruntime "runtime"
	"strconv"
)

// NewGVisorRunsc returns a capability that checks for the runsc binary in PATH.
// lookPath is injectable for testing (pass exec.LookPath in production).
func NewGVisorRunsc(lookPath func(string) (string, error)) HostCapability {
	return HostCapability{
		ID:      "gvisor-runsc",
		Summary: "gVisor runtime (runsc)",
		Detail:  "Required for --isolation container-enhanced.",
		Check: func(_ context.Context) error {
			_, err := lookPath("runsc")
			return err
		},
		Permanent: func(env Environment) bool {
			return env.InContainer // can't install binaries inside a container
		},
		Fix: func(_ Environment) []FixStep {
			return []FixStep{{
				Description: "Install gVisor",
				URL:         "https://gvisor.dev/docs/user_guide/install/",
				NeedsRoot:   true,
			}}
		},
	}
}

// ociRuntimeVersionPattern extracts the first X.Y[.Z] version number from an
// OCI runtime's `--version` output, e.g. "runc version 1.3.6" or
// "crun version 1.20.0".
var ociRuntimeVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// NewOCIRuntimeVersionFloor returns an advisory capability that checks an OCI
// runtime binary (runc, crun) against a minimum version, e.g. to warn about
// known container-escape CVEs fixed in later releases. It never blocks: a
// failure here is Advisory, surfaced via doctor's normal NeedsSetup tier and
// via a passive launch-time warning, never via FormatError's blocking path.
//
// lookPath and runVersion are injectable for testing (pass exec.LookPath and
// a `func(path string) ([]byte, error)` running "<path> --version" in
// production). On non-Linux hosts the check is skipped: the daemon runs
// inside a VM (Docker Desktop, Podman Machine), so the host PATH says
// nothing about the daemon's actual OCI runtime.
func NewOCIRuntimeVersionFloor(
	id, binaryName, summary, detail, installURL string,
	lookPath func(string) (string, error),
	runVersion func(string) ([]byte, error),
	meetsFloor func(major, minor, patch int) bool,
) HostCapability {
	return HostCapability{
		ID:       id,
		Summary:  summary,
		Detail:   detail,
		Advisory: true,
		Check: func(_ context.Context) error {
			if goruntime.GOOS != "linux" {
				return nil
			}
			path, lookErr := lookPath(binaryName)
			if lookErr != nil {
				//nolint:nilerr // not on PATH is not this check's job to flag; other
				// capabilities cover backend/binary presence.
				return nil
			}
			out, err := runVersion(path)
			if err != nil {
				return fmt.Errorf("run %s --version: %w", binaryName, err)
			}
			major, minor, patch, ok := parseOCIRuntimeVersion(out)
			if !ok {
				return fmt.Errorf("could not parse %s version from: %q", binaryName, ociRuntimeFirstLine(out))
			}
			if !meetsFloor(major, minor, patch) {
				return fmt.Errorf("%s %d.%d.%d is below the recommended version floor", binaryName, major, minor, patch)
			}
			return nil
		},
		Fix: func(_ Environment) []FixStep {
			return []FixStep{{
				Description: fmt.Sprintf("Upgrade %s — recent CVEs fixed in newer releases", binaryName),
				URL:         installURL,
				NeedsRoot:   true,
			}}
		},
	}
}

// ociRuntimeFirstLine returns the first line of --version output, for error messages.
func ociRuntimeFirstLine(out []byte) string {
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		return string(out[:i])
	}
	return string(out)
}

// parseOCIRuntimeVersion extracts major.minor[.patch] from the first line of
// --version output. patch is 0 if not present (e.g. "1.20").
func parseOCIRuntimeVersion(out []byte) (major, minor, patch int, ok bool) {
	m := ociRuntimeVersionPattern.FindStringSubmatch(ociRuntimeFirstLine(out))
	if m == nil {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	return major, minor, patch, true
}
