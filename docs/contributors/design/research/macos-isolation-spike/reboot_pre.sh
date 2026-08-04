#!/bin/bash
# ABOUTME: Half one of the only test a reboot can settle — sets up full pf enforcement, verifies
# ABOUTME: it works, and snapshots every fact the post-reboot half needs to compare against.
#
# Run: sudo bash reboot_pre.sh <sandbox-A> <sandbox-B>
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
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"; B_SB="${2:?need sandbox B}"

ANCHOR="com.apple/yoloai_rb"
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-pool.conf"
SUDOERS=/etc/sudoers.d/yoloai-reboot-probe
SLOTS=8
ALLOW_A=1.1.1.1; ALLOW_B=1.0.0.2; DENY=1.0.0.1
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../../../.." && pwd)"
SNAP="$HERE/results/reboot-snapshot.txt"
RESULTS="$HERE/results/reboot-pre.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() { local c; c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"; }
ok()  { printf '   PASS    %s\n' "$*"; }
bad() { printf '   FAIL    %s\n' "$*"; }

A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve both sandbox IPs (A=$A_IP B=$B_IP)"; exit 2; }

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
if [ "$e2" = 000 ] && [ "$e1" != 000 ] && [ "$e4" = 000 ] && [ "$e3" != 000 ]; then
  ok "enforcement live for both sandboxes"
else
  bad "enforcement NOT live before reboot (A:$e1/$e2 B:$e3/$e4) — fix before rebooting"; exit 1
fi

echo
echo "== snapshot =="
{
  echo "# reboot-snapshot — written by reboot_pre.sh, read by reboot_post.sh. Do not edit."
  echo "PRE_DATE=$(date '+%Y-%m-%d %H:%M:%S')"
  echo "ANCHOR=$ANCHOR"
  echo "SLOTS=$SLOTS"
  echo "A_SB=$A_SB"
  echo "B_SB=$B_SB"
  echo "A_IP=$A_IP"
  echo "B_IP=$B_IP"
  echo "ALLOW_A=$ALLOW_A"
  echo "ALLOW_B=$ALLOW_B"
  echo "DENY=$DENY"
  echo "ANCHOR_RULES=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  echo "SRC0=$(pfctl -a "$ANCHOR" -t yb_src_0 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')"
  echo "SRC1=$(pfctl -a "$ANCHOR" -t yb_src_1 -T show 2>/dev/null | tr -d ' ' | tr '\n' ',')"
  echo "MAIN_RULES=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "MAIN_SHA=$(pfctl -s rules 2>/dev/null | shasum | cut -d' ' -f1)"
  echo "PF_STATUS=$(pfctl -s info 2>/dev/null | head -1 | tr -s ' ')"
  echo "PF_TOKENS=$(pfctl -s References 2>/dev/null | grep -c . || true)"
  echo "BRIDGES=$(ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1; sub(/:$/,"",i)} /inet /{print i"="$2}' | grep '^bridge' | tr '\n' ' ')"
  echo "CONF_SHA=$(shasum "$CONF" | cut -d' ' -f1)"
  echo "SANDBOXES=$(asuser "$ROOT/yoloai" ls 2>/dev/null | tail -n +2 | awk '{print $1":"$2}' | tr '\n' ' ')"
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
