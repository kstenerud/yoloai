#!/bin/bash
# ABOUTME: M5 — install the D132 mechanism, use it, uninstall yoloAI the way a user would,
# ABOUTME: and enumerate exactly what is left behind and what it can still do.
#
# Run: sudo bash pf_uninstall_residue.sh
#
# WHY THIS EXISTS
#   D132 designs an install and no uninstall. `pfctl` has no verb that removes an anchor, and two
#   research anchors from this spike were still loaded three days after the run that made them. The
#   sudoers grant and the pinned ruleset file are ordinary root-owned files: nothing removes them
#   either, and both survive a reboot.
#
#   The residue that matters is not the anchor. It is the GRANT: a standing NOPASSWD authorization
#   to run /sbin/pfctl, left behind for a program that is no longer installed. So this does not just
#   list what remains — it tries to USE what remains, after the uninstall, which is the only way to
#   tell inert litter from a live capability.
#
#   U1 INSTALL the full mechanism: grant, pinned ruleset, loaded pool, a claimed slot.
#   U2 USE it, and prove enforcement is real before anything is removed (A22 — a residue found on a
#      host that never enforced would say nothing about a host that did).
#   U3 UNINSTALL as a user would: remove the binary and the data directory. That is the whole of
#      what a `brew uninstall` or a `rm` reaches. Nothing a user does touches /etc.
#   U4 ENUMERATE what remains, one line per artifact, with who owns it and what removes it.
#   U5 EXERCISE the residue: does the grant still authorize pfctl? Do the stale tables still hold
#      addresses? Is the anchor still there, and is there any verb that would delete it?
#
# SAFETY: installs a grant under a spike-specific filename and removes it at exit; the exit path
#   asserts /etc/sudoers.d is clean. Never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_u"
IMG=yoloai-base:latest
ALLOW=1.1.1.1; DENY=1.0.0.1
SLOTS=4; SLOT=1
SUDOERS=/etc/sudoers.d/yoloai-pf-spike-u
POOLDIR=/etc/yoloai-spike
POOLCONF="$POOLDIR/pf-pool.conf"
PASS=0; FAIL=0; UNKNOWN=0
IP=""

RESULTS="$HERE/results/pf-uninstall-residue.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
sudoers_list() {   # ls|grep trips shellcheck and mangles odd names; glob and loop instead
  local f out=""
  for f in /etc/sudoers.d/*; do [ -e "$f" ] && out="$out ${f##*/}"; done
  printf '%s' "${out# }"
}
# curl writes %{http_code} even when it fails (000) and ALSO exits non-zero. An `|| printf 000`
# fallback therefore appends a second 000, and every "is it blocked?" test silently compares
# "000000" against "000" and reads a successful block as a failure. Run 1 lost four verdicts to it.
try() { local o; o=$(asuser container exec ybu1 curl -s -o /dev/null -w '%{http_code}' \
          --max-time 5 "http://$1/" 2>/dev/null); printf '%s' "${o:-000}"; }

cleanup() {
  echo
  echo "== cleanup — removing what a real uninstall would have to remove =="
  rm -f "$SUDOERS"
  rm -rf "$POOLDIR"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser container rm -f ybu1 >/dev/null 2>&1
  echo "   sudoers.d now: [$(sudoers_list)]"
  echo "   $POOLDIR: $([ -e "$POOLDIR" ] && echo PRESENT || echo gone)"
  echo "   anchor rules: $(nrules)   main-refs: $(mainrefs)"
  echo "   NOTE the anchor NODE itself cannot be removed; see U5c. It is empty, which is the most"
  echo "        this cleanup can achieve, and that is the finding."
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "U1 INSTALL — the whole D132 mechanism, in its shipped shape"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2

mkdir -p "$POOLDIR"; chmod 755 "$POOLDIR"
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block return in quick from <yb_src_$i> to any"
  done; } > "$POOLCONF"
chown root:wheel "$POOLCONF"; chmod 644 "$POOLCONF"
note "pinned ruleset written: $POOLCONF ($(grep -c . "$POOLCONF") lines, root-owned)"

