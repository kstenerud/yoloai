#!/bin/bash
# ABOUTME: Does removing an address from an allowlist stop traffic that is ALREADY flowing?
# ABOUTME: pf matches established connections against its state table, not against the rules.
#
# Run: sudo bash pf_revocation.sh
#
# WHY THIS EXISTS
#   pf is stateful. A `pass` rule creates a state entry, and every subsequent packet of that
#   connection is matched against the STATE, not re-evaluated against the rules. So deleting an
#   address from a `dst` table may change nothing for a connection already open.
#
#   Both designs in flight assume otherwise. enforcement-state-reaping.md reclaims a dead sandbox's
#   entries and treats the reclamation as the moment enforcement changes. A liveness detector that
#   spots a fault and reinstalls the correct rules assumes the reinstall takes effect. If open
#   connections survive, then:
#
#     - revoking an allowlist entry is not a revocation, it is a change of future policy;
#     - a long-lived connection — an agent streaming from a websocket, an SSH session, a large
#       download — outlives the policy that permitted it, for as long as it stays open;
#     - the reaping design's window is not "until the sweep runs" but "until the last connection
#       that predates the sweep closes", which nothing bounds.
#
#   R1 BASELINE      the stream runs to completion while permitted. Without this a stream that dies
#                    for its own reasons would read as a successful revocation.
#   R2 REVOKE        delete the entry mid-stream and watch whether bytes keep arriving.
#   R3 THE CONTROL   a NEW connection after the revocation must FAIL. This is what separates "the
#                    state survived" from "the delete silently did nothing", and they are
#                    indistinguishable without it.
#   R4 THE FIX       `pfctl -k` kills matching states. Does it stop the stream, and is it in the
#                    shipped grant? (It is not — so the cost is a grant change, which is a
#                    security-boundary decision and not a code detail.)
#
# SAFETY: writes only into its own anchor; never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_r"
IMG=yoloai-base:latest
PORT=18643
SLOTS=4; SLOT=1
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfr.XXXXXX)
SRVPID=""; CURLPID=""

RESULTS="$HERE/results/pf-revocation.txt"
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
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }

# Streams one line per second for 40s, so progress is observable while it is still open.
start_server() {
  cat > "$WD/stream.py" <<'PY'
import http.server, socketserver, sys, time
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            for i in range(40):
                chunk = ("tick %03d\n" % i).encode()
                self.wfile.write(b"%x\r\n" % len(chunk) + chunk + b"\r\n")
                self.wfile.flush()
                time.sleep(1)
            self.wfile.write(b"0\r\n\r\n")
        except Exception:
            pass
    def log_message(self, *a): pass
class S(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
S(("0.0.0.0", int(sys.argv[1])), H).serve_forever()
PY
  python3 "$WD/stream.py" "$PORT" >/dev/null 2>&1 &
  SRVPID=$!
  sleep 1
  kill -0 "$SRVPID" 2>/dev/null
}

states() { pfctl -s state 2>/dev/null | grep -c "$1" || true; }

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null
  [ -n "$CURLPID" ] && kill "$CURLPID" 2>/dev/null
  flush
  asuser container rm -f ybr1 >/dev/null 2>&1
  rm -rf "$WD"
  echo "   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)  main-refs: $(mainrefs)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "R0 SETUP"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2
asuser container rm -f ybr1 >/dev/null 2>&1
asuser container run -d --name ybr1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybr1 ipv4Address); GW=$(netfield ybr1 ipv4Gateway)
note "guest=$IP  host as seen from the guest=$GW"
[ -n "$IP" ] && [ -n "$GW" ] || { bad "setup incomplete; ABORTING"; exit 1; }
start_server || { bad "the streaming server would not start; ABORTING"; exit 1; }
note "streaming server up on $GW:$PORT (one line per second, 40s)"

flush
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block return in quick from <yb_src_$i> to any"
  done; } > "$WD/pool.rules"
pfctl -a "$ANCHOR" -f "$WD/pool.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$IP" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$GW" >/dev/null 2>&1

