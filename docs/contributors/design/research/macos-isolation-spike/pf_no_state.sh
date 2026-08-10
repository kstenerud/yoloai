#!/bin/bash
# ABOUTME: Does pf's `no state` give the Cilium shape on macOS — policy on every packet?
# ABOUTME: If it does, macOS loses its state-teardown asymmetry and the `-k` grant widening dies.
#
# Run: sudo bash pf_no_state.sh
#
# WHY THIS EXISTS
#   The Linux half established that `ct state established,related accept` is the direct cause of the
#   revocation failure, and that dropping it costs nothing: with the fast-path an in-flight transfer
#   held 300 KB/s for 30s after its allowlist entry was removed and ZERO packets reached the deny
#   rule; without it, 0 KB/s within 10s and 12 packets on the counter.
#
#   macOS has no `ct state` line to delete, because pf is stateful by DEFAULT — a `pass` creates a
#   state and every later packet of that connection matches the state instead of the rules. That is
#   the same fast-path, welded in. pf's `no state` keyword is the off switch. If it works, the two
#   platforms converge completely: no `pfctl -k`, no D132 amendment, no macOS-only teardown step.
#
# WHAT RUN 1 ESTABLISHED, AND WHY THIS IS ROUND 2  (results/pf-no-state-run1.txt)
#   The prediction going into run 1 was that `no state` on the INGRESS rule alone would not be
#   enough, because every rule we write is `in` on the bridge: the host's reply travels `out`,
#   matches no rule, meets pf's default pass — and a passed packet creates state. pf states are
#   bidirectional, so that state then carries the guest's forward packets past rule evaluation.
#
#   Run 1 confirmed the mechanism exactly. Three states existed during a live flow and their source
#   was the GATEWAY, not the guest:
#       192.168.65.1:18651 -> 192.168.65.2:41632   TIME_WAIT
#       192.168.65.1:18651 -> 192.168.65.2:34104   ESTABLISHED
#   and the in-flight transfer held a flat 256 KB/s across the whole 30s window with the block
#   rule's counter frozen.
#
#   Run 1 then failed to test the fix, because its return-direction arm was gated on "did
#   correctness break". Correctness did not break — a return rule is not needed to make traffic
#   work, it is needed to stop pf minting state — so the arm was skipped and the section printed
#   "the prediction above is refuted" one screen above the census that confirmed it. Round 2 gates
#   the arm on the STATE CENSUS, which is the thing the prediction was actually about.
#
#   Run 2 (results/pf-no-state-run2-census-bug.txt) then measured the census wrong in three ways at
#   once, and reported a tidy 3/4/5 that was pure accumulation: it counted every state naming the
#   guest including NAT'd ones created outside the anchor, it never actually stopped the in-guest
#   curl so S1's states were still present when S2 and S3 were scored, and it ran the gateway
#   correctness probe onto the very endpoint the census counts. Round 3 splits the counter, kills
#   the stream in the guest, clears between probe and census, and voids any census that starts
#   dirty rather than scoring it.
#
#   Run 1 also exposed a second gap nothing had noticed. Every block rule in the interface-keyed
#   shape is `in` quick on the bridge, so the host's data travelling `out` to the guest is never
#   evaluated against a block at all. A download therefore has a permitted direction no matter what
#   the allowlist says. The retired address-keyed shape had `block return out quick to <src>` for
#   this (pf_revocation.sh R5); the interface-keyed rewrite dropped it and nothing caught that.
#
# THE THREE SHAPES, CHEAPEST TO DEFEND FIRST
#   S1  ingress `no state` only                      — measured in run 1: census 3, transfer survives
#   S2  + `pass out` on the bridge, scoped, no state  — does that alone zero the census?
#   S3  + `block drop out` on the bridge              — the complete bidirectional shape
#
#   Each is measured for CORRECTNESS (allowed reachable on both paths, denied refused) and for the
#   CENSUS (states naming the guest during a live flow). Revocation is then run on the cheapest
#   shape that reaches a zero census, and on S3 as well if the cheaper one still fails — because
#   "stateless is not enough" and "stateless plus an egress block is enough" are different answers.
#
#   S4 THE CONTROL   the winning shape with `no state` removed, which must SURVIVE revocation.
#                    Without it the experiment shows a transfer stopping, not `no state` stopping it.
#
# SAFETY: writes only into its own anchor; never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_n"
IMG=yoloai-base:latest
PORT=18651
NET=ybnsnet
G=ybns1
ALLOW=1.1.1.1
DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfns.XXXXXX)
SRVPID=""; CURLPID=""
SERIES=""
CLEAR_METHOD=""
LAST_ARM=""
VERDICT_SHAPE=""; VERDICT_RULES=""

