#!/bin/bash
# ABOUTME: The two X2 remedies nobody measured — a state timeout in the anchor, and pfctl -K.
# ABOUTME: Decides whether D132 must widen to permit a state kill, or can avoid it entirely.
#
# Run: sudo bash pf_revocation_alt.sh
#
# WHY THIS EXISTS
#   pf_revocation.sh settled that revoking an allowlist entry does not stop an established flow, and
#   that the return-direction rule does not either: only rule + `pfctl -k` stops it. That leaves a
#   security-boundary decision open, and D132 currently records the wrong reason for avoiding it.
#   Three candidate remedies, and only one of them has been measured:
#
#     `pfctl -k <guest>`   MEASURED, works. Not in the grant, and not anchor-scoped — the grant that
#                          permits it permits killing state anywhere on the machine.
#     `pfctl -K ...`       UNMEASURED. Named in two runs' WHAT WAS NOT TRIED as "narrower, and the
#                          obvious next question". Nobody has run it or read what it does here.
#     a short state timeout UNMEASURED, and the only candidate that needs NO NEW GRANT at all — it
#                          would live in the pinned ruleset D132 already permits one write of. If it
#                          works, the whole amendment disappears.
#
#   T1 CAN THE ANCHOR HOLD A TIMEOUT AT ALL? `set` is a ruleset option and pf.conf documents options
#      as belonging to the main ruleset. If an anchor refuses them, the no-grant remedy is dead in
#      the only place D132 can write, and what remains is a change to the HOST's global pf timeouts —
#      a wider blast radius than the grant it was meant to avoid. Asked first because everything
#      below it is moot if it fails.
#   T2 DOES A TIMEOUT BOUND THE CASE THAT MATTERS? A `tcp.established` timer is reset by traffic, so
#      it expires idle states, not busy ones — and the threat model is an agent actively moving data,
#      which keeps its own state fresh. Measured directly off pf's `expires` counter rather than
#      argued, and measured on BOTH an active and an idle state, because the active one alone would
#      look like a limitation of the test.
#   T3 pfctl -K, AND THE TWO-HOST FORM. Does either stop the flow, and is either narrower in a way a
#      sudoers rule could express? "Narrower" is the entire case for preferring it, and it is
#      currently an assumption about a grant nobody has written.
#   T4 SCOPE, MEASURED. A second sandbox streams throughout. A kill aimed at the first must leave it
#      untouched, or the remedy is not per-sandbox and no grant shape saves it.
#
# METHOD: every verdict is computed from a recorded value. Byte counts come from a host-backgrounded
#   `curl -N` writing into the guest, because a guest-side `curl &` dies with its exec session and
#   curl buffers to a file — an earlier run read a live stream as 0 bytes for both reasons at once.
#
# SAFETY: writes only into its own anchor and NEVER into the main ruleset — in particular it does not
#   change the host's global pf timeouts, which is why T1's negative is left as a bounded negative
#   rather than pursued. Asserts the main-ruleset reference count on exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_ra"
IMG=yoloai-base:latest
PORT=18644
T2_WINDOW=20
SLOTS=4; SLOT=1; SLOT2=2
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfra.XXXXXX)
SRVPID=""; CURLPID=""; CURLPID2=""; NCPID=""; IDLEPID=""
MAINREFS0=0

RESULTS="$HERE/results/pf-revocation-alt.txt"
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
states() { pfctl -s state 2>/dev/null | grep -c "$1" || true; }
# pf prints "age HH:MM:SS, expires in HH:MM:SS" under each state with -vv. The expiry is what a
# timeout would move, so it is read directly instead of inferred from whether a flow died.
# Seconds remaining on ONE specific ESTABLISHED state, or empty. Both requirements are load
# bearing. Returned as an integer because the question is whether the value DECAYS, and comparing
# HH:MM:SS strings for equality turns one second of sampling jitter into a flipped verdict -- which
# it did. And the state is pinned by BOTH endpoints and by ESTABLISHED, because both sandboxes
# stream to the same host port: a port-only match selected a different state on each sample, once
# reporting 4s and then 86399s for what was described as the same connection.
expires_of() {   # $1 = substring identifying the state's header line
  pfctl -s state -vv 2>/dev/null | awk -v pat="$1" '
    index($0, pat) && /ESTABLISHED:ESTABLISHED/ { want=1; next }
    want && /expires in/ {
      split($0, a, "expires in "); split(a[2], b, ",");
      n = split(b[1], t, ":");
      if (n == 3) print t[1]*3600 + t[2]*60 + t[3]; else if (n == 2) print t[1]*60 + t[2];
      exit
    }'
}

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
            for i in range(120):
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

