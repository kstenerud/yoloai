# Smoke Test Redesign Plan

## Problem with the current script

`scripts/smoke_test.sh` passes `.` (the yoloai repo itself) as the workdir. If `apply`
ever ran, it would write agent output back into the repo. The script also has no
pass/fail tracking, no meaningful assertions beyond "did the file appear", and no cleanup
on failure.

## Language: Python 3 (stdlib only)

The new script is `scripts/smoke_test.py`, run as `python3 scripts/smoke_test.py`.
No pip, no venv, no external dependencies — stdlib only.

Bash becomes painful for this level of logic: polling loops with proper timeouts,
building the backend matrix as a data structure, constructing prompts with injected
paths, and asserting subprocess output. Python is cleaner in every dimension, and
everything needed is in the standard library:

- `subprocess` — clean execution with `capture_output=True`; no shell quoting hazards
  when building prompt strings containing paths
- `time` — `time.time()` polling loops with explicit timeout
- `dataclasses` — backend matrix entries (stdlib since 3.7)
- `json` — parsing `yoloai system check --json` output
- `pathlib` / `os` / `shutil` — file operations, temp dirs, `shutil.which`
- `sys` / `platform` — OS detection
- `tempfile` — `mkdtemp()`
- `argparse` — any flags if needed

The script must be idiomatic, well-structured Python: type hints throughout, dataclasses
for structured data, functions with clear single responsibilities, and no global mutable
state beyond the top-level run context. Follow PEP 8. Prefer `pathlib.Path` over
string path manipulation.

Python 3.7+ is available on any machine that can run yoloai.

## Structural changes

### Temp project fixture — never the yoloai repo

All sandboxes get a project dir created under `tempfile.mkdtemp()` — a small fictional
project (`README.md` and `hello.py`). Each test gets its own `cp -r` copy so that
`apply` in test A cannot pollute test B's project dir, and nothing ever touches the
yoloai repo.

### Cleanup

A `SANDBOXES` list tracks every created sandbox name. A `finally` block (or `atexit`
handler) runs `yoloai destroy --yes <name>` for each entry and removes the temp dir.
This fires even if the script crashes halfway. Manual cleanup after an interrupted run:
`yoloai ls` to find `smoke-<timestamp>-*` names, then destroy each one.

### Pass/fail harness

Each test is a function that calls `pass_("description")` or `fail("description")`.
Test output and subprocess stderr are written to `$TMPDIR/logs/<testname>.log` for
post-run diagnosis. A summary is printed at the end; the script exits 1 if any test
failed.

### Sandbox naming

All sandboxes use a run-scoped prefix: `smoke-<unix_timestamp>-<testname>`, computed
once at startup. This makes all sandboxes from a run easy to identify.

### Wait helper

`wait_for_sentinel(name, sentinel, timeout=90)` polls `yoloai files ls <name>` every 3
seconds until `sentinel` appears in the output. Fails the test and returns False on
timeout. VM backends (Kata/QEMU, Kata/Firecracker, Tart) must use a longer timeout
(180s) to account for VM boot time, particularly under nested KVM.

Every prompt ends by creating a sentinel file in the exchange dir. Prompts must not rely
on the agent reading CLAUDE.md — past experience shows this is unreliable. Instead the
script computes the exchange dir path for the backend being tested and injects it
directly into the prompt string (see backend matrix section).

Every prompt is a bare shell command with no prefix, to minimise token usage and remove
ambiguity. For example, for a docker backend:

    `echo smoke > output.txt && touch /yoloai/files/done`

Do not add a `run:` prefix or any other framing — it adds no value and may confuse the
agent. The assertion only checks that the sentinel file exists, so it doesn't matter
whether the agent uses its bash tool or a file-writing tool — both produce the file.

## Prerequisites check

Before running any tests, the script runs a `check_prerequisites()` function that calls
`yoloai system check --json --backend <backend> --agent claude` for each backend in the
matrix and collects the results. `system check` already covers:

- Backend daemon reachable (docker socket, containerd socket, tart/seatbelt available)
- Base image built (`yoloai system build` has been run)
- Agent credentials set (`ANTHROPIC_API_KEY` present)
- For VM backends: `CAP_NET_ADMIN` available, `/dev/kvm` present, CNI plugins installed