RESULTS="$HERE/results/pf-no-state.txt"
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
# Flushing an anchor destroys TABLE MEMBERSHIP as well as rules, and a shape loaded with an empty
# allowlist enforces nothing while looking correct. pf-revocation-alt.txt T3 lost four arms to
# exactly that, so every load re-claims and the caller can check.
load()  { flush; printf '%s\n' "$1" > "$WD/n.rules"
          pfctl -a "$ANCHOR" -f "$WD/n.rules" 2>&1 | quiet_pf
          pfctl -a "$ANCHOR" -t yn_dst -T add "$GW" "$ALLOW" >/dev/null 2>&1; }
showrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null; }
held() { pfctl -a "$ANCHOR" -t yn_dst -T show 2>/dev/null | tr -d ' ' | grep -c . || true; }

# Per-rule counters, selected by rule TEXT and never by index: adding a return-direction rule
# shifts the block rule's index, and an index-based reader would go on quoting the pass rule's
# counter as the block rule's — silently, in the direction that manufactures a result.
rulestat() {   # $1 = substring identifying the rule, $2 = field (Evaluations|Packets|Bytes|States)
  pfctl -a "$ANCHOR" -vvs rules 2>/dev/null | awk -v pat="$1" -v f="$2:" '
    /^@/ { want = (index($0, pat) > 0); next }
    want && /Evaluations:/ {
      for (i=1;i<=NF;i++) if ($i==f) { print $(i+1); exit }
      exit
    }'
}
BLOCKIN="block drop in quick"
# Both blocks are read, and the verdict uses their SUM. S3's whole point is that the download
# direction was never evaluated against a block; when it finally is, the packets land on the OUT
# rule, and a reader watching only the IN rule sees a transfer stop against a frozen counter --
# which is the free negative this harness refuses to accept from anyone else.
BLOCKOUT="block drop out quick"

