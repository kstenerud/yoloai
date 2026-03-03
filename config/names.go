package config

// ABOUTME: Name validation constants shared by sandbox and profile validation.

import "regexp"

// MaxNameLength is the maximum allowed sandbox name length.
// Docker container names are limited to 63 chars; with the "yoloai-" prefix (7 chars),
// the name portion can be at most 56.
const MaxNameLength = 56

// ValidNameRe matches names: starts with a letter or digit, then
// letters, digits, underscores, dots, or hyphens.
var ValidNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
