#!/bin/bash
# ABOUTME: Half two of the reboot test — compares live state against the pre-reboot snapshot and
# ABOUTME: exercises unattended recovery after a REAL reboot, then removes everything it left.
#
# Run: sudo bash reboot_post.sh          (after reboot_pre.sh and an actual restart)
#
# Reads results/reboot-snapshot.txt. Every question is answered by comparison against a recorded
# pre-reboot value, so no verdict here can be produced by a test that could not have gone the other
# way — the snapshot IS the control.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../../../.." && pwd)"
SNAP="$HERE/results/reboot-snapshot.txt"
RESULTS="$HERE/results/reboot-post.txt"
[ -f "$SNAP" ] || { echo "no snapshot at $SNAP — run reboot_pre.sh first"; exit 2; }
exec > >(tee "$RESULTS") 2>&1

# Every value below comes from the snapshot. Declaring them first states the contract between the
# two halves explicitly, and stops shellcheck reading a sourced variable as a typo for a local one.
# All default to empty, including the numeric ones: a numeric default of 0 is a plausible-looking
# value that would sail through the load check below and be compared against as if it were recorded.
PRE_DATE=""; ANCHOR=""; SLOTS=""
A_SB=""; B_SB=""; T_SB=""; A_IP=""; B_IP=""; T_IP=""
A_MAC=""; B_MAC=""; T_MAC=""; START_ORDER=""; LEASES64=""; LEASES65=""
ALLOW_A=""; ALLOW_B=""; DENY=""
ANCHOR_RULES=""; SRC0=""; SRC1=""; SRC2=""
PRE_ENF_A=""; PRE_ENF_B=""
# PRE_ENF_T is a real snapshot field and is declared for the same contract reason as the rest, but
# nothing here reads it: P9 re-adds and re-verifies slots 0 and 1 only, so there is no post-reboot
# tart enforcement measurement for it to be the "before" of. Printing it alone would be half a
# comparison, which is the thing this harness keeps getting wrong. It stays recorded in the snapshot
# because that file is the round's durable record, and is consumed here by nothing.
# shellcheck disable=SC2034
PRE_ENF_T=""                               # empty when the round had no tart sandbox
MAIN_RULES=""; MAIN_SHA=""; PF_STATUS=""; PF_TOKENS=""; BRIDGES=""; CONF_SHA=""; SANDBOXES=""
# shellcheck disable=SC1090
. "$SNAP"
# Every field is checked, not two of them. The first run of this test sourced a snapshot whose
# space-carrying values were unquoted: PRE_DATE and PF_STATUS and SANDBOXES failed to assign at all,
# BRIDGES truncated to its first token without erroring, and the run went on to print "before:" lines
# that were empty and one UNKNOWN verdict derived from a control that was never loaded. A comparison
# against an absent control is not a weaker measurement, it is not a measurement.
#
# Run against that snapshot this guard names PRE_DATE, PF_STATUS and SANDBOXES — and not BRIDGES,
# which loaded truncated rather than empty. So this catches three of the four and the quoting in
# reboot_pre.sh catches the fourth; the guard is the backstop, not the fix.
missing=""
for v in PRE_DATE ANCHOR SLOTS A_SB B_SB A_IP B_IP ALLOW_A ALLOW_B DENY ANCHOR_RULES \
         SRC0 SRC1 MAIN_RULES MAIN_SHA PF_STATUS PF_TOKENS BRIDGES CONF_SHA SANDBOXES \
         A_MAC B_MAC START_ORDER LEASES64 LEASES65 PRE_ENF_A PRE_ENF_B; do
  [ -n "${!v}" ] || missing="$missing $v"
