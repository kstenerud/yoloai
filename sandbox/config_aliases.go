package sandbox

// Re-exports from config/ package for backward compatibility.
// These types and functions were originally defined in this package and have
// been moved to the top-level config/ package. Aliases are provided so that
// existing callers (e.g., internal/cli) continue to compile.

import "github.com/kstenerud/yoloai/config"

// Type aliases for config types.
type (
	AgentFilesConfig = config.AgentFilesConfig
	YoloaiConfig     = config.YoloaiConfig
	ResourceLimits   = config.ResourceLimits
	NetworkConfig    = config.NetworkConfig
	GlobalConfig     = config.GlobalConfig
	ProfileConfig    = config.ProfileConfig
	ProfileWorkdir   = config.ProfileWorkdir
	ProfileDir       = config.ProfileDir
	MergedConfig     = config.MergedConfig
	State            = config.State
)

// Function aliases for config functions.
var (
	ConfigPath               = config.ConfigPath
	GlobalConfigPath         = config.GlobalConfigPath
	LoadConfig               = config.LoadConfig
	LoadGlobalConfig         = config.LoadGlobalConfig
	ReadConfigRaw            = config.ReadConfigRaw
	ReadGlobalConfigRaw      = config.ReadGlobalConfigRaw
	IsGlobalKey              = config.IsGlobalKey
	GetEffectiveConfig       = config.GetEffectiveConfig
	GetConfigValue           = config.GetConfigValue
	UpdateConfigFields       = config.UpdateConfigFields
	DeleteConfigField        = config.DeleteConfigField
	UpdateGlobalConfigFields = config.UpdateGlobalConfigFields
	DeleteGlobalConfigField  = config.DeleteGlobalConfigField
	ProfileDirPath           = config.ProfileDirPath
	ValidateProfileName      = config.ValidateProfileName
	ProfileExists            = config.ProfileExists
	ProfileHasDockerfile     = config.ProfileHasDockerfile
	ListProfiles             = config.ListProfiles
	LoadProfile              = config.LoadProfile
	ResolveProfileChain      = config.ResolveProfileChain
	ResolveProfileImage      = config.ResolveProfileImage
	MergeProfileChain        = config.MergeProfileChain
	ValidateProfileBackend   = config.ValidateProfileBackend
	StatePath                = config.StatePath
	LoadState                = config.LoadState
	SaveState                = config.SaveState
)
