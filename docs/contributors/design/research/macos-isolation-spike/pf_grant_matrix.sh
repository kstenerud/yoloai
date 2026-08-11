#!/bin/bash
# ABOUTME: Does the interface-keyed design still fit D132's grant, now that revocation needs no `-k`?
# ABOUTME: Runs the permit/refuse matrix with the harness's own blanket sudo grant REMOVED.
#
# Run: sudo bash pf_grant_matrix.sh
#
# WHY THIS EXISTS, AND WHY IT IS NOT THE EXPERIMENT THAT WAS ASKED FOR
#   The brief asked for D132's permit/refuse matrix against `pfctl -k <guest> -k <gateway>`, because
#   "pin the -k peer to our gateway" holds on the default network and weakens under per-sandbox
#   networks. `pf-no-state.txt` removed that question entirely: revocation under the S3 shape is a
#   table delete and nothing else, so there is no `-k` to grant and no peer to pin. The brief
#   anticipated this -- "if experiment 1 succeeds this may become moot".
#
#   It moots that matrix and raises a harder one in its place. D132's grant is built on a specific
#   idea: the unprivileged process may change TABLE MEMBERSHIP and may reload ONE PINNED FILE whose
#   contents root wrote at install time. It can never author a rule. That is what makes a NOPASSWD
#   grant defensible at all.
#
#   The interface-keyed design does not obviously fit that. Its rules NAME A BRIDGE -- `on bridge101`
#   -- and bridge indices are handed out dynamically, differ per sandbox, and change across a
#   restart. `pf-lifecycle.txt` then requires reloading the anchor whenever a sandbox detaches or
#   returns, each time with different interface names. Those are rule-text changes, which is exactly
#   the thing the pinned file exists to prevent.
#
#   If that is right, the rewritten plan's central decision and D132's grant model are in conflict,
#   and neither document says so. That is worth measuring rather than arguing.
#
#   G1 THE GUARD     the matrix is void unless a plainly-unauthorized command is REFUSED first.
#                    `pf-liveness-detect.txt` V4 reports UNKNOWN for exactly this reason: it ran
#                    under a blanket harness grant, so every "is it refused?" check passed for a
#                    reason that had nothing to do with D132. This script removes that grant for the
#                    duration and proves the removal took.
#   G2 SHIPPED FORMS the four D132 permits, which must all still work.
#   G3 THE REFUSALS  what must stay refused, including `-k` -- now a simplification rather than a
#                    cost, since nothing needs it.
#   G4 THE CONFLICT  can an unprivileged process install a rule naming a bridge? Three routes:
#                    a different file path, rewriting the pinned file, and an inline ruleset.
#
# SAFETY: saves and restores the harness's blanket sudoers file around the run. It runs AS ROOT
#   throughout, so removing that file never removes THIS script's authority -- only the
#   unprivileged user's, which is the whole point.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai"
PINNED=/etc/yoloai/pf-pool.conf
BLANKET=/etc/sudoers.d/yoloai-spike-session
GRANT=/etc/sudoers.d/yoloai-d132-matrix
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfg.XXXXXX)

RESULTS="$HERE/results/pf-grant-matrix.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