r=$(asuser container exec ybr1 curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$GW:$PORT/" 2>/dev/null)
d=$(asuser container exec ybr1 curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://1.0.0.1/" 2>/dev/null)
note "allowed (the host) -> $r    denied (1.0.0.1) -> $d   (need non-000 then 000)"
if [ "$r" != 000 ] && [ "$d" = 000 ]; then
  ok "R0: enforcement is live and the stream endpoint is the allowlisted destination"
else
  bad "R0: no enforcement baseline (allowed=$r denied=$d). ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "R1 BASELINE — the stream runs to completion while the entry is present"
note "Without this, a stream that stops for its own reasons would read as a revocation working."
# Two fixes here, both learned from run 1 reading a live stream as 0 bytes.
#   1. Backgrounded on the HOST. Run 1 used `sh -c "curl ... &"` inside the guest, and that process
#      died with its `container exec` session.
#   2. `-N`. Without it curl buffers its output, and at ~9 bytes/second the buffer never fills, so
#      the file stays empty for the whole run while the transfer is in fact progressing normally.
#      Diagnosed by confirming the guest reached the port (200), the exec was alive, and a curl
#      process was running — all true while the file sat at 0 bytes.
asuser container exec ybr1 curl -sN --max-time 25 "http://$GW:$PORT/" -o /tmp/base.txt \
  >/dev/null 2>&1 &
CURLPID=$!
sleep 5
n1=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/base.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
sleep 6
n2=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/base.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
note "bytes received at +5s: $n1     at +11s: $n2"
if [ "${n2:-0}" -gt "${n1:-0}" ]; then
  ok "R1: the stream is genuinely live and growing while permitted"
else
  bad "R1: the stream did not grow ($n1 -> $n2); R2 would be meaningless. ABORTING"; exit 1
fi
kill "$CURLPID" 2>/dev/null; CURLPID=""
sleep 1

# ---------------------------------------------------------------------------
say "R2 REVOKE MID-STREAM — the question"
asuser container exec ybr1 curl -sN --max-time 30 "http://$GW:$PORT/" -o /tmp/rev.txt \
  >/dev/null 2>&1 &
CURLPID=$!
sleep 5
before=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/rev.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
ST=$(states "$IP")
note "before revocation: $before bytes received, $ST pf state(s) mentioning $IP"
note "revoking: pfctl -a $ANCHOR -t yb_dst_$SLOT -T delete $GW"
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T delete "$GW" 2>&1 | sed 's/^/        pfctl: /'
held=$(pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T show 2>/dev/null | tr -d ' ' | grep -c . || true)
note "allowlist now holds $held address(es) — the revocation is applied"
sleep 8
after=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/rev.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
note "8s after revocation: $after bytes (was $before)"

# ---------------------------------------------------------------------------
say "R3 THE CONTROL — a NEW connection must now fail"
note "Without this, 'the stream kept running' cannot be told apart from 'the delete did nothing'."
newc=$(asuser container exec ybr1 curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        "http://$GW:$PORT/" 2>/dev/null)
note "new connection to the same endpoint: $newc   (must be 000)"
if [ "$newc" != 000 ]; then
  unk "R3: the revocation did not take effect at all (new connection got $newc); R2 proves nothing"
elif [ "${after:-0}" -gt "${before:-0}" ]; then
  bad "R2/R3: THE STREAM SURVIVED REVOCATION. New connections are refused, but the connection that"
  note "     predated the delete kept receiving data. Removing an allowlist entry changes FUTURE"
  note "     policy only. So a reclaimed slot is not a stopped sandbox, and the exposure window is"
  note "     bounded by the lifetime of the longest open connection — which nothing bounds."
else
  ok "R2/R3: the stream stopped at revocation AND new connections are refused — revocation is"
  note "     immediate, and both designs' assumption holds."
fi

# ---------------------------------------------------------------------------
say "R4 THE FIX — does killing states stop it, and what does that cost?"
note "pfctl -k kills state entries. If R2 showed survival, this is the missing step in every"
note "revocation path: reaping, liveness reinstall, and sandbox teardown alike."
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$GW" >/dev/null 2>&1
asuser container exec ybr1 sh -c 'rm -f /tmp/k.txt' >/dev/null 2>&1
kill "$CURLPID" 2>/dev/null
asuser container exec ybr1 curl -sN --max-time 30 "http://$GW:$PORT/" -o /tmp/k.txt \
  >/dev/null 2>&1 &
CURLPID=$!
sleep 5
kb=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/k.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
note "stream re-established: $kb bytes; states mentioning $IP: $(states "$IP")"
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T delete "$GW" >/dev/null 2>&1
out=$(pfctl -k "$IP" 2>&1 | head -2 | tr '\n' ' ')
note "pfctl -k $IP -> ${out:-<no output>}"
note "states now: $(states "$IP")"
sleep 6
ka=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/k.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
note "6s after the kill: $ka bytes (was $kb)"
if [ "${ka:-0}" -le "${kb:-0}" ]; then
  ok "R4: killing states DOES stop an established connection — the fix exists and works"
else
  bad "R4: the stream survived even a state kill ($kb -> $ka)"
fi
note ""
note "and what it costs at the security boundary — the shipped D132 grant permits four forms:"
note "  -a <anchor> -t <table> -T add|delete|flush|show ...   /   -a <anchor> -f <pinned file>"
note "  -a <anchor> -s rules                                  /   -s info"
note "\`-k\` is NONE of them, and it is not scoped to an anchor: 'pfctl -k <host>' reaches every"
note "state on the machine, including the user's own. Granting it is a materially wider permission"
note "than table membership, so if R2 showed survival, the fix is a D132 amendment and not a patch."

say "R5 WHY DID IT SURVIVE THE KILL? — diagnosing, not asserting"
note "R4 killed every state naming the guest and the transfer still advanced. That should not happen"
note "if the block rule is reached, so something is re-permitting the flow. The leading hypothesis:"
note "the pool's rules are all \`in\`. The HOST's outbound data is \`out\` on the bridge, matches no"
note "rule, meets pf's default pass — and a passed packet CREATES STATE. That new state then carries"
note "the rest of the connection, including the guest's replies, straight past the inbound block."
note ""
note "If that is right, a rule covering the return direction closes it. Tested, with the control that"
note "legitimate allowed traffic must still work — a fix that blocks everything is not a fix."
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$GW" >/dev/null 2>&1
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block return in quick from <yb_src_$i> to any"
    echo "block return out quick to <yb_src_$i>"
  done; } > "$WD/pool2.rules"
pfctl -a "$ANCHOR" -f "$WD/pool2.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$IP"  >/dev/null 2>&1
pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$GW"  >/dev/null 2>&1
ctl=$(asuser container exec ybr1 curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$GW:$PORT/" 2>/dev/null)
note "CONTROL with the return-direction rule loaded: allowed destination -> ${ctl:-000}"
note "   (must still be non-000, or the rule is over-blocking and cannot be used)"
if [ "${ctl:-000}" = 000 ]; then
  unk "R5: the return-direction rule broke legitimate traffic; hypothesis untested"
else
  kill "$CURLPID" 2>/dev/null
  asuser container exec ybr1 sh -c 'rm -f /tmp/r5.txt' >/dev/null 2>&1
  asuser container exec ybr1 curl -sN --max-time 30 "http://$GW:$PORT/" -o /tmp/r5.txt \
    >/dev/null 2>&1 &
  CURLPID=$!
  sleep 5
  b5=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/r5.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
  note "stream running: $b5 bytes; states naming the guest: $(states "$IP")"
  note "states, verbatim, before the revocation:"
  pfctl -s state 2>/dev/null | grep "$IP" | head -4 | sed 's/^/          /'
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T delete "$GW" >/dev/null 2>&1
  pfctl -k "$IP" >/dev/null 2>&1
  note "after revoke + kill, states naming the guest: $(states "$IP")"
  pfctl -s state 2>/dev/null | grep "$IP" | head -4 | sed 's/^/          /'
  sleep 6
  a5=$(asuser container exec ybr1 sh -c 'wc -c < /tmp/r5.txt 2>/dev/null || echo 0' 2>/dev/null | tr -d ' ')
  note "6s later: $a5 bytes (was $b5)"
  if [ "${a5:-0}" -le "${b5:-0}" ]; then
    ok "R5: HYPOTHESIS CONFIRMED. Covering the return direction stops the transfer, so what kept it"
    note "    alive was a state created by the unmatched OUTBOUND packet. That makes the pool's"
    note "    in-only rules incomplete for revocation, and the fix is a rule rather than a grant —"
    note "    materially cheaper than widening D132 to allow \`pfctl -k\`."
  else
    bad "R5: the transfer survived even with the return direction covered ($b5 -> $a5). The"
    note "    hypothesis is wrong and the mechanism is still unexplained — do not design against it."
  fi
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - UDP, and long-lived idle connections. Only an actively streaming TCP connection was used;
          pf's state timeouts differ per protocol and per state, and an idle TCP state can persist
          for hours by default.
        - `pfctl -K` (kill by both endpoints), which would be narrower than `-k` and might be a
          more defensible grant. Not tried, and it is the obvious next question if -k is too wide.
        - Whether reloading the pool ruleset (the liveness repair path) drops states by itself.
          That would make repair disruptive to innocent sandboxes, and it is untested.
        - State timeouts as an alternative to killing: a short `tcp.established` timeout in the
          anchor would bound the survival window without any new grant. Not measured.
EOF