cat > "$SUDOERS" <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_u -t yb_(src|dst)_([0-9]|[12][0-9]|3[01]) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_u -f $POOLCONF\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_u -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
chmod 440 "$SUDOERS"
if visudo -c -f "$SUDOERS" >/dev/null 2>&1; then
  note "grant installed: $SUDOERS (mode $(stat -f '%Lp' "$SUDOERS"), owner $(stat -f '%Su' "$SUDOERS"))"
else
  bad "the grant did not validate; ABORTING"; exit 1
fi

asuser sudo -n /sbin/pfctl -a "$ANCHOR" -f "$POOLCONF" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
note "pool loaded through the grant: $(nrules) rules"

# ---------------------------------------------------------------------------
say "U2 USE IT — and prove enforcement was real, so the residue is a residue of something"
asuser container rm -f ybu1 >/dev/null 2>&1
asuser container run -d --name ybu1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybu1 ipv4Address)
note "guest=$IP"
[ -n "$IP" ] || { bad "no guest address; ABORTING"; exit 1; }
asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$IP"    >/dev/null 2>&1
asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1
a=$(try "$ALLOW"); d=$(try "$DENY")
note "allowed->$ALLOW=$a   denied->$DENY=$d   (need non-000 then 000)"
if [ "$a" != 000 ] && [ "$d" = 000 ]; then
  ok "U2: enforcement was live before the uninstall"
else
  bad "U2: enforcement was never live (allow=$a deny=$d); the residue below is of nothing. ABORTING"
  exit 1
fi

# ---------------------------------------------------------------------------
say "U3 UNINSTALL — everything a user's uninstall actually reaches"
note "A user removes the binary and their data directory. brew uninstall does the same. Neither"
note "knows /etc/sudoers.d or /etc/yoloai exists, and neither can unload a pf anchor."
note "Simulating exactly that: the sandbox goes away, and nothing under /etc is touched."
asuser container rm -f ybu1 >/dev/null 2>&1
note "sandbox removed; the yoloAI binary and ~/.yoloai are left in place here on purpose — this run"
note "must not delete the owner's working tree, and removing them would change nothing below."

# ---------------------------------------------------------------------------
say "U4 WHAT REMAINS — one line per artifact"
printf '        %-28s %-9s %s\n' ARTIFACT STATE "REMOVED BY"
printf '        %-28s %-9s %s\n' "--------" "-----" "----------"
printf '        %-28s %-9s %s\n' "sudoers grant" \
  "$([ -f "$SUDOERS" ] && echo PRESENT || echo gone)" "nothing — root-owned, survives reboot"
printf '        %-28s %-9s %s\n' "pinned ruleset file" \
  "$([ -f "$POOLCONF" ] && echo PRESENT || echo gone)" "nothing — root-owned, survives reboot"
printf '        %-28s %-9s %s\n' "anchor rules ($(nrules))" \
  "$([ "$(nrules)" -gt 0 ] && echo PRESENT || echo empty)" "pfctl -a <anchor> -F all (needs root)"
TBL=$(pfctl -a "$ANCHOR" -s Tables 2>/dev/null | grep -c . || true)
printf '        %-28s %-9s %s\n' "anchor tables ($TBL)" \
  "$([ "$TBL" -gt 0 ] && echo PRESENT || echo none)" "pfctl -a <anchor> -F all (needs root)"
printf '        %-28s %-9s %s\n' "the anchor node itself" "PRESENT" "no pfctl verb exists — see U5c"
note ""
note "addresses still held in the tables after the uninstall:"
pfctl -a "$ANCHOR" -s Tables 2>/dev/null | while read -r t; do
  n=$(pfctl -a "$ANCHOR" -t "$t" -T show 2>/dev/null | grep -c . || true)
  [ "$n" -gt 0 ] && printf '          %-12s %s address(es): %s\n' "$t" "$n" \
      "$(pfctl -a "$ANCHOR" -t "$t" -T show 2>/dev/null | tr -d ' ' | tr '\n' ' ')"
done
note "(the guest's address is among them — it outlived the sandbox it identified)"

# ---------------------------------------------------------------------------
say "U5 EXERCISE THE RESIDUE — inert litter, or a live capability?"

