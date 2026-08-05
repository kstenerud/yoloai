#!/bin/bash
# ABOUTME: No-reboot control for the reboot test — does a plain stop/start move a sandbox's address?
# ABOUTME: Isolates "the reboot moved it" from "any restart moves it". Touches no pf state.
#
# Run: bash restart_control.sh rb-a rb-b rb-t     (as the normal user, no sudo needed)
#
# Round 2 saw the tart guest go 192.168.65.2 -> .3 across a reboot, but that guest was also stopped
# and started. Those are two different claims with two different consequences, and the reboot result
# cannot separate them. This restarts each guest in place, on a host that has not rebooted, in the
# SAME order round 2 used — so the only variable removed is the reboot itself.

set -u
ROOT="$(cd "$(dirname "$0")" && pwd)"
[ -x "$ROOT/yoloai" ] || ROOT=/Users/karlstenerud/Projects/yoloai
Y="$ROOT/yoloai"

bk()    { "$Y" ls 2>/dev/null | awk -v n="$1" '$1==n {print $3}'; }
state() { "$Y" ls 2>/dev/null | awk -v n="$1" '$1==n {print $2}'; }

# Liveness is asked of the BACKEND, not of yoloAI. `yoloai ls` reports a running tart VM whose agent
# is not attached as "idle", and the post half's address lookup refused to read that state at all —
# which is how round 2 rendered UNKNOWN for a guest that was up and addressable the whole time.
live() {
  case "$(bk "$1")" in
    tart)  [ "$(tart get "yoloai-cli-$1" --format json 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin).get("State",""))' 2>/dev/null)" = running ] ;;
    apple) [ "$(container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"].get("state",""))' 2>/dev/null)" = running ] ;;
    *) false ;;
  esac
}
ipof() {
  live "$1" || { printf ''; return; }
  case "$(bk "$1")" in
    tart)  tart ip "yoloai-cli-$1" 2>/dev/null | tr -d '[:space:]' ;;
    apple) container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null ;;
  esac
}
# tart persists its MAC on disk; apple assigns one at start and only reports it while running. So a
# stopped apple container has no MAC to read, and that absence is a fact rather than a failure.
macof() {
  case "$(bk "$1")" in
    tart)  python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("macAddress",""))' \
             "$HOME/.tart/vms/yoloai-cli-$1/config.json" 2>/dev/null ;;
    apple) container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"]["networks"][0].get("macAddress",""))' 2>/dev/null ;;
  esac
}
wait_addr() { for _ in $(seq 1 30); do [ -n "$(ipof "$1")" ] && return; sleep 2; done; }

echo "=== restart control (NO reboot) — $(date '+%Y-%m-%d %H:%M:%S') ==="
echo "uptime: $(uptime | sed 's/.*up //; s/,.*users.*//')"
echo

for sb in "$@"; do
  b=$(bk "$sb")
  [ -n "$b" ] || { echo "$sb: not in the store, skipping"; continue; }
  ip0=$(ipof "$sb"); mac0=$(macof "$sb"); st0=$(state "$sb")
  echo "== $sb ($b) =="
  echo "   before: state=$st0 ip=${ip0:-<none>} mac=${mac0:-<none>}"
  [ -n "$ip0" ] || { echo "   no address before the restart — nothing to compare; skipping"; echo; continue; }

  "$Y" stop "$sb"  >/dev/null 2>&1; rc1=$?
  "$Y" start "$sb" >/dev/null 2>&1; rc2=$?
  wait_addr "$sb"
  ip1=$(ipof "$sb"); mac1=$(macof "$sb"); st1=$(state "$sb")
  echo "   after : state=$st1 ip=${ip1:-<none>} mac=${mac1:-<none>}   (stop rc=$rc1 start rc=$rc2)"

  if [ -z "$ip1" ]; then
    echo "   UNKNOWN  came back without an address"
  elif [ "$ip0" = "$ip1" ]; then
    echo "   STABLE   address survived a plain stop/start"
  else
    echo "   MOVED    $ip0 -> $ip1 with NO reboot involved"
  fi
  [ "$mac0" = "$mac1" ] && echo "   mac unchanged" || echo "   MAC CHANGED $mac0 -> $mac1"
  echo
done

echo "== leases now =="
python3 - <<'EOF'
import re
try: t=open('/var/db/dhcpd_leases').read()
except Exception as e: print("  unreadable:",e); raise SystemExit
for b in re.findall(r'\{(.*?)\}', t, re.S):
    ip=re.search(r'ip_address=(\S+)',b); hw=re.search(r'hw_address=(\S+)',b)
    if ip and hw and (ip.group(1).startswith('192.168.64.') or ip.group(1).startswith('192.168.65.')):
        print("  ",ip.group(1), hw.group(1))
EOF