The function then prints a summary table and takes one of three actions per backend:

- **Required** (docker/linux/container — the default for T2–T6): abort with a clear
  message if unavailable. There is no point running any tests without it.
- **Optional** (all other matrix entries): skip that backend's T1 run with a one-line
  note explaining why. The rest of the test suite continues.
- **All credentials missing**: abort immediately — no test can run.

This means on a freshly provisioned machine with nothing set up, the script tells you
exactly what to fix rather than failing mid-run with an opaque error. On a
fully-configured machine it runs silently past the check and straight into the tests.

The one thing `system check` cannot verify is whether nested virtualization is actually
working end-to-end (Proxmox CPU type = `host`, nested KVM enabled on the host). `/dev/kvm`
existing is a necessary but not sufficient condition. If the Proxmox setup is wrong,
the VM test will fail when Kata tries to start the inner VM — the error from yoloai at
that point is clear enough that no upfront check is worth adding.

## Tests

### T1: full\_workflow (runs in the backend matrix)

Prompt (docker example): `echo smoke > output.txt && touch /yoloai/files/done`

The exchange dir path is substituted per backend (see backend matrix).

1. Wait for `done` sentinel via `yoloai files ls`
2. `yoloai diff <name>` → assert non-empty output
3. `yoloai apply <name> --yes` → assert `output.txt` exists in the per-test project dir
   copy **and contains "smoke"** (verifies the full pipeline preserved content, not just
   that a file appeared)
4. `yoloai log <name>` → assert non-empty (folded in from the former T2)
5. `yoloai sandbox <name> info` → assert output contains the sandbox name and agent name
   (folded in from the former T3 — more meaningful here since the container has
   actually run, unlike `--no-start`)

### T2: stop\_start\_exec

Prompt (docker example): `touch /yoloai/files/done`

1. Wait for sentinel
2. `yoloai stop <name>` — the container may already be stopped if the agent process was
   the sole entrypoint; `stop` must be idempotent in this case
3. `yoloai start <name>` — this restarts the container and re-runs the agent with the
   same prompt. The `done` sentinel already exists so `wait_for_sentinel` will return
   immediately, but there is a window before the container is fully up. Use
   `wait_for_sentinel` again after `start` (returns instantly since the file exists) to
   confirm the container is reachable before calling `exec`.
4. `yoloai exec <name> echo alive` → assert "alive" in output

Tests credential re-injection on restart and container resume — real-runtime concerns
that automated tests do not cover.

### T3: files\_exchange

No agent run needed (`--no-start`).

1. Create `$TMPDIR/somefile.txt` with known content before the test
2. `yoloai files <name> put $TMPDIR/somefile.txt`
3. `yoloai files <name> ls` → assert `somefile.txt` appears
4. `yoloai files <name> get somefile.txt -o $TMPDIR/got/` → assert file landed with
   correct content

### T4: overlay (Linux only)

Prompt: `echo smoke > output.txt && touch /yoloai/files/done`

Uses `<project_dir>:overlay` workdir syntax with plain `container` isolation.

- `container-enhanced` (gVisor) is **incompatible** with overlay — yoloai rejects that
  combination at creation time (`create_instance.go`)
- `CAP_SYS_ADMIN` is **automatically added** by yoloai when overlay dirs are present;
  no sudo or manual cap flag is needed
- `FILES_DIR` is always `/yoloai/files` for overlay (seatbelt does not support overlay)

Steps:
1. Wait for sentinel
2. `yoloai diff <name>` → assert non-empty
3. `yoloai apply <name> --yes` → assert `output.txt` in project dir copy

### T5: clone

Prompt (docker example): `echo smoke > clone-output.txt && touch /yoloai/files/done`

1. Wait for sentinel in sandbox A
2. `yoloai clone A B` — B must be added to the cleanup list immediately after the clone
   command succeeds, before any assertions, so it is destroyed even if the test fails
3. `yoloai diff B` → assert `clone-output.txt` appears

This asserts that clone copies the full work copy state including agent modifications,
not just the baseline. If clone semantics ever change (e.g. clone from baseline), T5
will start failing and the test intent should be re-evaluated.

### T6: reset

Prompt (docker example): `echo smoke > reset-me.txt && touch /yoloai/files/done`

