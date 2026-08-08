#!/bin/bash
# ABOUTME: M1 — is there ANY key pf can match a VM's traffic on other than its address?
# ABOUTME: Tries process identity, MAC, tags across NAT, and per-sandbox networks.
#
# Run: sudo bash pf_nonaddress_key.sh
#
# WHY THIS EXISTS
#   enforcement-state-reaping.md rules 1/1b/1c exist entirely to make ADDRESS-KEYED enforcement
#   safe under recycling: a freed address inherits the dead sandbox's allowlist (D3). Cilium's
#   published answer to the same problem is to not key on addresses at all, and on Linux nftables
#   can match `meta cgroup`, which is per-instance. Nobody has asked whether macOS has an
#   equivalent — the premise "macOS is structurally stuck with addresses" is an assumption that
#   the whole reaping design rests on, and it has never been attacked.
#
#   A negative is as valuable as a positive here, PROVIDED it says what was tried. So each key is
#   named, tried, and reported, and the list of what was NOT tried is printed at the end.
#
#   K1 PROCESS IDENTITY. pf can match `user`/`group`, which is what the seatbelt design uses. A VM's
#      packets arrive over vmnet with no local socket — but that is an assumption too. `user
#      unknown` matches packets with no socket owner, so the two rules discriminate directly.
#   K2 LINK LAYER. A guest's MAC is stable across a restart in a way its DHCP lease is not. Can pf
#      express a MAC match at all? The parser settles it; three syntaxes tried so it is the concept
#      being refused and not one spelling.
#   K3 TAGS ACROSS NAT. `tag` is assigned by one rule and matched by another. If a tag applied on
#      INGRESS (keyed on the bridge, not on any address) survives translation and is matchable on
#      egress, then guest traffic stays distinguishable from host traffic AFTER NAT — which is
#      otherwise impossible, since post-NAT every guest wears the host's address.
#   K4 PER-SANDBOX NETWORK. `container network create` gives a network its own bridge, its own
#      subnet and its own gateway. One network per sandbox would make the INTERFACE the key. Two
#      questions decide it: does pf discriminate two guests by bridge, and — the one that matters —
#      does the bridge index RECYCLE the way an address does? A key that recycles is the same
#      hazard wearing a different name, and rules 1/1b/1c would survive unchanged.
#   K5 THE STATUS QUO, stated as a control: two guests on the shared default network sit on ONE
#      bridge, so the interface cannot tell them apart. K4 is only interesting if K5 fails.
#
# METHOD: every "it is blocked" is paired with a control that must still REACH in the same run
#   (A22). A sandbox with no network blocks everything for free and would pass every test here.
#
# SAFETY: writes only into its own anchor and its own networks. Never touches the main ruleset;
#   asserts the main-ruleset reference count is unchanged on exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
UID_U=$(id -u "$U")

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_k"
IMG=yoloai-base:latest
ALLOW=1.1.1.1; DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
NET_A=yb-k-a; NET_B=yb-k-b
MAINREFS0=0