done
[ -z "$missing" ] || {
  echo "snapshot at $SNAP did not load cleanly — empty after sourcing:$missing"
  echo "every verdict here is a comparison against those values; refusing to render one without them"
  exit 2
}
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-pool.conf"
SUDOERS=/etc/sudoers.d/yoloai-reboot-probe
PASS=0; FAIL=0; UNKNOWN=0
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
say() { printf '\n== %s ==\n' "$*"; }
asuser() { sudo -u "$U" "$@"; }
bk() { asuser "$ROOT/yoloai" ls 2>/dev/null | awk -v n="$1" '$1==n {print $3}'; }
state() { asuser "$ROOT/yoloai" ls 2>/dev/null | awk -v n="$1" '$1==n {print $2}'; }
sbstates() { asuser "$ROOT/yoloai" ls 2>/dev/null | tail -n +2 | awk '{print $1":"$2}' | tr '\n' ' '; }
# The gate below is not belt-and-braces: `tart ip` resolves through /var/db/dhcpd_leases, which
# survives a reboot, and round 1 reported a stopped VM's address as "preserved across reboot" off a
# lease record on a host where the VM's bridge did not exist. An address nothing holds is not an
# address. But the gate round 1 grew asked YOLOAI, matching `active|running` — and `yoloai ls` calls
# a running tart VM whose agent is not attached `idle`, which is the state the tart guest was in
# BEFORE the reboot as well as after. So the check excluded the one guest it was measuring, on both
# sides of its own comparison, and round 2's tart verdict was UNKNOWN for a VM that was up and
# addressable throughout. Ask the backend, which is the thing that knows.
live() {
  case "$(bk "$1")" in
    tart)  [ "$(asuser tart get "yoloai-cli-$1" --format json 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin).get("State",""))' 2>/dev/null)" = running ] ;;
    apple) [ "$(asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"].get("state",""))' 2>/dev/null)" = running ] ;;
    *) false ;;
  esac
}
ipof() {
  live "$1" || { printf ''; return; }
  case "$(bk "$1")" in
    tart)  asuser tart ip "yoloai-cli-$1" 2>/dev/null | tr -d '[:space:]' ;;
    apple) asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null ;;
    *) printf '' ;;
  esac
}
macof() {
  case "$(bk "$1")" in
    tart)  asuser python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("macAddress",""))' \
             "/Users/$U/.tart/vms/yoloai-cli-$1/config.json" 2>/dev/null ;;
    apple) asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"]["networks"][0].get("macAddress",""))' 2>/dev/null ;;
    *) printf '' ;;
  esac
}
leasecount() { # $1 = subnet prefix. apple does not use this file; a 0 for 64.x is the expected read.
  python3 - "$1" <<'EOF' 2>/dev/null
import re,sys
try: t=open('/var/db/dhcpd_leases').read()
except Exception: print(0); raise SystemExit
p=sys.argv[1]
print(sum(1 for b in re.findall(r'\{(.*?)\}',t,re.S)
          for m in [re.search(r'ip_address=(\S+)',b)] if m and m.group(1).startswith(p)))
EOF
}
egress() {
  local c
  case "$(bk "$1")" in
    tart)  c=$(asuser tart exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    apple) c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    *) c="" ;;
  esac
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

echo "=== reboot test, POST half — $(date '+%Y-%m-%d %H:%M:%S') ==="
echo "pre-reboot snapshot taken $PRE_DATE"
up=$(uptime | sed 's/.*up //; s/,.*users.*//')
echo "uptime now: $up"
case "$up" in *day*|*:*[0-9][0-9]*) : ;; esac
echo "        (if that is not a small number of minutes, the machine may not have rebooted)"

