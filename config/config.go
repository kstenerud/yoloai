package config

// ABOUTME: Config loading for defaults (~/.yoloai/defaults/config.yaml) and
// ABOUTME: global (~/.yoloai/config.yaml) configs. Provides dotted-path get/set.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/yoerrors"
	"gopkg.in/yaml.v3"
)

// AgentFilesConfig holds the parsed agent_files config option.
// nil means not specified (inherit from parent in merge).
type AgentFilesConfig struct {
	BaseDir string   // non-empty for string form (base directory path)
	Files   []string // non-nil for list form (explicit file/dir paths)
}

// IsStringForm returns true if the config uses the string (base directory) form.
func (c *AgentFilesConfig) IsStringForm() bool {
	return c != nil && c.BaseDir != ""
}

// YoloaiConfig holds the subset of config.yaml fields that the Go code reads.
type YoloaiConfig struct {
	OS                 string            `yaml:"os"`                   // os — guest OS: linux, mac
	ContainerBackend   string            `yaml:"container_backend"`    // container_backend — runtime backend: docker, podman, containerd
	TartImage          string            `yaml:"tart_image"`           // tart.image — custom base VM image for tart backend
	Agent              string            `yaml:"agent"`                // agent
	Model              string            `yaml:"model"`                // model
	Env                map[string]string `yaml:"env"`                  // env — environment variables passed to container
	Resources          *ResourceLimits   `yaml:"resources"`            // resources — container resource limits
	Network            *NetworkConfig    `yaml:"network"`              // network — network isolation settings
	Mounts             []string          `yaml:"mounts"`               // mounts — extra bind mounts (host:container[:ro])
	Ports              []string          `yaml:"ports"`                // ports — default port mappings (host:container)
	AgentArgs          map[string]string `yaml:"agent_args"`           // agent_args — per-agent default CLI args
	AgentFiles         *AgentFilesConfig `yaml:"-"`                    // agent_files — extra files to seed into agent-state
	CapAdd             []string          `yaml:"cap_add"`              // cap_add — Linux capabilities to add (Docker only)
	Devices            []string          `yaml:"devices"`              // devices — host devices to expose (Docker only)
	Setup              []string          `yaml:"setup"`                // setup — commands to run before agent launch (Docker only)
	AutoCommitInterval int               `yaml:"auto_commit_interval"` // auto_commit_interval — seconds between auto-commits in :copy dirs; 0 = disabled
	Isolation          string            `yaml:"isolation"`            // isolation — sandbox isolation mode: container, container-enhanced, vm, vm-enhanced
}

// ResourceLimits holds container resource constraints (CPU, memory).
type ResourceLimits struct {
	CPUs   string `yaml:"cpus" json:"cpus,omitempty"`
	Memory string `yaml:"memory" json:"memory,omitempty"`
}

// NetworkConfig holds network isolation settings.
type NetworkConfig struct {
	Isolated bool     `yaml:"isolated" json:"isolated,omitempty"`
	Allow    []string `yaml:"allow" json:"allow,omitempty"`
}

// GlobalConfig holds user preferences from ~/.yoloai/config.yaml.
// These settings apply to all sandboxes regardless of profile.
type GlobalConfig struct {
	TmuxConf     string            `yaml:"tmux_conf"`
	ModelAliases map[string]string `yaml:"model_aliases"`
}

// knownSetting defines a config key with its default value.
type knownSetting struct {
	Path    string
	Default string
}

// knownSettings lists every scalar config key the code recognizes, with defaults.
// Used by GetEffectiveConfig and GetConfigValue to fill in unset values.
var knownSettings = []knownSetting{
	{"os", "linux"},
	{"container_backend", ""},
	{"tart.image", ""},
	{"agent", "claude"},
	{"model", ""},
	{"resources.cpus", ""},
	{"resources.memory", ""},
	{"network.isolated", "false"},
	{"auto_commit_interval", "0"},
	{"isolation", ""},
}

// ValidateIsolationMode returns an error if mode is not a known isolation mode.
// Empty string is allowed (means "use default").
func ValidateIsolationMode(mode string) error {
	switch mode {
	case "", "container", "container-enhanced", "container-privileged", "vm", "vm-enhanced":
		return nil
	default:
		return yoerrors.NewUsageError("unknown isolation mode %q: valid values are container, container-enhanced, container-privileged, vm, vm-enhanced", mode)
	}
}