load_pool() {
  flush
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
      echo "block return out quick to <yb_src_$i>"
    done; } > "$WD/pool.rules"
  pfctl -a "$ANCHOR" -f "$WD/pool.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}
claim() { pfctl -a "$ANCHOR" -t "yb_src_$2" -T add "$1" >/dev/null 2>&1
          pfctl -a "$ANCHOR" -t "yb_dst_$2" -T add "$GW" >/dev/null 2>&1; }

# Opens a stream from $1 into /tmp/$2.txt and echoes bytes after 5s.
stream_begin() {
  asuser container exec "$1" sh -c "rm -f /tmp/$2.txt" >/dev/null 2>&1
  asuser container exec "$1" curl -sN --max-time 90 "http://$GW:$PORT/" -o "/tmp/$2.txt" \
    >/dev/null 2>&1 &
  sleep 5
  asuser container exec "$1" sh -c "wc -c < /tmp/$2.txt 2>/dev/null || echo 0" 2>/dev/null | tr -d ' '
}
bytes_of() { asuser container exec "$1" sh -c "wc -c < /tmp/$2.txt 2>/dev/null || echo 0" \
               2>/dev/null | tr -d ' '; }

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$SRVPID" ]   && kill "$SRVPID" 2>/dev/null
  [ -n "$CURLPID" ]  && kill "$CURLPID" 2>/dev/null
  [ -n "$CURLPID2" ] && kill "$CURLPID2" 2>/dev/null
  [ -n "$NCPID" ]    && kill "$NCPID" 2>/dev/null; kill "$IDLEPID" 2>/dev/null
  [ -n "$IDLEPID" ]  && kill "$IDLEPID" 2>/dev/null
  flush
  for g in ybra1 ybra2; do asuser container rm -f "$g" >/dev/null 2>&1; done
  rm -rf "$WD"
  local now; now=$(mainrefs)
  echo "   main-refs: before=$MAINREFS0 after=$now   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "A0 SETUP — two sandboxes, both streaming, so 'narrower' can be measured and not assumed"
asuser container system start >/dev/null 2>&1; sleep 2
MAINREFS0=$(mainrefs)
[ "$MAINREFS0" -gt 0 ] || { bad "host is already fail-open; run pf_anchor_eval.sh. ABORTING"; exit 1; }
for g in ybra1 ybra2; do asuser container rm -f "$g" >/dev/null 2>&1; done
asuser container run -d --name ybra1 "$IMG" sleep 900 >/dev/null 2>&1
asuser container run -d --name ybra2 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybra1 ipv4Address); GW=$(netfield ybra1 ipv4Gateway)
IP2=$(netfield ybra2 ipv4Address)
note "guest A=$IP   guest B=$IP2   gateway=$GW   port=$PORT"
[ -n "$IP" ] && [ -n "$IP2" ] && [ -n "$GW" ] || { bad "A0: missing an address; ABORTING"; exit 1; }
start_server || { bad "A0: the stream server did not start; ABORTING"; exit 1; }
load_pool
claim "$IP" "$SLOT"; claim "$IP2" "$SLOT2"
a0=$(stream_begin ybra1 a0); b0=$(stream_begin ybra2 b0)
note "A streaming: $a0 bytes   B streaming: $b0 bytes   (both must be >0)"
if [ "${a0:-0}" -gt 0 ] && [ "${b0:-0}" -gt 0 ]; then
  ok "A0: both sandboxes hold a live, permitted stream"
else
  bad "A0: a stream did not establish (A=$a0 B=$b0); nothing below is testable. ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "T1 CAN AN ANCHOR HOLD A TIMEOUT AT ALL? — asked first, because the rest is moot if not"
note "\`set timeout\` is a ruleset OPTION. pf.conf documents options as main-ruleset constructs, and"
note "D132 can write exactly one file: the pinned pool ruleset, loaded into our anchor. If an anchor"
note "will not carry the option, the no-grant remedy cannot live where D132 is allowed to write."
{ echo "set timeout tcp.established 20"
  echo "set timeout tcp.closing 5"
  cat "$WD/pool.rules"; } > "$WD/timeout.rules"