say "P1/P2 DID THE ANCHOR SURVIVE? — the premise the plan rests on"
now_rules=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
now_src0=$(pfctl -a "$ANCHOR" -t yb_src_0 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')
now_src1=$(pfctl -a "$ANCHOR" -t yb_src_1 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')
now_src2=$(pfctl -a "$ANCHOR" -t yb_src_2 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')
echo "        rules: before=$ANCHOR_RULES after=${now_rules:-0}"
echo "        src_0: before='$SRC0' after='${now_src0}'"
echo "        src_1: before='$SRC1' after='${now_src1}'"
echo "        src_2: before='$SRC2' after='${now_src2}'   (tart slot, empty if no tart sandbox)"
if [ "${now_rules:-0}" -eq 0 ]; then
  ok "anchor RULES did not survive the reboot — the plan's premise is CORRECT, and restoring the"
  echo "           pool at boot is mandatory rather than defensive"
elif [ "${now_rules:-0}" -eq "${ANCHOR_RULES:-0}" ]; then
  bad "anchor rules SURVIVED (${now_rules}) — the plan's premise is WRONG. Reboot recovery, the"
  echo "           pinned-file grant row and part of VERIFY exist for a problem that does not occur"
else
  unk "partial survival: $ANCHOR_RULES -> $now_rules"
fi
if [ -z "$now_src0$now_src1$now_src2" ] && [ -n "$SRC0$SRC1$SRC2" ]; then
  ok "table CONTENTS did not survive in ANY slot — membership must be rebuilt, not assumed"
elif [ "$now_src0" = "$SRC0" ] && [ "$now_src1" = "$SRC1" ]; then
  bad "table contents survived ('$now_src0') — if rules did not, this is the silent fail-open"
  echo "           state by default at every boot, which is worse than either alone"
fi

say "P3 IS pf STILL ENABLED? — if not, every rule yoloAI loads is inert"
now_status=$(pfctl -s info 2>/dev/null | head -1 | tr -s ' ')
now_tokens=$(pfctl -s References 2>/dev/null | grep -c . || true)
echo "        before: $PF_STATUS"
echo "        after : $now_status"
echo "        reference rows: before=$PF_TOKENS after=${now_tokens:-0}"
if printf '%s' "$now_status" | grep -qi "Status: Enabled"; then
  ok "pf is enabled after boot without yoloAI doing anything — the -E reference question is a"
  echo "           robustness concern, not a prerequisite"
else
  bad "pf is NOT enabled after boot ($now_status) — yoloAI must hold its own reference or every"
  echo "           isolated sandbox starts unfiltered. This changes Phase 1."
fi

say "P4 MAIN RULESET — did it come back identical?"
now_main=$(pfctl -s rules 2>/dev/null | grep -c . || true)
now_sha=$(pfctl -s rules 2>/dev/null | shasum | cut -d' ' -f1)
echo "        count: before=$MAIN_RULES after=${now_main:-0} | sha before=${MAIN_SHA:0:12} after=${now_sha:0:12}"
if [ "$now_sha" = "$MAIN_SHA" ]; then ok "main ruleset identical across reboot"
else bad "main ruleset CHANGED across reboot — anything keyed on its contents is fragile"; fi

say "P5 DID THE FILES SURVIVE? (expected yes — they are files, not kernel state)"
if [ -f "$CONF" ] && [ "$(shasum "$CONF" | cut -d' ' -f1)" = "$CONF_SHA" ]; then
  ok "pinned ruleset file survived unchanged"
else bad "pinned ruleset file missing or changed"; fi
if [ -f "$SUDOERS" ]; then ok "sudoers grant survived"; else bad "sudoers grant did not survive"; fi

say "P7 DID THE SANDBOXES SURVIVE, AND CAN THEY BE RESTARTED?"
# The apple `container` service is not registered with launchd, so it is down after every reboot, and
# yoloAI renders an unreachable daemon as a REMOVED container (DF180). Round 1 read that as "the
# sandboxes did not survive" and reasoned about recovery from it; they were intact. Start the service
# before reading any sandbox state, and say what it was, because "was it up?" is now part of the
# result rather than a precondition nobody recorded.
svc_before=$(asuser container system status 2>&1 | grep -Em1 '^status|not running')
echo "        apple container service before: $svc_before"
asuser container system start >/dev/null 2>&1
echo "        apple container service after : $(asuser container system status 2>&1 | grep -Em1 '^status|not running')"
# P1-P5 are answered above and are unaffected by what follows: they read kernel and file state, which
# starting a sandbox does not touch. Everything from here needs a guest that is actually running. The
# first run of this test measured none of it — after a reboot nothing is up, and the harness read the
# empty host as the answer, which is how "no vmnet bridges exist" became "the bridges moved".
echo "        before: $SANDBOXES"
echo "        after : $(sbstates)"
# DELIBERATELY REVERSED — B first, then A. Round 2 restarted them in creation order, saw the same
# addresses come back, and recorded "addresses preserved across reboot". A no-reboot control then
# showed every guest moves its address on an ordinary stop/start with a freshly generated MAC, so
# nothing preserves anything: both allocators hand out in start order, and round 2 restarted in the
# order that reproduced the original assignment. Same order, two explanations, one observation.
# Starting B first separates them — if B takes A's old address, a saved slot->address mapping does
# not go stale, it silently names the WRONG SANDBOX, which is a fail-open the plan must account for.
echo "        start order: pre='$START_ORDER' post='$B_SB $A_SB${T_SB:+ $T_SB}' (REVERSED on purpose — P10)"
restart_fail=0
for sb in "$B_SB" "$A_SB" ${T_SB:+"$T_SB"}; do
  st=$(state "$sb")
  printf '        %-6s came back %-9s' "$sb" "${st:-<absent>}"
  case "$st" in
    active|running) echo "— already up" ; continue ;;
    "")             echo "— gone from the store entirely"; restart_fail=$((restart_fail+1)); continue ;;
  esac
  out=$(asuser "$ROOT/yoloai" start "$sb" 2>&1); rc=$?
  echo "— start rc=$rc, now $(state "$sb")"
  if [ $rc -ne 0 ]; then
    restart_fail=$((restart_fail+1))
    printf '%s\n' "$out" | tail -3 | sed 's/^/           /'
  fi
done
echo "        states now: $(sbstates)"
# A guest's address lags its start by a few seconds. Poll for it rather than sleeping a guessed
# constant, so a slow host reads as slow rather than as address-less.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  [ -n "$(ipof "$A_SB")$(ipof "$B_SB")${T_SB:+$(ipof "$T_SB")}" ] && break
  sleep 2
done
if [ "$restart_fail" -eq 0 ]; then
  ok "every sandbox restarted after the reboot, so boot recovery has something to rebuild membership"
  echo "           FROM — the pool restore and the sandbox's return are separate events either way"
else
  bad "$restart_fail sandbox(es) could not be restarted after the reboot — recovery cannot re-add an"
  echo "           address for a sandbox that no longer runs, and 'removed' is a state the plan does"
  echo "           not currently account for"
fi

say "P6 vmnet BRIDGES — same subnets, or did they move? (DF172)"
# Read AFTER the restarts on purpose: apple's bridge is created by the first container (DF178), so on
# an idle host this question has no answer at all rather than a negative one.
now_bridges=$(ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1; sub(/:$/,"",i)} /inet /{print i"="$2}' | grep '^bridge' | tr '\n' ' ')
echo "        before: $BRIDGES"
echo "        after : ${now_bridges:-<none>}"
if [ "$now_bridges" = "$BRIDGES" ]; then ok "bridges returned on the same subnets and the same indices"
elif [ -z "$now_bridges" ]; then
  unk "no bridges are up even after the restarts — nothing to compare; treat P6 as unmeasured"
else
  ok "bridges differ across reboot ($BRIDGES -> $now_bridges) — rules keyed on an interface name or"
  echo "           on a cached subnet cannot survive a boot"
fi

say "P8 ADDRESSES — did the guests come back on the same ones?"
newA=$(ipof "$A_SB"); newB=$(ipof "$B_SB")
macA=$(macof "$A_SB"); macB=$(macof "$B_SB")
echo "        A: before=$A_IP after=${newA:-<none>} | B: before=$B_IP after=${newB:-<none>}"
echo "        A mac: before=$A_MAC after=${macA:-<none>}"
echo "        B mac: before=$B_MAC after=${macB:-<none>}"
if [ -n "${T_SB:-}" ]; then
  newT=$(ipof "$T_SB"); macT=$(macof "$T_SB")
  echo "        tart $T_SB: before=${T_IP:-<none>} after=${newT:-<none>} (yoloai says '$(state "$T_SB")',"
  echo "           backend says $(live "$T_SB" && echo running || echo 'not running'))"
  echo "        tart mac: before=${T_MAC:-<none>} after=${macT:-<none>}"
  if [ -z "$newT" ]; then
    unk "the tart VM has no address after boot and could not be given one"
  elif [ "$newT" = "${T_IP:-}" ]; then
    ok "tart address preserved across reboot"
  else
    ok "tart address CHANGED across reboot ($T_IP -> $newT)"
  fi
fi
if [ -z "$newA" ] && [ -z "$newB" ]; then
  unk "neither sandbox has an address after boot (likely stopped) — restart them and re-check if"
  echo "           the address question matters to you; P1-P6 are unaffected"
elif [ "$newA" = "$A_IP" ] && [ "$newB" = "$B_IP" ]; then
  ok "each guest came back on its own former address DESPITE the reversed start order — the address"
  echo "           tracks the sandbox, not the order, and P10 below says so with the MACs"
else
  ok "addresses CHANGED across reboot (A $A_IP->${newA:-none}, B $B_IP->${newB:-none}) — membership"
  echo "           must be rebuilt from live state at boot; a saved slot->address mapping is stale"
fi
# A MAC that changes proves the address was newly LEASED rather than remembered, which is the
# mechanism behind everything above. A MAC that survives while the address moves would mean
# something else entirely, and the two are indistinguishable without this line.
# Three positional args, not a packed string: a MAC is full of colons, so any colon-delimited
# packing here splits inside the address and reports "unchanged" for every input it is given.
macline() { # name before after
  [ -n "$3" ] || return 0
  if [ "$2" = "$3" ]; then echo "        $1 mac unchanged ($3)"
  else echo "        $1 MAC REGENERATED $2 -> $3"; fi
}
macline A "$A_MAC" "$macA"
macline B "$B_MAC" "$macB"
[ -z "${T_SB:-}" ] || macline T "${T_MAC:-}" "${macT:-}"

say "P10 IS THE ADDRESS THE SANDBOX'S, OR THE START ORDER'S? — B was started FIRST"
echo "        pre-reboot order '$START_ORDER' gave A=$A_IP B=$B_IP"
echo "        post-reboot order started $B_SB first and gave A=${newA:-<none>} B=${newB:-<none>}"
if [ -z "$newA" ] || [ -z "$newB" ]; then
  unk "one or both apple guests have no address — P10 unmeasured"
elif [ "$newB" = "$A_IP" ]; then
  bad "B TOOK A'S OLD ADDRESS ($A_IP). Allocation follows start order, not sandbox identity, so a"
  echo "           saved slot->address mapping does not merely go stale — after a reboot it names the"
  echo "           WRONG SANDBOX, and restoring it would hand B the allowlist authorized for A."
  echo "           Round 2's 'addresses preserved' was the creation order reproducing the assignment."
elif [ "$newA" = "$A_IP" ] && [ "$newB" = "$B_IP" ]; then
  ok "reversing the order did NOT move either address — allocation is pinned to the sandbox somehow,"
  echo "           which the no-reboot control did not predict. Worth explaining before relying on it."
else
  ok "both addresses moved and B did not land on A's ($A_IP) — allocation is neither identity-pinned"
  echo "           nor a clean order swap; the pool simply advanced. Rebuild membership from live state"
fi

say "P11 THE ADDRESS POOLS THEMSELVES"
now64=$(leasecount 192.168.64.); now65=$(leasecount 192.168.65.)
echo "        dhcpd_leases 64.x (apple): before=$LEASES64 after=$now64"
echo "        dhcpd_leases 65.x (tart) : before=$LEASES65 after=$now65   (253 = the whole /24 is spoken for)"
if [ "$now64" -eq 0 ] 2>/dev/null; then
  ok "apple holds NO record in /var/db/dhcpd_leases — its vmnet plugin runs a separate allocator, so"
  echo "           the two backends do not share a pool and do not share its exhaustion"
else
  unk "apple has $now64 lease record(s) here; the separate-allocator reading needs revisiting"
fi
if [ "$now65" -ge 253 ] 2>/dev/null; then
  bad "tart's pool is EXHAUSTED across the reboot ($now65/253) — leases are consumed per START, so"
  echo "           every new VM now recycles an address a previous VM held. That is the precondition"
  echo "           for a stale table entry naming a live sandbox, and a reboot does not clear it."
elif [ "$now65" -lt "$LEASES65" ] 2>/dev/null; then
  ok "the lease table SHRANK across the reboot ($LEASES65 -> $now65) — exhaustion is boot-healed"
else
  ok "lease table carried across the reboot ($LEASES65 -> $now65), still below the 253 ceiling"
fi

say "P9 UNATTENDED RECOVERY AFTER A REAL REBOOT"
if [ ! -f "$SUDOERS" ]; then
  unk "no grant present; recovery untested"
else
  su "$U" -c "sudo -K" >/dev/null 2>&1
  out=$(su "$U" -c "sudo -k -n /sbin/pfctl -a $ANCHOR -f $CONF </dev/null" 2>&1); rc=$?
  after=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
  want=$((SLOTS*2))
  echo "        restore rc=$rc rules now=${after:-0} (want $want)"
  [ -n "$out" ] && printf '        %s\n' "$out"
  if [ "${after:-0}" -eq "$want" ]; then
    ok "the pool restored unattended from the pinned file after a REAL reboot"
    if [ -n "${newA:-}" ]; then
      pfctl -a "$ANCHOR" -t yb_src_0 -T add "$newA"     >/dev/null 2>&1
      pfctl -a "$ANCHOR" -t yb_dst_0 -T add "$ALLOW_A"  >/dev/null 2>&1
      r1=$(egress "$A_SB" "$ALLOW_A"); r2=$(egress "$A_SB" "$DENY")
      echo "        re-added A's CURRENT address: allow=$r1 deny=$r2  (before reboot: $PRE_ENF_A)"
      if [ "$r2" = 000 ] && [ "$r1" != 000 ]; then
        ok "enforcement restored end to end after a real reboot"
      else bad "enforcement not restored (allow=$r1 deny=$r2)"; fi
      # Second slot, second allowlist — proves the restore brought back the whole pool, not one rule.
      if [ -n "${newB:-}" ]; then
        pfctl -a "$ANCHOR" -t yb_src_1 -T add "$newB"    >/dev/null 2>&1
        pfctl -a "$ANCHOR" -t yb_dst_1 -T add "$ALLOW_B" >/dev/null 2>&1
        r3=$(egress "$B_SB" "$ALLOW_B"); r4=$(egress "$B_SB" "$DENY")
        echo "        re-added B into slot 1 with its own allowlist: allow=$r3 deny=$r4  (before reboot: $PRE_ENF_B)"
        if [ "$r4" = 000 ] && [ "$r3" != 000 ]; then
          ok "a SECOND slot with a DIFFERENT allowlist also works after the reboot"
        else bad "second slot not restored (allow=$r3 deny=$r4)"; fi
      fi
    else
      unk "A has no address; end-to-end recovery not exercised"
    fi
  else
    bad "restore did not reload the pool"
  fi
fi

say "cleanup — removing everything the pre half left"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
rm -f "$SUDOERS"; rm -rf "$CONFDIR"
echo "   $SUDOERS present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
echo "   $CONFDIR present: $([ -d "$CONFDIR" ] && echo YES-BAD || echo no)"
echo "   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
echo "   main ruleset: $(pfctl -s rules 2>/dev/null | grep -c . || true) (was $MAIN_RULES)"
chown "$U" "$RESULTS" 2>/dev/null

# Stamp the snapshot spent. This is what lets reboot_pre.sh tell "a round is mid-flight and you just
# ran the wrong half" from "the last round finished and this is a new one" — an mtime older than boot
# describes both, and refusing on that alone would block every legitimate round after the first.
printf "CONSUMED='%s'\n" "$(date '+%Y-%m-%d %H:%M:%S')" >> "$SNAP"
chown "$U" "$SNAP" 2>/dev/null

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"