// knownCollectionSetting defines a non-scalar config key (map or list)
// with its default YAML node kind.
type knownCollectionSetting struct {
	Path string
	Kind yaml.Kind // yaml.MappingNode or yaml.SequenceNode
}

// knownCollectionSettings lists non-scalar config keys shown in effective config.
// Each appears as an empty mapping ({}) or sequence ([]) when not set by the user.
var knownCollectionSettings = []knownCollectionSetting{
	{"agent_args", yaml.MappingNode},
	{"env", yaml.MappingNode},
	{"mounts", yaml.SequenceNode},
	{"ports", yaml.SequenceNode},
	{"network.allow", yaml.SequenceNode},
	{"cap_add", yaml.SequenceNode},
	{"devices", yaml.SequenceNode},
	{"setup", yaml.SequenceNode},
}

// globalKnownSettings lists scalar config keys belonging to the global config.
var globalKnownSettings = []knownSetting{
	{"tmux_conf", ""},
}

// globalKnownCollectionSettings lists non-scalar config keys belonging to global config.
var globalKnownCollectionSettings = []knownCollectionSetting{
	{"model_aliases", yaml.MappingNode},
}

// knownDefaultsKeys: valid top-level keys in defaults/config.yaml.
var knownDefaultsKeys = map[string]bool{
	"os": true, "agent": true, "model": true, "container_backend": true,
	"isolation": true, "tart": true, "network": true, "agent_files": true,
	"mounts": true, "ports": true, "resources": true, "agent_args": true,
	"env": true, "auto_commit_interval": true, "cap_add": true,
	"devices": true, "setup": true,
}

// knownProfileKeys: valid top-level keys in profiles/<name>/config.yaml.
// Superset of defaults keys — adds workdir and directories.
var knownProfileKeys = map[string]bool{
	"os": true, "agent": true, "model": true, "container_backend": true,
	"isolation": true, "tart": true, "network": true, "agent_files": true,
	"mounts": true, "ports": true, "resources": true, "agent_args": true,
	"env": true, "auto_commit_interval": true, "cap_add": true,
	"devices": true, "setup": true,
	"workdir": true, "directories": true, // profile-only
	// backend is kept for profile backend constraint (different from container_backend)
	"backend": true,
}

// ConfigPath returns the path to ~/.yoloai/defaults/config.yaml.
// Used by config get/set/reset for non-global settings.
func ConfigPath() string {
	return DefaultsConfigPath()
}

// GlobalConfigPath returns the path to ~/.yoloai/config.yaml.
func GlobalConfigPath() string {
	return filepath.Join(YoloaiDir(), "config.yaml")
}

// yoloaiConfigHandler is a function that handles a single YAML key in a YoloaiConfig.
type yoloaiConfigHandler func(cfg *YoloaiConfig, val *yaml.Node) error

// yoloaiConfigHandlers maps top-level YAML keys to their handler functions.
var yoloaiConfigHandlers = map[string]yoloaiConfigHandler{
	"agent":                yoloaiScalarHandler(func(c *YoloaiConfig) *string { return &c.Agent }),
	"model":                yoloaiScalarHandler(func(c *YoloaiConfig) *string { return &c.Model }),
	"os":                   yoloaiScalarHandler(func(c *YoloaiConfig) *string { return &c.OS }),
	"container_backend":    yoloaiScalarHandler(func(c *YoloaiConfig) *string { return &c.ContainerBackend }),
	"mounts":               yoloaiExpandedSeqHandler(func(c *YoloaiConfig) *[]string { return &c.Mounts }, "mounts[]"),
	"ports":                yoloaiRawSeqHandler(func(c *YoloaiConfig) *[]string { return &c.Ports }),
	"cap_add":              yoloaiExpandedSeqHandler(func(c *YoloaiConfig) *[]string { return &c.CapAdd }, "cap_add[]"),
	"devices":              yoloaiExpandedSeqHandler(func(c *YoloaiConfig) *[]string { return &c.Devices }, "devices[]"),
	"setup":                yoloaiExpandedSeqHandler(func(c *YoloaiConfig) *[]string { return &c.Setup }, "setup[]"),
	"env":                  yoloaiStringMapHandler(func(c *YoloaiConfig) *map[string]string { return &c.Env }, "env"),
	"agent_args":           yoloaiStringMapHandler(func(c *YoloaiConfig) *map[string]string { return &c.AgentArgs }, "agent_args"),
	"tart":                 handleYoloaiTart,
	"resources":            handleYoloaiResources,
	"network":              handleYoloaiNetwork,
	"agent_files":          handleYoloaiAgentFiles,
	"auto_commit_interval": handleYoloaiAutoCommitInterval,
	"isolation":            handleYoloaiIsolation,
}

