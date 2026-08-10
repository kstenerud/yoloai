#!/bin/bash
# ABOUTME: Re-asks whether macOS has a usable per-sandbox NON-ADDRESS key, after Linux found one.
# ABOUTME: Does a bridge index recycle while a sandbox HOLDS it, and is an ingress tag per-sandbox?
#
# Run: sudo bash pf_interface_key.sh
#
# WHY THIS EXISTS
#   The parallel Linux pass refuted "there is no stable per-sandbox non-address key on either
#   platform". On Linux a policy keyed only on a per-sandbox bridge, with zero address-matching
#   rules, discriminates per sandbox; the names do not recycle; and a guest holding CAP_NET_ADMIN
#   cannot defeat it, because it holds no handle on the host-side interface. That sentence was the
#   one that closed this investigation for BOTH platforms, and half of it is now known to be false.
#
#   macOS's own K4 had already found the two hard parts working: per-sandbox networks get distinct
#   bridges, and pf discriminates two guests BY INTERFACE with no address anywhere in the rule.
#   K4 was abandoned on one result — K4c, "the bridge index RECYCLED" — and that result was
#   produced by DELETING the network and creating it again. It therefore shows recycling AFTER
#   RELEASE, which is not the question. The question a claim-time check turns on is whether an
#   index can be reassigned while a sandbox is STILL HOLDING IT. Those two have opposite remedies:
#
#     - recycles only after release  -> read the real bridge at acquisition, tear the rule down at
#                                       release. A stale rule is then a teardown bug, not a keying
#                                       defect, and the key is sound.
#     - recycles while held          -> the key is defeasible by an unrelated sandbox starting, and
#                                       it is dead exactly the way an address is.
#
#   I1 HELD vs RELEASED  four networks held at once, then new ones created against them. Whether a
#                        held index is ever handed out, and — as the control that makes the negative
#                        mean something — whether a RELEASED one is, which K4c says it is.
#   I2 THE DETACH WINDOW the hazard neither run has looked at. `net-ceiling.txt` N1b found a network
#                        gets no host bridge until a container attaches, and LOSES it on detach. So
#                        a sandbox that merely RESTARTS releases its index without releasing its
#                        network — and if another sandbox can take it in that window, a claim-time
#                        check is not sufficient and the whole approach fails here.
#   I4 DOES THE RULE SURVIVE? whether a loaded rule naming `bridgeN` re-attaches when a bridge of
#                        that name returns, or silently matches nothing from then on. This is what
#                        decides whether I2 is a window or a permanent lapse, and the two have very
#                        different remedies.
#   I3 THE INGRESS TAG   M1's K3 found a tag applied on the bridge survives NAT and is matchable on
#                        egress — the one positive result in that run. It was applied on the SHARED
#                        default bridge, so it could not be per-sandbox. Re-asked with one network
#                        per sandbox, which is what would make the tag a per-sandbox key.
#   I5 THE TWO COMBINED  I2 and I4 together predict that a rule left behind by one sandbox governs
#                        whoever takes its name. Only the direction follows deductively, and the
#                        direction is not the interesting part: a stale BLOCK over-restricts a
#                        stranger, while a stale PASS hands it someone else's allowlist, which is a
#                        cross-sandbox privilege leak. Both rules end up on one interface name and pf
#                        is first-match-with-quick, so which one wins is an ordering question that
#                        neither earlier section can answer. I5b then runs the plan's own remedy —
#                        withdraw the rule at detach — so the fix is measured and not just advised.
#
# METHOD NOTES
#   Every reachability check is TCP/HTTP from inside a guest, and every control is the same protocol
#   over the same path — three runs in the Linux pass recorded a reassuring "blocked" that was free
#   because they probed with ICMP and controlled with TCP.
#   Verdicts below are computed from the recorded values, never echoed alongside them.
#
# SAFETY: writes only into its own anchor; asserts the main-ruleset reference count on exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_i"
IMG=yoloai-base:latest
ALLOW=1.1.1.1; DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
NETS=(yb-i-a yb-i-b yb-i-c yb-i-d yb-i-e yb-i-f yb-i-s)
GUESTS=(ybi1 ybi2 ybi3 ybi4 ybi5 ybi6 ybi7)
SNET=yb-i-s; SGUEST=ybi7   # I5's stranger: the sandbox that takes a name it was never given
MAINREFS0=0

RESULTS="$HERE/results/pf-interface-key.txt"
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

