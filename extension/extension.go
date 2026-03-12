// Package extension implements loading, validation, and types for yoloAI
// extensions — user-defined custom commands stored as YAML files.
package extension

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"gopkg.in/yaml.v3"
)

// Extension represents a user-defined custom command loaded from a YAML file.
type Extension struct {
	Name        string           // derived from filename (not in YAML)
	Description string           `yaml:"description"`
	Agent       *AgentConstraint `yaml:"agent"`
	Args        []Arg            `yaml:"args"`
	Flags       []Flag           `yaml:"flags"`
	Action      string           `yaml:"action"`
}

// Arg defines a positional argument for an extension.
type Arg struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Flag defines a named flag for an extension.
type Flag struct {
	Name        string `yaml:"name"`
	Short       string `yaml:"short"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
}

// AgentConstraint restricts which agents an extension supports.
// A nil constraint (omitted field) means any agent is allowed.
type AgentConstraint struct {
	Names []string // nil = any agent
}

// UnmarshalYAML handles scalar ("claude"), sequence (["claude", "codex"]), or absent.
func (ac *AgentConstraint) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		ac.Names = []string{value.Value}
		return nil
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return err
		}
		ac.Names = names
		return nil
	default:
		return fmt.Errorf("agent must be a string or list of strings")
	}
}

// SupportsAgent returns true if the extension supports the given agent name.
// Always true if no agent constraint is set.
func (ext *Extension) SupportsAgent(name string) bool {
	if ext.Agent == nil {
		return true
	}
	for _, n := range ext.Agent.Names {
		if n == name {
			return true
		}
	}
	return false
}

// ExitError carries a script exit code through Cobra back to the root command.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("extension exited with code %d", e.Code)
}

// ExtensionsDir returns the path to the extensions directory (~/.yoloai/extensions/).
func ExtensionsDir() string {
	return config.ExtensionsDir()
}

// Load parses a single YAML extension file. The extension name is derived
// from the filename stem (e.g., "from-issue.yaml" -> "from-issue").
func Load(path string) (*Extension, error) {
	data, err := os.ReadFile(path) //nolint:gosec // user-owned config file
	if err != nil {
		return nil, fmt.Errorf("read extension %s: %w", path, err)
	}

	var ext Extension
	if err := yaml.Unmarshal(data, &ext); err != nil {
		return nil, fmt.Errorf("parse extension %s: %w", path, err)
	}

	base := filepath.Base(path)
	ext.Name = strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")

	return &ext, nil
}

// LoadAll scans the extensions directory for *.yaml and *.yml files, loads
// each one, and returns them sorted by name. Returns an empty slice (not error)
// if the directory doesn't exist.
func LoadAll() ([]*Extension, error) {
	dir := ExtensionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extensions directory: %w", err)
	}

	var exts []*Extension
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		ext, err := Load(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		exts = append(exts, ext)
	}

	sort.Slice(exts, func(i, j int) bool {
		return exts[i].Name < exts[j].Name
	})

	return exts, nil
}

// identRe matches valid identifier names for args and flags.
var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// reservedFlags are flag names/shorts that conflict with root persistent flags.
var reservedFlags = map[string]bool{
	"verbose":  true,
	"v":        true,
	"quiet":    true,
	"q":        true,
	"no-color": true,
	"json":     true,
	"debug":    true,
	"help":     true,
	"h":        true,
	"yes":      true,
	"y":        true,
}

// ReservedNames are built-in command names that extensions cannot shadow.
var ReservedNames = map[string]bool{
	"new": true, "attach": true, "diff": true, "apply": true, "files": true,
	"start": true, "stop": true, "restart": true, "destroy": true, "reset": true,
	"system": true, "sandbox": true, "ls": true, "log": true, "exec": true,
	"profile": true, "help": true, "config": true, "version": true,
	"x": true, "ext": true, "sb": true,
}

// Validate checks that an extension is well-formed. Returns an error describing
// the first problem found.
func Validate(ext *Extension) error {
	if ext.Action == "" {
		return fmt.Errorf("extension %q: action is required", ext.Name)
	}

	if ReservedNames[ext.Name] {
		return fmt.Errorf("extension %q: name conflicts with built-in command", ext.Name)
	}

	// Validate args
	argNames := make(map[string]bool)
	for _, a := range ext.Args {
		if a.Name == "" {
			return fmt.Errorf("extension %q: arg name is required", ext.Name)
		}
		if !identRe.MatchString(a.Name) {
			return fmt.Errorf("extension %q: invalid arg name %q (must match %s)", ext.Name, a.Name, identRe.String())
		}
		if argNames[a.Name] {
			return fmt.Errorf("extension %q: duplicate arg name %q", ext.Name, a.Name)
		}
		argNames[a.Name] = true
	}

	// Validate flags
	flagNames := make(map[string]bool)
	flagShorts := make(map[string]bool)
	for _, f := range ext.Flags {
		if f.Name == "" {
			return fmt.Errorf("extension %q: flag name is required", ext.Name)
		}
		if !identRe.MatchString(f.Name) {
			return fmt.Errorf("extension %q: invalid flag name %q (must match %s)", ext.Name, f.Name, identRe.String())
		}
		if flagNames[f.Name] {
			return fmt.Errorf("extension %q: duplicate flag name %q", ext.Name, f.Name)
		}
		if reservedFlags[f.Name] {
			return fmt.Errorf("extension %q: flag %q conflicts with reserved flag", ext.Name, f.Name)
		}
		flagNames[f.Name] = true

		if f.Short != "" {
			if len(f.Short) != 1 {
				return fmt.Errorf("extension %q: flag short %q must be a single character", ext.Name, f.Short)
			}
			if flagShorts[f.Short] {
				return fmt.Errorf("extension %q: duplicate flag short %q", ext.Name, f.Short)
			}
			if reservedFlags[f.Short] {
				return fmt.Errorf("extension %q: flag short %q conflicts with reserved flag", ext.Name, f.Short)
			}
			flagShorts[f.Short] = true
		}
	}

	// Validate agent constraint
	if ext.Agent != nil {
		for _, name := range ext.Agent.Names {
			if agent.GetAgent(name) == nil {
				return fmt.Errorf("extension %q: unknown agent %q", ext.Name, name)
			}
		}
	}

	return nil
}