// yoloaiScalarHandler returns a handler that expands env vars and stores the result in the field pointed to by ptr.
func yoloaiScalarHandler(ptr func(*YoloaiConfig) *string) yoloaiConfigHandler {
	return func(cfg *YoloaiConfig, val *yaml.Node) error {
		expanded, err := expandEnvBraced(val.Value)
		if err != nil {
			return err
		}
		*ptr(cfg) = expanded
		return nil
	}
}

// yoloaiExpandedSeqHandler returns a handler that appends expanded sequence items to the slice pointed to by ptr.
func yoloaiExpandedSeqHandler(ptr func(*YoloaiConfig) *[]string, label string) yoloaiConfigHandler {
	return func(cfg *YoloaiConfig, val *yaml.Node) error {
		if val.Kind != yaml.SequenceNode {
			return nil
		}
		for _, item := range val.Content {
			expanded, err := expandEnvBraced(item.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			*ptr(cfg) = append(*ptr(cfg), expanded)
		}
		return nil
	}
}

// yoloaiRawSeqHandler returns a handler that appends raw (unexpanded) sequence items to the slice pointed to by ptr.
func yoloaiRawSeqHandler(ptr func(*YoloaiConfig) *[]string) yoloaiConfigHandler {
	return func(cfg *YoloaiConfig, val *yaml.Node) error {
		if val.Kind != yaml.SequenceNode {
			return nil
		}
		for _, item := range val.Content {
			*ptr(cfg) = append(*ptr(cfg), item.Value)
		}
		return nil
	}
}

// yoloaiStringMapHandler returns a handler that populates a map[string]string field with expanded values.
func yoloaiStringMapHandler(ptr func(*YoloaiConfig) *map[string]string, prefix string) yoloaiConfigHandler {
	return func(cfg *YoloaiConfig, val *yaml.Node) error {
		if val.Kind != yaml.MappingNode {
			return nil
		}
		m := make(map[string]string, len(val.Content)/2)
		for k := 0; k < len(val.Content)-1; k += 2 {
			key := val.Content[k].Value
			expanded, err := expandEnvBraced(val.Content[k+1].Value)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", prefix, key, err)
			}
			m[key] = expanded
		}
		*ptr(cfg) = m
		return nil
	}
}

func handleYoloaiTart(cfg *YoloaiConfig, val *yaml.Node) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	for k := 0; k < len(val.Content)-1; k += 2 {
		subKey := val.Content[k].Value
		subExpanded, err := expandEnvBraced(val.Content[k+1].Value)
		if err != nil {
			return fmt.Errorf("tart.%s: %w", subKey, err)
		}
		if subKey == "image" {
			cfg.TartImage = subExpanded
		}
	}
	return nil
}

func handleYoloaiResources(cfg *YoloaiConfig, val *yaml.Node) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	cfg.Resources = &ResourceLimits{}
	for k := 0; k < len(val.Content)-1; k += 2 {
		subKey := val.Content[k].Value
		subExpanded, err := expandEnvBraced(val.Content[k+1].Value)
		if err != nil {
			return fmt.Errorf("resources.%s: %w", subKey, err)
		}
		switch subKey {
		case "cpus":
			cfg.Resources.CPUs = subExpanded
		case "memory":
			cfg.Resources.Memory = subExpanded
		}
	}
	return nil
}

