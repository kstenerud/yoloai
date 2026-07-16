CLEANING UP STUCK STATE

  Normal `yoloai destroy` and `yoloai system prune` handle the
  everyday cases. This topic covers the rare failure modes where
  state survives those — a crashed container backend, a sandbox
  whose lock file points at a dead PID, or a Kata VM stuck in
  CREATED state with no shim process. The recipes here clean each
  type of leftover state without nuking your whole config.

  Try the soft cleanup first. If it doesn't free the resource,
  jump to the matching "stuck state" recipe below.

SOFT CLEANUP (run these first)

  yoloai destroy <name>           Remove a sandbox you no longer need
  yoloai destroy --all            Remove every sandbox
  yoloai sandbox <name> unlock    Clear a stale lock file (rare;
                                    only when destroy reports a lock
                                    held by a dead PID)
  yoloai system prune             Remove orphaned backend resources
                                    (containers, volumes, stale temp
                                    dirs) and reclaim the no-rebuild
                                    cache (build cache, volumes)
  yoloai system prune --images    Also remove backend base/profile
                                    images (forces base image rebuild)

  These are sufficient for >95% of cases. If a resource resists
  removal, identify which "stuck state" applies below.

STUCK STATE: CONTAINERD TASK IN "CREATED" STATE

  Symptom: `yoloai destroy` returns "task must be stopped before
  deletion: created: failed precondition", or `ctr -n yoloai tasks
  list` shows a task with STATUS=CREATED and no agent activity.

  Cause: the Kata shim crashed or was killed before the VM finished
  starting, leaving containerd with a task record that has no live
  process.

  Fix (requires sudo):

     # 1. Find the orphaned Kata shim, if any
     ps aux | grep "containerd-shim-kata.*<sandbox-name>"

     # 2. If a shim PID is shown, kill it
     sudo kill -KILL <PID>

     # 3. Force-delete the task (now containerd will accept it)
     sudo ctr -n yoloai tasks delete --force yoloai-cli-<sandbox-name>

     # 4. Delete the container record
     sudo ctr -n yoloai containers delete yoloai-cli-<sandbox-name>

  After these four steps, `yoloai destroy <sandbox-name>` will
  succeed (or report not-found, which is fine).

STUCK STATE: LEFTOVER NETWORK NAMESPACE

  Symptom: `sudo ip netns list | grep yoloai` shows a namespace
  whose sandbox is already destroyed.

  Fix (the netns wraps the container id, so it is doubly prefixed;
  use the exact name `ip netns list` printed):

     sudo ip netns delete yoloai-yoloai-cli-<sandbox-name>

  This is safe — netns deletion only succeeds if no process is
  using it.

STUCK STATE: STALE CNI IPAM LEASE

  Symptom: new sandboxes fail with "Address already in use" or
  every sandbox gets the same IP. The yoloai CNI plugin stores
  lease files in /run/cni/networks/yoloai/.

  Fix (requires sudo):

     # List leases
     sudo ls /run/cni/networks/yoloai/

     # Remove a specific lease (filename is the IP)
     sudo rm /run/cni/networks/yoloai/<ip-address>

     # Or remove all leases — only safe with NO running containerd
     # sandboxes (check with `sudo ctr -n yoloai tasks list`)
     sudo rm /run/cni/networks/yoloai/*

STUCK STATE: LEFTOVER /tmp DIRECTORIES

  yoloai creates /tmp/yoloai-secrets-* dirs for credential injection
  (deleted shortly after container start) and /tmp/yoloai-smoke-*
  dirs for test fixtures (deleted when the test cleans up). A
  crashed process can leave them behind.

  Fix (no sudo needed; they're owned by your user):

     rm -rf /tmp/yoloai-secrets-* /tmp/yoloai-smoke-*

  Safe to run any time — the dirs are recreated on each fresh
  sandbox.

STUCK STATE: ORPHANED SANDBOX DIRECTORY

  Symptom: `yoloai destroy` says sandbox not found, but
  `~/.yoloai/sandboxes/<name>/` still exists on disk.

  Fix (no sudo needed):

     rm -rf ~/.yoloai/sandboxes/<name>

  yoloai will not recreate this dir; only `yoloai new` does.

STUCK STATE: KATA SHIM SOCKET (`EADDRINUSE`)

  Symptom: `yoloai new` fails creating a containerd-VM sandbox
  with "Address already in use" referencing /run/kata/<name>/
  or /run/containerd/s/<sha>. A previous shim died without
  cleanup.

  This is normally handled automatically by lifecycle code
  (killStaleKataShims + removeKataStateDir). If it persists:

     # Find and kill any matching shim
     ps aux | grep "containerd-shim-kata.*<sandbox-name>"
     sudo kill -KILL <PID>

     # Remove the Kata management directory
     sudo rm -rf /run/kata/<container-name>

     # Remove any TTRPC socket file matching this sandbox
     # (the path is sha256-based; safer to retry yoloai new
     # which recomputes and cleans up its own socket path)
     yoloai new ...

NUCLEAR OPTION: RESET EVERYTHING

  When you want to wipe all yoloai-managed state and start fresh
  (e.g., upgrading across a major version, or recovering from a
  broken host). This destroys every sandbox and reclaims all
  backend caches.

     # 1. Destroy every sandbox
     yoloai destroy --all

     # 2. Prune every backend, including base/profile images
     yoloai system prune --images

     # 3. Manually clean any remnants from the recipes above
     #    (e.g., leftover /tmp/yoloai-* dirs, orphan netns)

     # 4. Remove the yoloai data directory itself (loses config
     #    and any sandbox state — be sure)
     rm -rf ~/.yoloai

WHEN TO REPORT A BUG

  If you regularly hit stuck states even after the soft cleanup
  succeeds — especially "task in CREATED state" or "leftover
  shim process" — please file a bug:

     yoloai sandbox <name> bugreport unsafe

  The bug report includes recent cli.jsonl, sandbox.jsonl, and the
  monitor's detector tail, which together identify what crashed
  during sandbox setup.

  Filing: https://github.com/kstenerud/yoloai/issues