REQ_EST=20
t1out=$(pfctl -a "$ANCHOR" -f "$WD/timeout.rules" 2>&1)
t1rc=$?
note "pfctl exit=$t1rc, output:"
printf '%s\n' "${t1out:-<none>}" | sed 's/^/          /'
note ""
note "and what the anchor then REPORTS, which is the part that decides it. Exit 0 means the file"
note "parsed, not that the option took — and an option that parses and does nothing is the shape"
note "most likely to be mistaken for a working remedy:"
pfctl -a "$ANCHOR" -s timeouts 2>/dev/null | grep -E 'tcp\.(established|closing)' | sed 's/^/          anchor: /'
pfctl -s timeouts 2>/dev/null | grep -E 'tcp\.(established|closing)' | sed 's/^/          global: /'
anchor_est=$(pfctl -a "$ANCHOR" -s timeouts 2>/dev/null | awk '/tcp\.established/{print $2+0; exit}')
note "requested tcp.established=${REQ_EST}s, anchor reports=${anchor_est:-<none>}s"
if [ "$t1rc" -ne 0 ] || printf '%s' "$t1out" | grep -qiE 'syntax error|not allowed|unknown option'; then
  bad "T1: the anchor REFUSED the timeout option outright"
  ANCHOR_TIMEOUT=refused
elif [ "${anchor_est:-0}" = "$REQ_EST" ]; then
  ok "T1: the anchor accepted the timeout AND applied it (${anchor_est}s)"
  ANCHOR_TIMEOUT=applied
else
  bad "T1: the anchor ACCEPTED the option AND IGNORED IT. pfctl exited 0, printed no error, and the"
  note "    anchor still reports ${anchor_est:-?}s against the ${REQ_EST}s requested — the global"
  note "    default, unchanged. This is worse than a refusal: a refusal is visible, and this would"
  note "    have shipped as a working remedy in the one file D132 permits writing. \`set\` is a"
  note "    ruleset option and an anchor is not a ruleset for that purpose."
  ANCHOR_TIMEOUT=inert
fi
note ""
note "So the no-grant remedy cannot be written where D132 is allowed to write. What remains is"
note "changing the HOST's global pf timeouts — a wider change than the grant it was meant to avoid,"
note "affecting every connection on the machine — and this harness deliberately does not attempt it:"
note "it never touches the main ruleset."
# load_pool flushes the anchor, which destroys table MEMBERSHIP as well as the rules. Re-claiming is
# not optional: with empty src tables every `block ... from <yb_src_N>` matches nothing, traffic
# meets pf's default pass, and every arm below would read "survives" for a reason that has nothing
# to do with the kill being tested. The first run of this section did exactly that.
load_pool >/dev/null
claim "$IP" "$SLOT"; claim "$IP2" "$SLOT2"

# ---------------------------------------------------------------------------
say "T2 WOULD A TIMEOUT BOUND THE CASE THAT MATTERS? — read off pf's own expiry counter"
note "A tcp.established timer is reset by traffic. The exposure this is meant to bound is an agent"
note "still moving data after its allowlist entry was revoked — which keeps its own state fresh. So"
note "the question is not whether a timeout works, it is whether it can ever reach a BUSY state."
note ""
note "Two states are watched over ${T2_WINDOW}s, and the IDLE one is the control: if the method cannot see"
note "an expiry decay where decay must be happening, then a busy state showing no decay proves"
note "nothing about the state and everything about the instrument."
IDLE_PORT=18645
# A host-side `nc -l` is not usable here: it closes its write half as soon as its stdin hits EOF,
# which under a script is immediately, and the connection landed in FIN_WAIT_2 within seconds. The
# state was then correctly rejected by the ESTABLISHED requirement and the control read as absent.
cat > "$WD/idle_listen.py" <<'PY2'
import socket, sys, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", int(sys.argv[1])))
s.listen(1)
conns = []
s.settimeout(300)
try:
    while True:
        c, _ = s.accept()
        conns.append(c)          # held open, never read, never closed
except Exception:
    pass