func handleYoloaiNetwork(cfg *YoloaiConfig, val *yaml.Node) error {
	if val.Kind != yaml.MappingNode {
		return nil
	}
	cfg.Network = &NetworkConfig{}
	for k := 0; k < len(val.Content)-1; k += 2 {
		subKey := val.Content[k].Value
		switch subKey {
		case "isolated":
			cfg.Network.Isolated = val.Content[k+1].Value == "true"
		case "allow":
			if val.Content[k+1].Kind == yaml.SequenceNode {
				for _, item := range val.Content[k+1].Content {
					cfg.Network.Allow = append(cfg.Network.Allow, item.Value)
				}
			}
		}
	}
	return nil
}

func handleYoloaiAgentFiles(cfg *YoloaiConfig, val *yaml.Node) error {
	af, err := parseAgentFilesNode(val)
	if err != nil {
		return err
	}
	cfg.AgentFiles = af
	return nil
}

func handleYoloaiAutoCommitInterval(cfg *YoloaiConfig, val *yaml.Node) error {
	n, err := strconv.Atoi(val.Value)
	if err != nil {
		return err
	}
	cfg.AutoCommitInterval = n
	return nil
}

func handleYoloaiIsolation(cfg *YoloaiConfig, val *yaml.Node) error {
	expanded, err := expandEnvBraced(val.Value)
	if err != nil {
		return err
	}
	if err := ValidateIsolationMode(expanded); err != nil {
		return err
	}
	cfg.Isolation = expanded
	return nil
}

// parseConfigYAML parses a config YAML document into a YoloaiConfig.
// source is used in error messages. knownKeys is the set of allowed top-level keys;
// if nil, no unknown-key validation is performed.
func parseConfigYAML(data []byte, source string, knownKeys map[string]bool) (*YoloaiConfig, error) {
	root, err := parseYAMLRoot(data, source, knownKeys)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return &YoloaiConfig{}, nil
	}

	cfg := &YoloaiConfig{}
	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		handler, ok := yoloaiConfigHandlers[key]
		if !ok {
			continue
		}
		if err := handler(cfg, val); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", source, key, err)
		}
	}
	return cfg, nil
}

// parseYAMLRoot parses data into a yaml.Node and returns the root mapping node.
// Returns nil if the document is empty or not a mapping. Validates unknown keys
// against knownKeys when non-nil.
func parseYAMLRoot(data []byte, source string, knownKeys map[string]bool) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	if knownKeys != nil {
		if err := validateKnownKeys(root, source, knownKeys); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// validateKnownKeys checks that all top-level keys in root are present in knownKeys.
func validateKnownKeys(root *yaml.Node, source string, knownKeys map[string]bool) error {
	var unknown []string
	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i].Value
		if !knownKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s: unknown config field(s): %s", source, strings.Join(unknown, ", "))
	}
	return nil
}

// LoadBakedInDefaults parses the embedded defaults YAML into a YoloaiConfig.
// Returns a fully-populated config with every field at its baked-in default.
func LoadBakedInDefaults() (*YoloaiConfig, error) {
	return parseConfigYAML([]byte(DefaultConfigYAML), "<baked-in>", knownDefaultsKeys)
}

// LoadDefaultsConfig loads the effective config for the no-profile path:
// baked-in defaults merged with ~/.yoloai/defaults/config.yaml.
// Used by sandbox.Create() when no --profile is given.
func LoadDefaultsConfig() (*YoloaiConfig, error) {
	base, err := LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(DefaultsConfigPath()) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, fmt.Errorf("read defaults/config.yaml: %w", err)
	}

	override, err := parseConfigYAML(data, DefaultsConfigPath(), knownDefaultsKeys)
	if err != nil {
		return nil, err
	}

	return mergeConfigs(base, override), nil
}

// mergeStringField returns override if non-empty, else base.
func mergeStringField(base, override string) string {
	if override != "" {
		return override
	}
	return base
}

// mergeMapFields merges two map[string]string values: override wins on conflict.
// Returns nil if both are empty.
func mergeMapFields(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	result := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}

// mergeSlices concatenates base and override. Returns nil if both are empty.
func mergeSlices(base, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	return append(append([]string{}, base...), override...)
}

