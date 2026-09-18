> **ABOUTME:** Describes the directory structure of various toolchains for languages supported by the Seatbelt backend.

# Survey of various toolchains' directory structure

2026-09-08 (preliminary research, facilitated by an LLM and not fully verified)

## Background

The Seatbelt backend profile gives guests access to 6 different languages' toolchains
(python3, node, ruby, go, rustc, java), by:

1. Identifying a tool binary on the host's PATH
2. Treating the binary's **grandparent** directory as the installation prefix
3. Granting guests read access to the prefix directory

This seems to work, for the most part, but there are a couple of gotchas:

- The following (hypthetical) prefixes include sensitive files and are therefore not made available in guests:

  - "~"
  - "~/.local", since credentials may exist inside "~/.local/share"
  - "~/.cargo", due to the existence of "~/.cargo/credentials.toml"

  For now, for those specific paths, the "…/bin" and "…/lib" subdirectories (and nothing else) are granted instead.

- Some toolchains install third-party packages in directories _external to_ the prefix:

  - RVM installs packages in "~/.rvm/gems/…"
  - rustup installs the compiler and packages in "~/.rustup/toolchains/…"

  which are therefore not visible to yoloAI guests.

One appealing remedy is to grant access to specific subtrees (for example "<prefix>/bin" and "<prefix>/lib")
rather than the entire prefix,
but the exact list of relevant subdirectories and subfiles **depends on the toolchain in question.**
This document attempts to assess the assets that are be used by various common toolchains,
and may thus help to inform a future solution that tightens things up by only ever allowlisting specific subtrees.

The second column of the table below also highlights commands that could be executed in the host
in order to directly query for an active toolchain's installation prefix during profile generation
(but only if we are comfortable `exec`ing such binaries on the host).

## Toolchain survey

| Family | Executable on PATH | Grandparent of resolved executable | Safe to grant read access to the grandparent? | Necessary assets in grandparent (or in home directory) | Command that returns exact installation prefix |
| --- | --- | --- | --- | --- | --- |
| go: official installer | `/usr/local/go/bin/go` binary | `/usr/local/go` (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `pkg/`, `src/`, `api/`, `VERSION` | `go env GOROOT` |
| go: Homebrew | `/usr/local/bin/go` symlink → Cellar keg | Cellar keg (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `pkg/`, `src/`, `api/`, `VERSION` | `go env GOROOT` |
| go: goenv | `~/.goenv/shims/go` shim script | `~/.goenv` | yes | `versions/<v>/` (bin, pkg, src, api, VERSION) | `go env GOROOT` |
| go: mise | `~/.local/share/mise/shims/go` shim script | `~/.local/share/mise` | yes | `installs/go/<v>/` (bin, pkg, src, api, VERSION) | `go env GOROOT` |
| go: asdf | `~/.asdf/shims/go` shim script | `~/.asdf` | yes | `installs/go/<v>/` (bin, pkg, src, api, VERSION) | `go env GOROOT` |
| python: Homebrew | `/usr/local/bin/python3` symlink → Cellar keg | Cellar keg (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `lib/python3.x/`, `include/` | `sysconfig.get_paths` |
| python: pyenv | `~/.pyenv/shims/python3` shim script | `~/.pyenv` | yes | `versions/<v>/` (bin, lib, include) | `sysconfig.get_paths` |
| python: uv | `~/.local/bin/python3` symlink → `~/.local/share/uv/python/cpython-<v>/bin/` | `~/.local/share/uv/python/cpython-<v>` | yes | `bin/`, `lib/`, `include/` | `sysconfig.get_paths` |
| python: `--prefix=~/.local` source install | `~/.local/bin/python3` binary | `~/.local` | no — the tree mixes many applications' state and credentials under `share/` | `bin/`, `lib/python3.x/` | `sysconfig.get_paths` |
| python: conda | `~/miniconda3/bin/python3` binary | `~/miniconda3` | yes | `bin/`, `lib/` | `sysconfig.get_paths` |
| node: Homebrew | `/usr/local/bin/node` symlink → Cellar keg | Cellar keg (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `lib/node_modules/` | `npm root -g` |
| node: nvm | `~/.nvm/versions/node/<v>/bin/node` binary | `~/.nvm/versions/node/<v>` | yes | `bin/`, `lib/node_modules/` | `npm root -g` |
| node: fnm | `~/.local/state/fnm_multishells/<id>/node` symlink → `~/.local/share/fnm/node-versions/<v>/installation/bin/node` | `~/.local/share/fnm/node-versions/<v>/installation` | yes | `bin/`, `lib/node_modules/` | `npm root -g` |
| node: volta | `~/.volta/bin/node` shim binary | `~/.volta` | yes | `tools/image/node/<v>/` (bin, lib) | `npm root -g` |
| ruby: system | `/usr/bin/ruby` binary | `/usr` (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `lib/ruby/` | `RbConfig` / `gem env` |
| ruby: Homebrew | `/opt/homebrew/opt/ruby/bin/ruby` symlink → Cellar keg | Cellar keg (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `lib/ruby/` | `RbConfig` / `gem env` |
| ruby: rvm | `~/.rvm/rubies/<v>/bin/ruby` binary | `~/.rvm/rubies/<v>` | yes, but the gems tree is a sibling the grandparent never reaches | `bin/`, `lib/`; `~/.rvm/gems/<v>/` from home | `RbConfig` / `gem env` |
| ruby: rbenv | `~/.rbenv/shims/ruby` shim script | `~/.rbenv` | yes | `versions/<v>/` (bin, lib) | `RbConfig` / `gem env` |
| rust: Homebrew rustup | `/usr/local/opt/rustup/bin/rustc` symlink → Cellar keg | Cellar keg (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/` (proxies only); `~/.rustup/toolchains/<tc>/bin/`, `…/lib/rustlib/` from home | `rustup which rustc` |
| rust: rustup default | `~/.cargo/bin/rustc` proxy binary | `~/.cargo` | no — `credentials.toml` and registry state sit beside the proxies | `bin/` (proxies); `~/.rustup/toolchains/<tc>/bin/`, `…/lib/rustlib/` from home | `rustup which rustc` |
| java: system/Homebrew | `/Library/Java/JavaVirtualMachines/<v>/Contents/Home/bin/java` binary | `…/Contents/Home` (covered by `systemReadPaths()`) | yes (and already covered via syspath) | `bin/`, `lib/`, `conf/`, `include/` | `java.home` |
| java: sdkman | `~/.sdkman/candidates/java/<v>/bin/java` binary | `~/.sdkman/candidates/java/<v>` | yes | `bin/`, `lib/`, `conf/`, `include/` | `java.home` |

## References

- https://docs.python.org/3/library/sysconfig.html#posix-user
- https://openjdk.org/jeps/220#New-run-time-image-structure