time.sleep(300)
PY2
python3 "$WD/idle_listen.py" "$IDLE_PORT" >/dev/null 2>&1 &
NCPID=$!
sleep 1
# Two traps, both already documented in this file's METHOD note and both hit anyway. The guest
# image has no `nc`, so the first form never opened a connection at all and read as "no state
# found". And a process backgrounded INSIDE the guest dies with its `container exec` session --
# the second form reached FIN_WAIT_2 within seconds for that reason. The exec is backgrounded on
# the HOST instead, exactly as the streaming curl already is.
asuser container exec ybra1 python3 -c \
  "import socket,time; s=socket.create_connection(('$GW',$IDLE_PORT)); time.sleep(300)" \
  >/dev/null 2>&1 &
IDLEPID=$!
sleep 4
if [ -z "$(expires_of ":$IDLE_PORT <- $IP:")" ]; then
  note "!! the idle control connection did not establish; T2 has no control and will report UNKNOWN"
fi
act1=$(expires_of ":$PORT <- $IP:"); idle1=$(expires_of ":$IDLE_PORT <- $IP:")
note "t=0    active(:$PORT) expires in ${act1:-<none>}s    idle(:$IDLE_PORT) expires in ${idle1:-<none>}s"
sleep "$T2_WINDOW"
act2=$(expires_of ":$PORT <- $IP:"); idle2=$(expires_of ":$IDLE_PORT <- $IP:")
note "t=${T2_WINDOW}s   active(:$PORT) expires in ${act2:-<none>}s    idle(:$IDLE_PORT) expires in ${idle2:-<none>}s"
kill "$NCPID" 2>/dev/null
note ""
note "the two states, verbatim:"
pfctl -s state -vv 2>/dev/null | grep -A1 -E ":($PORT|$IDLE_PORT) <- $IP:" \
  | head -6 | sed 's/^/          /'
act_drop=$(( ${act1:-0} - ${act2:-0} ))
idle_drop=$(( ${idle1:-0} - ${idle2:-0} ))
note ""
note "decay over ${T2_WINDOW}s:  active=${act_drop}s   idle=${idle_drop}s   (a timer running down drops by ~${T2_WINDOW})"
if [ -z "${act1:-}" ] || [ -z "${idle1:-}" ] || [ -z "${act2:-}" ] || [ -z "${idle2:-}" ]; then
  unk "T2: one of the two states could not be read; the mechanism is not characterised"
  TIMEOUT_BOUNDS=unknown
elif [ "$idle_drop" -lt $((T2_WINDOW - 3)) ]; then
  unk "T2: the IDLE control did not decay either (${idle_drop}s over ${T2_WINDOW}s). The method cannot see"
  note "     decay where decay must exist, so the active reading below it means nothing."
  TIMEOUT_BOUNDS=unknown
elif [ "$act_drop" -lt 3 ]; then
  ok "T2: the idle state decayed ${idle_drop}s over ${T2_WINDOW}s and the BUSY state decayed ${act_drop}s. The"
  note "    method sees decay, and the busy state is not decaying — its timer is reset by its own"
  note "    traffic. A tcp.established timeout expires IDLE states and cannot reach a flow that is"
  note "    still moving data, which is precisely the flow revocation exists to stop."
  TIMEOUT_BOUNDS=no
else
  ok "T2: the BUSY state decayed ${act_drop}s over ${T2_WINDOW}s alongside the idle control's ${idle_drop}s, so its"
  note "    timer is NOT reset by traffic on this host and a short timeout could bound an active"
  note "    flow. That contradicts the usual reading of tcp.established — reproduce it before"
  note "    designing on it."
  TIMEOUT_BOUNDS=yes
fi

# ---------------------------------------------------------------------------
say "T3 WHICH KILL FORMS STOP IT — with the control the first run of this section lacked"
note "\`-k <host>\` works and is the wide one: a grant permitting it permits naming ANY host. The case"
note "for an alternative is that the policy could constrain it, so what matters is which forms stop"
note "the flow AND take arguments a regex can pin. What each form does, from pfctl(8) verbatim,"
note "because two runs have now cited -K as 'the narrower one' without anyone reading it:"
man pfctl 2>/dev/null | col -b | grep -A6 '^     -K host' | sed 's/^/          /'
man pfctl 2>/dev/null | col -b | grep -A5 '^     -k host' | sed 's/^/          /'
note ""
note "Two things follow from that text and are measured below rather than assumed. -K kills SOURCE"
note "TRACKING entries, not state entries, so it is not a narrower state kill — it is a different"
note "object. And the two-host form is DIRECTIONAL: 'from the first host to the second'. Which"
note "direction works is therefore a real question, and BOTH orders are run rather than reasoned"
note "about: R4's state dump shows the surviving state sourced from the HOST, which argues for"
note "gateway-then-guest, and that argument is recorded here so the measurement can contradict it."