// mergeResources merges two ResourceLimits: per-field, non-empty override wins.
// Returns nil if both are nil.
func mergeResources(base, override *ResourceLimits) *ResourceLimits {
	if base == nil && override == nil {
		return nil
	}
	result := &ResourceLimits{}
	if base != nil {
		result.CPUs = base.CPUs
		result.Memory = base.Memory
	}
	if override != nil {
		result.CPUs = mergeStringField(result.CPUs, override.CPUs)
		result.Memory = mergeStringField(result.Memory, override.Memory)
	}
	return result
}

// mergeNetwork merges two NetworkConfig values: Isolated is last-wins, Allow is additive.
// Returns nil if both are nil.
func mergeNetwork(base, override *NetworkConfig) *NetworkConfig {
	if base == nil && override == nil {
		return nil
	}
	result := &NetworkConfig{}
	if base != nil {
		result.Isolated = base.Isolated
		result.Allow = append(result.Allow, base.Allow...)
	}
	if override != nil {
		result.Isolated = override.Isolated
		result.Allow = append(result.Allow, override.Allow...)
	}
	if len(result.Allow) == 0 {
		result.Allow = nil
	}
	return result
}

// mergeConfigs merges override into base, returning a new YoloaiConfig.
// Merge semantics:
//   - Scalars (OS, Agent, Model, ContainerBackend, TartImage, Isolation): non-empty overrides
//   - Maps (Env, AgentArgs): map merge, override wins on conflict
//   - Lists (Mounts, Ports, CapAdd, Devices, Setup): additive
//   - Resources: per-field override (non-empty override wins)
//   - Network: Isolated overrides (last wins), Allow is additive
//   - AgentFiles: replacement semantics (non-nil replaces)
//   - AutoCommitInterval: non-zero override wins
func mergeConfigs(base, override *YoloaiConfig) *YoloaiConfig {
	agentFiles := base.AgentFiles
	if override.AgentFiles != nil {
		agentFiles = override.AgentFiles
	}
	autoCommit := base.AutoCommitInterval
	if override.AutoCommitInterval > 0 {
		autoCommit = override.AutoCommitInterval
	}
	return &YoloaiConfig{
		OS:                 mergeStringField(base.OS, override.OS),
		ContainerBackend:   mergeStringField(base.ContainerBackend, override.ContainerBackend),
		TartImage:          mergeStringField(base.TartImage, override.TartImage),
		Agent:              mergeStringField(base.Agent, override.Agent),
		Model:              mergeStringField(base.Model, override.Model),
		Isolation:          mergeStringField(base.Isolation, override.Isolation),
		AutoCommitInterval: autoCommit,
		AgentFiles:         agentFiles,
		Env:                mergeMapFields(base.Env, override.Env),
		AgentArgs:          mergeMapFields(base.AgentArgs, override.AgentArgs),
		Mounts:             mergeSlices(base.Mounts, override.Mounts),
		Ports:              mergeSlices(base.Ports, override.Ports),
		CapAdd:             mergeSlices(base.CapAdd, override.CapAdd),
		Devices:            mergeSlices(base.Devices, override.Devices),
		Setup:              mergeSlices(base.Setup, override.Setup),
		Resources:          mergeResources(base.Resources, override.Resources),
		Network:            mergeNetwork(base.Network, override.Network),
	}
}

// LoadConfig reads ~/.yoloai/defaults/config.yaml and extracts known fields.
// Kept for backwards compatibility; new code should prefer LoadDefaultsConfig.
func LoadConfig() (*YoloaiConfig, error) {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &YoloaiConfig{}, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}

	return parseConfigYAML(data, configPath, nil)
}

// LoadGlobalConfig reads ~/.yoloai/config.yaml and extracts global settings.
func LoadGlobalConfig() (*GlobalConfig, error) {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalConfig{}, nil
		}
		return nil, fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse global config.yaml: %w", err)
	}

	cfg := &GlobalConfig{}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return cfg, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return cfg, nil
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if err := applyGlobalConfigField(cfg, key.Value, val); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// applyGlobalConfigField updates cfg for a single top-level key/value pair.
func applyGlobalConfigField(cfg *GlobalConfig, key string, val *yaml.Node) error {
	switch key {
	case "tmux_conf":
		expanded, err := expandEnvBraced(val.Value)
		if err != nil {
			return fmt.Errorf("tmux_conf: %w", err)
		}
		cfg.TmuxConf = expanded
	case "model_aliases":
		if val.Kind != yaml.MappingNode {
			return nil
		}
		aliases, err := parseModelAliases(val)
		if err != nil {
			return err
		}
		cfg.ModelAliases = aliases
	}
	return nil
}