flush() { pfctl -a "$ANCHOR" -F all >/dev/null 2>&1; }
load()  { flush; printf '%s\n' "$1" > /tmp/pfi.rules
          pfctl -a "$ANCHOR" -f /tmp/pfi.rules 2>&1 | quiet_pf; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

# curl writes %{http_code} even when it fails (000) AND exits non-zero, so an `|| printf 000`
# fallback appends a second one and every block reads as "000000" != "000". Round 4 lost four
# verdicts to exactly that; the substitution below is the corrected form.
gtry() { local o; o=$(asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' \
           --max-time 5 "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"; }
htry() { local o; o=$(asuser curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
           "http://$1/" 2>/dev/null); printf '%s' "${o:-000}"; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
gwof() { netfield "$1" ipv4Gateway; }
# The bridge carrying a given gateway address, e.g. 192.168.65.1 -> bridge102
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }
# The bridge a named guest is on, resolved through its gateway. Empty if it has none.
brofguest() { local g; g=$(gwof "$1"); [ -n "$g" ] && brof "$g"; }
egress() { route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}'; }

mknet()   { asuser container network create "$1" >/dev/null 2>&1; }
rmnet()   { asuser container network delete "$1" >/dev/null 2>&1; }
mkguest() { asuser container rm -f "$1" >/dev/null 2>&1
            asuser container run -d --name "$1" --network "$2" "$IMG" sleep 900 >/dev/null 2>&1
            sleep 3; }

cleanup() {
  echo
  echo "== cleanup =="
  flush
  for g in "${GUESTS[@]}"; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in "${NETS[@]}";   do rmnet "$n"; done
  rm -f /tmp/pfi.rules
  local now; now=$(mainrefs)
  echo "   main-refs: before=$MAINREFS0 after=$now"
  [ "$now" -lt "$MAINREFS0" ] && echo "   !! the main ruleset LOST a reference — run pf_anchor_eval.sh"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | egress=$(egress)"

# ---------------------------------------------------------------------------
say "I0 SETUP — and the reachability baseline every block below is measured against"
asuser container system start >/dev/null 2>&1; sleep 2
MAINREFS0=$(mainrefs)
note "main-refs=$MAINREFS0"
[ "$MAINREFS0" -gt 0 ] || { bad "host is already fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
for n in "${NETS[@]}"; do rmnet "$n"; done
h_allow=$(htry "$ALLOW"); h_deny=$(htry "$DENY")
note "host->$ALLOW=$h_allow  host->$DENY=$h_deny  (both must answer, or nothing below is testable)"
if [ "$h_allow" = 000 ] || [ "$h_deny" = 000 ]; then
  bad "I0: the host cannot reach the probe destinations; every 'blocked' below would be free. ABORTING"
  exit 1
fi
ok "I0: both probe destinations answer from the host"

# ---------------------------------------------------------------------------
say "I1 HELD vs RELEASED — is an index ever handed out while a sandbox still holds it?"
note "K4c deleted the network before recreating it, so it measured recycling AFTER RELEASE. That is"
note "not the question a claim-time check turns on. Here four networks are created and HELD (each"
note "with a container attached, since N1b found the bridge exists only while something is), and"
note "then new networks are created against them."
declare -a HELD_NET=() HELD_BR=()
for i in 0 1 2 3; do
  mknet "${NETS[$i]}"
  mkguest "${GUESTS[$i]}" "${NETS[$i]}"
  b=$(brofguest "${GUESTS[$i]}")
  HELD_NET+=("${NETS[$i]}"); HELD_BR+=("${b:-<none>}")
  note "held: ${NETS[$i]} / ${GUESTS[$i]} -> ${b:-<no bridge>}"
done
uniq_n=$(printf '%s\n' "${HELD_BR[@]}" | sort -u | grep -vc '^<none>$' || true)
note "distinct bridges among the four held: $uniq_n of 4"
if [ "$uniq_n" -ne 4 ]; then
  unk "I1a: the four held networks did not land on four distinct bridges; the rest of I1 is moot"
else
  ok "I1a: four concurrently-held networks hold four distinct bridge indices"
fi

note ""
note "I1b  now create two MORE networks while all four are still held. If either is handed an index"
note "     that a live sandbox is using, interface keying is defeasible by an unrelated start."
mknet "${NETS[4]}"; mkguest "${GUESTS[4]}" "${NETS[4]}"; br_e=$(brofguest "${GUESTS[4]}")
mknet "${NETS[5]}"; mkguest "${GUESTS[5]}" "${NETS[5]}"; br_f=$(brofguest "${GUESTS[5]}")
note "new while four held: ${NETS[4]} -> ${br_e:-<none>}   ${NETS[5]} -> ${br_f:-<none>}"
collide=0
for b in "${HELD_BR[@]}"; do
  [ "$b" = "$br_e" ] && collide=$((collide+1))
  [ "$b" = "$br_f" ] && collide=$((collide+1))
done
note "collisions against a held index: $collide"
if [ "$collide" -eq 0 ]; then
  ok "I1b: NO held index was reassigned. Recycling happens on release, not under a live holder."
  HELD_SAFE=yes
else
  bad "I1b: a held index was handed to a new sandbox ($collide collisions) — the key is defeasible"
  HELD_SAFE=no
fi

note ""
note "I1c  THE CONTROL. A negative in I1b is only meaningful if this allocator recycles at all — if"
note "     it never reuses an index, I1b passed for free. Release two and create one."
asuser container rm -f "${GUESTS[2]}" >/dev/null 2>&1; rmnet "${HELD_NET[2]}"
asuser container rm -f "${GUESTS[3]}" >/dev/null 2>&1; rmnet "${HELD_NET[3]}"
asuser container rm -f "${GUESTS[5]}" >/dev/null 2>&1; rmnet "${NETS[5]}"
sleep 2
# The released set must include NETS[5]'s own index. Run 1 compared only against the other two and
# so recorded "no released index came back" while the new network had in fact been handed exactly
# the index it just gave up — the check excluded the one case that occurred.
RELEASED="${HELD_BR[2]} ${HELD_BR[3]} ${br_f:-}"
note "released: ${HELD_NET[2]} (was ${HELD_BR[2]}), ${HELD_NET[3]} (was ${HELD_BR[3]}), ${NETS[5]} (was ${br_f:-<none>})"
mknet "${NETS[5]}"; mkguest "${GUESTS[5]}" "${NETS[5]}"; br_g=$(brofguest "${GUESTS[5]}")
note "next network created got: ${br_g:-<none>}   (released set: $RELEASED)"
if [ -n "$br_g" ] && printf '%s\n' "$RELEASED" | grep -qx "$br_g"; then
  ok "I1c: a RELEASED index was reused — the allocator does recycle, so I1b's negative is real"
  RECYCLES=yes
else
  unk "I1c: no released index came back here. Note this does NOT establish that the allocator"
  note "     never recycles — I2 below reuses an index within seconds — so treat I1b as unconfirmed"
  note "     rather than as evidence of stability."
  RECYCLES=no
fi

# ---------------------------------------------------------------------------
say "I2 THE DETACH WINDOW — a sandbox that restarts releases its index without releasing itself"
note "N1b: a network has no host bridge until a container attaches, and the bridge goes away on"
note "detach. So an ordinary sandbox restart opens a window in which the index is free while the"
note "sandbox still exists and its pf rule still names it. If another sandbox can take it there,"
note "a claim-time read is NOT sufficient and interface keying fails on macOS for a reason that has"
note "nothing to do with K4c."
a_net="${HELD_NET[0]}"; a_guest="${GUESTS[0]}"; a_br="${HELD_BR[0]}"
note "sandbox A = $a_guest on $a_net, currently $a_br"
asuser container stop "$a_guest" >/dev/null 2>&1; sleep 3
a_br_gone=$(brof "$(gwof "$a_guest")" 2>/dev/null)
note "A stopped. its bridge now: ${a_br_gone:-<gone, as N1b predicts>}"
note "creating a NEW sandbox inside A's detach window:"
mknet "${NETS[4]}" 2>/dev/null
asuser container rm -f "${GUESTS[4]}" >/dev/null 2>&1
rmnet "${NETS[4]}"; sleep 1; mknet "${NETS[4]}"
mkguest "${GUESTS[4]}" "${NETS[4]}"
w_br=$(brofguest "${GUESTS[4]}")
note "the sandbox started during the window got: ${w_br:-<none>}"
asuser container start "$a_guest" >/dev/null 2>&1; sleep 4
a_br_back=$(brofguest "$a_guest")
note "A restarted and came back on: ${a_br_back:-<none>}  (it was $a_br)"
# `brof` resolves a bridge THROUGH the gateway address, so two networks reported on one bridge is
# ambiguous: either the index was reused, or the SUBNET was reused and both lookups matched the
# same address. Those are very different findings — the second is a live cross-sandbox address
# collision — and the reported name alone cannot separate them. Print the state instead.
# w_br was read BEFORE A restarted. Re-read it now: if the index moved again when A came back, then
# an index is not stable across any attach event on the host, which is a wider claim than I2's and
# the two readings are the only way to tell them apart.
w_br_after=$(brofguest "${GUESTS[4]}")
note "the window sandbox is NOW on: ${w_br_after:-<no bridge>}  (it was $w_br when it started)"
if [ "$w_br" != "${w_br_after:-}" ]; then
  note "  => its index CHANGED while it was running and untouched. Whatever a rule named for it at"
  note "     claim time no longer refers to it, without that sandbox doing anything at all."
  # `brof` matches a bridge by its GATEWAY ADDRESS, so "<no bridge>" may mean the sandbox lost its
  # host-side interface, or only that this lookup cannot see it. Those differ by whether the guest
  # still has egress, and nothing but a probe separates them. No policy is loaded at this point, so
  # a failure here is the plumbing and not a block.
  w_reach=$(gtry "${GUESTS[4]}" "$ALLOW"); a_reach=$(gtry "$a_guest" "$ALLOW")
  note "  egress with NO policy loaded: window sandbox->$ALLOW=$w_reach   A->$ALLOW=$a_reach"
  if [ "$w_reach" = 000 ] && [ "$a_reach" != 000 ]; then
    bad "I2c: A RUNNING SANDBOX LOST ITS EGRESS because an unrelated sandbox restarted. It is still"
    note "     'running', it never asked for anything, and no policy is loaded. That is a backend"
    note "     defect in its own right, separate from every keying question on this page."
  elif [ "$w_reach" != 000 ]; then
    note "  it still has egress, so the missing bridge is this lookup's blind spot rather than a"
    note "  lost interface — the index moved, the connectivity did not."
  fi
fi
note ""
note "the state both readings came from, verbatim:"
for g in "$a_guest" "${GUESTS[4]}"; do
  note "  $g: state=$(asuser container inspect "$g" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["status"]["state"])' 2>/dev/null) addr=$(netfield "$g" ipv4Address) gw=$(gwof "$g")"
done
note "  host bridges and their addresses:"
ifconfig -a 2>/dev/null | awk '/^bridge/{br=$1; sub(":","",br)} /inet /{print "    "br" "$2}' \
  | sed 's/^/        /'
a_gw=$(gwof "$a_guest"); w_gw=$(gwof "${GUESTS[4]}")
if [ -n "$a_gw" ] && [ "$a_gw" = "$w_gw" ]; then
  bad "I2b: TWO LIVE SANDBOXES SHARE A GATEWAY ($a_gw) on separate networks. This is not merely"
  note "     an interface-key problem — it is a cross-sandbox address collision, and every rule"
  note "     keyed on an ADDRESS is defeated by it too. Compare the L4 collision found on Linux."
elif [ -n "$a_gw" ] && [ -n "$w_gw" ]; then
  note "  the two gateways differ ($a_gw vs $w_gw), so the shared bridge NAME above is an index"
  note "  reuse and not a subnet collision — the addresses stayed distinct."
fi
if [ -n "$w_br" ] && [ "$w_br" = "$a_br" ]; then
  bad "I2: THE WINDOW IS REAL — a new sandbox took A's index while A was merely restarting."
  note "    A rule naming $a_br now governs a different sandbox, and A never released its network."
  WINDOW=hazard
elif [ -n "$a_br_back" ] && [ "$a_br_back" != "$a_br" ]; then
  bad "I2: A came back on a DIFFERENT index ($a_br -> $a_br_back) while its rule still named the"
  note "    old one. Nothing else took it here, but the binding a claim-time read established is"
  note "    already stale, and the sandbox is unenforced rather than misenforced."
  WINDOW=moves
elif [ -n "$a_br_back" ]; then
  ok "I2: A kept its index across a restart and nothing took it in the window"
  WINDOW=stable
else
  unk "I2: A did not come back with a bridge at all; the window is not characterised"
  WINDOW=unknown
fi

# ---------------------------------------------------------------------------
say "I3 THE INGRESS TAG, PER SANDBOX — K3's one positive result, re-asked on per-sandbox networks"
note "K3 established that a tag applied on INGRESS, keyed on the bridge and on no address, survives"
note "NAT and is matchable on the egress rule. It was applied on the shared default bridge, so it"
note "tagged every guest identically and could not be a per-sandbox key. With one network per"
note "sandbox each guest arrives on its own bridge, which is what would make the tag discriminate."
b_guest="${GUESTS[1]}"; b_br="${HELD_BR[1]}"
a_br_now=$(brofguest "$a_guest")
EG=$(egress)
note "A=$a_guest on ${a_br_now:-<none>}   B=$b_guest on ${b_br:-<none>}   egress=$EG"
if [ -z "$a_br_now" ] || [ -z "$b_br" ] || [ "$a_br_now" = "$b_br" ] || [ -z "$EG" ]; then
  unk "I3: need two guests on two distinct bridges and a known egress interface; cannot ask"
else
  load "$(printf '%s\n' \
    "pass in quick on $a_br_now all tag YBI_A" \
    "pass in quick on $b_br all tag YBI_B" \
    "block drop out quick on $EG proto tcp from any to $DENY tagged YBI_A")" \
    | sed 's/^/        pfctl: /'
  note "rules loaded: $(nrules)   (no address appears in any of them)"
  ta=$(gtry "$a_guest" "$DENY")     # tagged A, blocked   -> want 000
  tb=$(gtry "$b_guest" "$DENY")     # tagged B, untouched -> want non-000
  taa=$(gtry "$a_guest" "$ALLOW")   # A elsewhere         -> want non-000
  th=$(htry "$DENY")                # untagged host       -> want non-000
  note "A->$DENY =$ta   (000 => A's own tag reached the egress rule)"
  note "B->$DENY =$tb   (non-000 => B was NOT caught by A's rule — this is the discrimination)"
  note "A->$ALLOW=$taa  (non-000 => the tag did not blanket-block A — control)"
  note "host->$DENY=$th (non-000 => untagged host traffic untouched — control)"
  if [ "$taa" = 000 ] || [ "$th" = 000 ]; then
    unk "I3: a control failed (A-allow=$taa host=$th); the block above is not attributable"
    TAGKEY=unusable
  elif [ "$ta" = 000 ] && [ "$tb" != 000 ]; then
    ok "I3: THE INGRESS TAG IS PER-SANDBOX. Two guests, one blocked and one not, under a ruleset"
    note "    containing no address at all — the same shape Linux's K1 established there."
    TAGKEY=per_sandbox
  elif [ "$ta" = 000 ] && [ "$tb" = 000 ]; then
    bad "I3: both guests were blocked — the tag is not discriminating, it is blanketing"
    TAGKEY=blanket
  else
    bad "I3: A was not blocked by its own tag (A=$ta) — the tag did not reach the egress rule here"
    TAGKEY=no
  fi
fi

# ---------------------------------------------------------------------------
say "I4 DOES A RULE SURVIVE ITS INTERFACE DISAPPEARING? — this decides what I2 costs"
note "I2 showed the index is released whenever a sandbox detaches. What that COSTS depends entirely"
note "on something nobody has measured: whether a loaded rule naming \`bridgeN\` re-attaches when a"
note "bridge of that name comes back, or silently matches nothing from then on."
note "  re-attaches  -> the exposure is the restart window, and a claim-time read plus teardown is"
note "                  close to sufficient."
note "  goes inert   -> EVERY sandbox is unenforced after its first restart, with pf reporting a"
note "                  correct-looking ruleset throughout. That is the shadowed-anchor failure"
note "                  again, one level down, and it would not be visible to any rule inspection."
a4=$(brofguest "$a_guest")
note ""
note "I4a  will pf even accept a rule naming an interface that does not exist? (pre-loading a pool"
note "     of per-sandbox rules at install time depends on it)"
i4a=$(printf 'block drop in quick on %s all\n' "bridge999" > /tmp/pfi4.rules; \
      pfctl -a "$ANCHOR" -f /tmp/pfi4.rules 2>&1 | quiet_pf | tr '\n' ' ')
i4arc=$?
note "loading a rule on bridge999 -> rc=$i4arc ${i4a:-<no output>}"
if [ "$i4arc" -eq 0 ]; then
  ok "I4a: pf accepts a rule naming an absent interface (it resolves by name, not at load time)"
else
  note "     pf refused it, so rules can only name interfaces that already exist"
fi

if [ -z "$a4" ]; then
  unk "I4: sandbox A has no bridge; cannot ask the deciding question"
  RULE_SURVIVES=untested
else
  note ""
  note "I4b  THE DECIDING ARM. A is enforced by an interface-keyed rule with no source address in it."
  note "     Nothing else is started, so the index is not raced — this is an ordinary restart alone."
  load "$(printf '%s\n' \
    "pass  in quick on $a4 proto tcp to $ALLOW" \
    "block drop in quick on $a4 proto tcp to any")" | sed 's/^/        pfctl: /'
  b4_allow=$(gtry "$a_guest" "$ALLOW"); b4_deny=$(gtry "$a_guest" "$DENY")
  note "before restart: A on $a4  ->$ALLOW=$b4_allow  ->$DENY=$b4_deny  (need non-000 then 000)"
  if [ "$b4_allow" = 000 ] || [ "$b4_deny" != 000 ]; then
    unk "I4b: the interface rule is not enforcing before the restart; nothing below is testable"
    RULE_SURVIVES=untested
  else
    ok "I4b precondition: the interface-keyed rule enforces, with no source address in it"
    asuser container stop "$a_guest" >/dev/null 2>&1; sleep 4
    note "A stopped. bridge carrying its gateway now: $(brof "$(gwof "$a_guest")" 2>/dev/null || true)${_none:-}"
    note "the anchor's rules while the interface is GONE (pf keeps them, which is the trap):"
    pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/          /'
    asuser container start "$a_guest" >/dev/null 2>&1; sleep 5
    a4b=$(brofguest "$a_guest"); newip=$(netfield "$a_guest" ipv4Address)
    note "A restarted: bridge=${a4b:-<none>} (was $a4), address=$newip"
    note "NOTHING has been reloaded. Asking the same two questions again:"
    a4_allow=$(gtry "$a_guest" "$ALLOW"); a4_deny=$(gtry "$a_guest" "$DENY")
    note "after restart:  ->$ALLOW=$a4_allow  ->$DENY=$a4_deny"
    if [ "$a4b" != "$a4" ]; then
      unk "I4b: A came back on a DIFFERENT bridge ($a4 -> $a4b), so this arm cannot separate 'the"
      note "     rule went inert' from 'the rule is correctly not matching a different interface'."
      RULE_SURVIVES=moved
    elif [ "$a4_deny" = 000 ] && [ "$a4_allow" != 000 ]; then
      ok "I4b: the rule RE-ATTACHED. Same index, no reload, and the denied destination is still"
      note "     blocked while the allowed one still reaches — and A's address changed to $newip,"
      note "     which the rule never named. pf resolves the interface by name at match time, so"
      note "     I2's window is a window and not a permanent lapse."
      RULE_SURVIVES=yes
    elif [ "$a4_deny" != 000 ]; then
      bad "I4b: THE RULE WENT INERT. Same bridge name, rules still listed by pfctl, and the denied"
      note "     destination now answers $a4_deny. A sandbox is unenforced after an ordinary restart"
      note "     and every inspection of the ruleset reports it healthy — the shadowed-anchor class"
      note "     again, and interface keying cannot be used without a reload on every attach."
      RULE_SURVIVES=no
    else
      unk "I4b: the guest reached nothing at all after the restart (allow=$a4_allow); that is the"
      note "     NAT-death masking case this directory has hit twice, not a verdict about the rule."
      RULE_SURVIVES=unknown
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "I5 THE COMBINED HAZARD — a stale rule, and a stranger holding the same name"
note "I2 says a sandbox that restarts gives up its index and a stranger can take it. I4 says a rule"
note "re-attaches by name. Put together they predict that a rule left behind by one sandbox will"
note "govern a different one — but the DIRECTION is all that follows deductively, and the direction"
note "is not the part that matters. What matters is which policy the stranger ends up under:"
note "  a stale BLOCK inherited -> the stranger is over-restricted. Wrong, visible, not a leak."
note "  a stale PASS inherited  -> the stranger reaches a destination from SOMEONE ELSE'S allowlist,"
note "                             which is a cross-sandbox privilege leak and the same class as X3."
note "Neither follows from I2 and I4; both rules end up loaded on one interface name and pf is"
note "first-match-with-quick, so the outcome is an ordering question nobody has watched."
note ""
note "Both sandboxes get a REAL policy and the two allowlists are disjoint, because that is what"
note "makes a leak observable at all: with no policy every destination answers, so 'the stranger"
note "reached it' would prove nothing. A is allowed $ALLOW and denied everything else; the stranger"
note "is allowed $DENY and denied everything else. If the stranger reaches $ALLOW, it reached a"
note "destination its own policy forbids, using a grant that belongs to a sandbox that is not running."
a5=$(brofguest "$a_guest")
STALE_EFFECT=untested; REMEDY=untested
if [ -z "$a5" ]; then
  unk "I5: sandbox A has no bridge; the collision cannot be built"
else
  load "$(printf '%s\n' \
    "pass  in quick on $a5 proto tcp to $ALLOW" \
    "block drop in quick on $a5 proto tcp to any")" | sed 's/^/        pfctl: /'
  p5a=$(gtry "$a_guest" "$ALLOW"); p5d=$(gtry "$a_guest" "$DENY")
  note "A under its own policy on $a5: ->$ALLOW=$p5a  ->$DENY=$p5d  (need non-000 then 000)"
  if [ "$p5a" = 000 ] || [ "$p5d" != 000 ]; then
    unk "I5: A's policy is not enforcing before the handover; nothing below would be attributable"
  else
    ok "I5 precondition: A is enforced by an interface-keyed rule, allowlist = $ALLOW"
    asuser container stop "$a_guest" >/dev/null 2>&1; sleep 4
    note ""
    note "A stopped, and its rule is DELIBERATELY left loaded. That IS the lifecycle bug being"
    note "priced — the plan's remedy is to withdraw it here, and I5b below runs that arm."
    mknet "$SNET"; mkguest "$SGUEST" "$SNET"
    s_br=$(brofguest "$SGUEST"); s_ip=$(netfield "$SGUEST" ipv4Address)
    note "the stranger came up on: ${s_br:-<none>}   (A held $a5)   addr=$s_ip"
    if [ "$s_br" != "$a5" ]; then
      unk "I5: the stranger did not take A's index this run, so the collision does not exist and"
      note "     nothing below is a measurement of it. I2 reproduced it three times; re-run."
    else
      ok "I5: the collision is real — a live stranger is holding the name A's rule still points at"
      note ""
      note "I5a  BOTH rules loaded on one name: A's stale pair first (it was never withdrawn), the"
      note "     stranger's own pair appended at its claim, exactly as a running system would have it."
      load "$(printf '%s\n' \
        "pass  in quick on $a5 proto tcp to $ALLOW" \
        "block drop in quick on $a5 proto tcp to any" \
        "pass  in quick on $a5 proto tcp to $DENY" \
        "block drop in quick on $a5 proto tcp to any")" | sed 's/^/        pfctl: /'
      note "the anchor, in evaluation order:"
      pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/          /'
      s_allow=$(gtry "$SGUEST" "$ALLOW"); s_deny=$(gtry "$SGUEST" "$DENY")
      note "stranger ->$ALLOW (A's allowlist, NOT its own) = $s_allow"
      note "stranger ->$DENY  (its OWN allowlist)          = $s_deny"
      if [ "$s_allow" != 000 ] && [ "$s_deny" = 000 ]; then
        bad "I5a: THE STRANGER INHERITED A'S POLICY WHOLE. It reached $ALLOW — a destination its own"
        note "     allowlist forbids, granted by a rule belonging to a sandbox that is not running —"
        note "     and was refused $DENY, which its own allowlist permits. A cross-sandbox privilege"
        note "     leak AND a denial of the sandbox's real policy, from one un-withdrawn rule."
        STALE_EFFECT=leak_and_deny
      elif [ "$s_allow" != 000 ]; then
        bad "I5a: THE STRANGER LEAKED. It reached $ALLOW, which only A was granted."
        STALE_EFFECT=leak
      elif [ "$s_deny" = 000 ]; then
        bad "I5a: the stranger was refused its OWN allowlisted destination by A's stale block. Not a"
        note "     leak — the stranger is over-restricted by a rule that is not about it."
        STALE_EFFECT=over_restricted
      else
        ok "I5a: the stranger got its own policy despite A's stale rule sitting ahead of it"
        STALE_EFFECT=none
      fi

      note ""
      note "I5b  THE REMEDY, measured rather than recommended. Same collision, same stranger, with"
      note "     A's rule WITHDRAWN as the plan's lifecycle rule requires. Only the stranger's own"
      note "     pair is loaded. If this is clean, the lifecycle rule is sufficient for this hazard."
      load "$(printf '%s\n' \
        "pass  in quick on $a5 proto tcp to $DENY" \
        "block drop in quick on $a5 proto tcp to any")" | sed 's/^/        pfctl: /'
      r_allow=$(gtry "$SGUEST" "$ALLOW"); r_deny=$(gtry "$SGUEST" "$DENY")
      note "stranger ->$ALLOW (must now be refused) = $r_allow"
      note "stranger ->$DENY  (must now reach)      = $r_deny"
      if [ "$r_allow" = 000 ] && [ "$r_deny" != 000 ]; then
        ok "I5b: withdrawing the stale rule restores the stranger's own policy exactly. The"
        note "     lifecycle rule the plan recommends is sufficient for this hazard, measured."
        REMEDY=sufficient
      else
        bad "I5b: withdrawing the stale rule did NOT restore correct policy (allow=$r_allow"
        note "     deny=$r_deny). The recommended remedy does not close this and the design needs"
        note "     more than an attach/detach discipline."
        REMEDY=insufficient
      fi
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "VERDICT — composed from the values above, not asserted"
note "held index reassigned while live : ${HELD_SAFE:-untested}"
note "allocator recycles at all        : ${RECYCLES:-untested}"
note "restart/detach window            : ${WINDOW:-untested}"
note "ingress tag per sandbox          : ${TAGKEY:-untested}"
note "rule survives its interface going away: ${RULE_SURVIVES:-untested}"
note "what a stale rule does to a stranger: ${STALE_EFFECT:-untested}"
note "withdrawing it restores correct policy: ${REMEDY:-untested}"
note ""
if [ "${HELD_SAFE:-no}" = yes ] && [ "${RECYCLES:-no}" = yes ] && [ "${WINDOW:-x}" = stable ]; then
  ok "macOS HAS a usable per-sandbox non-address key, unconditionally. An index is stable for as"
  note "    long as a sandbox holds it, including across a restart, and is only reused after release."
  note "    Reading the real bridge at acquisition and dropping the rule at release is sufficient."
elif [ "${TAGKEY:-no}" = per_sandbox ] && [ "${RULE_SURVIVES:-no}" = yes ]; then
  bad "macOS HAS the key, and ONE lifecycle rule is the whole price. Everything the key needs works:"
  note "    a held index is never reassigned, the interface discriminates with no address in the"
  note "    rule, the ingress tag is per-sandbox, and a rule re-attaches BY NAME when its interface"
  note "    comes back — A's address changed underneath it and enforcement did not lapse."
  note ""
  note "    The single failure is the detach window, and I5 prices it rather than naming it. A"
  note "    sandbox that merely restarts gives up its index; a sandbox started in that window takes"
  note "    it; and the rule left behind then does BOTH bad things at once (measured:"
  note "    ${STALE_EFFECT}). The stranger reached a destination only the departed sandbox was"
  note "    granted — a cross-sandbox privilege leak of X3's class — AND was refused the"
  note "    destination its own allowlist permits, because pf is first-match-with-quick and the"
  note "    stale pair sits ahead of its own. Not 'unenforced': wrongly enforced, in both"
  note "    directions, with every rule present and correct-looking to any inspection."
  note ""
  note "    The remedy is measured and not merely advised (I5b: ${REMEDY}). Withdrawing the stale"
  note "    rule restores the stranger's own policy exactly. So: withdraw a sandbox's rule when its"
  note "    interface goes, re-read the real index when it returns. That is a lifecycle"
  note "    requirement, not a missing key, and it is a far smaller gap than 'there is no stable"
  note "    per-sandbox non-address key', which is what closed this on macOS. Linux needs no such"
  note "    rule because its names do not recycle; macOS needs one because its indices do."
elif [ "${TAGKEY:-no}" = per_sandbox ] && [ "${WINDOW:-x}" != stable ]; then
  unk "SPLIT RESULT. The tag discriminates per sandbox, but the interface it is keyed on is not"
  note "    stable across a restart and it is NOT established that a rule re-attaches when the name"
  note "    returns (I4: ${RULE_SURVIVES:-untested}). Without that, the window cannot be priced."
else
  bad "macOS does NOT have a usable per-sandbox non-address key on this evidence, and the divergence"
  note "    from Linux is a measured constraint rather than an assumption: on Linux the interface is"
  note "    a per-sandbox key that holds; here the index is released whenever the sandbox detaches."
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - tart. Its guests share the vmnet bridges rather than getting one network each, so none of
          I1-I3 transfers to it; the question there is a different one and it is not asked here.
        - Concurrent starts. The detach window in I2 is opened by hand, one sandbox at a time. Two
          sandboxes racing for a freed index is the case a real pool would hit and it is not run.
        - Whether ORDER changes I5a's outcome. A's stale pair was loaded first, which is what a real
          system produces, and pf is first-match-with-quick so it wins twice over. A stranger whose
          rules landed FIRST is a different arm and is not run -- it would say whether the leak is a
          property of staleness or only of ordering, and the remedy is the same either way.
        - Recovery for the sandbox displaced in I2c. I5b withdraws the stale rule and the STRANGER
          becomes correct; what happens to the original sandbox when it returns to find its name
          taken is not measured, and it is the other half of the lifecycle rule.
        - The tag namespace. Whether pf tags are a limited resource, and what happens at 32 of them,
          is unmeasured; K3 and I3 both use two.
        - Any of this under the shipped D132 grant. Every rule here is loaded as root. The grant
          authorizes table membership, and an interface-keyed or tag-keyed design needs RULES to
          change per sandbox, which the grant does not permit at all. That is the largest untested
          consequence of a positive result here, and it is a D132 question rather than a pf one.
EOF