gtry() { local o; o=$(asuser container exec "$G" curl -s -o /dev/null -w '%{http_code}' \
           --max-time 5 "http://$1/" 2>/dev/null); printf '%s' "${o:-000}"; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
gwof() { netfield "$1" ipv4Gateway; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }
brofguest() { local g; g=$(gwof "$1"); [ -n "$g" ] && brof "$g"; }
egress() { route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}'; }
states() { pfctl -s state 2>/dev/null | grep -c "$1" || true; }
# Run 2's census counted EVERY state naming the guest and so measured accumulation: states left by
# earlier sections were still present, and each shape's own external probe added one more, giving a
# tidy 3/4/5 that had nothing to do with the rules under test.
#
# Two counters instead of one, because they answer different questions.
#   flow_states  states for the gateway flow — bridge-local, host-terminated, and the ONLY ones a
#                bridge-scoped rule can govern. This is the census.
#   nat_states   translated states (the `a -> b -> c` form) on the egress interface. They are created
#                outside this anchor by rules we do not own, so no `no state` on the bridge can
#                prevent them. Reported as a standing fact, never as a shape's score.
FLOWPAT=""   # what counts as "the flow under test"; defaults to the gateway stream
flow_states() { pfctl -s state 2>/dev/null | grep -c "${FLOWPAT:-$GW:$PORT}" || true; }
nat_states()  { pfctl -s state 2>/dev/null | grep "$IP" | grep -c -- '-> .* ->' || true; }

# A rate-controlled sink: 32 KB every 125 ms is 256 KB/s, sustained for 150s. Rate control matters
# because an unthrottled transfer finishes before the revocation lands, and "it stopped" would then
# be indistinguishable from "it completed".
start_server() {
  cat > "$WD/rate.py" <<'PY'
import http.server, socketserver, sys, time
CHUNK = 32768
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        blob = b"x" * CHUNK
        end = time.time() + 150
        try:
            while time.time() < end:
                self.wfile.write(b"%x\r\n" % CHUNK + blob + b"\r\n")
                self.wfile.flush()
                time.sleep(0.125)
            self.wfile.write(b"0\r\n\r\n")
        except Exception:
            pass
    def log_message(self, *a): pass
class S(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True
S(("0.0.0.0", int(sys.argv[1])), H).serve_forever()
PY
  python3 "$WD/rate.py" "$PORT" >/dev/null 2>&1 &
  SRVPID=$!
  sleep 1
  kill -0 "$SRVPID" 2>/dev/null
}

gbytes() { asuser container exec "$G" sh -c "wc -c < /tmp/$1 2>/dev/null || echo 0" 2>/dev/null \
           | tr -d ' '; }
# EVERY byte sample is taken through `container exec`, so the sampler shares a path with the thing
# under test. S3 adds `block drop out` on the bridge; if the backend's exec traffic rides bridge TCP
# from the gateway, revoking the gateway kills the SAMPLER, every sample reads 0, and the arm
# reports a textbook successful revocation while nothing was revoked. Checked rather than assumed.
exec_alive() { [ "$(asuser container exec "$G" sh -c 'echo ok' 2>/dev/null | tr -d ' \r\n')" = ok ]; }
stop_stream() { [ -n "$CURLPID" ] && kill "$CURLPID" 2>/dev/null; CURLPID=""; }
start_stream() {  # $1 = tag, $2 = host:port, $3 = curl --max-time
  stop_stream
  asuser container exec "$G" sh -c "rm -f /tmp/$1" >/dev/null 2>&1
  asuser container exec "$G" curl -sN --max-time "$3" "http://$2/" -o "/tmp/$1" >/dev/null 2>&1 &
  CURLPID=$!
}
# Killing CURLPID kills the host-side `sudo`, NOT the curl running inside the guest — that one runs
# on to its own --max-time, holding an ESTABLISHED state across every later section. Run 2 measured
# S1's leftovers three times for exactly this reason. So: kill it in the guest, wait for its state
# to go, then clear the residue and REPORT what is left, because a census that starts dirty is void.
clear_flow() {
  stop_stream
  # /proc walk last, because pkill and killall are both absent from plenty of minimal images and
  # their absence is silent — the stream would simply run on and void the next census.
  asuser container exec "$G" sh -c '
    pkill -9 curl 2>/dev/null
    killall -9 curl 2>/dev/null
    for c in /proc/[0-9]*/cmdline; do
      grep -qa curl "$c" 2>/dev/null && kill -9 "${c#/proc/}" 2>/dev/null
    done
    true' >/dev/null 2>&1
  sleep 2
  # `pfctl -k <host>` kills states SOURCED FROM that host. The state carrying a download is sourced
  # from the GATEWAY -- run 3 cleared with `-k <guest>` alone and left exactly one behind, every
  # time, which is the same asymmetry T3 found when gateway-first killed zero states. Escalating
  # through the forms and recording which one worked, because `-k` is the remedy the reaping design
  # currently prescribes and "which form reaps a download" is a design fact, not a harness detail.
  CLEAR_METHOD=""
  pfctl -k "$IP" >/dev/null 2>&1;            sleep 1
  if [ "$(flow_states)" -ne 0 ]; then
    pfctl -k "$IP" -k "$GW" >/dev/null 2>&1; sleep 1
    CLEAR_METHOD="-k guest -k gateway"
  else
    CLEAR_METHOD="-k guest"
  fi
  if [ "$(flow_states)" -ne 0 ]; then
    pfctl -k "$GW" -k "$IP" >/dev/null 2>&1; sleep 1
    CLEAR_METHOD="-k gateway -k guest"
  fi
  if [ "$(flow_states)" -ne 0 ]; then
    pfctl -F states >/dev/null 2>&1;         sleep 1
    CLEAR_METHOD="-F states (no -k form sufficed)"
  fi
}
series() {   # $1 = tag, $2 = samples, $3 = interval seconds. Leaves the KB/s series in SERIES.
  local prev cur i
  SERIES=""; prev=$(gbytes "$1")
  for ((i=1;i<=$2;i++)); do
    sleep "$3"
    cur=$(gbytes "$1")
    SERIES="$SERIES $(( (${cur:-0} - ${prev:-0}) / 1024 / $3 ))"
    prev=${cur:-0}
  done
}

# CORRECTNESS + CENSUS for whatever ruleset is currently loaded. Sets SH_OK / SH_STATES / SH_LIVE.
# The census has to be taken with a transfer ACTUALLY RUNNING; counting states on an idle host
# measures nothing and would report every shape as clean.
measure_shape() {   # $1 = tag used for the census stream
  SH_OK=no; SH_STATES=-1; SH_LIVE=0
  local a b c
  a=$(gtry "$GW:$PORT"); b=$(gtry "$ALLOW"); c=$(gtry "$DENY")
  note "correctness: gateway->$a  external(NAT'd)->$b  denied->$c   (need non-000, non-000, 000)"
  [ "$a" != 000 ] && [ "$b" != 000 ] && [ "$c" = 000 ] && SH_OK=yes
  # The gateway correctness probe above lands on the very endpoint the census counts and leaves a
  # TIME_WAIT there, so the slate is cleared AFTER probing and BEFORE the census, never once at the
  # top. Ordering was the third of run 2's three census defects.
  clear_flow
  local dirty; dirty=$(flow_states)
  note "slate cleared by: $CLEAR_METHOD"
  if [ "$dirty" -ne 0 ]; then
    note "census VOID: $dirty state(s) on $GW:$PORT survived even a full state flush:"
    pfctl -s state 2>/dev/null | grep "$GW:$PORT" | sed 's/^/            /'
    SH_OK=no; SH_STATES=-1; return
  fi
  start_stream "$1" "$GW:$PORT" 8
  sleep 4
  SH_LIVE=$(gbytes "$1")
  SH_STATES=$(flow_states)
  note "census: transfer at ${SH_LIVE:-0} bytes, states on the gateway flow = $SH_STATES"
  note "        (translated states on $EG, which no bridge rule can prevent: $(nat_states))"
  pfctl -s state 2>/dev/null | grep "$IP" | sed 's/^/          /'
  if ! exec_alive; then
    SH_OK=no; SH_STATES=-1
    note "  !! \`container exec\` is not working under this shape. The census and the byte count above"
    note "     are both taken through it, so neither means anything. Shape marked unusable."
  fi
  stop_stream
  sleep 1
}

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$SRVPID" ]  && kill "$SRVPID"  2>/dev/null
  [ -n "$CURLPID" ] && kill "$CURLPID" 2>/dev/null
  flush
  asuser container rm -f "$G" >/dev/null 2>&1
  asuser container network delete "$NET" >/dev/null 2>&1
  rm -rf "$WD"
  echo "   main-refs: before=${MAINREFS0:-?} after=$(mainrefs)   anchor rules: $(showrules | grep -c . || true)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | egress=$(egress)"

# ---------------------------------------------------------------------------
say "N0 SETUP — a per-sandbox network, which is the shape the rewritten design specifies"
MAINREFS0=$(mainrefs)
[ "$MAINREFS0" -gt 0 ] || { bad "host is fail-open (main-refs=0); run pf_anchor_eval.sh first. ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2
asuser container rm -f "$G" >/dev/null 2>&1
asuser container network delete "$NET" >/dev/null 2>&1
asuser container network create "$NET" >/dev/null 2>&1
asuser container run -d --name "$G" --network "$NET" "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield "$G" ipv4Address); GW=$(gwof "$G"); BR=$(brofguest "$G"); EG=$(egress)
note "guest=$IP  gateway=$GW  bridge=$BR  egress=$EG"
[ -n "$IP" ] && [ -n "$GW" ] && [ -n "$BR" ] || { bad "setup incomplete; ABORTING"; exit 1; }
start_server || { bad "the rate-controlled server would not start; ABORTING"; exit 1; }
note "rate-controlled server up on $GW:$PORT — 32 KB per 125 ms = 256 KB/s, 150s"

S1_RULES="table <yn_dst> persist
pass  in quick on $BR proto tcp from any to <yn_dst> no state
block drop in quick on $BR proto tcp from any to any"

S2_RULES="table <yn_dst> persist
pass  in  quick on $BR proto tcp from any to <yn_dst> no state
pass  out quick on $BR proto tcp from <yn_dst> to any no state
block drop in quick on $BR proto tcp from any to any"

S3_RULES="table <yn_dst> persist
pass  in  quick on $BR proto tcp from any to <yn_dst> no state
pass  out quick on $BR proto tcp from <yn_dst> to any no state
block drop in  quick on $BR proto tcp from any to any
block drop out quick on $BR proto tcp from any to any"

# ---------------------------------------------------------------------------
say "N1 DOES \`no state\` SURVIVE THE ROUND TRIP THROUGH pfctl?"
note "pfctl exiting 0 is not the question. This anchor accepts \`set timeout\` and silently ignores"
note "it (pf-revocation-alt.txt T1), so the only honest check is to read the rule back out."
load "$S1_RULES" | sed 's/^/        pfctl: /'
showrules | sed 's/^/          /'
if showrules | grep -q 'no state'; then
  ok "N1: pf accepted \`no state\` AND reports it back — the keyword is really in the ruleset"
else
  bad "N1: \`no state\` is NOT in the ruleset pf reports back. Everything below would be measuring a"
  note "     plain stateful ruleset. ABORTING."; exit 1
fi
note "allowlist holds $(held) address(es) after the load — a flushed anchor loses table membership,"
note "and an empty allowlist enforces nothing while the rules still read correctly."

# ---------------------------------------------------------------------------
say "N2 THE THREE SHAPES — correctness and state census for each"
note "Gated on the CENSUS, not on correctness. Run 1 asked the wrong question here: a return rule is"
note "not needed to make traffic work, it is needed to stop pf minting state on the reply. Correctness"
note "passed, the arm was skipped, and the section announced a refutation of the very mechanism the"
note "census on the next screen had just confirmed."
note ""
note "S1  ingress \`no state\` only — the shape run 1 measured"
measure_shape s1
S1_OK=$SH_OK; S1_STATES=$SH_STATES
note ""
note "S2  + a return rule scoped to the allowlist, also \`no state\`"
load "$S2_RULES" | sed 's/^/        pfctl: /'
measure_shape s2
S2_OK=$SH_OK; S2_STATES=$SH_STATES
note ""
note "S3  + \`block drop out\` — the complete bidirectional shape."
note "    Every block rule in the interface-keyed design is \`in\` on the bridge, so the host's data"
note "    travelling OUT to the guest is never evaluated against a block at all. A download has a"
note "    permitted direction regardless of the allowlist. The retired address-keyed shape carried"
note "    \`block return out quick to <src>\` for this; the rewrite dropped it and nothing caught it."
load "$S3_RULES" | sed 's/^/        pfctl: /'
measure_shape s3
S3_OK=$SH_OK; S3_STATES=$SH_STATES

note ""
note "               correctness   gateway-flow states during a live transfer"
note "  S1 in-only        $S1_OK              $S1_STATES"
note "  S2 +pass out      $S2_OK              $S2_STATES"
note "  S3 +block out     $S3_OK              $S3_STATES"

WINNER=""; WINNER_RULES=""
if [ "$S2_OK" = yes ] && [ "$S2_STATES" -eq 0 ]; then WINNER=S2; WINNER_RULES="$S2_RULES"
elif [ "$S3_OK" = yes ] && [ "$S3_STATES" -eq 0 ]; then WINNER=S3; WINNER_RULES="$S3_RULES"
fi
if [ -n "$WINNER" ]; then
  ok "N2: $WINNER is correct AND holds zero states during a live transfer — the stateless shape is"
  note "    actually achieved, not merely requested. Revocation is measured below, starting with it;"
  note "    a zero census is what makes the arm interpretable, not what makes it succeed."
else
  bad "N2: no shape tried reaches a zero census with working traffic. pf is minting state for this"
  note "     flow from somewhere none of these rules covers — the NAT'd state on $EG is the standing"
  note "     candidate, and it is created outside this anchor entirely."
fi

# ---------------------------------------------------------------------------
say "N3 REVOCATION OF AN IN-FLIGHT TRANSFER — the P1b shape"
note "Sustained transfer, remove the allowlist entry with a table delete and NOTHING ELSE — no"
note "\`pfctl -k\`, no reload — then sample the rate for 30s and read the block rule's counter."
note "Linux, for comparison: fast-path 300 KB/s for 30s with counter 0; no fast-path 0 KB/s inside"
note "10s with counter 12. A rate of zero with a zero counter is a free negative, not a result."

RESULT_NOSTATE=untested
revoke_arm() {   # $1 = label, $2 = ruleset. Sets ARM_VERDICT and ARM_SERIES.
  ARM_VERDICT=untested; ARM_SERIES=""; LAST_ARM="$1"
  load "$2" >/dev/null 2>&1
  [ "$(held)" -ge 2 ] || { note "$1: allowlist did not re-claim after the load; arm is void"; return; }
  clear_flow
  local dirty; dirty=$(flow_states)
  if [ "$dirty" -ne 0 ]; then
    note "$1: $dirty stale state(s) on ${FLOWPAT:-$GW:$PORT} before the arm even starts; arm void"
    pfctl -s state 2>/dev/null | grep "${FLOWPAT:-$GW:$PORT}" | sed 's/^/            /'
    return
  fi
  start_stream "rev$1" "$GW:$PORT" 60
  sleep 5; local p1; p1=$(gbytes "rev$1")
  sleep 3; local p2; p2=$(gbytes "rev$1")
  note "$1 before revocation: $p1 then $p2 bytes; states on the gateway flow: $(flow_states)"
  if [ "${p2:-0}" -le "${p1:-0}" ]; then
    note "$1: the transfer was not live before the revocation; this arm proves nothing"
    return
  fi
  local b4i b4o b4; b4i=$(rulestat "$BLOCKIN" Packets); b4o=$(rulestat "$BLOCKOUT" Packets)
  b4=$(( ${b4i:-0} + ${b4o:-0} ))
  pfctl -a "$ANCHOR" -t yn_dst -T delete "$GW" >/dev/null 2>&1
  note "$1 revoked ($(held) address(es) left in the allowlist)"
  series "rev$1" 10 3
  local afi afo af; afi=$(rulestat "$BLOCKIN" Packets); afo=$(rulestat "$BLOCKOUT" Packets)
  af=$(( ${afi:-0} + ${afo:-0} ))
  ARM_SERIES=$SERIES
  note "$1 KB/s over the 30s after revocation:$SERIES"
  note "$1 block counters — in: ${b4i:-0} -> ${afi:-0}   out: ${b4o:-0} -> ${afo:-0}   (out is '-' when"
  note "   the shape has no out-block rule);  gateway-flow states now: $(flow_states)"
  if exec_alive; then
    note "$1: \`container exec\` still answers under this shape — the backend's control plane does not"
    note "   ride bridge TCP, so an egress block on the bridge does not cost us exec."
  fi
  if ! exec_alive; then
    ARM_VERDICT=void-sampler
    note "$1: \`container exec\` STOPPED WORKING during this arm. Every byte sample is taken through"
    note "    it, so the zeros above are the sampler dying, not the transfer stopping. Arm void —"
    note "    and any rule shape that breaks exec is unusable in the product regardless."
    stop_stream; return
  fi
  local last=${SERIES##* }
  local delta=$(( ${af:-0} - ${b4:-0} ))
  if [ "${last:-1}" -eq 0 ] && [ "$delta" -gt 0 ]; then ARM_VERDICT=stops
  elif [ "${last:-1}" -eq 0 ];                     then ARM_VERDICT=stops-no-counter
  else                                                  ARM_VERDICT=survives
  fi
  stop_stream
}

if [ -n "$WINNER" ]; then
  revoke_arm "$WINNER" "$WINNER_RULES"
  RESULT_NOSTATE=$ARM_VERDICT
  VERDICT_SHAPE="$WINNER"; VERDICT_RULES="$WINNER_RULES"
  case "$ARM_VERDICT" in
    stops) ok "N3/$WINNER: the transfer stopped AND packets reached the block rule — the counter is"
           note "         what makes this a result rather than a coincidence." ;;
    stops-no-counter)
           unk "N3/$WINNER: the rate fell to zero but the block counter did not move. That is the free"
           note "         negative: something stopped the transfer and nothing shows the deny rule did it." ;;
    survives) bad "N3/$WINNER: the transfer SURVIVED revocation with a zero-state ruleset." ;;
    void-sampler) unk "N3/$WINNER: the shape broke \`container exec\`, so the arm measured its own sampler." ;;
    *)     unk "N3/$WINNER: arm void" ;;
  esac
  # If the cheap shape held zero states but still could not revoke, the missing piece is the egress
  # block rather than statefulness, and that is a different sentence in the design.
  if [ "$WINNER" = S2 ] && [ "$ARM_VERDICT" = survives ]; then
    note ""
    note "S2 held no state and still could not revoke, so the survival is not about state at all."
    note "Re-running on S3, which adds the missing \`block drop out\`:"
    revoke_arm S3 "$S3_RULES"
    RESULT_NOSTATE=$ARM_VERDICT
    VERDICT_SHAPE=S3; VERDICT_RULES="$S3_RULES"
    case "$ARM_VERDICT" in
      stops) ok "N3/S3: revocation is immediate once BOTH directions are blocked. The missing piece"
             note "        was the egress block, not the state table." ;;
      survives) bad "N3/S3: the transfer survived even with both directions blocked and no state." ;;
      stops-no-counter)
             unk "N3/S3: the rate fell to zero but NEITHER block rule's counter moved. Something other"
             note "        than our policy stopped it, and that has to be found before S3 is quoted." ;;
      void-sampler) unk "N3/S3: \`block drop out\` broke \`container exec\`. That is a product-blocking"
                    note "        fact about the shape, independent of what it does to revocation." ;;
      *) unk "N3/S3: inconclusive" ;;
    esac
  fi