// parseModelAliases expands env vars in each alias value and returns the map.
func parseModelAliases(val *yaml.Node) (map[string]string, error) {
	aliases := make(map[string]string, len(val.Content)/2)
	for k := 0; k < len(val.Content)-1; k += 2 {
		aliasKey := val.Content[k].Value
		expanded, err := expandEnvBraced(val.Content[k+1].Value)
		if err != nil {
			return nil, fmt.Errorf("model_aliases.%s: %w", aliasKey, err)
		}
		aliases[aliasKey] = expanded
	}
	return aliases, nil
}

// ReadConfigRaw reads the raw bytes of config.yaml. Returns nil, nil if the
// file does not exist.
func ReadConfigRaw() ([]byte, error) {
	configPath := ConfigPath()
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}
	return data, nil
}

// ReadGlobalConfigRaw reads the raw bytes of the global config.yaml.
// Returns nil, nil if the file does not exist.
func ReadGlobalConfigRaw() ([]byte, error) {
	configPath := GlobalConfigPath()
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read global config.yaml: %w", err)
	}
	return data, nil
}

// isGlobalKey returns true if the top-level key belongs to global config
// rather than profile config.
func isGlobalKey(path string) bool {
	top := splitDottedPath(path)[0]
	for _, s := range globalKnownSettings {
		if splitDottedPath(s.Path)[0] == top {
			return true
		}
	}
	for _, cs := range globalKnownCollectionSettings {
		if splitDottedPath(cs.Path)[0] == top {
			return true
		}
	}
	return false
}

// IsGlobalKey is the exported version for use by CLI commands.
func IsGlobalKey(path string) bool {
	return isGlobalKey(path)
}

// buildEffectiveConfigDefaults constructs a yaml.Node mapping tree with all
// known settings at their default values (scalars and empty collections).
func buildEffectiveConfigDefaults() *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range globalKnownSettings {
		setYAMLField(root, s.Path, s.Default)
	}
	for _, s := range knownSettings {
		setYAMLField(root, s.Path, s.Default)
	}
	for _, cs := range globalKnownCollectionSettings {
		setCollectionDefault(root, cs)
	}
	for _, cs := range knownCollectionSettings {
		setCollectionDefault(root, cs)
	}
	return root
}

// setCollectionDefault adds an empty collection node at the path given by cs.
func setCollectionDefault(root *yaml.Node, cs knownCollectionSetting) {
	parts := splitDottedPath(cs.Path)
	parent := root
	for _, p := range parts[:len(parts)-1] {
		parent = getOrCreateMapping(parent, p)
	}
	setNodeValue(parent, parts[len(parts)-1], &yaml.Node{Kind: cs.Kind})
}

// overlayConfigFile reads raw YAML from readFn and merges it into root.
// Errors from readFn are returned; YAML parse errors are silently ignored
// (best-effort overlay, matching original GetEffectiveConfig behaviour).
func overlayConfigFile(root *yaml.Node, readFn func() ([]byte, error)) error {
	data, err := readFn()
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	mergeRawYAMLInto(root, data)
	return nil
}

// mergeRawYAMLInto parses data as YAML and merges its top-level mapping into root.
// Parse errors are silently ignored (best-effort overlay).
func mergeRawYAMLInto(root *yaml.Node, data []byte) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return //nolint:nilerr // best-effort: ignore YAML parse errors in effective-config overlay
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if src := doc.Content[0]; src.Kind == yaml.MappingNode {
			mergeNodes(root, src)
		}
	}
}

// GetEffectiveConfig returns YAML showing all known settings with their
// effective values (file overrides + defaults), plus any extra keys from the
// files that aren't in the known settings list.
func GetEffectiveConfig() (string, error) {
	root := buildEffectiveConfigDefaults()

	if err := overlayConfigFile(root, ReadGlobalConfigRaw); err != nil {
		return "", err
	}
	if err := overlayConfigFile(root, ReadConfigRaw); err != nil {
		return "", err
	}

	sortMappingNodesRecursive(root)

	doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("marshal effective config: %w", err)
	}
	return string(out), nil
}

