package tart

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
)

// RuntimeVersion represents a resolved Apple simulator runtime
type RuntimeVersion struct {
	Platform string // "ios", "tvos", "watchos", "visionos" (lowercase)
	Version  string // "26.2" (semantic version string)
	Build    string // "23B86" (build identifier, for tie-breaking)
}

// ParseRuntime parses "platform[:version]" format
// Examples: "ios", "ios:26.2", "iOS:latest"
func ParseRuntime(input string) (platform, ver string, err error) {
	parts := strings.SplitN(input, ":", 2)
	platform = strings.ToLower(strings.TrimSpace(parts[0]))

	// Validate platform
	validPlatforms := map[string]bool{"ios": true, "tvos": true, "watchos": true, "visionos": true}
	if !validPlatforms[platform] {
		return "", "", fmt.Errorf("invalid platform %q, must be one of: ios, tvos, watchos, visionos", platform)
	}

	if len(parts) == 2 {
		ver = strings.TrimSpace(parts[1])
		// ":latest" treated same as omitted (both defer to QueryAvailableRuntimes)
		if ver == "latest" {
			ver = ""
		}
	}

	return platform, ver, nil
}

// simctlRuntime represents a runtime from `xcrun simctl list runtimes --json`
type simctlRuntime struct {
	Platform    string `json:"platform"`
	Version     string `json:"version"`
	Build       string `json:"buildversion"`
	IsAvailable bool   `json:"isAvailable"`
	BundlePath  string `json:"bundlePath"` // needed for runtime copying
}

type simctlRuntimesOutput struct {
	Runtimes []simctlRuntime `json:"runtimes"`
}

// QueryAvailableRuntimes queries xcrun simctl on the HOST for available runtimes
func QueryAvailableRuntimes() ([]RuntimeVersion, error) {
	cmd := exec.Command("xcrun", "simctl", "list", "runtimes", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("query simctl runtimes: %w", err)
	}

	var result simctlRuntimesOutput
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("parse simctl output: %w", err)
	}

	var runtimes []RuntimeVersion
	for _, rt := range result.Runtimes {
		if !rt.IsAvailable {
			continue
		}
		runtimes = append(runtimes, RuntimeVersion{
			Platform: strings.ToLower(rt.Platform),
			Version:  rt.Version,
			Build:    rt.Build,
		})
	}

	return runtimes, nil
}

// ResolveRuntimeVersions resolves user input to specific runtime versions
// If version omitted/":latest", picks latest by semantic version
// If version specified, matches exact version or errors
func ResolveRuntimeVersions(inputs []string) ([]RuntimeVersion, error) {
	available, err := QueryAvailableRuntimes()
	if err != nil {
		return nil, err
	}

	var resolved []RuntimeVersion
	for _, input := range inputs {
		platform, requestedVersion, err := ParseRuntime(input)
		if err != nil {
			return nil, err
		}

		// Find matching runtimes for this platform
		var candidates []RuntimeVersion
		for _, rt := range available {
			if rt.Platform == platform {
				if requestedVersion == "" || rt.Version == requestedVersion {
					candidates = append(candidates, rt)
				}
			}
		}

		if len(candidates) == 0 {
			if requestedVersion != "" {
				return nil, fmt.Errorf("%s %s not found on host", platform, requestedVersion)
			}
			return nil, fmt.Errorf("no %s runtimes available on host", platform)
		}

		// Pick latest by semantic version
		best := candidates[0]
		if len(candidates) > 1 {
			bestVer, _ := version.NewVersion(best.Version)
			for _, candidate := range candidates[1:] {
				candVer, _ := version.NewVersion(candidate.Version)
				if candVer != nil && bestVer != nil && candVer.GreaterThan(bestVer) {
					best = candidate
					bestVer = candVer
				}
			}
		}

		resolved = append(resolved, best)
	}

	return resolved, nil
}

// GenerateCacheKey creates a sorted, deterministic cache key from runtime versions
// Example: [{tvos:26.1}, {ios:26.2}] → "ios-26.2-tvos-26.1"
func GenerateCacheKey(runtimes []RuntimeVersion) string {
	// Sort by platform, then version within platform
	sorted := make([]RuntimeVersion, len(runtimes))
	copy(sorted, runtimes)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Platform != sorted[j].Platform {
			return sorted[i].Platform < sorted[j].Platform
		}
		return sorted[i].Version < sorted[j].Version
	})

	var parts []string
	for _, rt := range sorted {
		parts = append(parts, fmt.Sprintf("%s-%s", rt.Platform, rt.Version))
	}

	return strings.Join(parts, "-")
}

// FormatRuntimeList returns a human-readable string like "iOS 26.4, tvOS 26.1"
func FormatRuntimeList(runtimes []RuntimeVersion) string {
	var parts []string
	for _, rt := range runtimes {
		platformCap := strings.ToUpper(rt.Platform[:1]) + rt.Platform[1:]
		parts = append(parts, fmt.Sprintf("%s %s", platformCap, rt.Version))
	}
	return strings.Join(parts, ", ")
}
