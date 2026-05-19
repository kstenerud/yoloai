// ABOUTME: Defines environment archetypes and auto-detection logic for project types.
// ABOUTME: Archetypes: simple, compose, devcontainer, apple. Detection inspects the workdir.

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Archetype represents a high-level environment type that expands to specific configuration.
type Archetype string

const (
	ArchetypeSimple       Archetype = "simple"
	ArchetypeCompose      Archetype = "compose"
	ArchetypeDevcontainer Archetype = "devcontainer"
	ArchetypeApple        Archetype = "apple"
)

// ParseArchetype validates and returns an Archetype for the given string.
func ParseArchetype(s string) (Archetype, error) {
	switch Archetype(s) {
	case ArchetypeSimple, ArchetypeCompose, ArchetypeDevcontainer, ArchetypeApple:
		return Archetype(s), nil
	default:
		return "", fmt.Errorf("unknown archetype %q: valid values are %v", s, ValidArchetypes())
	}
}

// ValidArchetypes returns a sorted list of valid archetype names.
func ValidArchetypes() []string {
	names := []string{
		string(ArchetypeSimple),
		string(ArchetypeCompose),
		string(ArchetypeDevcontainer),
		string(ArchetypeApple),
	}
	sort.Strings(names)
	return names
}

// DetectArchetype inspects workdir and returns the detected archetype plus
// human-readable signal strings for transparency output.
// Detection priority (first match wins):
//  1. .devcontainer/devcontainer.json or devcontainer.json → devcontainer
//  2. docker-compose.yaml or docker-compose.yml (no devcontainer) → compose
//  3. .xcodeproj, .xcworkspace, or Package.swift at root → apple
//  4. Nothing → simple
func DetectArchetype(workdir string) (Archetype, []string) {
	// 1. devcontainer
	devcontainerPaths := []string{
		filepath.Join(workdir, ".devcontainer", "devcontainer.json"),
		filepath.Join(workdir, "devcontainer.json"),
	}
	for _, p := range devcontainerPaths {
		if fileExists(p) {
			rel, _ := filepath.Rel(workdir, p)
			return ArchetypeDevcontainer, []string{fmt.Sprintf("found %s", rel)}
		}
	}

	// 2. docker-compose
	composePaths := []string{
		filepath.Join(workdir, "docker-compose.yaml"),
		filepath.Join(workdir, "docker-compose.yml"),
	}
	for _, p := range composePaths {
		if fileExists(p) {
			rel, _ := filepath.Rel(workdir, p)
			return ArchetypeCompose, []string{fmt.Sprintf("found %s", rel)}
		}
	}

	// 3. Apple platform signals (directory entries and files)
	appleSignals := findAppleSignals(workdir)
	if len(appleSignals) > 0 {
		return ArchetypeApple, appleSignals
	}

	// 4. Default
	return ArchetypeSimple, []string{"no project signals detected"}
}

// findAppleSignals looks for Xcode project files at the workdir root.
func findAppleSignals(workdir string) []string {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return nil
	}

	var signals []string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.IsDir() && (filepath.Ext(name) == ".xcodeproj" || filepath.Ext(name) == ".xcworkspace"):
			signals = append(signals, fmt.Sprintf("found %s", name))
		case !entry.IsDir() && name == "Package.swift":
			signals = append(signals, fmt.Sprintf("found %s", name))
		}
	}
	return signals
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