// mergeNodes recursively copies values from src into dst, overwriting
// existing values and adding keys that don't exist in dst.
func mergeNodes(dst, src *yaml.Node) {
	for i := 0; i < len(src.Content)-1; i += 2 {
		key := src.Content[i].Value
		val := src.Content[i+1]

		if val.Kind == yaml.MappingNode {
			dstChild := getOrCreateMapping(dst, key)
			mergeNodes(dstChild, val)
		} else {
			setNodeValue(dst, key, val)
		}
	}
}

// setNodeValue sets a key in a mapping node to the given value node,
// replacing any existing value. Works for all node kinds (scalar,
// sequence, mapping).
func setNodeValue(parent *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = val
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	parent.Content = append(parent.Content, keyNode, val)
}

// GetConfigValue reads a value at the given dotted path from the appropriate
// config file. Global keys (tmux_conf, model_aliases) are read from
// ~/.yoloai/config.yaml; profile keys from ~/.yoloai/defaults/config.yaml.
// Returns the raw string value for scalars, or marshaled YAML for
// mappings/sequences. Falls back to the default for known settings.
// The bool return indicates whether the key was found (in file or defaults).
func GetConfigValue(path string) (string, bool, error) {
	var configPath string
	var defaults []knownSetting

	if isGlobalKey(path) {
		configPath = GlobalConfigPath()
		defaults = globalKnownSettings
	} else {
		configPath = ConfigPath()
		defaults = knownSettings
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path from GlobalConfigPath/ConfigPath
	if err != nil {
		if os.IsNotExist(err) {
			return knownDefaultFrom(path, defaults)
		}
		return "", false, fmt.Errorf("read config: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("parse config: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return knownDefaultFrom(path, defaults)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return knownDefaultFrom(path, defaults)
	}

	parts := splitDottedPath(path)
	node := root
	for _, part := range parts {
		found := false
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == part {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return knownDefaultFrom(path, defaults)
		}
	}

	if node.Kind == yaml.ScalarNode {
		return node.Value, true, nil
	}

	// For mappings/sequences, sort and marshal the subtree.
	sortMappingNodesRecursive(node)
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", false, fmt.Errorf("marshal subtree: %w", err)
	}
	return string(out), true, nil
}

// knownDefaultFrom returns the default value for a known setting path
// from the given defaults list.
func knownDefaultFrom(path string, defaults []knownSetting) (string, bool, error) {
	for _, s := range defaults {
		if s.Path == path {
			return s.Default, true, nil
		}
	}
	return "", false, nil
}

// UpdateConfigFields updates specific fields in config.yaml using yaml.Node
// manipulation to preserve comments and formatting.
func UpdateConfigFields(fields map[string]string) error {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		return fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}

	var root *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		root = doc.Content[0]
	} else {
		// Empty or all-comments file (e.g., scaffold): initialize a fresh mapping.
		root = &yaml.Node{Kind: yaml.MappingNode}
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{root}
	}

	for fieldPath, value := range fields {
		setYAMLField(root, fieldPath, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// DeleteConfigField removes a key at a dotted path from config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteConfigField(path string) error {
	configPath := ConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/defaults/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to delete
		}
		return fmt.Errorf("read config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	deleteYAMLField(root, path)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

// UpdateGlobalConfigFields updates specific fields in the global config.yaml
// using yaml.Node manipulation to preserve comments and formatting.
func UpdateGlobalConfigFields(fields map[string]string) error {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		return fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("global config.yaml has unexpected structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("global config.yaml root is not a mapping")
	}

	for fieldPath, value := range fields {
		setYAMLField(root, fieldPath, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal global config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write global config.yaml: %w", err)
	}

	return nil
}

// DeleteGlobalConfigField removes a key at a dotted path from the global config.yaml.
// Returns nil if the file doesn't exist or the key is already absent.
func DeleteGlobalConfigField(path string) error {
	configPath := GlobalConfigPath()

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path is ~/.yoloai/config.yaml
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read global config.yaml: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse global config.yaml: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	deleteYAMLField(root, path)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal global config.yaml: %w", err)
	}

	if err := fileutil.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("write global config.yaml: %w", err)
	}

	return nil
}

// deleteYAMLField removes a key at a dotted path from a yaml.Node mapping tree.
func deleteYAMLField(root *yaml.Node, path string) {
	parts := splitDottedPath(path)
	node := root

	// Navigate to the parent mapping.
	for _, part := range parts[:len(parts)-1] {
		found := false
		for i := 0; i < len(node.Content)-1; i += 2 {
			if node.Content[i].Value == part && node.Content[i+1].Kind == yaml.MappingNode {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return // parent path doesn't exist, nothing to delete
		}
	}

	// Remove the leaf key-value pair.
	leafKey := parts[len(parts)-1]
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == leafKey {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

// setYAMLField sets a dotted path (e.g., "defaults.tmux_conf") to a value
// in a yaml.Node mapping tree. Creates intermediate mappings as needed.
func setYAMLField(root *yaml.Node, path string, value string) {
	parts := splitDottedPath(path)
	node := root

	// Navigate to the parent mapping, creating intermediate mappings as needed.
	for _, part := range parts[:len(parts)-1] {
		node = getOrCreateMapping(node, part)
	}

	// Set the leaf value.
	leafKey := parts[len(parts)-1]
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == leafKey {
			node.Content[i+1].Value = value
			node.Content[i+1].Tag = "!!str"
			if value == "true" || value == "false" {
				node.Content[i+1].Tag = "!!bool"
			}
			return
		}
	}

	// Key doesn't exist — append it.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: leafKey}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: "!!str"}
	if value == "true" || value == "false" {
		valNode.Tag = "!!bool"
	}
	node.Content = append(node.Content, keyNode, valNode)
}

// getOrCreateMapping finds or creates a mapping node under the given key.
func getOrCreateMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}

	// Create new mapping.
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	mapNode := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, keyNode, mapNode)
	return mapNode
}

