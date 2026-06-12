// ABOUTME: ResolveDirSpecifier and SelectTrackedDir resolve a user-supplied path
// ABOUTME: fragment to a unique tracked directory for the diff/apply commands.
package cliutil

import (
	"fmt"
	"path/filepath"
	"strings"

	yoloai "github.com/kstenerud/yoloai"
)

// ResolveDirSpecifier resolves a user-supplied path fragment to exactly one
// tracked dir in env. Match precedence, first rule yielding a unique match wins:
//  1. exact host path
//  2. exact mount path
//  3. basename of the host path
//  4. segment-aligned suffix of the host path ("b/web" matches "/a/b/web")
//
// No match -> error listing the tracked dirs. Multiple matches -> error listing
// the candidates so the user can disambiguate.
func ResolveDirSpecifier(env *yoloai.Environment, fragment string) (yoloai.DirInfo, error) {
	tracked := env.TrackedDirs()
	// Try each rule in order; first rule with a unique match wins.
	rules := []func(yoloai.DirInfo) bool{
		func(d yoloai.DirInfo) bool { return d.HostPath == fragment },
		func(d yoloai.DirInfo) bool { return d.MountPath == fragment },
		func(d yoloai.DirInfo) bool { return filepath.Base(d.HostPath) == fragment },
		func(d yoloai.DirInfo) bool { return segmentAlignedSuffix(d.HostPath, fragment) },
	}
	for _, rule := range rules {
		var matches []yoloai.DirInfo
		for _, d := range tracked {
			if rule(d) {
				matches = append(matches, d)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return yoloai.DirInfo{}, fmt.Errorf("ambiguous specifier %q — matches multiple tracked dirs:\n%s", fragment, formatDirList(matches))
		}
	}
	return yoloai.DirInfo{}, fmt.Errorf("no tracked dir matches %q — tracked dirs:\n%s", fragment, formatDirList(tracked))
}

// SelectTrackedDir decides the target dir for diff/apply. When env has 2+
// tracked dirs, the first positional in args is the REQUIRED specifier: it is
// popped from args and resolved, returning its host path. With 0-1 tracked
// dirs, no specifier is consumed and hostPath is "" (the workdir / current
// behavior). Returns the (possibly shortened) remaining args and the selected DirInfo.
func SelectTrackedDir(env *yoloai.Environment, args []string) (hostPath string, selected yoloai.DirInfo, rest []string, err error) {
	tracked := env.TrackedDirs()
	if len(tracked) < 2 {
		return "", env.Workdir(), args, nil
	}
	if len(args) == 0 || args[0] == "--" {
		return "", yoloai.DirInfo{}, args, fmt.Errorf("sandbox %q has %d tracked dirs — specify one:\n%s", env.Name, len(tracked), formatDirList(tracked))
	}
	dir, resolveErr := ResolveDirSpecifier(env, args[0])
	if resolveErr != nil {
		return "", yoloai.DirInfo{}, args, resolveErr
	}
	return dir.HostPath, dir, args[1:], nil
}

// segmentAlignedSuffix reports whether fragment is a trailing path-segment
// match of hostPath. "b/web" matches "/a/b/web" but "eb" does not.
func segmentAlignedSuffix(hostPath, fragment string) bool {
	fragSegs := splitPathSegments(fragment)
	hostSegs := splitPathSegments(hostPath)
	if len(fragSegs) == 0 || len(fragSegs) > len(hostSegs) {
		return false
	}
	tail := hostSegs[len(hostSegs)-len(fragSegs):]
	for i, s := range fragSegs {
		if tail[i] != s {
			return false
		}
	}
	return true
}

// splitPathSegments splits a path into its non-empty segments.
func splitPathSegments(p string) []string {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

// formatDirList formats a list of DirInfo for error messages.
// Each line: "  <basename>   <hostpath>"
func formatDirList(dirs []yoloai.DirInfo) string {
	var sb strings.Builder
	for _, d := range dirs {
		fmt.Fprintf(&sb, "  %-20s  %s\n", filepath.Base(d.HostPath), d.HostPath)
	}
	return strings.TrimRight(sb.String(), "\n")
}
