#!/bin/bash
# ABOUTME: Half one of the only test a reboot can settle — sets up full pf enforcement, verifies
# ABOUTME: it works, and snapshots every fact the post-reboot half needs to compare against.
#
# Run: sudo bash reboot_pre.sh <apple-A> <apple-B> [tart-C]
#   then REBOOT, then: sudo bash reboot_post.sh
#
# WHY THIS EXISTS
#   The plan asserts "pf anchor contents are in-kernel state and do not survive a reboot". Every
#   result supporting it emptied the anchor BY HAND. That premise carries a sudoers row, a
#   root-owned /etc file, and the mandatory start-time verification — it is the largest unmeasured
#   thing left, and only an actual reboot settles it.
#
#   While the machine is going down anyway, this captures everything else a reboot can answer that
#   nothing else can:
#     P1 do the anchor's RULES survive?
#     P2 do the TABLES (contents) survive?
#     P3 is pf still ENABLED? (/etc/pf.conf: not auto-enabled, reference counted — if nothing
#        re-enables it, every rule yoloAI loads is inert and the design needs a -E story)
#     P4 does the main ruleset come back identical?
#     P5 do the pinned root-owned file and the sudoers grant survive? (expected yes; they are files)
#     P6 do the vmnet bridges come back on the SAME subnets? (DF172 territory)
#     P7 do sandboxes survive, and in what state?
#     P8 do sandbox ADDRESSES change across a reboot? If they do, membership must be rebuilt from
#        live state at boot, not restored from a saved mapping.
#     P9 does unattended restore work after a REAL reboot, not a simulated one?
#     P10 is an address determined by START ORDER rather than by sandbox identity? A no-reboot
#        control established that both backends move every guest's address on a plain stop/start
#        with a fresh MAC, so P8 cannot distinguish "the reboot preserved it" from "we restarted
#        them in the same order and the allocator counts from the same place". The post half starts
#        B FIRST for exactly this reason; if B takes A's old address, a saved slot->address mapping
#        is not merely stale, it points at the wrong sandbox.
#
# WHAT THIS LEAVES ON THE MACHINE ACROSS THE REBOOT — deliberately, because the point is to see
# whether it survives:
#     /etc/sudoers.d/yoloai-reboot-probe    NOPASSWD, narrow: table membership on ONE anchor,
#                                           restore from one pinned path, and two read-only queries.
#     /etc/yoloai/pf-pool.conf              root-owned ruleset.
#     pf anchor com.apple/yoloai_rb         rules + table membership.
#   reboot_post.sh removes all three. IF YOU NEVER RUN IT, remove them by hand:
#     sudo rm -f /etc/sudoers.d/yoloai-reboot-probe && sudo rm -rf /etc/yoloai \
#       && sudo pfctl -a com.apple/yoloai_rb -F all
#   The main ruleset is never loaded or flushed, and pf enable state is never touched.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SNAP="$HERE/results/reboot-snapshot.txt"
RESULTS="$HERE/results/reboot-pre.txt"

