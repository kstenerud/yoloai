#!/bin/bash
# ABOUTME: Measures what apple's `container` CLI reports in each daemon state, so Inspect can tell
# ABOUTME: "no such container" from "could not ask" (DF180), and when the vmnet bridge exists (DF178).
#
# Run: bash apple_daemon_states.sh          (no sudo; stops and restarts the container service)
#
# TWO FINDINGS, ONE WINDOW
#   DF180: `runtime/apple/apple.go` maps EVERY `container inspect` failure to runtime.ErrNotFound.
#     Status turns that into StatusRemoved, and Remove turns it into "already gone, return nil" —
#     so with the daemon down, `yoloai ls` calls live sandboxes `removed` and `yoloai destroy`
#     exits 0 while leaking the container. The fix has to discriminate, which means knowing what
#     the CLI actually emits in each state. That is P1/P2 here.
#   DF178: `runtime/apple/reach.go` claims the vmnet bridge is host-bindable once the service runs,
#     "BEFORE any of our VMs is created". Measured false: the bridge is created by the first
#     container. The open question the finding names is whether a real `yoloai new --broker` on a
#     freshly started service actually degrades to handing the key into the sandbox. That is P3.
#
#   Both need the service stopped, and stopping it is the expensive part, so they share one run.
#
# WHAT THIS LEAVES BEHIND: nothing. The service is restarted at exit even on failure, and the probe
# sandbox is destroyed. If it dies mid-run: `container system start`.

set -u

BOX=df178-broker-probe
GW=192.168.64.1

hr() { printf '\n== %s\n' "$1"; }
note() { printf '  %-44s %s\n' "$1" "$2"; }

cleanup() {
  container system start >/dev/null 2>&1
  yoloai destroy "$BOX" --abandon-unapplied >/dev/null 2>&1
  printf '\n[cleanup] container service restarted; probe sandbox removed\n'
}
trap cleanup EXIT

command -v container >/dev/null || { echo "container not on PATH"; exit 2; }
command -v yoloai >/dev/null || { echo "yoloai not on PATH"; exit 2; }

printf '=== apple daemon states: what the CLI says, and when the bridge exists ===\n'
printf 'date: %s\nhost: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$(sw_vers -productVersion)"

# Captures stderr and the REAL exit code (not a pipeline's), because both are candidate
# discriminators and the fix may key on either.
probe() { # probe <label> <args...>
  local label=$1; shift
  local out rc
  out=$("$@" 2>&1); rc=$?
  printf '  %-26s rc=%-3s %s\n' "$label" "$rc" "$(printf '%s' "$out" | head -1)"
}

hr "P1  daemon UP — the two cases that must be distinguishable"
container system start >/dev/null 2>&1
sleep 2
note "service status" "$(container system status 2>&1 | awk '/^status/{print $2}')"
probe "inspect missing name" container inspect no-such-container-xyz
probe "list --all" container list --all
note "gateway $GW assigned?" \
  "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo no)"

hr "P2  daemon DOWN — the state after every reboot (the service is not launchd-registered)"
container system stop >/dev/null 2>&1
sleep 3
note "service status" "$(container system status 2>&1 | head -1)"
probe "inspect missing name" container inspect no-such-container-xyz
probe "inspect plausible name" container inspect yoloai-cli-anything
probe "list --all" container list --all
note "gateway $GW assigned?" \
  "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo no)"
printf '\n  READ THIS ROW PAIR: if P1 and P2 emit different text or different exit codes for the\n'
printf '  SAME command, Inspect can discriminate. If they are identical, it cannot, and the fix\n'
printf '  has to ask the service directly instead.\n'

hr "P3  DF178 — does a real --broker sandbox degrade on a freshly started service?"
container system start >/dev/null 2>&1
sleep 3
note "service just started; gateway assigned?" \
  "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo 'no  <-- DF178: the bridge is not up yet')"
WORKDIR=$(mktemp -d)
git -C "$WORKDIR" init -q
printf 'x\n' > "$WORKDIR/README.md"
git -C "$WORKDIR" add -A
git -C "$WORKDIR" -c user.email=s@e.com -c user.name=s commit -qm init

# The authoritative signal is injector.json — the persisted handle to a RUNNING sidecar
# (internal/broker/host.go). Its presence with a live PID means the key stayed host-side; its
# absence means delivery went direct. An earlier version of this probe sampled `env` inside a
# fresh `container exec` shell instead, which does not reproduce the agent process's
# environment and so could not distinguish the two at all.
brokerstate() { # brokerstate <box>
  local sb rec pid
  sb="$(yoloai files "$1" path 2>/dev/null)/../.."
  rec=$(find "$sb" -maxdepth 2 -name injector.json 2>/dev/null | head -1)
  if [ -z "$rec" ]; then printf 'no injector.json (delivery went DIRECT)'; return; fi
  pid=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('PID',''))" "$rec" 2>/dev/null)
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    printf 'injector.json present, pid %s ALIVE (key held HOST-SIDE)' "$pid"
  else
    printf 'injector.json present, pid %s not alive' "${pid:-?}"
  fi
}

printf '  arm 1: explicit --broker on the fresh service\n'
printf '    (documented as "errors if the backend can\x27t", so a failure here is the CORRECT\n'
printf '     outcome and a success means the bridge was up in time)\n'
newout=$(yoloai new --backend apple --broker "$BOX" "$WORKDIR" 2>&1); newrc=$?
printf '    new rc=%s\n' "$newrc"
[ "$newrc" != 0 ] && printf '%s\n' "$newout" | grep -iE "broker|inject|error" | head -2 | sed 's/^/      /'
[ "$newrc" = 0 ] && note "  broker state" "$(brokerstate "$BOX")"
note "  gateway now?" "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo no)"
yoloai destroy "$BOX" --abandon-unapplied >/dev/null 2>&1

printf '  arm 2: DEFAULT (no broker flag) on a fresh service — the path users take\n'
container system stop >/dev/null 2>&1; sleep 3; container system start >/dev/null 2>&1; sleep 3
note "  gateway before this sandbox" \
  "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo no)"
newout2=$(yoloai new --backend apple "$BOX" "$WORKDIR" 2>&1); newrc2=$?
printf '    new rc=%s\n' "$newrc2"
[ "$newrc2" != 0 ] && printf '%s\n' "$newout2" | grep -iE "broker|inject|error" | head -2 | sed 's/^/      /'
note "  broker state" "$(brokerstate "$BOX")"
note "  gateway after first container" \
  "$(ifconfig 2>/dev/null | grep -q "inet $GW " && echo yes || echo no)"

printf '\n== what this settles ==\n'
printf '  DF180: compare P1 and P2. DF178: P3 is the end-to-end run the finding asked for.\n'
