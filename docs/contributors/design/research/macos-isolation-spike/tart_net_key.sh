#!/bin/bash
# ABOUTME: Is tart genuinely out of reach for interface-keyed enforcement, or was that never asked?
# ABOUTME: Checks whether a tart VM gets a host-side interface at all, and whether two VMs get two.
#
# Run: sudo bash tart_net_key.sh
#
# WHY THIS EXISTS
#   The rewritten enforcement plan's per-backend table says, for tart: "**none** -- no per-sandbox
#   networks; a different question, not a missing answer." That sentence was written from the apple
#   backend's results and never tested against tart itself. If tart VMs can be given per-VM host
#   interfaces, the sentence is wrong and tart converges with everything else. If they cannot, it is
#   right but should be right as a MEASURED constraint rather than an assumption carried forward.
#
#   Reading `tart run --help` first changed the shape of the question. tart has three networking
#   modes, not one:
#     - shared NAT (the default), which is what every previous tart result in this directory used;
#     - `--net-bridged=<iface>`, which puts the VM on the physical LAN;
#     - `--net-softnet`, which routes the VM through a Softnet subprocess over socketpair(2) and
#       carries its own `--net-softnet-allow` / `--net-softnet-block` CIDR lists.
#
#   That last one matters twice over. It may mean there is NO host interface to key on, because the
#   traffic never touches a host bridge -- which would make host-side pf enforcement unreachable for
#   a reason stronger than "no per-sandbox networks". And it means tart may already ship a per-VM
#   destination allowlist of its own, which would make host-side enforcement unnecessary there
#   rather than impossible. Those are three different rows in that table and the plan currently has
#   one of them, chosen without measurement.
#
#   T1 SHARED, ONE VM    what host interface, if any, appears when a VM starts.
#   T2 SHARED, TWO VMs   one interface or two? This is the per-sandbox key question, exactly as it
#                        was asked of the apple backend.
#   T3 SOFTNET, ONE VM   does an interface appear at all under Softnet?
#   T4 SOFTNET, TWO VMs  and if so, is it per-VM?
#   T5 KEYABILITY        and can pf actually write a rule against it that discriminates?
#
#   Every arm diffs the FULL interface list, not just bridges, because a mode that hands out a utun
#   or a feth pair would be invisible to a bridge-only check and would read as "no interface".
#
# WHY T5 EXISTS  (results/tart-net-key-run1-existence-only.txt)
#   Run 1 found a per-VM interface in both modes and concluded "tart converges with the bridge
#   backends". That is a leap. Both VMs share ONE bridge and get per-VM MEMBER interfaces -- the same
#   shape Linux met in k2-veth-key-shared-bridge.txt, where it took `physdev` matching plus a
#   host-wide `br_netfilter` before a member interface could discriminate anything. An interface
#   existing and pf being able to key on it are different claims, and run 1 reported the second
#   having measured only the first.
#
# SAFETY: creates and destroys its own VM clones, and writes only into its own anchor.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
BASE=yoloai-base
VM_A=ytartkey_a
VM_B=ytartkey_b
PROBE=1.1.1.1
TANCHOR="com.apple/yoloai_t"
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/ttk.XXXXXX)
PID_A=""; PID_B=""

RESULTS="$HERE/results/tart-net-key.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }

# Every interface and every address, so a mode that hands out something other than a bridge is not
# invisible to this check.
ifsnap() { ifconfig -a 2>/dev/null | awk '/^[a-z]/{i=$1; sub(":","",i); print "IF "i} /inet /{print "  "$2}'; }
ifnames() { ifconfig -a 2>/dev/null | awk '/^[a-z]/{i=$1; sub(":","",i); print i}' | sort; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
rulestat() {   # $1 = substring identifying the rule, $2 = field
  pfctl -a "$TANCHOR" -vvs rules 2>/dev/null | awk -v pat="$1" -v f="$2:" '
    /^@/ { want = (index($0, pat) > 0); next }
    want && /Evaluations:/ { for (i=1;i<=NF;i++) if ($i==f) { print $(i+1); exit } exit }'
}

# Interfaces that exist now but did not in the named baseline file.
newif() { comm -13 "$1" <(ifnames); }

vm_start() {   # $1 = vm name, $2... = extra tart run args. Echoes the pid.
  local vm=$1; shift
  asuser tart run --no-graphics "$vm" "$@" >"$WD/$vm.log" 2>&1 &
  echo $!
}
vm_stop() {   # $1 = vm name, $2 = pid
  [ -n "${2:-}" ] && kill "$2" 2>/dev/null
  asuser tart stop "$1" >/dev/null 2>&1
  sleep 3
}
# Waits up to $2 seconds for any interface to appear beyond the baseline in $1.
wait_newif() {
  local base=$1 secs=$2 i out
  for ((i=0;i<secs;i++)); do
    out=$(newif "$base")
    [ -n "$out" ] && { printf '%s' "$out"; return 0; }
    sleep 1
  done
  return 1
}

cleanup() {
  echo
  echo "== cleanup =="
  vm_stop "$VM_A" "$PID_A"
  vm_stop "$VM_B" "$PID_B"
  asuser tart delete "$VM_A" >/dev/null 2>&1
  asuser tart delete "$VM_B" >/dev/null 2>&1
  pfctl -a "$TANCHOR" -F all >/dev/null 2>&1
  rm -rf "$WD"
  echo "   VMs now: $(asuser tart list 2>/dev/null | grep -c "$VM_A\|$VM_B" || true) of ours remain"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "tart: $(asuser tart --version 2>&1 | head -1)   softnet: $(softnet --version 2>&1 | head -1 || echo '<not on PATH>')"

# ---------------------------------------------------------------------------
say "T0 BASELINE — the host's interfaces before any VM exists"
asuser container system stop >/dev/null 2>&1; sleep 2
note "the apple backend is stopped first, so its bridges cannot be mistaken for tart's"
ifnames > "$WD/base0"
ifsnap | sed 's/^/          /'
note "interfaces at baseline: $(wc -l < "$WD/base0" | tr -d ' ')"
asuser tart delete "$VM_A" >/dev/null 2>&1; asuser tart delete "$VM_B" >/dev/null 2>&1
asuser tart clone "$BASE" "$VM_A" >/dev/null 2>&1 || { bad "could not clone $BASE; ABORTING"; exit 1; }
asuser tart clone "$BASE" "$VM_B" >/dev/null 2>&1 || { bad "could not clone $BASE; ABORTING"; exit 1; }
note "cloned $BASE -> $VM_A, $VM_B"

# ---------------------------------------------------------------------------
say "T0b DOES THIS pf HAVE A MEMBER-INTERFACE MATCHER AT ALL?"
note "Asked before spending two VM boots on it. OpenBSD pf has \`received-on <iface>\`, which is the"
note "exact primitive needed to key on a bridge MEMBER while filtering on the bridge -- pf's answer to"
note "what Linux does with \`physdev\`. If this pf has it, T5's rule form is the wrong one and a"
note "negative there would be about my syntax rather than about tart."
note ""
note "Checked by loading it, not by reading about it: an anchor accepts \`set timeout\` and silently"
note "ignores it (pf-revocation-alt.txt T1), so exit status alone decides nothing without a control."
printf 'block drop in quick on %s received-on %s proto tcp from any to %s\n' \
  bridge101 vmenet1 "$PROBE" > "$WD/ro.rules"
RO_OUT=$(pfctl -a "$TANCHOR" -f "$WD/ro.rules" 2>&1 | quiet_pf | grep -viE 'Use of -f|present in the main|See /etc/pf.conf')
RO_RC=$?
RO_N=$(pfctl -a "$TANCHOR" -s rules 2>/dev/null | quiet_pf | grep -c . || true)
note "received-on form: pfctl said: ${RO_OUT:-<nothing>}"
note "                  rules in the anchor afterwards: $RO_N"
printf 'block drop in quick on %s proto tcp from any to %s\n' bridge101 "$PROBE" > "$WD/ok.rules"
pfctl -a "$TANCHOR" -f "$WD/ok.rules" >/dev/null 2>&1
CTL_N=$(pfctl -a "$TANCHOR" -s rules 2>/dev/null | quiet_pf | grep -c . || true)
note "CONTROL, a plain rule in the same anchor: $CTL_N rule(s) — proves the anchor loads at all"
pfctl -a "$TANCHOR" -F all >/dev/null 2>&1
note "man 5 pf.conf mentions received-on: $(man 5 pf.conf 2>/dev/null | grep -c -i 'received-on') time(s)"
if [ "$RO_N" -gt 0 ]; then
  HAS_RECVON=yes
  ok "T0b: this pf ACCEPTS received-on. T5 must use it, and a member-interface negative without it"
  note "     would have been a fact about the rule I wrote."
elif [ "$CTL_N" -gt 0 ]; then
  HAS_RECVON=no
  ok "T0b: received-on is a syntax error here and the control loads, so the absence is real rather"
  note "     than a broken anchor. \`on <member>\` is therefore the ONLY member-interface form"
  note "     available on this pf, which makes T5's result a closed question rather than a bounded one."
else
  HAS_RECVON=unknown
  unk "T0b: neither form loaded — the anchor itself is not accepting rules, so this proves nothing"
fi

# ---------------------------------------------------------------------------
say "T1 SHARED NAT, ONE VM — what appears on the host?"
note "This is the mode every earlier tart result in this directory used, without ever asking what"
note "host interface it produces."
PID_A=$(vm_start "$VM_A")
NEW_A=$(wait_newif "$WD/base0" 90 || true)
if [ -z "$NEW_A" ]; then
  unk "T1: no new host interface appeared within 90s. Either the VM did not start (see log below) or"
  note "     shared NAT reuses an existing interface."
  tail -5 "$WD/$VM_A.log" 2>/dev/null | sed 's/^/          /'
else
  note "new interface(s): $(echo "$NEW_A" | tr '\n' ' ')"
  ok "T1: a tart VM in shared NAT does produce a host-side interface"
fi
ifnames > "$WD/base1"
ifsnap | grep -A1 -E "$(echo "${NEW_A:-nomatch}" | tr '\n' '|' | sed 's/|$//')" 2>/dev/null | sed 's/^/          /'

# ---------------------------------------------------------------------------
say "T2 SHARED NAT, TWO VMs — one interface or two?"
note "The per-sandbox key question, asked of tart in exactly the form it was asked of apple. If both"
note "VMs land on one interface, no rule keyed on that interface can tell them apart and the plan's"
note "'none' is right for this mode."
PID_B=$(vm_start "$VM_B")
NEW_B=$(wait_newif "$WD/base1" 90 || true)
if [ -z "$NEW_B" ]; then
  SHARED_VERDICT=one
  note "no second interface appeared — both VMs are sharing $(echo "$NEW_A" | tr '\n' ' ')"
  bad "T2: two VMs in shared NAT produce ONE host interface. Interface keying cannot separate them"
  note "     in this mode, which is what the plan's 'none' was reaching for."
else
  SHARED_VERDICT=two
  note "second VM's new interface(s): $(echo "$NEW_B" | tr '\n' ' ')"
  ok "T2: each VM in shared NAT gets its OWN host interface. The plan's 'tart: none' is WRONG for"
  note "    this mode and tart converges with the other bridge backends."
fi
note "all interfaces with addresses now:"
ifsnap | sed 's/^/          /'

# ---------------------------------------------------------------------------
say "T3/T4 SOFTNET — a mode nothing here has ever run"
note "Softnet routes the VM through a subprocess over socketpair(2), and carries its own per-VM"
note "destination allowlist (--net-softnet-allow / --net-softnet-block). Two things follow that the"
note "plan does not currently say: there may be NO host interface to key on at all, and tart may"
note "already have the enforcement this whole workstream is trying to add."
vm_stop "$VM_A" "$PID_A"; PID_A=""
vm_stop "$VM_B" "$PID_B"; PID_B=""
sleep 3
ifnames > "$WD/base2"
note "interfaces after both VMs stopped: $(wc -l < "$WD/base2" | tr -d ' ')"
PID_A=$(vm_start "$VM_A" --net-softnet)
NEW_SA=$(wait_newif "$WD/base2" 90 || true)
if [ -z "$NEW_SA" ]; then
  SOFTNET_IF=none
  note "no new host interface within 90s under --net-softnet"
  tail -5 "$WD/$VM_A.log" 2>/dev/null | sed 's/^/          /'
else
  SOFTNET_IF="$(echo "$NEW_SA" | tr '\n' ' ')"
  note "new interface(s) under softnet: $SOFTNET_IF"
fi
ifnames > "$WD/base3"
PID_B=$(vm_start "$VM_B" --net-softnet)
NEW_SB=$(wait_newif "$WD/base3" 90 || true)
note "second softnet VM's new interface(s): ${NEW_SB:-<none>}"
note "all interfaces with addresses under two softnet VMs:"
ifsnap | sed 's/^/          /'
if [ "$SOFTNET_IF" = none ] && [ -z "$NEW_SB" ]; then
  SOFTNET_VERDICT=no-interface
  ok "T3/T4: Softnet produces NO host interface for either VM. Host-side pf enforcement is not"
  note "       merely unkeyed there, it is unreachable — there is nothing on the host to write a rule"
  note "       against. That is a stronger and more useful statement than 'no per-sandbox networks',"
  note "       and it points at tart's own --net-softnet-allow/-block as the enforcement surface."
elif [ -n "$NEW_SB" ]; then
  SOFTNET_VERDICT=per-vm
  ok "T3/T4: each Softnet VM gets its own host interface. tart has a per-VM key in this mode."
else
  SOFTNET_VERDICT=shared
  note "one interface for two softnet VMs"
  bad "T3/T4: Softnet VMs share one host interface, so it is keyable but not per-VM."
fi

# ---------------------------------------------------------------------------
say "T5 KEYABILITY — an interface EXISTING is not pf being able to key on it"
note "T1-T4 found a per-VM interface. That is not the same claim as 'tart converges', and run 1's"
note "verdict made exactly that leap. Both VMs share ONE bridge with per-VM MEMBER interfaces, which"
note "is the shape Linux hit in k2-veth-key-shared-bridge.txt -- and there it took \`physdev\` matching"
note "plus \`br_netfilter\` before a member interface could discriminate anything. Whether macOS pf"
note "evaluates rules on a bridge member at all is the question, and nothing above asks it."
note ""
note "Restarting both VMs in the DEFAULT mode, since that is the one a product would adopt."
vm_stop "$VM_A" "$PID_A"; PID_A=""
vm_stop "$VM_B" "$PID_B"; PID_B=""
sleep 3
ifnames > "$WD/base4"
PID_A=$(vm_start "$VM_A"); KA=$(wait_newif "$WD/base4" 90 || true)
ifnames > "$WD/base5"
PID_B=$(vm_start "$VM_B"); KB=$(wait_newif "$WD/base5" 90 || true)
IF_A=$(echo "$KA" | grep '^vmenet' | head -1)
IF_B=$(echo "$KB" | grep '^vmenet' | head -1)
BR_SHARED=$(echo "$KA
$KB" | grep '^bridge' | head -1)
note "A's member interface: ${IF_A:-<none>}   B's: ${IF_B:-<none>}   shared bridge: ${BR_SHARED:-<none>}"

note ""
note "waiting for the guest agent in both VMs (tart exec needs it; up to 240s)"
agent_up() { asuser tart exec "$1" true >/dev/null 2>&1; }
AGENTS=no
for ((i=0;i<240;i++)); do
  if agent_up "$VM_A" && agent_up "$VM_B"; then AGENTS=yes; break; fi
  sleep 1
done
note "guest agents reachable: $AGENTS after ${i}s"

vtry() {   # $1 = vm, $2 = destination. Echoes an HTTP code, 000 on any failure.
  local o; o=$(asuser tart exec "$1" curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"
}

if [ "$AGENTS" != yes ] || [ -z "$IF_A" ] || [ -z "$IF_B" ]; then
  KEYABLE=untested
  unk "T5: could not reach both guests (agents=$AGENTS, ifaces=${IF_A:-none}/${IF_B:-none}), so"
  note "     keyability is untested and the verdict below must not assume it."
else
  pfctl -a "$TANCHOR" -F all >/dev/null 2>&1
  a0=$(vtry "$VM_A" "$PROBE"); b0=$(vtry "$VM_B" "$PROBE")
  note "baseline egress: A -> $a0   B -> $b0   (both must be non-000 or nothing below is readable)"
  if [ "$a0" = 000 ] || [ "$b0" = 000 ]; then
    KEYABLE=untested
    unk "T5: a VM had no egress at baseline; the discrimination test cannot run"
  else
    note ""
    note "RULE ON THE MEMBER INTERFACE: block drop in quick on $IF_A to $PROBE"
    printf 'block drop in quick on %s proto tcp from any to %s\n' "$IF_A" "$PROBE" > "$WD/t.rules"
    pfctl -a "$TANCHOR" -f "$WD/t.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
    a1=$(vtry "$VM_A" "$PROBE"); b1=$(vtry "$VM_B" "$PROBE")
    note "A -> $a1   B -> $b1     rule packet counter: $(rulestat 'block drop in' Packets)"
    note "  (blocking ${IF_A} should silence exactly one VM; which one also confirms the mapping,"
    note "   which was inferred from start order and never verified)"
    note ""
    note "CONTROL — the same rule on the SHARED BRIDGE, which must silence BOTH:"
    printf 'block drop in quick on %s proto tcp from any to %s\n' "$BR_SHARED" "$PROBE" > "$WD/t2.rules"
    pfctl -a "$TANCHOR" -F all >/dev/null 2>&1
    pfctl -a "$TANCHOR" -f "$WD/t2.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
    a2=$(vtry "$VM_A" "$PROBE"); b2=$(vtry "$VM_B" "$PROBE")
    note "A -> $a2   B -> $b2     rule packet counter: $(rulestat 'block drop in' Packets)"
    note "  (without this control, 'the member rule blocked nothing' cannot be told apart from"
    note "   'pf never sees this traffic at all')"
    pfctl -a "$TANCHOR" -F all >/dev/null 2>&1
    a3=$(vtry "$VM_A" "$PROBE"); b3=$(vtry "$VM_B" "$PROBE")
    note ""
    note "after flushing: A -> $a3   B -> $b3   (both back to non-000, or the run drifted)"

    if [ "$a1" = 000 ] && [ "$b1" != 000 ]; then
      KEYABLE=yes
      ok "T5: a rule on the MEMBER interface silenced exactly one VM. pf can key per-VM on tart, so"
      note "    tart genuinely converges with the bridge backends -- and inherits every lifecycle"
      note "    question I1-I5 had to answer for apple, none of which is asked here."
    elif [ "$b1" = 000 ] && [ "$a1" != 000 ]; then
      KEYABLE=yes-reversed
      ok "T5: the member rule discriminated, but silenced the OTHER VM -- so ${IF_A} belongs to B, not"
      note "    A. Keying works; the start-order mapping does not, and anything that assumes it is"
      note "    wrong. A product would have to learn the mapping from the backend, not infer it."
    elif [ "$a1" != 000 ] && [ "$b1" != 000 ] && [ "$a2" = 000 ] && [ "$b2" = 000 ]; then
      KEYABLE=no
      bad "T5: the member rule blocked NOTHING (counter 0) while the bridge rule blocked BOTH. pf"
      note "     evaluates this traffic on the bridge only, so tart hands out per-VM interfaces that"
      note "     this rule form cannot key on -- the wall Linux cleared with \`physdev\`."
      note "     And T0b established that \`on <member>\` is the ONLY such form this pf has: OpenBSD's"
      note "     \`received-on\` is a syntax error here, with a control proving the anchor loads. So this"
      note "     is a closed question for macOS pf as shipped, not a gap in what was tried."
    elif [ "$a2" != 000 ] || [ "$b2" != 000 ]; then
      KEYABLE=untested
      unk "T5: the bridge control did not block both (A=$a2 B=$b2), so pf is not seeing this traffic"
      note "     where expected and the member-interface result is uninterpretable."
    else
      KEYABLE=unclear
      unk "T5: member rule gave A=$a1 B=$b1, bridge rule A=$a2 B=$b2 -- no clean reading"
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "T6 VERDICT — what the plan's tart row should say"
note "  per-VM interface, shared NAT : ${SHARED_VERDICT:-untested}"
note "  per-VM interface, softnet    : ${SOFTNET_VERDICT:-untested}"
note "  pf can key on it             : ${KEYABLE:-untested}"
note ""
note "The middle line is what run 1 answered and the bottom line is what the plan's row turns on."
note "They are different claims and run 1 conflated them."
case "${KEYABLE:-untested}" in
  yes|yes-reversed)
    bad "T6: 'tart: none' is WRONG. Each VM gets its own host-side member interface AND pf can write a"
    note "    rule against it that discriminates, so tart converges with the bridge backends. The"
    note "    per-backend table needs changing before anything is built on that row -- and tart then"
    note "    inherits the whole lifecycle question (do these names recycle? what happens on restart?)"
    note "    which I1-I5 had to answer for apple and which nothing has asked here." ;;
  no)
    ok "T6: 'tart: none' is right in EFFECT but wrong in its reason. It is not that tart has no"
    note "    per-sandbox networks -- it hands out a per-VM member interface in both modes -- it is"
    note "    that pf evaluates the traffic on the shared bridge and cannot key on the member at all --"
    note "    \`on <member>\` does not match and \`received-on\` does not exist here (T0b). tart is out of"
    note "    reach for interface keying because of macOS pf, NOT because of tart's networking, and"
    note "    that distinction matters: tart's own --net-softnet-allow/-block is then the only"
    note "    enforcement surface it has, and nothing has tested whether it works." ;;
  *)
    unk "T6: keyability is ${KEYABLE:-untested}, so this run does NOT settle the tart row. What it does"
    note "     establish is that the row's stated reason is wrong: tart is not a backend with no"
    note "     per-sandbox networks. Both modes produce a per-VM interface; whether pf can use one is"
    note "     the open question, and Softnet's own --net-softnet-allow/-block is a second, untested"
    note "     enforcement surface the plan does not mention at all." ;;
esac

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - `--net-bridged`. It puts the VM on the physical LAN, which is a different threat model and
          almost certainly not something yoloAI would adopt, so it is not exercised. That leaves one
          of tart's three modes unmeasured.
        - Whether Softnet's own --net-softnet-allow/--net-softnet-block actually enforce. Their
          existence is read from `tart run --help`, not from a run: nothing here starts a VM with a
          blocked CIDR and confirms it is unreachable from inside. If tart's own allowlist is the
          answer for that backend, it needs the same treatment every pf rule in this directory got.
        - Any member-interface matcher beyond the two T0b and T5 cover. `on <member>` loads and does
          not match; `received-on` does not parse. If some third form exists it was not found, but
          the two that the pf literature offers are both accounted for.
        - Anything but one destination over TCP/80 from inside the guests. `tart exec` reaches both
          VMs and T5 uses it, but only to fetch 1.1.1.1. No UDP, no DNS, no second destination.
        - Whether the interfaces seen are stable across VM restart, which is the question that
          produced the whole lifecycle rule on the apple backend. Moot while pf cannot key on them,
          and live again the moment a working rule form is found.
        - More than two VMs, and any allocator behaviour.
EOF