note "U5a  does the grant still authorize pfctl, with yoloAI gone?"
if asuser sudo -n /sbin/pfctl -s info >/dev/null 2>&1; then
  bad "U5a: YES. A standing NOPASSWD grant on /sbin/pfctl outlives the program it was for."
  note "     This is the residue that matters. It is not litter — it is authority, and the user has"
  note "     no way to know it is there. An uninstall that does not remove it leaves the security"
  note "     boundary widened forever."
else
  ok "U5a: the grant no longer authorizes anything"
fi

note ""
note "U5b  do the stale tables still hold the dead sandbox's address?"
STALE=$(pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T show 2>/dev/null | tr -d ' ' | grep -c . || true)
if [ "$STALE" -gt 0 ]; then
  note "     yes: $(pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T show 2>/dev/null | tr -d ' ' | tr '\n' ' ')"
  note "     With the rules still loaded this is D3 in its resting state: whoever next receives that"
  note "     address inherits the allowlist. That is what rule 1 exists for — but rule 1 runs at"
  note "     ACQUISITION, and after an uninstall there is no next acquisition to run it."
  ok "U5b: stale membership survives the uninstall, recorded"
else
  ok "U5b: no stale membership remained"
fi

note ""
note "U5c  is there ANY pfctl verb that removes an anchor?"
note "     -F all empties it; the node remains. Trying the plausible spellings so the negative names"
note "     what was attempted rather than asserting an absence."
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
note "     after -F all: rules=$(nrules) tables=$(pfctl -a "$ANCHOR" -s Tables 2>/dev/null | grep -c . || true)"
verb() {   # run pfctl with the given argv and report its first line
  local out; out=$(pfctl "$@" 2>&1 | head -1 | tr -d '\n')
  printf '%s' "${out:-<no output, no effect>}"
}
printf '          pfctl %-24s -> %s\n' "-a <anchor> -F anchors" "$(verb -a "$ANCHOR" -F anchors)"
printf '          pfctl %-24s -> %s\n' "-a <anchor> -X"         "$(verb -a "$ANCHOR" -X)"
printf '          pfctl %-24s -> %s\n' "-a <anchor> -R"         "$(verb -a "$ANCHOR" -R)"
LISTED=$(pfctl -a com.apple -s Anchors 2>/dev/null | grep -c "$(basename "$ANCHOR")" || true)
note "     still listed under com.apple: $([ "$LISTED" -gt 0 ] && echo YES || echo no)"
if [ "$LISTED" -gt 0 ]; then
  ok "U5c: confirmed — the anchor can be emptied but not removed; only a reboot clears the node"
else
  note "     the node is gone after flushing; record which step did it"
fi

# ---------------------------------------------------------------------------
say "U6 WHAT AN UNINSTALL WOULD HAVE TO DO"
cat <<EOF
        1. rm /etc/sudoers.d/<grant>              root. The one that matters — it is authority.
        2. rm -rf /etc/yoloai/                    root. The pinned ruleset the grant points at.
        3. pfctl -a <anchor> -F all               root. Empties rules and tables. Note the grant
                                                  does NOT permit -F, so this needs a real sudo
                                                  prompt — the uninstall cannot use the grant it
                                                  is deleting.
        4. the anchor node                        cannot be removed; cleared at reboot.
        Order matters: 3 before 1, or the last step loses the privilege it needs. And every step is
        root, so an uninstall is an interactive, privileged operation — which is the opposite of the
        install, whose whole design goal was to need no prompt at runtime.
EOF

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - A reboot. pf-reboot.txt already measured that the loaded ruleset is rebuilt from
          /etc/pf.conf at boot, so the anchor node does not survive; that was NOT re-run here, and
          the sudoers file and pinned ruleset plainly do survive since they are ordinary files.
        - A real `brew uninstall`. The removal was simulated by reasoning about what it reaches
          (the binary and the data dir); the formula was not executed, because this host's yoloAI is
          the working tree.
        - Removing ~/.yoloai. Deliberately not done — it is the owner's data. Nothing in the residue
          list lives there, so it changes no result.
        - Whether a second install re-uses a stale slot. That is the reaping design's own question
          (rule 1), not the uninstall's.
EOF