1. Wait for sentinel
2. `yoloai diff <name>` → assert non-empty
3. `yoloai reset --yes <name>` — reset is synchronous and blocks until the work copy is
   restored and the container is restarted; no additional wait is needed
4. `yoloai diff <name>` → assert empty (clean)

## Backend matrix

The script is designed to be run on both a Linux host and a macOS host as a final
check. The two runs are complementary — each covers backends unavailable on the other
platform.

The smoke test never uses `sudo -E`. Permission and ownership issues that previously
required sudo for container tests are solved. VM tests require specific capabilities and
device access, but these are granted by configuring the Linux test machine once (see
below) rather than running yoloai as root.

T2–T6 (non-matrix tests) run once using **docker with linux container isolation** as the
explicit default on both hosts. If docker is not available the script prints a clear
message and exits rather than silently skipping. T1 runs across all applicable backends.

The exchange dir path injected into prompts is computed per backend invocation:

- All container/VM backends (Docker, Podman, Tart, containerd): `/yoloai/files`
- Seatbelt (macOS host-filesystem backend): `~/.yoloai/sandboxes/<name>/files`

### Linux host

All Linux host tests use `--os linux`. T4 (overlay) runs on Linux only, using
docker/container.

| `--backend` | `--isolation` | Notes |
|-------------|---------------|-------|
| docker | container | |
| podman | container | |
| (omit) | container-enhanced | requires gVisor (runsc) |
| (omit) | vm | requires Kata + QEMU, nested KVM |
| (omit) | vm-enhanced | requires Kata + Firecracker, nested KVM |

#### Required one-time setup for VM tests on the Linux host

The Linux test machine is a Proxmox VM. VM tests (Kata/QEMU, Kata/Firecracker) use
nested KVM — VMs inside the Proxmox VM. This works cleanly with two Proxmox-level
prerequisites:

1. **Enable nested virtualization on the Proxmox host** (not inside the guest):
   ```bash
   # Intel
   echo "options kvm-intel nested=1" > /etc/modprobe.d/kvm-intel.conf
   modprobe -r kvm-intel && modprobe kvm-intel
   # AMD
   echo "options kvm-amd nested=1" > /etc/modprobe.d/kvm-amd.conf
   modprobe -r kvm-amd && modprobe kvm-amd
   ```

2. **Set the VM's CPU type to `host`** in the Proxmox UI (VM → Hardware → Processors →
   Type: `host`) or in the `.conf` file as `cpu: host`. This passes through the
   `vmx`/`svm` flag to the guest, causing `/dev/kvm` to appear inside it.

Once those are set, `/dev/kvm` is present inside the guest and the rest of the setup
below applies unchanged.

The containerd/Kata backend needs three things that a plain user doesn't have by
default. Configure them once on the test machine:

```bash
# /dev/kvm access
sudo usermod -aG kvm $USER

# Containerd socket access
sudo groupadd -f containerd
sudo usermod -aG containerd $USER
printf '\n[grpc]\n  gid = %s\n' "$(id -g containerd)" | sudo tee -a /etc/containerd/config.toml
sudo systemctl restart containerd

# CAP_NET_ADMIN for CNI network namespace creation
sudo setcap cap_net_admin+ep $(which yoloai)
```

Log out and back in after group changes.

**Note on setcap:** `cap_net_admin+ep` is lost whenever the yoloai binary is rebuilt.
Run `sudo setcap cap_net_admin+ep $(which yoloai)` again after each `make install`. If
the capability is missing, yoloai will print a clear error — it won't silently fail.

### macOS host

T4 (overlay) is skipped on macOS entirely.

| `--os` | `--backend` | `--isolation` | Notes |
|--------|-------------|---------------|-------|
| linux | docker | container | |
| linux | podman | container | |
| linux | (omit) | vm | Tart, Linux guest |
| mac | (omit) | container | Seatbelt |
| mac | (omit) | vm | Tart, macOS guest |

## Out of scope

- **`attach`** — inherently interactive (tmux), not automatable in a script
- **Agent reads a file from the exchange dir** — the exchange dir container-side path
  varies by backend; better tested manually
- **Multi-agent (Gemini, Codex)** — belongs in a separate run mode gated on key
  presence; out of scope for now