else
  unk "N3: no zero-state shape to revoke on; skipped rather than run on a shape N2 rejected."
fi

# ---------------------------------------------------------------------------
say "N3b THE NAT'd PATH — the destination class the product actually revokes"
note "Both arms above run against the gateway, which the host TERMINATES: those packets never reach"
note "$EG and are never translated. The product revokes EXTERNAL destinations, and run 1 found the"
note "NAT'd flow carrying a state created on $EG by rules outside this anchor entirely. pf's default"
note "state policy is floating, so if that state can match the guest's packets on the bridge, no"
note "bridge-scoped rule can revoke an external destination and S3's result does not transfer to the"
note "case that matters. Untested, this is the single largest gap in the section above."
note ""
note "Best effort: it needs a real external endpoint over plain HTTP. If none streams, the section"
note "reports NOT RUN rather than inventing a verdict."
NAT_RESULT=untested
EXTIP=""; EXTHOST=""; EXTPATH=""
for cand in "ipv4.download.thinkbroadband.com /200MB.zip" "speedtest.tele2.net /100MB.zip"; do
  h=${cand%% *}; pth=${cand##* }
  ip=$(python3 -c 'import socket,sys
try: print(socket.gethostbyname(sys.argv[1]))
except Exception: pass' "$h" 2>/dev/null)
  [ -n "$ip" ] || { note "could not resolve $h"; continue; }
  note "trying $h ($ip)$pth"
  load "$S3_RULES" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t yn_dst -T add "$ip" >/dev/null 2>&1
  FLOWPAT="$ip"
  clear_flow
  asuser container exec "$G" sh -c "rm -f /tmp/natrev" >/dev/null 2>&1
  # --limit-rate is what makes this a revocation test rather than a download test. Without it the
  # 200MB file completed in 10s and the section read a flat zero across the whole window -- the
  # transfer had simply finished, and its state was already FIN_WAIT_2 when the revocation landed.
  # Throttling client-side keeps the connection open and the packets flowing for the full 30s, and
  # matches the 256 KB/s the gateway server is rate-controlled to.
  asuser container exec "$G" curl -sN --max-time 90 --limit-rate 256k -H "Host: $h" \
    "http://$ip$pth" -o /tmp/natrev >/dev/null 2>&1 &
  CURLPID=$!
  sleep 6; e1=$(gbytes natrev)
  sleep 4; e2=$(gbytes natrev)
  note "  after 10s: $e1 then $e2 bytes"
  if [ "${e2:-0}" -gt "${e1:-0}" ]; then EXTIP=$ip; EXTHOST=$h; EXTPATH=$pth; stop_stream; break; fi
  stop_stream
done

# One external arm per ruleset, so the NAT'd path gets the same 2x2 the gateway path got. Without
# the stateful arm, "the block rules alone suffice for external destinations" stays unrefuted.
ext_arm() {   # $1 = label, $2 = ruleset. Sets EXT_VERDICT.
  EXT_VERDICT=untested
  load "$2" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t yn_dst -T add "$EXTIP" >/dev/null 2>&1
  FLOWPAT="$EXTIP"
  clear_flow
  asuser container exec "$G" sh -c "rm -f /tmp/nat$1" >/dev/null 2>&1
  asuser container exec "$G" curl -sN --max-time 90 --limit-rate 256k -H "Host: $EXTHOST" \
    "http://$EXTIP$EXTPATH" -o "/tmp/nat$1" >/dev/null 2>&1 &
  CURLPID=$!
  sleep 6; local x1; x1=$(gbytes "nat$1")
  sleep 4; local x2; x2=$(gbytes "nat$1")
  note "$1: $x1 then $x2 bytes; states naming $EXTIP: $(flow_states)"
  pfctl -s state 2>/dev/null | grep "$EXTIP" | head -3 | sed 's/^/            /'
  if [ "${x2:-0}" -le "${x1:-0}" ]; then
    note "$1: the external transfer was not live; arm void"; stop_stream; FLOWPAT=""; return
  fi
  local bi bo ai ao
  bi=$(rulestat "$BLOCKIN" Packets); bo=$(rulestat "$BLOCKOUT" Packets)
  pfctl -a "$ANCHOR" -t yn_dst -T delete "$EXTIP" >/dev/null 2>&1
  series "nat$1" 10 3
  ai=$(rulestat "$BLOCKIN" Packets); ao=$(rulestat "$BLOCKOUT" Packets)
  note "$1 KB/s after revocation:$SERIES"
  note "$1 block counters — in: ${bi:-0} -> ${ai:-0}   out: ${bo:-0} -> ${ao:-0};  states now: $(flow_states)"
  local nlast=${SERIES##* }
  local nd=$(( ${ai:-0} + ${ao:-0} - ${bi:-0} - ${bo:-0} ))
  if ! exec_alive;                                     then EXT_VERDICT=void-sampler
  elif [ "${nlast:-1}" -eq 0 ] && [ "$nd" -gt 0 ];     then EXT_VERDICT=stops
  elif [ "${nlast:-1}" -eq 0 ];                        then EXT_VERDICT=stops-no-counter
  else                                                      EXT_VERDICT=survives
  fi
  note "$1 verdict: $EXT_VERDICT"
  stop_stream; FLOWPAT=""
}

if [ -z "$EXTIP" ]; then
  FLOWPAT=""
  unk "N3b: no external endpoint streamed over plain HTTP; NOT RUN. The gateway result therefore"
  note "     stands only for host-terminated destinations, which is not the product case."
else
  note "endpoint: $EXTHOST ($EXTIP)$EXTPATH, throttled to 256 KB/s client-side"
  note "  (unthrottled, the 200MB file COMPLETED in 10s and the window read a flat zero that was"
  note "   the download finishing, not a revocation -- the free negative this arm nearly shipped)"
  note ""
  note "N3b-1  S3, stateless + both blocks"
  ext_arm S3 "$S3_RULES"
  NAT_RESULT=$EXT_VERDICT
  note ""
  note "N3b-2  the same rules with \`no state\` removed — the control, which must SURVIVE"
  ext_arm CTL "$(printf '%s\n' "$S3_RULES" | sed 's/ no state$//')"
  NAT_CTL=$EXT_VERDICT
  note ""
  if [ "$NAT_RESULT" = stops ] && [ "$NAT_CTL" = survives ]; then
    ok "N3b: the NAT'd path behaves exactly as the gateway path — S3 revokes it, the stateful control"
    note "     does not, and the translated state on $EG does not rescue the flow. The result transfers"
    note "     to the destination class the product actually uses."
  elif [ "$NAT_RESULT" = stops ]; then
    unk "N3b: S3 revoked the NAT'd path but the control did not survive ($NAT_CTL), so the two arms"
    note "     do not separate and \`no state\` is not shown to be load-bearing on this path."
  elif [ "$NAT_RESULT" = survives ]; then
    bad "N3b: the NAT'd path SURVIVED revocation under S3. The state created on $EG outside this anchor"
    note "     is carrying it, and no bridge-scoped rule can reach it. S3's gateway result does NOT"
    note "     transfer to external destinations, which is the only class the product revokes."
  else
    unk "N3b: inconclusive (S3=$NAT_RESULT control=$NAT_CTL) — an external endpoint can stop for its"
    note "     own reasons, so a zero rate without counter movement is not usable either way."
  fi
fi

# ---------------------------------------------------------------------------
say "N4 THE CONTROL — the VERDICT shape with \`no state\` REMOVED, which must SURVIVE"
note "Without this arm the experiment shows a transfer stopping, not \`no state\` stopping it. Same"
note "protocol, same path, same destination, same rules — one keyword different."
note ""
note "Built from the arm that produced the verdict, which is not always the census winner. Run 5"
note "controlled S2 while the result came from S3, leaving the one cell that decides the design"
note "unmeasured: stateful WITH the egress block. If that cell also stops, the egress block is doing"
note "all the work and \`no state\` is decoration."
RESULT_STATEFUL=untested
if [ -n "${VERDICT_SHAPE:-}" ]; then
  CTL_RULES=$(printf '%s\n' "${VERDICT_RULES}" | sed 's/ no state$//')
  note "controlling shape $VERDICT_SHAPE"
  load "$CTL_RULES" >/dev/null 2>&1
  note "the control anchor, read back (there must be NO \`no state\` here):"
  showrules | sed 's/^/          /'
  if showrules | grep -q 'no state'; then
    unk "N4: the control still contains \`no state\`; it is not a control. Skipped."
  else
    revoke_arm CTL "$CTL_RULES"
    RESULT_STATEFUL=$ARM_VERDICT
    if [ "$ARM_VERDICT" = survives ]; then
      ok "N4: the stateful control SURVIVED revocation, as R2/R3/R5a found. The 2x2 is complete and"
      note "     \`no state\` is load-bearing: with it the same rules revoke, without it they do not."
    else
      bad "N4: the stateful control ALSO stopped ($ARM_VERDICT). Then \`no state\` is NOT what bought"
      note "     the revocation — the egress block did it alone, and pf keeps its state table. That is"
      note "     a cheaper fix than statelessness and a different sentence in the design, so it must be"
      note "     stated as such rather than filed under a convergence that did not happen."
    fi
  fi
else
  unk "N4: no winning shape to build a control from."
fi

# ---------------------------------------------------------------------------
say "N5 VERDICT — derived from the arms above, not asserted"
note "  zero-state shape achieved:   ${WINNER:-none}"
note "  states under S1 / S2 / S3:   $S1_STATES / $S2_STATES / $S3_STATES"
note "  verdict shape:               ${VERDICT_SHAPE:-none}"
note "  no-state arm (gateway path): $RESULT_NOSTATE"
note "  NAT'd path arm / control:     ${NAT_RESULT:-untested} / ${NAT_CTL:-untested}"
note "  stateful control arm:        $RESULT_STATEFUL"
if [ "$RESULT_NOSTATE" = stops ] && [ "$RESULT_STATEFUL" = survives ]; then
  ok "N5: pf CAN be made to evaluate policy on every packet, and revocation is then immediate with a"
  note "    table delete alone — which is already inside D132's shipped grant. macOS loses its"
  note "    state-teardown asymmetry: no \`pfctl -k\`, no grant widening, no gateway-pinning tension."
  note "    The price is the shape ${VERDICT_SHAPE:-?}, which is more than one line: BOTH directions need"
  note "    a \`no state\` pass rule AND a block. The complete 2x2 measured above:"
  note "        stateless, no egress block  -> survives      stateless + egress block  -> STOPS"
  note "        stateful,  no egress block  -> survives      stateful  + egress block  -> survives"
  note "    so neither ingredient is sufficient alone, and the egress block is the one the"
  note "    interface-keyed rewrite does not currently have."
  note ""
  note ""
  note "    Both destination classes were measured, each with its own control: the host-terminated"
  note "    gateway path and the NAT'd external path (N3b: ${NAT_RESULT:-untested} / control"
  note "    ${NAT_CTL:-untested}). The translated state on $EG survives the revocation and does not"
  note "    rescue the flow, which was the open question about pf's floating state policy."
elif [ "$RESULT_NOSTATE" = survives ]; then
  bad "N5: \`no state\` does NOT buy revocation on macOS. The asymmetry stands, \`pfctl -k\` stays in"
  note "    the design, and the D132 permit/refuse matrix for a two-endpoint kill is back on the list."
else
  unk "N5: not decided by this run — read the arm verdicts above rather than this line."
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - UDP. Every rule and every probe here is TCP. pf's stateless handling of UDP is a separate
          question and DNS rides on it.
        - Throughput cost. Linux measured allowlist sizes 1/1000/10000 and found run-to-run variance
          larger than any difference between configurations. Nothing here measures what per-packet
          evaluation costs on pf at any allowlist size, so "it costs nothing" is a Linux result only.
        - `set state-policy if-bound`. Run 1 found a NAT'd state on the egress interface
          (guest -> host-lan -> 1.1.1.1) created outside this anchor entirely. If pf's default
          floating state policy lets that state match the flow on the bridge, that is a second way
          to defeat `no state` that no bridge-scoped rule can close. An anchor may well ignore
          `set state-policy` the way it ignores `set timeout`.
        - A RATE-CONTROLLED NAT'd destination. N3b uses a public plain-HTTP file server, so its
          throughput is whatever the internet gives and a stop can have causes this rig cannot see.
          That is why its no-counter case is reported as unusable rather than as a success.
        - What `block drop out` costs. It blocks host-initiated traffic to the sandbox as well as
          replies, and the credential broker's injector endpoint lives on the gateway address. If S3
          is the shape that works, that interaction is a design question, not a detail.
        - Concurrency. One sandbox, one flow. Whether these rules disturb a second sandbox was
          measured for the stateful shape (pf-revocation-alt.txt T4) and not here.
        - Long-lived idle connections, which is where state timeouts would have mattered.
EOF