// sortMappingNodesRecursive sorts all mapping nodes in the tree alphabetically.
func sortMappingNodesRecursive(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		// Sort children first
		for i := 1; i < len(node.Content); i += 2 {
			sortMappingNodesRecursive(node.Content[i])
		}
		sortMappingNode(node)
	}
}

// sortMappingNode sorts a mapping node's key-value pairs alphabetically by key.
func sortMappingNode(node *yaml.Node) {
	n := len(node.Content) / 2
	if n < 2 {
		return
	}

	type kv struct {
		key *yaml.Node
		val *yaml.Node
	}
	pairs := make([]kv, n)
	for i := 0; i < n; i++ {
		pairs[i] = kv{node.Content[i*2], node.Content[i*2+1]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].key.Value < pairs[j].key.Value
	})
	for i, p := range pairs {
		node.Content[i*2] = p.key
		node.Content[i*2+1] = p.val
	}
}

// parseAgentFilesNode parses an agent_files yaml.Node into AgentFilesConfig.
// Supports two forms:
//   - String (scalar): base directory path, e.g. "~/.claude" or "${HOME}"
//   - List (sequence): explicit file/dir paths, e.g. ["~/.claude/settings.json"]
func parseAgentFilesNode(val *yaml.Node) (*AgentFilesConfig, error) {
	switch val.Kind {
	case yaml.ScalarNode:
		expanded, err := ExpandPath(val.Value)
		if err != nil {
			return nil, err
		}
		return &AgentFilesConfig{BaseDir: expanded}, nil
	case yaml.SequenceNode:
		files := make([]string, 0, len(val.Content))
		for _, item := range val.Content {
			expanded, err := ExpandPath(item.Value)
			if err != nil {
				return nil, err
			}
			files = append(files, expanded)
		}
		return &AgentFilesConfig{Files: files}, nil
	default:
		return nil, fmt.Errorf("expected string or list, got %v", val.Kind)
	}
}

// splitDottedPath splits "a.b.c" into ["a", "b", "c"].
func splitDottedPath(path string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	parts = append(parts, path[start:])
	return parts
}

// FindYAMLValue returns the value node for a top-level key in a mapping.
func FindYAMLValue(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}