# One arm: re-permit, open a fresh stream, revoke, run one kill form, report. Every arm runs the
# identical sequence with only the kill changed, so a difference between them is the kill.
ARM_RESULT=""; KILL_B_SURVIVED=""
kill_arm() {   # $1 = tag, $2 = label, rest = the pfctl kill argv
  local tag="$1" label="$2"; shift 2
  # Membership is re-asserted and CHECKED every arm, because the whole fixture rests on it and an
  # empty src table is invisible in the byte counts.
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$GW" >/dev/null 2>&1
  if ! pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T show 2>/dev/null | grep -q "$IP"; then
    note "  !! $IP is not in yb_src_$SLOT — the pool is not enforcing and this arm is void"
    ARM_RESULT=void; return
  fi
  local before after out bbefore bafter
  before=$(stream_begin ybra1 "$tag")
  bbefore=$(bytes_of ybra2 b0)
  note ""
  note "--- $label ---"
  note "A streaming: $before bytes; states naming A: $(states "$IP")"
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T delete "$GW" >/dev/null 2>&1
  out=$(pfctl "$@" 2>&1 | grep -viE 'ALTQ' | tr '\n' ' ')
  note "pfctl $* -> ${out:-<no output>}"
  sleep 6
  after=$(bytes_of ybra1 "$tag"); bafter=$(bytes_of ybra2 b0)
  note "A: $before -> $after bytes; states naming A now: $(states "$IP")"
  note "B (untargeted, must keep running): $bbefore -> $bafter bytes"
  # Sets globals rather than echoing: this function prints its own human-readable lines, and a
  # verdict returned through command substitution would arrive with the entire log glued to it.
  if [ "${after:-0}" -le "${before:-0}" ]; then ARM_RESULT=stops; else ARM_RESULT=survives; fi
  KILL_B_SURVIVED=$([ "${bafter:-0}" -gt "${bbefore:-0}" ] && echo yes || echo no)
  note "arm verdict: $ARM_RESULT"
}

note ""
note "T3control  plain \`-k <guest>\`. pf-revocation.txt R5b stopped the flow with exactly this, so it"
note "           MUST stop it here too. Without this arm, an alternative that fails is unattributable"
note "           — it could be the form, or it could be this fixture, and the two are"
note "           indistinguishable. The first run of this section omitted it and drew verdicts anyway."
kill_arm c "control: pfctl -k <guest>" -k "$IP"; CTRL=$ARM_RESULT
if [ "$CTRL" = stops ]; then
  ok "T3control: plain -k stops the flow here, reproducing R5b — the fixture is sound and the arms"
  note "           below are attributable to the form they change"
  CTRL_OK=1
else
  bad "T3control: plain -k did NOT stop the flow in this fixture, though R5b says it does. Something"
  note "           differs from that run and NOTHING in T3 can be read until it is found."
  CTRL_OK=0
fi

