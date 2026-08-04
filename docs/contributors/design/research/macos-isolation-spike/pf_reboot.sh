#!/bin/bash
# ABOUTME: Can yoloAI restore its pf parent anchor unattended after a reboot, without granting
# ABOUTME: the parent write that the containment model forbids? And can it see pf's enable state?
#
# Run: sudo bash pf_reboot.sh <sandbox-A>
#
# WHY THIS EXISTS
#   pf anchor contents are in-kernel state. After a reboot com.apple/yoloai is empty, and a
#   per-sandbox load into it still SUCCEEDS while never evaluating (measured, pf-shapea.txt S5) —
#   silent fail-open. Restoring the parent means WRITING the parent, which the grant refuses on
#   purpose: permitting `-a com.apple/yoloai -f -` would let its holder install `nat-anchor "*"`
#   and re-enable translation in every sub-anchor.
#
#   R1/R2 test the way out: keep the content in a ROOT-OWNED file at a path pinned exactly in the
#   sudoers regex. The old objection to `-f <path>` — whoever writes the file controls the rules —
#   dissolves when the file and its directory are root-owned and the path is literal. yoloAI can
#   then restore the parent unattended and cannot alter what gets restored.
#
#   R3 is the property that decides whether recovery is cheap: sub-anchor rules survive in the
#   kernel; only the parent's dispatch line is missing. If restoring the parent alone revives them,
#   recovery is one command and does not require restarting every running sandbox.
#
#   R4 covers the adjacent gap. /etc/pf.conf: pf "will not be automatically enabled … each
#   component which utilizes PF is responsible for enabling and disabling PF via -E and -X", and it
#   is reference counted. If nothing has enabled pf, every rule we load is inert — the same silent
#   fail-open from a different cause — and the current grant cannot even see it, because enable
#   state is reported by `-s info`, which is not granted.
#
# DELIBERATELY NOT TESTED: `pfctl -E` / `-X`. Holding our own enable reference is a real design
# question, but getting -X wrong drops the count and would break vmnet NAT for every VM on this
# host. Read-only detection only; the reference question is recorded as open.
#
# WHAT IT TOUCHES, AND HOW IT UNDOES IT
#   com.apple/yoloai_s and its sub-anchors; /etc/yoloai (created, removed); a sudoers.d probe file
#   (validated with visudo -c before install, removed after). The main ruleset is never loaded or
#   flushed, and pf enable/disable state is never changed.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <sandbox-A>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"

PARENT="com.apple/yoloai_s"
CONFDIR=/etc/yoloai
CONF="$CONFDIR/pf-parent.conf"
DECOY="$CONFDIR/evil.conf"
SUDOERS=/etc/sudoers.d/yoloai-reboot-probe
DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-reboot.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

PF_BEFORE=$(pfctl -s info 2>/dev/null | head -1)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$PARENT/s1" -F all >/dev/null 2>&1
  pfctl -a "$PARENT" -F all >/dev/null 2>&1
  rm -f "$SUDOERS"; rm -rf "$CONFDIR"
  echo "   parent rules=$(pfctl -a "$PARENT" -s rules 2>/dev/null | grep -c . || true)"
  echo "   $CONFDIR present: $([ -d "$CONFDIR" ] && echo YES-BAD || echo no)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   pf before: $PF_BEFORE"
  echo "   pf after : $(pfctl -s info 2>/dev/null | head -1)"
  echo "   A egress restored: $(egress "$A_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}
A_IP=$(ipof "$A_SB"); [ -n "$A_IP" ] || { echo "could not resolve $A_SB"; exit 2; }
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) | pf: $PF_BEFORE"

# ---------------------------------------------------------------------------
say "R0 BASELINE + a working parent/sub-anchor pair"
a0=$(egress "$A_SB" $DENY)
[ "$a0" != "000" ] || { bad "baseline egress already broken; ABORTING"; exit 1; }
ok "baseline egress works ($a0)"
install -d -m 0755 -o root -g wheel "$CONFDIR"
printf 'anchor "*"\n' > "$CONF"; chown root:wheel "$CONF"; chmod 0644 "$CONF"
printf 'nat-anchor "*"\nanchor "*"\n' > "$DECOY"; chown root:wheel "$DECOY"; chmod 0644 "$DECOY"
echo "        $CONFDIR: $(stat -f '%Sp %Su:%Sg' "$CONFDIR")"
echo "        $CONF: $(stat -f '%Sp %Su:%Sg' "$CONF")"
if asuser test -w "$CONF" || asuser test -w "$CONFDIR"; then
  bad "the invoking user can write the parent file or its directory — the pinned-path grant is unsafe"
else
  ok "parent file and directory are root-owned and NOT user-writable"
fi
pfctl -a "$PARENT" -f "$CONF" 2>&1 | quiet_pf | sed 's/^/        parent: /'
printf 'block drop in quick from %s to any\n' "$A_IP" | pfctl -a "$PARENT/s1" -f - 2>&1 | quiet_pf | sed 's/^/        sub: /'
a1=$(egress "$A_SB" $DENY)
echo "        parent loaded from file, sub-anchor loaded: A->deny=$a1"
if [ "$a1" = "000" ]; then
  ok "enforcement works with the parent loaded FROM THE FILE (not stdin)"
else
  bad "no enforcement even with a good parent; nothing below is meaningful"; exit 1
fi