# Refuse to run when the obvious mistake has been made: an UNCONSUMED snapshot written before the
# machine's current boot means the reboot has already happened and the half you want is the other
# one. This must come BEFORE the tee below, because the tee is the damage — it truncates the log in
# place, and round 3's pre-half log was destroyed exactly this way by a paste of the wrong filename.
# Nothing else noticed: the run then died at the IP gate, whose message ("could not resolve both
# sandbox IPs") describes a symptom of the stopped guests and names neither the reboot nor the log.
#
# It also comes before the root check, for two reasons. It needs no privilege — it reads a file mtime
# and a sysctl — so putting it first means the wrong-half mistake is caught BEFORE the password
# prompt rather than after it. And it makes the refusal testable without sudo, which matters because
# the first attempt to verify this guard could not exercise the refusing path at all: the only way to
# reach it was a real privileged run, so "confirm it refuses" and "run the thing it must refuse" were
# the same command.
#
# Consumption is what makes this safe to gate on. A snapshot older than boot is the NORMAL state at
# the start of every round after the first, so mtime alone would refuse every legitimate new round;
# reboot_post.sh stamps CONSUMED into the file when it finishes reading it, and only an unstamped
# one is still waiting for its other half.
if [ -f "$SNAP" ] && ! grep -q '^CONSUMED=' "$SNAP" 2>/dev/null; then
  snap_written=$(stat -f %m "$SNAP" 2>/dev/null || echo 0)
  # Anchored at ^{ deliberately: kern.boottime reads "{ sec = N, usec = M }", and an unanchored
  # ".*sec = " matches greedily through the "usec = " and captures the MICROseconds. That parse
  # yields a five-digit epoch, every comparison against it is false, and the guard becomes a silent
  # no-op that looks like it is working. Caught here only by printing the value.
  booted=$(sysctl -n kern.boottime 2>/dev/null | sed -n 's/^{ sec = \([0-9][0-9]*\).*/\1/p')
  if [ -n "$booted" ] && [ "$booted" -gt "$snap_written" ] 2>/dev/null; then
    echo "REFUSING: the machine booted $(date -r "$booted" '+%Y-%m-%d %H:%M:%S'), AFTER the unconsumed"
    echo "snapshot at $SNAP was written $(date -r "$snap_written" '+%Y-%m-%d %H:%M:%S')."
    echo "The reboot this test is waiting on has already happened — you want:"
    echo "    sudo bash $HERE/reboot_post.sh"
    echo
    echo "Running the pre half here would truncate $RESULTS and destroy this round's log."
    echo "If you really do mean to start a NEW round, delete the snapshot first."
    exit 2
  fi
fi

[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"; B_SB="${2:?need sandbox B}"; T_SB="${3:-}"   # 3rd optional: a TART sandbox

ANCHOR="com.apple/yoloai_rb"
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-pool.conf"
SUDOERS=/etc/sudoers.d/yoloai-reboot-probe
SLOTS=8
ALLOW_A=1.1.1.1; ALLOW_B=1.0.0.2; DENY=1.0.0.1
ROOT="$(cd "$HERE/../../../../.." && pwd)"
mkdir -p "$(dirname "$RESULTS")"

exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
# Backend-aware: apple answers through `container`, tart through `tart`. Derived from `yoloai ls`
# so a mis-ordered argument fails loudly rather than measuring one guest twice.
bk() { asuser "$ROOT/yoloai" ls 2>/dev/null | awk -v n="$1" '$1==n {print $3}'; }
# Liveness is asked of the BACKEND, never of yoloAI. Both halves need this and they must agree, so
# the definition is duplicated verbatim rather than approximated: `yoloai ls` reports a running tart
# VM whose agent is not attached as `idle`, so gating on yoloAI's status refuses to read a guest that
# is up and addressable — which is how round 2 rendered UNKNOWN for the tart leg on both sides of its
# own comparison. A gate is needed at all because a dhcpd_leases record outlives the VM that held it,
# and round 1 read a stopped guest's surviving lease as an address "preserved across the reboot".
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
# tart persists its MAC in the VM's own config.json; apple assigns one at start and reports it only
# while the container runs, so a stopped apple guest has no MAC to read. Captured because an address
# that moves with a new MAC is a lease allocation, and an address that moves under a stable MAC is
# something else entirely — without this the post half can only say "it changed".
macof() {
  case "$(bk "$1")" in
    tart)  asuser python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("macAddress",""))' \
             "/Users/$U/.tart/vms/yoloai-cli-$1/config.json" 2>/dev/null ;;
    apple) asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"]["networks"][0].get("macAddress",""))' 2>/dev/null ;;
    *) printf '' ;;
  esac
}
# The vmnet DHCP pool tart draws from is finite, shared, and consumed one lease per START rather than
# one per VM. Counting it makes exhaustion and wrap-around visible as a number instead of as a
# surprising address. apple does not appear in this file at all — its vmnet plugin runs a separate
# allocator — so a zero for 64.x is the expected reading, not a broken probe.
leasecount() { # $1 = subnet prefix, e.g. 192.168.65.
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
ok()  { printf '   PASS    %s\n' "$*"; }
bad() { printf '   FAIL    %s\n' "$*"; }

A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve both sandbox IPs (A=$A_IP B=$B_IP)"; exit 2; }
T_IP=""
if [ -n "$T_SB" ]; then
  [ "$(bk "$T_SB")" = tart ] || { echo "$T_SB is backend '$(bk "$T_SB")', expected tart"; exit 2; }
  T_IP=$(ipof "$T_SB"); [ -n "$T_IP" ] || { echo "could not resolve tart sandbox $T_SB"; exit 2; }
fi

echo "=== reboot test, PRE half — $(date '+%Y-%m-%d %H:%M:%S') ==="
echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP)"

