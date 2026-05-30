// ABOUTME: Façade re-exports of profile image-build helpers. The implementation
// ABOUTME: lives in the profiles/ leaf; these aliases keep the public sandbox API stable.
package sandbox

import "github.com/kstenerud/yoloai/internal/sandbox/profiles"

// ProfileImageBuilder is optionally implemented by backends that support
// building custom images from profile Dockerfiles.
type ProfileImageBuilder = profiles.ProfileImageBuilder

// EnsureProfileImage ensures a profile's Docker image and its inheritance
// chain are built and up to date. See profiles.EnsureProfileImage.
var EnsureProfileImage = profiles.EnsureProfileImage