# ---------------------------------------------------------------------------
say "R3 REBOOT SIMULATION — does restoring the parent alone revive live sandboxes?"
# A reboot empties the parent but the per-sandbox rules are reloaded by yoloAI at next start.
# The interesting case is a sandbox already running: if restoring the parent alone revives it,
# recovery is one command rather than a restart of everything.
pfctl -a "$PARENT" -F all >/dev/null 2>&1
sub_still=$(pfctl -a "$PARENT/s1" -s rules 2>/dev/null | grep -c . || true)
a2=$(egress "$A_SB" $DENY)
echo "        parent emptied: sub-anchor still holds ${sub_still:-0} rule(s), A->deny=$a2"
if [ "$a2" != "000" ]; then
  ok "confirms the S5 fail-open: rules present, parent gone, sandbox unfiltered"
else
  unk "sandbox still filtered with an empty parent — unexpected; S5 said otherwise"
fi
pfctl -a "$PARENT" -f "$CONF" 2>&1 | quiet_pf >/dev/null
a3=$(egress "$A_SB" $DENY)
echo "        parent restored from file: A->deny=$a3"
if [ "$a3" = "000" ]; then
  ok "restoring the parent ALONE revives a running sandbox — recovery needs no restart"
else
  bad "restoring the parent did not revive it; recovery would require reloading every sandbox"
fi

# ---------------------------------------------------------------------------
say "R4 CAN WE SEE pf's ENABLE STATE? (read-only; -E/-X deliberately untested)"
echo "        pfctl -s info head: $(pfctl -s info 2>/dev/null | head -1)"
refs=$(pfctl -s References 2>/dev/null | head -3)
echo "        pfctl -s References: ${refs:-<unsupported or empty>}"
if pfctl -s info 2>/dev/null | grep -qiE '^Status: (Enabled|Disabled)'; then
  ok "'-s info' reports enable state, so a grant row for it makes the state observable"
else
  unk "could not parse enable state from -s info"
fi

# ---------------------------------------------------------------------------
say "R1/R2 THE PINNED-PATH GRANT — does it permit restore and refuse everything else?"
cat > /tmp/pfrb.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s -f /etc/yoloai/pf-parent\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s -s (Anchors|rules)\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s/[A-Za-z0-9][A-Za-z0-9._-]* -f -\$
EOF
echo "        candidate policy:"; sed 's/^/          /' /tmp/pfrb.sudoers
if ! visudo -c -f /tmp/pfrb.sudoers >/dev/null 2>&1; then
  bad "policy fails visudo -c"; visudo -c -f /tmp/pfrb.sudoers 2>&1 | sed 's/^/        /'
else
  install -m 0440 -o root -g wheel /tmp/pfrb.sudoers "$SUDOERS"
  ok "policy validated and installed"
  su "$U" -c "sudo -K" >/dev/null 2>&1
  matrix=$(cat <<'MATRIX'
restore parent from pinned file|permit|/sbin/pfctl -a com.apple/yoloai_s -f /etc/yoloai/pf-parent.conf
read pf enable state|permit|/sbin/pfctl -s info
enumerate sub-anchors|permit|/sbin/pfctl -a com.apple/yoloai_s -s Anchors
load a sandbox sub-anchor|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -f -
ESCAPE parent from a DIFFERENT file same dir|refuse|/sbin/pfctl -a com.apple/yoloai_s -f /etc/yoloai/evil.conf
ESCAPE parent from a user-writable path|refuse|/sbin/pfctl -a com.apple/yoloai_s -f /tmp/pfrb.sudoers
ESCAPE parent from stdin|refuse|/sbin/pfctl -a com.apple/yoloai_s -f -
ESCAPE pinned file into ANOTHER anchor|refuse|/sbin/pfctl -a com.apple/other -f /etc/yoloai/pf-parent.conf
ESCAPE main ruleset from pinned file|refuse|/sbin/pfctl -f /etc/yoloai/pf-parent.conf
ESCAPE disable pf|refuse|/sbin/pfctl -d
ESCAPE flush all|refuse|/sbin/pfctl -F all
MATRIX
)
  while IFS='|' read -r label expect cmdline; do
    [ -n "$label" ] || continue
    if su "$U" -c "sudo -k -n $cmdline </dev/null" >/dev/null 2>&1; then got=permit; else got=refuse; fi
    if [ "$got" = "$expect" ]; then ok "$label -> $got"; else bad "$label -> $got, expected $expect"; fi
  done <<< "$matrix"
  if su "$U" -c "sudo -k -n /usr/bin/true </dev/null" >/dev/null 2>&1; then
    bad "CONTROL: unlisted command permitted — matrix invalid"
  else
    ok "CONTROL: unlisted command refused — refusals reflect policy, not cache"
  fi
  # End to end, as the user, with no tty: empty the parent (the post-reboot state) and recover.
  pfctl -a "$PARENT" -F all >/dev/null 2>&1
  pre=$(egress "$A_SB" $DENY)
  su "$U" -c "sudo -k -n /sbin/pfctl -a com.apple/yoloai_s -f /etc/yoloai/pf-parent.conf </dev/null" >/dev/null 2>&1
  post=$(egress "$A_SB" $DENY)
  echo "        unattended recovery: before=$pre after=$post"
  if [ "$pre" != "000" ] && [ "$post" = "000" ]; then
    ok "UNATTENDED REBOOT RECOVERY WORKS — no password, no parent-write grant, content fixed"
  else
    bad "unattended recovery failed (before=$pre after=$post)"
  fi
fi
rm -f /tmp/pfrb.sudoers

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"