echo
echo "== baseline =="
b1=$(egress "$A_SB" $ALLOW_A); b2=$(egress "$A_SB" $DENY)
echo "        A: allow=$b1 deny=$b2"
if [ "$b1" = 000 ] || [ "$b2" = 000 ]; then bad "baseline incomplete; ABORTING"; exit 1; fi
ok "baseline reachable"

echo
echo "== install the full arrangement =="
install -d -m 0755 -o root -g wheel "$CONFDIR"
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done; } > "$CONF"
chown root:wheel "$CONF"; chmod 0644 "$CONF"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
pfctl -a "$ANCHOR" -f "$CONF" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
pfctl -a "$ANCHOR" -t yb_src_0 -T add "$A_IP"    >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_0 -T add "$ALLOW_A" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1 -T add "$B_IP"    >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_1 -T add "$ALLOW_B" >/dev/null 2>&1
if [ -n "$T_IP" ]; then
  pfctl -a "$ANCHOR" -t yb_src_2 -T add "$T_IP"    >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t yb_dst_2 -T add "$ALLOW_A" >/dev/null 2>&1
fi
cat > /tmp/rbp.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_rb -t yb_(src|dst)_[0-7] -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_rb -f /etc/yoloai/pf-pool\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_rb -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
if visudo -c -f /tmp/rbp.sudoers >/dev/null 2>&1; then
  install -m 0440 -o root -g wheel /tmp/rbp.sudoers "$SUDOERS"; rm -f /tmp/rbp.sudoers
  ok "sudoers grant installed"
else
  bad "sudoers candidate failed visudo -c; ABORTING"; exit 1
fi

echo
echo "== verify enforcement works BEFORE the reboot (or the after-comparison is meaningless) =="
e1=$(egress "$A_SB" $ALLOW_A); e2=$(egress "$A_SB" $DENY); e3=$(egress "$B_SB" $ALLOW_B); e4=$(egress "$B_SB" $DENY)
echo "        A: allow=$e1 deny=$e2 | B: allow=$e3 deny=$e4"
tok=0
if [ -n "$T_SB" ]; then
  t1=$(egress "$T_SB" $ALLOW_A); t2=$(egress "$T_SB" $DENY)
  echo "        tart $T_SB: allow=$t1 deny=$t2"
  { [ "$t2" = 000 ] && [ "$t1" != 000 ]; } && tok=1
fi
if [ "$e2" = 000 ] && [ "$e1" != 000 ] && [ "$e4" = 000 ] && [ "$e3" != 000 ] && { [ -z "$T_SB" ] || [ "$tok" = 1 ]; }; then
  ok "enforcement live for every sandbox given"
else
  bad "enforcement NOT live before reboot (A:$e1/$e2 B:$e3/$e4) — fix before rebooting"; exit 1
fi

