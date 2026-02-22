# Critique of RESEARCH.md and DESIGN.md

## RESEARCH.md

### Internal Consistency

1. **"Post-v1 optimization" contradicts DESIGN.md.** The recommendation section says "This is a **post-v1 optimization** — the current full-copy approach works everywhere and is simpler to implement and debug." But DESIGN.md already incorporates overlay as the default strategy (`copy_strategy: auto`), with full descriptions in the workflow, container startup, security considerations, and a resolved design decision (#8). Either the research needs to drop the "post-v1" framing or the design needs to revert to full-copy-only for v1.

2. **Design Implications section is stale.** Item 12 says "at minimum, support `--network none` for paranoid mode." The design has moved well past this — it now has `--network-isolated` (Anthropic API only) and `--network-allow <domain>`. The Design Implications section should reflect the current state of the design, or be removed entirely since DESIGN.md is the source of truth. Note: network isolation itself is a v1 must-have (see #9).

### Minor Issues

3. **VirtioFS benchmark framing is slightly misleading.** "MySQL import 90% faster, PHP composer install 87% faster" — these are improvements over the old gRPC-FUSE, not comparisons to native performance. The reader might conflate "90% faster than old" with "90% of native." The relevant metric for our design decision is the ~3x slowdown vs native, which is stated separately but could be clearer.

4. **nono user sentiment not captured.** The previous critique noted SSL cert verification blocked under kernel sandbox (issue #93) and Seatbelt blocks network-bind (issue #131). These weren't added to the nono entry. Minor, but the critique specifically called them out.

---

## DESIGN.md

### Logic Errors

5. **Overlay setup ordering is wrong.** The workflow (step 3) describes the overlay mount as if it happens on the host before the container starts, but step 6 says "Start Docker container." OverlayFS mounts happen inside the container (they require `CAP_SYS_ADMIN` inside the container's mount namespace). The overlay setup must happen at container startup time — in the entrypoint script, after the container is running, before Claude launches. Currently the workflow implies: set up overlay → start container. It should be: start container → entrypoint sets up overlay → launch tmux/Claude.

6. **`yolo start` doesn't address overlay re-mounting.** Docker destroys the mount namespace on container stop, so mounts don't survive stop/start. The upper directory persists (it's on the container filesystem), but the merged overlayfs mount must be re-established. This is fine — the entrypoint runs again on start and re-mounts. The design should: (a) state explicitly that mounts don't survive stop/start, (b) require the entrypoint to be idempotent (`mkdir -p`, check `mountpoint -q` before mounting), and (c) note that this is by design, not a bug.

7. **`yolo destroy` doesn't need special overlay handling.** The kernel tears down the mount namespace when the container process dies. `docker rm --force` is sufficient — no explicit unmount needed. A one-line note is adequate. No edge cases with busy mounts since the container is already stopped.

8. **Overlay + existing `.git/` directory has a subtle interaction.** If the original directory is a git repo, `.git/` is in the lower layer (read-only). When Claude or the entrypoint runs `git add`/`git commit`, git writes to `.git/` (objects, index, refs). These writes go to the overlay upper directory via copy-on-write. This means: (a) the upper directory will contain modified `.git/` files, and (b) `yolo apply` via `git diff HEAD~n` needs to diff against the *original* repo's HEAD (from `meta.json`), not whatever HEAD the sandbox has moved to. The workflow handles this by recording the original HEAD SHA, but the interaction between overlay copy-on-write of `.git/` internals and the diff mechanism should be explicitly documented as a design consideration.

### Missing Specifications

9. **`--network-isolated` implementation needs a sketch.** The design says "Allow only Anthropic API traffic" but doesn't explain how. This is a **v1 must-have** — real CVEs demonstrate the threat (CVE-2025-55284: DNS exfiltration of `.env` contents; Claude Pirate: 30MB data upload via Anthropic File API), competitors treat it as table stakes (Codex disables network by default, Anthropic's own sandbox-runtime uses proxy filtering, Trail of Bits built iptables whitelisting), and community demand is high (351-upvote HN thread, multiple GitHub issues). The proven approach across existing tools is: **HTTP/SOCKS proxy inside the container** with domain whitelist. Container runs on an isolated network, proxy is the only exit. `--network-isolated` whitelists Anthropic API domains; `--network-allow <domain>` extends the list. Known limitation: only covers HTTP/HTTPS — raw TCP/SSH bypass the proxy (GitHub issues #11481, #24091 against Anthropic's own implementation). The design should add an implementation sketch describing this proxy-based approach and note the HTTP-only constraint.

10. **`yolo list` and `yolo build` have no description sections.** Every other command has a dedicated `### yolo <command>` section. These two are missing. `yolo list` especially needs one — what columns does it show? What status values exist? Can you filter (e.g., `yolo list --running`)?

11. **Profile `directories` doesn't support mount points.** The directory mappings in profiles show `path` and `mode` but not custom mount points (`=<path>`). If the CLI supports `./my-app:copy=/opt/myapp`, the config equivalent should too — otherwise complex setups can't be fully expressed in config, breaking the "CLI for one-offs, config for repeatability" principle.

12. **`yolo apply` dry-run mechanism unspecified.** "Performs a `--dry-run` first" — of what? `git apply --check`? `git diff --stat`? The mechanism matters because it determines what the user sees before confirming.

13. **Goal statement is stale.** "Project directories are copied in" is no longer accurate with the overlay strategy. The goal should describe the abstraction (isolated writable views) rather than the mechanism.

14. **Environment variable interpolation in config.** The Recipes section shows `${TAILSCALE_AUTHKEY}` in the YAML config. Does the config support `${VAR}` interpolation from the environment? If yes, this is a feature that needs to be specified. If no, the example implies something that doesn't work, and the secret would need to be hardcoded in the config file — which the security section already flags as a risk.

### Ergonomics

15. **Command argument order should be options-first.** Change `yolo new <name> [options] <dir> [<dir>...]` to `yolo new [options] <name> <dir> [<dir>...]`. Options before positionals is the standard CLI convention (git, docker, kubectl all follow this). It avoids ambiguity when parsing a variable-length directory list after flags.

16. **`yolo exec` always uses `-it` flags.** "Implemented as `docker exec -it yolo-<name> <command>`" — the `-it` flags assume interactive + TTY. What about non-interactive use like `yolo exec my-sandbox ls /work` or piped input like `echo "test" | yolo exec my-sandbox cat`? Should detect whether stdin is a TTY and adjust flags accordingly.

17. **Make `yolo start` idempotent: "get it running, however needed."** Currently `yolo start` and `yolo restart` have overlapping, fuzzy semantics. Simplify: `yolo start` means "ensure the sandbox is running" — if the container is stopped, start it; if Claude exited but the container is up, relaunch Claude; if already running, no-op. Then `yolo restart` is simply `stop` + `start` with no independent semantics. This eliminates the user needing to diagnose *why* it's not running before choosing a command.

### Inconsistencies

18. **`work/` directory semantics change with overlay but descriptions don't.** The ASCII architecture diagram says `work/ (project copies)`. The directory layout section says "copies of :copy dirs only." With overlay, `work/<dirname>/` contains the overlayfs upper directory (deltas only), not a full copy. The descriptions should reflect this dual meaning — or use a different directory name for the upper dir (e.g., `upper/`).

19. **Drop `max_copy_size` — `disk` already covers it.** The container disk limit (`disk`) caps total storage for both copy strategies. `max_copy_size` is redundant: in overlay mode it's meaningless (nothing is copied upfront), and in full copy mode it's just a subset of the disk budget. Remove it and let `disk` be the single storage control. The only thing lost is a fast-fail before a long copy, which is a minor optimization, not a config knob.

20. **Move all yolo internals to a separate tree (e.g., `/yolo/`).** Instead of injecting `.sandbox-context.md` into the working directory and fighting `.gitignore` interactions, keep all yolo-managed files in a dedicated internal tree outside `/work/`. This includes: context file, git baseline repo, overlay upper/work dirs, metadata. Benefits: (a) no collision with project files, (b) no `.gitignore` manipulation needed, (c) works cleanly for `:rw` mounts where there's no overlay to hide our files, (d) ephemeral from the user's perspective — everything vanishes when the container is destroyed. Claude can be pointed at `/yolo/context.md` via `--append-system-prompt` or similar, and the git baseline can use `--git-dir=/yolo/baseline/<dir>` + `--work-tree=/work/<dir>` to track changes without polluting the work tree.

21. **Rename from `yolo` to `yoloai`.** The `yolo` CLI name conflicts with Ultralytics YOLO (object detection framework, 7.7M downloads/month, 40k+ GitHub stars) which installs a `yolo` command via pip. `yoloai` is available on PyPI, npm, and Homebrew. Rename throughout: CLI command, config file (`yoloai.yaml`), container prefix (`yoloai-<name>`), Docker image, sandbox state directory (`~/.yoloai/`), all documentation and examples.