if [ "$CTRL_OK" -eq 1 ]; then
  kill_arm f "guest-then-gateway: pfctl -k <guest> -k <gw>" -k "$IP" -k "$GW"; TWOHOST_FWD=$ARM_RESULT
  kill_arm r "gateway-then-guest: pfctl -k <gw> -k <guest>" -k "$GW" -k "$IP"; TWOHOST_REV=$ARM_RESULT
  SCOPED=${KILL_B_SURVIVED:-unknown}
  kill_arm k "source-tracking: pfctl -K <guest>" -K "$IP"; KFORM=$ARM_RESULT

  note ""
  if [ "$TWOHOST_REV" = stops ] && [ "$TWOHOST_FWD" = survives ]; then
    ok "T3: the two-host kill works, and ONLY in the gateway-to-guest direction. That matches R4's"
    note "    state dump exactly — the surviving state is sourced from the host — and it means a"
    note "    grant can name BOTH endpoints, pinning the peer to our own gateway rather than"
    note "    permitting \`-k <anything>\`. The intuitive order is the one that does nothing."
  elif [ "$TWOHOST_FWD" = stops ] && [ "$TWOHOST_REV" = survives ]; then
    ok "T3: the two-host kill works, and ONLY as \`-k <guest> -k <gateway>\` — guest FIRST."
    note "    The mechanism argument above predicted the opposite order and is refuted: reasoning"
    note "    from which endpoint owns the surviving state does not predict which argument order"
    note "    pfctl matches. An amendment must pin this order exactly; the reverse kills 0 states"
    note "    and reports success while the transfer continues, which is the worst possible"
    note "    failure for a grant to be written against."
  elif [ "$TWOHOST_REV" = stops ] || [ "$TWOHOST_FWD" = stops ]; then
    ok "T3: a two-host form stops it (fwd=$TWOHOST_FWD rev=$TWOHOST_REV), so both endpoints can be"
    note "    named in the grant"
  else
    bad "T3: neither two-host order stopped the flow while plain -k did. The narrower form does not"
    note "    exist, and an amendment would have to permit the wide \`-k <host>\`."
  fi
  if [ "$KFORM" = stops ]; then
    unk "T3: -K stopped the flow, which contradicts pfctl(8) — it documents -K as killing source"
    note "    tracking entries, not states. Reproduce before relying on it either way."
  else
    ok "T3: -K does NOT stop the flow, as its documentation says it would not — it kills source"
    note "    tracking entries, a different object. The 'use -K, it is narrower' suggestion carried"
    note "    by two runs' WHAT WAS NOT TRIED is closed: it is not a state kill at all."
  fi
  if [ "${SCOPED:-no}" = yes ]; then
    ok "T4: the untargeted sandbox B streamed through every kill — the effect is per-sandbox"
  else
    bad "T4: sandbox B stopped too. A kill aimed at one sandbox reached another, and no grant shape"
    note "    makes that safe."
  fi
else
  unk "T3/T4: skipped — the control failed, so no arm below it could be interpreted"
  TWOHOST_FWD=untested; TWOHOST_REV=untested; KFORM=untested; SCOPED=untested
fi

# ---------------------------------------------------------------------------
say "VERDICT — composed from the values above"
note "anchor accepts a timeout   : ${ANCHOR_TIMEOUT:-untested}"
note "timeout can reach a busy flow: ${TIMEOUT_BOUNDS:-untested}"
note "two-host kill, guest->gw   : ${TWOHOST_FWD:-untested}"
note "two-host kill, gw->guest   : ${TWOHOST_REV:-untested}"
note "-K (source tracking) stops it: ${KFORM:-untested}"
note "kill left the other sandbox alone: ${SCOPED:-untested}"
note ""
if [ "${ANCHOR_TIMEOUT:-refused}" = accepted ] && [ "${TIMEOUT_BOUNDS:-no}" = yes ]; then
  ok "THE NO-GRANT REMEDY IS AVAILABLE. A timeout in the pinned ruleset bounds the window without"
  note "    widening D132 at all, and the amendment can be dropped."
elif { [ "${TWOHOST_REV:-survives}" = stops ] || [ "${TWOHOST_FWD:-survives}" = stops ]; } && [ "${SCOPED:-no}" = yes ]; then
  bad "D132 MUST WIDEN, but it can widen NARROWLY. The no-grant timeout does not reach a busy flow"
  note "    (${ANCHOR_TIMEOUT}/${TIMEOUT_BOUNDS}), so a state kill is required. The two-host form"
  note "    stops it and names BOTH endpoints, so the grant can pin the peer to our own gateway"
  note "    rather than permitting \`-k <anything>\` — materially narrower than the form D132"
  note "    rejected, and the shape an amendment should take."
else
  unk "NEITHER PATH IS ESTABLISHED. Do not design against any of these until this is re-run."
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Changing the HOST's global pf timeouts. Deliberate: it needs a main-ruleset write, which
          is both outside D132's grant and the fail-open path this directory has been burned by
          twice. So T1's negative bounds what the ANCHOR can hold, and says nothing about whether a
          global timeout would work — only that reaching for one is a bigger change, not a smaller.
        - The sudoers regex for a two-host kill. T3a shows the form exists and works; whether a
          regex can pin its second argument the way the table rules are pinned is a policy question
          and needs the permit/refuse matrix, not this harness. It is the next step if -k is taken.
        - UDP, and idle TCP. Everything here is one actively streaming TCP connection, which is the
          hard case for a timeout and the easy case for a kill. An idle state's behaviour under
          each form is unmeasured.
        - Whether -K and the two-host form differ in what they reach. Both stopped the target here;
          neither was measured against a state belonging to a THIRD party outside our anchor, which
          is the actual scope claim behind calling one of them narrower.
EOF