echo
echo "== snapshot =="
# Every value is single-quoted. The first run of this test emitted them bare, and the four carrying
# spaces — PRE_DATE, PF_STATUS, BRIDGES, SANDBOXES — did not survive the post half sourcing the
# file: two errored, and BRIDGES truncated to its first token in silence. A comparison whose
# *control* is empty still renders a verdict, which is the A28 shape wearing a different hat.
snap() { printf "%s='%s'\n" "$1" "${2//\'/\'\\\'\'}"; }
{
  echo "# reboot-snapshot — written by reboot_pre.sh, read by reboot_post.sh. Do not edit."
  snap PRE_DATE     "$(date '+%Y-%m-%d %H:%M:%S')"
  snap ANCHOR       "$ANCHOR"
  snap SLOTS        "$SLOTS"
  snap A_SB         "$A_SB"
  snap B_SB         "$B_SB"
  snap A_IP         "$A_IP"
  snap B_IP         "$B_IP"
  snap T_SB         "$T_SB"
  snap T_IP         "$T_IP"
  snap A_MAC        "$(macof "$A_SB")"
  snap B_MAC        "$(macof "$B_SB")"
  snap T_MAC        "${T_SB:+$(macof "$T_SB")}"
  # The order the guests were started in, recorded because it is the variable the post half changes.
  # Round 2 restarted them in this same order and read the addresses coming back unchanged as the
  # guests having KEPT them; both allocators hand out in start order, so that verdict was ordering
  # and not preservation. The post half now starts B first, which makes the two explanations differ.
  snap START_ORDER  "$A_SB $B_SB${T_SB:+ $T_SB}"
  snap LEASES64     "$(leasecount 192.168.64.)"
  snap LEASES65     "$(leasecount 192.168.65.)"
  snap SRC2         "$(pfctl -a "$ANCHOR" -t yb_src_2 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')"
  snap ALLOW_A      "$ALLOW_A"
  snap ALLOW_B      "$ALLOW_B"
  snap DENY         "$DENY"
  # The pre-reboot enforcement measurements, recorded because the post half's "restored" verdicts are
  # claims about a CHANGE and were being compared against a number that existed only in this script's
  # stdout. When round 3's log was clobbered the digits went with it. What survived was the stronger
  # half of the evidence — the gate above exits non-zero unless every sandbox enforces, so a snapshot
  # existing at all proves enforcement was live — but "reconstruct it from the control flow of the
  # script that did not write it down" is not a thing a reader should have to do.
  snap PRE_ENF_A    "allow=$e1 deny=$e2"
  snap PRE_ENF_B    "allow=$e3 deny=$e4"
  snap PRE_ENF_T    "${T_SB:+allow=$t1 deny=$t2}"
  snap ANCHOR_RULES "$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  snap SRC0         "$(pfctl -a "$ANCHOR" -t yb_src_0 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')"
  snap SRC1         "$(pfctl -a "$ANCHOR" -t yb_src_1 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')"
  snap MAIN_RULES   "$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  snap MAIN_SHA     "$(pfctl -s rules 2>/dev/null | shasum | cut -d' ' -f1)"
  snap PF_STATUS    "$(pfctl -s info 2>/dev/null | head -1 | tr -s ' ')"
  snap PF_TOKENS    "$(pfctl -s References 2>/dev/null | grep -c . || true)"
  snap BRIDGES      "$(ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1; sub(/:$/,"",i)} /inet /{print i"="$2}' | grep '^bridge' | tr '\n' ' ')"
  snap CONF_SHA     "$(shasum "$CONF" | cut -d' ' -f1)"
  snap SANDBOXES    "$(asuser "$ROOT/yoloai" ls 2>/dev/null | tail -n +2 | awk '{print $1":"$2}' | tr '\n' ' ')"
} > "$SNAP"
chown "$U" "$SNAP"
sed 's/^/        /' "$SNAP"

echo
echo "==================================================================="
echo " NOW REBOOT, then run:  sudo bash $HERE/reboot_post.sh"
echo
echo " Left on the machine deliberately (post half removes them):"
echo "   $SUDOERS"
echo "   $CONF"
echo "   pf anchor $ANCHOR"
echo " If you never run the post half, remove by hand:"
echo "   sudo rm -f $SUDOERS && sudo rm -rf $CONFDIR && sudo pfctl -a $ANCHOR -F all"
echo "==================================================================="
chown "$U" "$RESULTS" 2>/dev/null
