> **ABOUTME:** Let profile config declare named package-manager-cache volumes that persist
> across sandboxes, so new sandboxes on the same profile skip cold-cache re-downloads.

# Shared cache volumes

- **Status:** UNSPECIFIED — idea only; profile config syntax undecided.
- **Depends on:** —

Allow profile config to declare named Docker volumes for package manager caches (npm, pip, cargo, etc.) that persist across sandboxes. Currently each sandbox starts with a cold cache. Shared volumes would avoid re-downloading dependencies when creating new sandboxes with the same profile.

Inspired by [amazing-sandbox](https://github.com/ashishb/amazing-sandbox), which mounts ~15 named volumes for various package manager caches.

Design considerations:
- Profile config syntax: e.g. `cache_volumes: {npm: /root/.npm, pip: /root/.cache/pip, cargo: /usr/local/cargo}`
- Volumes are named per-profile to avoid cross-profile conflicts (e.g. `yoloai-base-npm`)
- Optional: `yoloai prune --caches` to clean up cache volumes
- Consider whether the base profile should ship with sensible defaults for common caches
- Read-write mount; acceptable since these are caches, not project files