RESULTS="$HERE/results/pf-nonaddress-key.txt"
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
# Load a ruleset into our own anchor, printing whatever pfctl says. Always starts from empty, so
# `nrules` afterwards reports THIS load and never a leftover.
load()   { flush; printf '%s\n' "$1" > /tmp/pfk.rules
           pfctl -a "$ANCHOR" -f /tmp/pfk.rules 2>&1 | quiet_pf; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

# Guest reach. Returns the HTTP code; 000 means it did not get through.
gtry() { asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
           "http://$2/" 2>/dev/null || printf '000'; }
# Host reach, as the invoking user — the positive control for every guest block.
htry() { asuser curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$1/" 2>/dev/null \
           || printf '000'; }
# One inspect answers address, gateway and MAC; $2 picks the field.
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
ipof()  { netfield "$1" ipv4Address; }
gwof()  { netfield "$1" ipv4Gateway; }
macof() { netfield "$1" macAddress; }
# The bridge carrying a given gateway address, e.g. 192.168.65.1 -> bridge102
brof()  { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }

mkguest() {   # $1 = name, $2 = network (empty for default)
  asuser container rm -f "$1" >/dev/null 2>&1
  if [ -n "${2:-}" ]; then
    asuser container run -d --name "$1" --network "$2" "$IMG" sleep 900 >/dev/null 2>&1
  else
    asuser container run -d --name "$1" "$IMG" sleep 900 >/dev/null 2>&1
  fi
  sleep 3
}

cleanup() {
  echo
  echo "== cleanup =="
  flush
  for g in ybk1 ybk2 ybk3 ybk4; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in "$NET_A" "$NET_B"; do asuser container network delete "$n" >/dev/null 2>&1; done
  rm -f /tmp/pfk.rules
  local now; now=$(mainrefs)
  echo "   anchor flushed | main-refs now=$now was=$MAINREFS0"
  [ "$now" = "$MAINREFS0" ] && echo "   main ruleset unchanged" \
                            || echo "   !! MAIN RULESET CHANGED — run pf_anchor_eval.sh"
  echo "   guests/networks removed:"; asuser container network ls 2>&1 | sed 's/^/      /'
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | user=$U uid=$UID_U"
echo "pf:   $(pfctl -s info 2>/dev/null | head -1)"

# ---------------------------------------------------------------------------
say "K0 SETUP AND THE CONTROLS EVERYTHING ELSE LEANS ON"
asuser container system start >/dev/null 2>&1; sleep 2
MAINREFS0=$(mainrefs)
note "main-refs=$MAINREFS0  (0 means pf never descends into our anchor and NOTHING below is valid)"
if [ "$MAINREFS0" -eq 0 ]; then
  bad "host is fail-open before we start; run pf_anchor_eval.sh first. ABORTING"; exit 1
fi

mkguest ybk1 ""
mkguest ybk2 ""
IP1=$(ipof ybk1); IP2=$(ipof ybk2)
MAC1=$(macof ybk1)
GW1=$(gwof ybk1)
BR_DEF=$(brof "$GW1")
note "ybk1=$IP1 mac=$MAC1   ybk2=$IP2   gateway=$GW1 on ${BR_DEF:-<unknown>}"
[ -n "$IP1" ] && [ -n "$IP2" ] && [ -n "$BR_DEF" ] || { bad "setup incomplete; ABORTING"; exit 1; }

b1=$(gtry ybk1 "$DENY"); b2=$(gtry ybk2 "$DENY"); bh=$(htry "$DENY")
note "unfiltered: ybk1->$DENY=$b1  ybk2->$DENY=$b2  host->$DENY=$bh   (all must be non-000)"
if [ "$b1" != 000 ] && [ "$b2" != 000 ] && [ "$bh" != 000 ]; then
  ok "baseline reach on both guests and the host — blocks below mean something"
else
  bad "no baseline reach; every 'blocked' below would be free. ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "K1 PROCESS IDENTITY — is guest traffic attributed to any uid/gid?"
note "pf matches \`user\`/\`group\` against the socket that owns a packet. A VM's packets are"
note "forwarded, not generated locally, so they should own no socket — but that is the assumption"
note "under test. \`user unknown\` matches exactly the no-socket case, so the two rules split it."

note ""
note "K1a  rule: block in quick on $BR_DEF proto tcp from any to $DENY user unknown"
load "block drop in quick on $BR_DEF proto tcp from any to $DENY user unknown" | sed 's/^/        pfctl: /'
if [ "$(nrules)" -eq 0 ]; then
  unk "K1a: pf refused the \`user unknown\` syntax; cannot ask the question this way"
else
  g=$(gtry ybk1 "$DENY"); h=$(htry "$DENY")
  note "guest->$DENY=$g   host->$DENY=$h  (host is the control: it owns a socket, must stay non-000)"
  if [ "$g" = 000 ] && [ "$h" != 000 ]; then
    ok "K1a: guest traffic matches \`user unknown\` => it carries NO process identity"
  elif [ "$g" = 000 ] && [ "$h" = 000 ]; then
    unk "K1a: the rule hit the host too — it is not discriminating on user at all"
  else
    bad "K1a: guest was NOT matched by \`user unknown\` — it does own a socket. Investigate."
  fi
fi
flush

note ""
note "K1b  rule: block in quick on $BR_DEF proto tcp from any to $DENY user = $UID_U"
note "     If the VM's traffic were attributed to the user running it, this would block the guest."
load "block drop in quick on $BR_DEF proto tcp from any to $DENY user = $UID_U" | sed 's/^/        pfctl: /'
g=$(gtry ybk1 "$DENY")
note "guest->$DENY=$g   (non-000 => not attributed to uid $UID_U)"
flush
note "     control: the same rule shape on the OUTBOUND path must bite the HOST, or K1b proves nothing"
load "block drop out quick proto tcp from any to $DENY user = $UID_U" | sed 's/^/        pfctl: /'
h=$(htry "$DENY")
note "host->$DENY=$h   (000 => the rule form works and pf does match user for real sockets)"
if [ "$g" != 000 ] && [ "$h" = 000 ]; then
  ok "K1b: the rule form works (it blocks the host) and does NOT match the guest"
  ok "K1 VERDICT: VM traffic carries no process identity. \`user\`/\`group\` cannot key it."
elif [ "$h" != 000 ]; then
  unk "K1b: the control failed — the rule never bit the host, so the guest result means nothing"
else
  bad "K1b: guest WAS blocked by a uid rule; VM traffic is attributed to uid $UID_U"
fi
flush

# ---------------------------------------------------------------------------
say "K2 LINK LAYER — can pf match a MAC at all?"
note "A guest's MAC is stable in ways a DHCP lease is not, so it would be a better key if pf could"
note "see it. Three spellings, so what is refused is the concept and not one syntax."
K2_ANY=0
for expr in \
  "block drop in quick on $BR_DEF from $MAC1 to any" \
  "block drop in quick on $BR_DEF ether from $MAC1 to any" \
  "block drop in quick on $BR_DEF l2 from $MAC1"
do
  err=$(load "$expr" 2>&1 | head -2 | tr '\n' ' ')
  n=$(nrules)
  if [ "$n" -gt 0 ]; then
    note "ACCEPTED: $expr"; K2_ANY=1
  else
    note "refused:  $expr"
    note "          -> ${err:-<no message>}"
  fi
  flush
done
if [ "$K2_ANY" -eq 0 ]; then
  ok "K2: pf's parser has no MAC/link-layer match. The guest's MAC is visible to the host but"
  note "    not expressible as policy — so a stable identifier exists with no way to enforce on it."
else
  bad "K2: pf accepted a link-layer match — chase it, this would be a real non-address key"
fi
note "for the record, macOS's other packet filter is gone: ipfw was removed in 10.10, and the"
note "bridge(4) driver exposes no filter hooks — so pf is the only policy layer available."

# ---------------------------------------------------------------------------
say "K3 TAGS ACROSS NAT — does an ingress tag survive translation?"
note "The tag is assigned by a rule keyed on the BRIDGE, never on an address, so this is a genuine"
note "non-address key if it holds. What makes it valuable: after NAT every guest wears the host's"
note "address, so nothing else can tell guest traffic from host traffic on the way out."
load "$(printf '%s\n' \
  "pass in quick on $BR_DEF all tag YBK_GUEST" \
  "block drop out quick on en0 proto tcp from any to $DENY tagged YBK_GUEST")" \
  | sed 's/^/        pfctl: /'
if [ "$(nrules)" -lt 2 ]; then
  unk "K3: the tag ruleset did not load; cannot ask"
else
  g=$(gtry ybk1 "$DENY"); h=$(htry "$DENY"); ga=$(gtry ybk1 "$ALLOW")
  note "guest->$DENY=$g  (000 => the tag survived NAT and was matched on egress)"
  note "host ->$DENY=$h  (non-000 => untagged host traffic is untouched — the control)"
  note "guest->$ALLOW=$ga (non-000 => the tag did not blanket-block the guest — the other control)"
  if [ "$g" = 000 ] && [ "$h" != 000 ] && [ "$ga" != 000 ]; then
    ok "K3: tags DO survive NAT. Ingress interface is carryable to the egress rule as a key."
  elif [ "$g" != 000 ]; then
    ok "K3: tags do NOT reach the egress rule (guest still reached $DENY) — post-NAT keying is out"
  else
    unk "K3: controls did not hold (host=$h guest-allow=$ga); result not usable"
  fi
fi
flush

# ---------------------------------------------------------------------------
say "K5 THE STATUS QUO — one bridge for every guest on the default network"
note "Stated as a control for K4: if the shared bridge already discriminated, K4 would be moot."
load "block drop in quick on $BR_DEF proto tcp from any to $DENY" | sed 's/^/        pfctl: /'
g1=$(gtry ybk1 "$DENY"); g2=$(gtry ybk2 "$DENY"); h=$(htry "$DENY")
note "ybk1=$g1  ybk2=$g2  host=$h   (both guests 000, host non-000 => interface is per-NETWORK)"
if [ "$g1" = 000 ] && [ "$g2" = 000 ] && [ "$h" != 000 ]; then
  ok "K5: the default bridge blocks BOTH guests together — it cannot distinguish them"
else
  unk "K5: unexpected split (ybk1=$g1 ybk2=$g2 host=$h)"
fi
flush

# ---------------------------------------------------------------------------
say "K4 PER-SANDBOX NETWORK — one network per sandbox makes the INTERFACE the key"
note "\`container network create\` gives each network its own bridge, subnet and gateway. If yoloAI"
note "created one per sandbox, policy could name the interface instead of the address."
asuser container network create "$NET_A" >/dev/null 2>&1
asuser container network create "$NET_B" >/dev/null 2>&1
sleep 2
mkguest ybk3 "$NET_A"
mkguest ybk4 "$NET_B"
IP3=$(ipof ybk3); IP4=$(ipof ybk4)
GW3=$(gwof ybk3); GW4=$(gwof ybk4)
BR3=$(brof "$GW3"); BR4=$(brof "$GW4")
note "ybk3=$IP3 gw=$GW3 on ${BR3:-<none>}   ybk4=$IP4 gw=$GW4 on ${BR4:-<none>}"

if [ -z "$BR3" ] || [ -z "$BR4" ] || [ "$BR3" = "$BR4" ]; then
  unk "K4a: the two networks did not land on distinct bridges; interface keying is not available"
else
  ok "K4a: distinct networks get distinct bridges ($BR3 vs $BR4)"
  b3=$(gtry ybk3 "$DENY"); b4=$(gtry ybk4 "$DENY")
  note "baseline before any rule: ybk3=$b3 ybk4=$b4  (both must be non-000)"
  if [ "$b3" = 000 ] || [ "$b4" = 000 ]; then
    unk "K4b: a guest on a created network has no egress at all; blocks below would be free"
  else
    load "block drop in quick on $BR3 proto tcp from any to $DENY" | sed 's/^/        pfctl: /'
    g3=$(gtry ybk3 "$DENY"); g4=$(gtry ybk4 "$DENY"); a3=$(gtry ybk3 "$ALLOW")
    note "rule names $BR3 only:  ybk3->$DENY=$g3  ybk4->$DENY=$g4  ybk3->$ALLOW=$a3"
    if [ "$g3" = 000 ] && [ "$g4" != 000 ] && [ "$a3" != 000 ]; then
      ok "K4b: pf discriminates two guests BY INTERFACE, with no address in the rule"
    else
      bad "K4b: interface did not discriminate (ybk3=$g3 ybk4=$g4 allow=$a3)"
    fi
    flush
  fi

  # The question that decides whether this is a cure or the same disease renamed.
  note ""
  note "K4c  DOES THE BRIDGE INDEX RECYCLE? This is the whole question. An address recycles, which"
  note "     is why rules 1/1b/1c exist. A key that recycles the same way is the same hazard with a"
  note "     new name, and a stale rule naming $BR4 would silently apply to whoever gets it next."
  asuser container rm -f ybk4 >/dev/null 2>&1
  asuser container network delete "$NET_B" >/dev/null 2>&1
  sleep 3
  gone=$(ifconfig "$BR4" 2>&1 | grep -c 'does not exist' || true)
  note "after deleting $NET_B: $BR4 $([ "$gone" -gt 0 ] && echo 'is gone' || echo 'still exists')"
  asuser container network create "$NET_B" >/dev/null 2>&1
  sleep 2
  mkguest ybk4 "$NET_B"
  GW4b=$(gwof ybk4)
  BR4b=$(brof "$GW4b")
  note "recreated $NET_B: gw=$GW4b on ${BR4b:-<none>}   (was $GW4 on $BR4)"
  if [ -n "$BR4b" ] && [ "$BR4b" = "$BR4" ]; then
    ok "K4c: the bridge index RECYCLED ($BR4 reused). Interface keying inherits the SAME staleness"
    note "     hazard as addresses — rules 1/1b/1c would survive unchanged, only the key's name"
    note "     changes. That is a negative for the 'macOS can converge with Linux' hope, and it is"
    note "     the result this item existed to get."
  elif [ -n "$BR4b" ]; then
    ok "K4c: the index did NOT recycle ($BR4 -> $BR4b). Worth pursuing: a per-sandbox network"
    note "     would give macOS a non-recycling key, though see the costs printed below."
  else
    unk "K4c: could not determine the new bridge"
  fi
fi

# ---------------------------------------------------------------------------
say "K4d WHAT PER-SANDBOX NETWORKS COST"
note "Only meaningful if K4 pointed anywhere, but cheap to record either way."
t0=$(python3 -c 'import time;print(time.time())')
asuser container network create yb-k-t >/dev/null 2>&1
t1=$(python3 -c 'import time;print(time.time())')
asuser container network delete yb-k-t >/dev/null 2>&1
note "create+settle for one network: $(python3 -c "print('%.2f' % ($t1-$t0))")s added to every start"
note "subnets are allocated per network from 192.168.64.0/16-ish space; the ceiling on concurrent"
note "vmnet networks is not probed here (see NOT TRIED)."

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED — so the negative is readable as the bounded claim it is"
cat <<'EOF'
        - tart. Its guests use the same vmnet bridges, so K1/K2/K3 should transfer, but only the
          apple backend was measured here. `--net-softnet` is a separate mechanism (it filters in
          a helper process, not pf) and was not examined.
        - The vmnet network ceiling. How many networks can exist at once, and whether creation
          fails or degrades at the limit, decides whether one-network-per-sandbox is even feasible.
        - pf `label`, `probability`, `os` fingerprinting, `rtable`. Labels name a rule, not a
          packet; the others are not identity.
        - NetworkExtension flow attribution, which CAN see the owning process — but only for host
          processes, and it does not see forwarded VM traffic. That is M6.
        - Whether the guest can be made to SET something matchable (a DSCP/TOS bit, a source port
          range). It can, which is exactly why it is not a security key: the guest controls it.
EOF