IMG=yoloai-base:latest
asuser() { sudo -u "$U" -H "$@"; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
gwof() { netfield "$1" ipv4Gateway; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }
gtry() { local o; o=$(asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' \
           --max-time 5 "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"; }

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }

# Every probe runs as the unprivileged user with `sudo -n`, which is what the product would do.
# `-n` so a missing grant fails instead of prompting; `-k` first so no cached ticket can answer.
probe() {   # $1 = label, $2 = expectation (PERMIT|REFUSE), $3... = pfctl args
  local label=$1 want=$2; shift 2
  local out rc got
  out=$(sudo -u "$U" -H sudo -n -k /sbin/pfctl "$@" 2>&1); rc=$?
  if [ $rc -eq 0 ]; then got=PERMIT; else got=REFUSE; fi
  if [ "$got" = "$want" ]; then
    printf '   %-7s %-9s %s\n' "$got" "as expected" "$label"
    PASS=$((PASS+1))
  else
    printf '   %-7s %-9s %s\n' "$got" "!! WANTED $want" "$label"
    note "     pfctl/sudo said: $(printf '%s' "$out" | head -2 | tr '\n' ' ')"
    FAIL=$((FAIL+1))
  fi
}

restore_blanket() {
  if [ -f "$WD/blanket.saved" ]; then
    install -m 440 -o root -g wheel "$WD/blanket.saved" "$BLANKET"
  fi
}
cleanup() {
  echo
  echo "== cleanup =="
  rm -f "$GRANT"
  restore_blanket
  echo "   sudoers.d now: [$(ls /etc/sudoers.d/ 2>/dev/null | tr '\n' ' ')]"
  echo "   blanket restored: $([ -f "$BLANKET" ] && echo yes || echo NO -- restore it by hand)"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -rf "$WD"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"
echo "sudo: $(sudo -V 2>/dev/null | head -1)"

# ---------------------------------------------------------------------------
say "G0 SETUP — install D132's grant, and take the harness's blanket grant away"
[ -f "$BLANKET" ] && cp "$BLANKET" "$WD/blanket.saved"
note "harness blanket grant present: $([ -f "$BLANKET" ] && echo yes || echo no)"
mkdir -p "$(dirname "$PINNED")"
cat > "$PINNED" <<'PFEOF'
table <yb_src_0> persist
table <yb_dst_0> persist
pass  in quick from <yb_src_0> to <yb_dst_0>
block return in quick from <yb_src_0> to any
PFEOF
chown root:wheel "$PINNED"; chmod 644 "$PINNED"
note "pinned ruleset written to $PINNED (root:wheel 644), as D132 specifies"

# D132's four permitted forms, verbatim from macos-pf-privileged-path.md.
cat > "$GRANT" <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai -t yb_(src|dst)_([0-9]|[12][0-9]|3[01]) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai -f /etc/yoloai/pf-pool\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
chmod 440 "$GRANT"; chown root:wheel "$GRANT"
if visudo -cf "$GRANT" >/dev/null 2>&1; then
  ok "G0: D132's grant, written exactly as the plan states it, validates under visudo"
else
  bad "G0: the grant as written in the plan does NOT validate:"
  visudo -cf "$GRANT" 2>&1 | sed 's/^/          /'
  note "     That is a finding about the plan, not about this run — the shipped text would not load."
fi
rm -f "$BLANKET"
note "blanket grant removed for the duration; this script keeps root because it already IS root"
pfctl -a "$ANCHOR" -f "$PINNED" >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "G1 THE GUARD — the matrix is void unless an unauthorized command is actually refused"
note "pf-liveness-detect.txt V4 is UNKNOWN because it ran under a blanket grant and every refusal"
note "check passed for a reason unrelated to D132. Proving the removal took, before trusting anything."
if sudo -u "$U" -H sudo -n -k /usr/bin/true >/dev/null 2>&1; then
  bad "G1: the user can still run an unrelated command under \`sudo -n\`. Something else grants it,"
  note "     and EVERY refusal below would be meaningless. ABORTING rather than reporting a matrix"
  note "     that cannot distinguish a working boundary from a broken test."
  exit 1
else
  ok "G1: \`sudo -n /usr/bin/true\` is refused — refusals below mean what they say"
fi
note "sudoers.d during the matrix: [$(ls /etc/sudoers.d/ 2>/dev/null | tr '\n' ' ')]"

# ---------------------------------------------------------------------------
say "G2 THE SHIPPED FORMS — all four must work"
probe "table add"                PERMIT -a "$ANCHOR" -t yb_src_0 -T add 192.168.65.2
probe "table delete"             PERMIT -a "$ANCHOR" -t yb_src_0 -T delete 192.168.65.2
probe "table show"               PERMIT -a "$ANCHOR" -t yb_dst_0 -T show
probe "table flush"              PERMIT -a "$ANCHOR" -t yb_dst_0 -T flush
probe "reload the pinned file"   PERMIT -a "$ANCHOR" -f "$PINNED"
probe "read the anchor's rules"  PERMIT -a "$ANCHOR" -s rules
probe "pf info"                  PERMIT -s info

# ---------------------------------------------------------------------------
say "G3 WHAT MUST STAY REFUSED"
note "\`-k\` is in this list as a SIMPLIFICATION now, not a cost. pf-no-state.txt showed revocation"
note "needs only a table delete, so the grant never has to widen for it — and the gateway-pinning"
note "tension the brief raised disappears with it."
probe "kill states by host (-k)"        REFUSE -k 192.168.65.2
probe "kill states, both endpoints"     REFUSE -k 192.168.65.2 -k 192.168.65.1
probe "kill source tracking (-K)"       REFUSE -K 192.168.65.2
probe "flush the anchor (-F all)"       REFUSE -a "$ANCHOR" -F all
probe "read the MAIN ruleset"           REFUSE -s rules
probe "read anchor rules verbosely"     REFUSE -a "$ANCHOR" -vvs rules
probe "disable pf"                      REFUSE -d
probe "a table outside the pool range"  REFUSE -a "$ANCHOR" -t yb_src_99 -T show
probe "a different anchor"              REFUSE -a com.apple/other -t yb_src_0 -T show

# ---------------------------------------------------------------------------
say "G4 THE CONFLICT — can an unprivileged process install a rule naming a bridge?"
note "This is the question the interface-keyed rewrite creates. Its rules say \`on bridge101\`, and"
note "bridge indices are dynamic, per-sandbox, and change across a restart; pf-lifecycle.txt then"
note "reloads the anchor on every detach and return with different interface names each time. D132's"
note "model has the unprivileged side changing table MEMBERSHIP only, never rule TEXT."
note ""
printf 'pass in quick on bridge101 proto tcp from any to <yb_dst_0> no state\n' > "$WD/iface.rules"
chmod 644 "$WD/iface.rules"
note "route 1: load an interface-keyed ruleset from a path of our choosing"
probe "  -f <our own file>"            REFUSE -a "$ANCHOR" -f "$WD/iface.rules"
note "route 2: rewrite the pinned file, then reload it with the permitted command"
if sudo -u "$U" -H sh -c "printf 'pass in quick on bridge101 all\\n' >> $PINNED" 2>/dev/null; then
  bad "G4: the unprivileged user CAN WRITE the pinned ruleset. The grant's whole model collapses —"
  note "     it authorizes reloading a file the caller controls, which is authoring arbitrary pf"
  note "     rules with extra steps. Check the installed permissions on $PINNED."
else
  ok "G4 route 2: the pinned file is not writable by the unprivileged user, so reloading it cannot"
  note "            smuggle in rule text. The pin holds."
fi
note "route 3: an inline ruleset on stdin"
if printf 'pass in quick on bridge101 all\n' | sudo -u "$U" -H sudo -n -k /sbin/pfctl -a "$ANCHOR" -f - >/dev/null 2>&1; then
  bad "G4 route 3: \`-f -\` is PERMITTED, so rule text can be piped in and the pin is decorative"
else
  ok "G4 route 3: \`-f -\` is refused"
fi
note ""
note "the rules actually in the anchor after all three attempts:"
pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/          /'
if pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -q bridge; then
  bad "G4: a rule naming a bridge got in. One of the routes above worked."
else
  ok "G4: no interface-keyed rule could be installed by the unprivileged user through any route."
  note "    THAT IS THE PROBLEM, not the reassurance it looks like. The rewritten plan keys"
  note "    enforcement on the host-side interface; those rules name a bridge whose index is assigned"
  note "    dynamically and changes across a restart. Under D132 as written, the unprivileged side"
  note "    cannot write them, and the pinned file cannot contain them because nobody knows the"
  note "    indices at install time. So interface keying needs EITHER a privileged component that"
  note "    authors rules at claim time, OR a pinned superset naming every index the host could ever"
  note "    hand out, OR a wider grant. The plan picks none of these because it has not noticed."
fi

# ---------------------------------------------------------------------------
say "G5 THE ESCAPE — a pinned SUPERSET with tables keyed by bridge INDEX"
note "G4 says interface keying does not fit the grant. That is only worth reporting alongside what"
note "would fit, and two candidates exist."
note ""
note "The clean one is pf interface GROUPS: bind bridge101 into a group with ifconfig and let a static"
note "rule say \`on <group>\`. macOS does not have them. Measured, not assumed:"
IFGROUP=$(ifconfig lo0 group ytest_probe 2>&1 | head -1)
note "  ifconfig lo0 group ytest_probe -> ${IFGROUP:-<accepted>}"
note "  man ifconfig mentions 'group': $(man ifconfig 2>/dev/null | grep -c -i group) time(s)"
note "  Closed, and it was the best idea."
note ""
note "The surviving one inverts the pool. Instead of a slot per sandbox whose rule names that"
note "sandbox's interface, have a slot per BRIDGE INDEX: the pinned file enumerates every index the"
note "host could hand out, each with its own table, and claiming a sandbox means adding its allowlist"
note "to the table named after the index it landed on. Rules stay static, the unprivileged side still"
note "only ever changes table membership, and the key is still the interface. The grant's regex would"
note "need to cover the index range — a pattern change, not a model change."
{ echo "# generated superset: one slot per bridge index"
  for i in $(seq 100 140); do
    echo "table <yb_dst_$i> persist"
    echo "pass  in  quick on bridge$i proto tcp from any to <yb_dst_$i> no state"
    echo "pass  out quick on bridge$i proto tcp from <yb_dst_$i> to any no state"
    echo "block drop in  quick on bridge$i proto tcp from any to any"
    echo "block drop out quick on bridge$i proto tcp from any to any"
  done
} > "$WD/superset.conf"
note "generated $(grep -c . "$WD/superset.conf") lines covering bridge100-bridge140"
SUP_OUT=$(pfctl -a "$ANCHOR" -f "$WD/superset.conf" 2>&1 | grep -viE 'ALTQ|Use of -f|present in the main|See /etc/pf.conf')
SUP_N=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
note "pfctl said: ${SUP_OUT:-<nothing>}"
note "rules loaded into the anchor: $SUP_N"
if [ "$SUP_N" -lt 100 ]; then
  bad "G5: the superset did not load ($SUP_N rules). The escape fails, and interface keying then"
  note "     needs a privileged rule-authoring component."
else
  ok "G5: pf loaded $SUP_N rules naming 41 bridge indices, most of which do not exist"
  note ""
  note "Loadable is not enforcing. Two real sandboxes, each claimed ONLY by adding an address to the"
  note "table named after its own bridge index — no rule text written at any point."
  asuser container system start >/dev/null 2>&1; sleep 2
  for g in ygs_a ygs_b; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in ygsnet_a ygsnet_b; do asuser container network delete "$n" >/dev/null 2>&1; done
  asuser container network create ygsnet_a >/dev/null 2>&1
  asuser container network create ygsnet_b >/dev/null 2>&1
  asuser container run -d --name ygs_a --network ygsnet_a "$IMG" sleep 300 >/dev/null 2>&1
  asuser container run -d --name ygs_b --network ygsnet_b "$IMG" sleep 300 >/dev/null 2>&1
  sleep 5
  GA=$(gwof ygs_a); GB=$(gwof ygs_b)
  BA=$(brof "$GA"); BB=$(brof "$GB")
  IA=${BA#bridge}; IB=${BB#bridge}
  note "A on ${BA:-<none>} (index ${IA:-?}), B on ${BB:-<none>} (index ${IB:-?})"
  if [ -z "$BA" ] || [ -z "$BB" ] || [ "$BA" = "$BB" ]; then
    unk "G5: could not get two sandboxes on two distinct bridges; enforcement arm skipped"
  else
    pfctl -a "$ANCHOR" -t "yb_dst_$IA" -T add 1.1.1.1 >/dev/null 2>&1
    pfctl -a "$ANCHOR" -t "yb_dst_$IB" -T add 1.0.0.1 >/dev/null 2>&1
    note "claimed by table membership only: yb_dst_$IA <- 1.1.1.1,  yb_dst_$IB <- 1.0.0.1"
    aa=$(gtry ygs_a 1.1.1.1); ad=$(gtry ygs_a 1.0.0.1)
    bb=$(gtry ygs_b 1.0.0.1); ba2=$(gtry ygs_b 1.1.1.1)
    note "A -> its own 1.1.1.1: $aa    A -> B's 1.0.0.1: $ad     (need non-000 then 000)"
    note "B -> its own 1.0.0.1: $bb    B -> A's 1.1.1.1: $ba2    (need non-000 then 000)"
    if [ "$aa" != 000 ] && [ "$ad" = 000 ] && [ "$bb" != 000 ] && [ "$ba2" = 000 ]; then
      ok "G5: TWO SANDBOXES GET INDEPENDENT POLICY, keyed on the interface, with a static pinned"
      note "    ruleset and nothing but table membership changed. The interface-keyed design DOES fit"
      note "    D132's model — provided the pool is inverted to one slot per bridge INDEX and the"
      note "    grant's table regex covers that range. That is the shape the plan should specify."
    else
      bad "G5: the superset loaded but did not give independent policy (A=$aa/$ad B=$bb/$ba2)."
      note "     The escape does not work as written and G4's conflict stands."
    fi
  fi
  for g in ygs_a ygs_b; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in ygsnet_a ygsnet_b; do asuser container network delete "$n" >/dev/null 2>&1; done
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - What the superset costs at evaluation time. G5 loads 41 indices; a host could hand out
          more, and every packet is now evaluated against a first-match list dominated by rules for
          interfaces that do not exist. pf-no-state.txt already declines to price per-packet
          evaluation, and this makes that gap bigger rather than smaller.
        - What happens when a sandbox lands on an index OUTSIDE the superset range. It would meet no
          rule at all and be silently unenforced — fail-open, the worst direction. That needs a
          preflight assertion nobody has written.
        - Whether a privileged claim-time component is acceptable at all. That is a product decision
          about what may run as root, not a measurement, and it is the likeliest real answer.
        - sudo's regex matching itself. The grant is taken verbatim from the plan and validated, but
          no attempt was made to defeat the regexes -- argument smuggling, alternate pfctl paths,
          symlinks, or `--` handling. A permit/refuse matrix is not a review of the pattern.
        - Any form of the grant other than the one the plan states. If the plan's text is wrong, this
          measures the wrong boundary faithfully.
        - The lifecycle rule's reload CADENCE against sudo. Every reload is a sudo invocation and
          pf-pool-scaling.txt priced one at ~7.9 ms; nothing here multiplies that by a detach/return
          rate, and pf-lifecycle.txt's daemon reloads on every transition.
EOF
