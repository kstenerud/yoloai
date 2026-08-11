#!/bin/bash
# ABOUTME: DF190 -- a running sandbox loses all egress when an unrelated one reclaims its bridge.
# ABOUTME: Settles ownership (Apple's `container` or ours) and reads the daemon log across the event.
#
# Run: sudo bash pf_df190_mechanism.sh
#
# WHY THIS EXISTS
#   DF190 was filed from `pf-interface-key.txt` I2c as a symptom with no mechanism: a running
#   sandbox, still reporting `running` and still holding an address, lost egress entirely when an
#   unrelated sandbox restarted and took its bridge index. No `container` daemon log was ever read,
#   so nobody could say whether it is an Apple defect or one we cause.
#
#   That question decides who owns it, and it sits underneath every keying decision on this backend:
#   if a sandbox can silently lose its network because a neighbour restarted, then enforcement keyed
#   on the interface is being layered on top of an interface layer that already loses sandboxes.
#
#   `pf-lifecycle.txt` L5 turned the symptom into a TRIGGER: a returning sandbox reclaims its old
#   index off whoever holds it, and the incumbent is not rehomed. That is a reliable way to cause it
#   on demand, which is what makes this run possible at all.
#
#   D1 OWNERSHIP     reproduce with NO pf anchor of ours loaded and no rule of ours anywhere. If it
#                    still happens, nothing yoloAI does is involved and the defect is Apple's. This
#                    is the whole ownership question and it is one arm.
#   D2 THE STATE     what the displaced sandbox and the host each believe afterwards -- inspect
#                    output, host bridges, routes, and the network object. A symptom described only
#                    as "no egress" cannot be reported upstream.
#   D3 THE LOG       `container system logs` across the displacement window, which is the thing
#                    DF190 records as never having been read.
#   D4 RECOVERY      does it recover on its own, on restart, or not at all? An unrecoverable state
#                    is a different severity from a self-healing one.
#
# SAFETY: loads NO pf rules at any point. It asserts our anchor is empty rather than assuming it.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
IMG=yoloai-base:latest
NET_A=ydnet_a; G_A=yd_a
NET_W=ydnet_w; G_W=yd_w
PROBE=1.1.1.1
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfd.XXXXXX)

RESULTS="$HERE/results/df190-mechanism.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }

netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
statef() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
print(json.load(sys.stdin)[0]["status"]["state"])' 2>/dev/null; }
gwof() { netfield "$1" ipv4Gateway; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }
gtry() { local o; o=$(asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' \
           --max-time 5 "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"; }
bridges() { ifconfig -a 2>/dev/null | awk '/^bridge/{b=$1; sub(":","",b)} /inet /{print b" "$2}'; }
anchor_rules() { pfctl -s rules -a 'com.apple/yoloai_l' 2>/dev/null | grep -c . || true; }

describe() {   # $1 = guest label, $2 = container name
  local gw br
  gw=$(gwof "$2"); br=""; [ -n "$gw" ] && br=$(brof "$gw")
  note "$1: state=$(statef "$2")  addr=$(netfield "$2" ipv4Address)  gw=${gw:-<none>}  host bridge=${br:-<NONE>}"
}

cleanup() {
  echo
  echo "== cleanup =="
  for g in "$G_A" "$G_W"; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in "$NET_A" "$NET_W"; do asuser container network delete "$n" >/dev/null 2>&1; done
  rm -rf "$WD"
  echo "   host bridges now:"; bridges | sed 's/^/     /'
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "container CLI: $(asuser container --version 2>&1 | head -1)"

# ---------------------------------------------------------------------------
say "D0 CO-TENANCY — can one host bridge carry two networks at once?"
note "Asked because run 1's recovery line reported the displaced sandbox back on bridge101 while the"
note "returning sandbox was also on bridge101. If one bridge can carry two networks' gateways, then"
note "keying enforcement on the interface is NOT per-sandbox, and that claim is load-bearing in the"
note "rewritten plan. Three concurrent networks, and every inet on every bridge printed."
asuser container system start >/dev/null 2>&1; sleep 2
for i in 1 2 3; do
  asuser container rm -f "ycot_g$i" >/dev/null 2>&1
  asuser container network delete "ycot_n$i" >/dev/null 2>&1
done
for i in 1 2 3; do
  asuser container network create "ycot_n$i" >/dev/null 2>&1
  asuser container run -d --name "ycot_g$i" --network "ycot_n$i" "$IMG" sleep 300 >/dev/null 2>&1
done
sleep 6
note "every bridge and every address it carries:"
bridges | sed 's/^/          /'
COT_GWS=()
for i in 1 2 3; do
  g=$(gwof "ycot_g$i"); b=$(brof "$g")
  note "  ycot_g$i: gw=${g:-<none>} -> ${b:-<none>}"
  COT_GWS+=("${b:-none}")
done
COT_UNIQ=$(printf '%s\n' "${COT_GWS[@]}" | sort -u | grep -c .)
COT_N=$(printf '%s\n' "${COT_GWS[@]}" | grep -c .)
note "distinct bridges across $COT_N concurrent networks: $COT_UNIQ"
if [ "$COT_UNIQ" = "$COT_N" ] && [ "$COT_N" = 3 ]; then
  ok "D0: three concurrent per-sandbox networks get three DISTINCT bridges, one gateway each. So"
  note "    co-tenancy is not a thing here, and the interface key stays per-sandbox. Whatever run 1"
  note "    saw was a bridge changing hands over time, not two networks sharing one."
else
  bad "D0: $COT_N concurrent networks landed on $COT_UNIQ distinct bridge(s). If a bridge carries two"
  note "     networks' gateways at once, keying on the interface is NOT per-sandbox and the rewritten"
  note "     plan's macOS row needs revisiting before anything is built on it."
fi
for i in 1 2 3; do
  asuser container rm -f "ycot_g$i" >/dev/null 2>&1
  asuser container network delete "ycot_n$i" >/dev/null 2>&1
done
sleep 2

# ---------------------------------------------------------------------------
say "D1 OWNERSHIP — reproduce with NO rule of ours anywhere"
note "This is the arm that decides who owns DF190. If a sandbox loses its network while yoloAI has"
note "loaded nothing, then nothing yoloAI does is involved and the defect belongs to Apple's"
note "\`container\`. Asserting our anchors are empty rather than assuming it, because 'we did not load"
note "anything this run' and 'nothing is loaded' are different claims and a previous section in this"
note "directory has already conflated them."
for a in yoloai_l yoloai_n yoloai_r yoloai_v; do
  n=$(pfctl -s rules -a "com.apple/$a" 2>/dev/null | grep -c . || true)
  note "  anchor com.apple/$a holds $n rule(s)"
done
TOTAL=0
for a in yoloai_l yoloai_n yoloai_r yoloai_v; do
  n=$(pfctl -s rules -a "com.apple/$a" 2>/dev/null | grep -c . || true)
  TOTAL=$((TOTAL + n))
done
if [ "$TOTAL" -ne 0 ]; then
  bad "D1: $TOTAL rule(s) of ours are loaded; this run could not settle ownership. ABORTING"; exit 1
fi
note "total rules of ours loaded: 0 — anything that happens below happens without us"

asuser container system start >/dev/null 2>&1; sleep 2
for g in "$G_A" "$G_W"; do asuser container rm -f "$g" >/dev/null 2>&1; done
for n in "$NET_A" "$NET_W"; do asuser container network delete "$n" >/dev/null 2>&1; done

asuser container network create "$NET_A" >/dev/null 2>&1
asuser container run -d --name "$G_A" --network "$NET_A" "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
A_GW=$(gwof "$G_A"); A_BR=$(brof "$A_GW")
describe "A" "$G_A"
[ -n "$A_BR" ] || { bad "D1: A never got a bridge; ABORTING"; exit 1; }
a0=$(gtry "$G_A" "$PROBE")
note "A egress before anything happens: $a0  (need non-000)"
[ "$a0" != 000 ] || { bad "D1: A has no egress at baseline; nothing below would mean anything. ABORTING"; exit 1; }

note ""
note "stopping A so its index is released, then creating W to take it"
LOGSTART=$(date '+%Y-%m-%d %H:%M:%S')
asuser container stop "$G_A" >/dev/null 2>&1
sleep 3
asuser container network create "$NET_W" >/dev/null 2>&1
asuser container run -d --name "$G_W" --network "$NET_W" "$IMG" sleep 900 >/dev/null 2>&1
sleep 5
W_GW=$(gwof "$G_W"); W_BR=$(brof "$W_GW")
describe "W" "$G_W"
w0=$(gtry "$G_W" "$PROBE")
note "W egress: $w0  (need non-000)"
if [ "$W_BR" != "$A_BR" ]; then
  unk "D1: W took ${W_BR:-<none>}, not A's $A_BR — the trigger did not fire this run, so nothing"
  note "     below is DF190. Re-run; pf-lifecycle.txt L5 and pf-interface-key.txt I2 both reproduce it."
  TRIGGERED=no
else
  note "W has taken A's index ($A_BR) — the precondition holds"
  TRIGGERED=yes
  note ""
  note "now starting A again. pf-lifecycle.txt L5 established that A reclaims its index off W."
  asuser container start "$G_A" >/dev/null 2>&1
  sleep 6
  describe "A" "$G_A"
  describe "W" "$G_W"
  a1=$(gtry "$G_A" "$PROBE"); w1=$(gtry "$G_W" "$PROBE")
  note "after A's return:  A -> $PROBE = $a1     W -> $PROBE = $w1"
  note "   (the finding is A working and W not, with W still reporting \`running\`)"
  if [ "$a1" != 000 ] && [ "$w1" = 000 ] && [ "$(statef "$G_W")" = running ]; then
    ok "D1: DF190 REPRODUCES WITH ZERO RULES OF OURS LOADED. W reports \`running\`, holds an address,"
    note "    and cannot reach a destination A reaches from the same host in the same second. No pf"
    note "    anchor of ours exists — so this is a defect in Apple's \`container\`, not in yoloAI, and"
    note "    it is not caused by or fixable in the enforcement design."
  elif [ "$w1" != 000 ]; then
    unk "D1: W kept its egress ($w1) — DF190 did not reproduce this run despite the index changing"
  else
    unk "D1: partial ($a1/$w1, W state=$(statef "$G_W")); not a clean reproduction"
  fi
fi

# ---------------------------------------------------------------------------
say "D2 THE STATE — what each side believes afterwards"
note "\"No egress\" is not a bug report. This is what a maintainer would need to see."
note "host bridges and their addresses:"
bridges | sed 's/^/          /'
note ""
note "the displaced sandbox's own view:"
asuser container inspect "$G_W" 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)[0]
    n=d["status"]["networks"][0] if d["status"].get("networks") else {}
    print("          state    :", d["status"]["state"])
    print("          address  :", n.get("ipv4Address","<none>"))
    print("          gateway  :", n.get("ipv4Gateway","<none>"))
    print("          network  :", n.get("network","<none>"))
except Exception as e:
    print("          inspect unreadable:", e)' 2>/dev/null
note ""
note "the network objects the daemon still lists:"
asuser container network list 2>/dev/null | sed 's/^/          /'
note ""
note "and from inside the displaced guest — does it even have a route?"
asuser container exec "$G_W" sh -c 'ip -4 addr show 2>/dev/null | grep -E "inet |^[0-9]" ; echo "--- routes ---"; ip route 2>/dev/null' 2>/dev/null | sed 's/^/          /'
note ""
note "can it reach its own gateway, one hop away?"
WGW_NOW=$(gwof "$G_W")
note "  W -> its gateway ${WGW_NOW:-<none>}: $([ -n "$WGW_NOW" ] && gtry "$G_W" "$WGW_NOW" || echo 'n/a')"

# ---------------------------------------------------------------------------
say "D3 THE LOG — the thing DF190 records as never having been read"
note "\`container system logs\` across the window. Filtered to the two networks and the two sandboxes,"
note "then shown unfiltered at the tail, because a filter that hides the relevant line is worse than"
note "no filter at all."
asuser container system logs --last 5m > "$WD/log.txt" 2>&1
note "captured $(grep -c . "$WD/log.txt" 2>/dev/null || echo 0) line(s) since $LOGSTART"
note ""
note "lines naming our networks, sandboxes, or a bridge:"
grep -Ei "$NET_A|$NET_W|$G_A|$G_W|bridge|vmnet|network" "$WD/log.txt" 2>/dev/null | tail -40 \
  | sed 's/^/          /' || note "  (none matched)"
note ""
note "lines carrying an error or warning, whether or not they name us:"
grep -Ei "error|warn|fail|unable|cannot" "$WD/log.txt" 2>/dev/null | tail -25 \
  | sed 's/^/          /' || note "  (none matched)"
note ""
note "last 15 lines unfiltered:"
tail -15 "$WD/log.txt" 2>/dev/null | sed 's/^/          /'

# ---------------------------------------------------------------------------
say "D4 RECOVERY — is the displaced sandbox recoverable, and by what?"
note "An unrecoverable state is a different severity from a self-healing one, and the difference"
note "decides whether the product can paper over this or has to avoid causing it."
if [ "${TRIGGERED:-no}" != yes ]; then
  unk "D4: the trigger did not fire, so there is nothing displaced to recover"
else
  sleep 10
  r1=$(gtry "$G_W" "$PROBE")
  note "after a further 10s with no intervention: W -> $PROBE = $r1"
  asuser container stop "$G_W" >/dev/null 2>&1; sleep 2
  asuser container start "$G_W" >/dev/null 2>&1; sleep 6
  # BOTH sides, because a "recovery" that hands the bridge back by taking it off the other sandbox
  # is not a recovery -- it is the same defect pointing the other way, and run 1 checked only W.
  describe "W after a stop/start" "$G_W"
  describe "A after W's stop/start" "$G_A"
  note "bridges at this moment:"; bridges | sed 's/^/            /'
  r2=$(gtry "$G_W" "$PROBE"); r2a=$(gtry "$G_A" "$PROBE")
  note "after W's stop/start:  W -> $PROBE = $r2     A -> $PROBE = $r2a"
  if [ "$r1" != 000 ]; then
    ok "D4: it self-heals within 10s with no intervention — a transient, not a stuck state"
  elif [ "$r2" != 000 ] && [ "$r2a" != 000 ]; then
    ok "D4: it does NOT self-heal, but a stop/start recovers it WITHOUT costing the other sandbox —"
    note "    both have egress afterwards. The price is a restart of an innocent workload, which for"
    note "    an agent mid-task is not free, but it is bounded."
  elif [ "$r2" != 000 ] && [ "$r2a" = 000 ]; then
    bad "D4: the 'recovery' MOVED THE DEFECT. W has egress again and A has now lost it — the bridge"
    note "     changed hands rather than being rebuilt, so restarting the victim just picks a new"
    note "     victim. Run 1 reported this as a clean recovery because it only checked W."
  else
    bad "D4: neither waiting nor a stop/start restored it (W=$r2 A=$r2a). The sandbox is stuck without"
    note "     egress while reporting healthy, and nothing short of recreating it is known to help."
  fi
fi

# ---------------------------------------------------------------------------
say "D5 THE MECHANISM — is it the surviving NETWORK that causes the collision?"
note "D3's log shows the vmnet helper for the departing sandbox's network STAYING ALIVE across the"
note "stop -- \`released session [allocations=1]\`, not a shutdown -- and then re-allocating on the same"
note "network id when the sandbox returns. D0 showed three networks created fresh never collide. So"
note "the candidate mechanism is the network object outliving its last container and re-creating its"
note "vmnet interface on an index that has since been handed to someone else."
note ""
note "That is testable rather than inferable: run the same sequence but DELETE the network while it is"
note "empty, so no helper survives to re-attach. If the collision disappears, the surviving network is"
note "the cause. If it still happens, it is not, and the log reading above is a coincidence."
for g in "$G_A" "$G_W"; do asuser container rm -f "$g" >/dev/null 2>&1; done
for n in "$NET_A" "$NET_W"; do asuser container network delete "$n" >/dev/null 2>&1; done
sleep 2
asuser container network create "$NET_A" >/dev/null 2>&1
asuser container run -d --name "$G_A" --network "$NET_A" "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
D5_A_BR=$(brof "$(gwof "$G_A")")
note "A is on ${D5_A_BR:-<none>}"
note "removing the container AND deleting its network, so no helper survives"
asuser container rm -f "$G_A" >/dev/null 2>&1
asuser container network delete "$NET_A" >/dev/null 2>&1
sleep 3
note "networks the daemon lists now:"
asuser container network list 2>/dev/null | sed 's/^/          /'
asuser container network create "$NET_W" >/dev/null 2>&1
asuser container run -d --name "$G_W" --network "$NET_W" "$IMG" sleep 900 >/dev/null 2>&1
sleep 5
D5_W_BR=$(brof "$(gwof "$G_W")")
note "W is on ${D5_W_BR:-<none>} (A had held ${D5_A_BR:-<none>})"
note "now recreating A's network and sandbox from scratch"
asuser container network create "$NET_A" >/dev/null 2>&1
asuser container run -d --name "$G_A" --network "$NET_A" "$IMG" sleep 900 >/dev/null 2>&1
sleep 5
describe "A (recreated)" "$G_A"
describe "W (incumbent)"  "$G_W"
note "bridges:"; bridges | sed 's/^/            /'
d5a=$(gtry "$G_A" "$PROBE"); d5w=$(gtry "$G_W" "$PROBE")
note "A -> $PROBE = $d5a     W -> $PROBE = $d5w   (both must be non-000)"
if [ "$d5a" != 000 ] && [ "$d5w" != 000 ]; then
  ok "D5: with the network DELETED rather than left empty, the returning sandbox does NOT displace"
  note "    the incumbent — both keep egress. So the collision is caused by the network object (and"
  note "    its vmnet helper) outliving its last container and re-attaching onto an index handed out"
  note "    in the meantime. That is a mechanism, and it is also a WORKAROUND: delete the network"
  note "    when its last sandbox goes, and this defect does not arise."
elif [ "$d5w" = 000 ]; then
  bad "D5: the incumbent lost egress anyway ($d5w), so a surviving network is NOT the cause and the"
  note "     log reading above is a coincidence. The mechanism is still unknown."
else
  unk "D5: the recreated sandbox has no egress ($d5a); this arm did not set up cleanly"
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Any privileged tracing. No dtrace, no vmnet-level instrumentation, no attach to the
          daemon. `container system logs` is whatever the daemon chose to emit, and if it emits
          nothing about the displacement then this run cannot say why it happens -- only that it
          does, and that we do not cause it.
        - More than one displaced sandbox. One incumbent, one returner.
        - Whether the same thing happens when the incumbent is BUSY rather than idle. Both sandboxes
          here run `sleep`; a sandbox holding open connections may be treated differently, and that
          is the case the product actually has.
        - tart. This is an `apple` backend defect as far as this run goes, and nothing here says
          whether tart's networking has an equivalent.
        - Whether the address, as opposed to the bridge, is what is lost. The two are not separated
          here.
        - Reporting it upstream. Nothing here has been checked against Apple's issue tracker for a
          known duplicate.
EOF
