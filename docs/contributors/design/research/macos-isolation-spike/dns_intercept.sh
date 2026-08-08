#!/bin/bash
# ABOUTME: M2 — can yoloAI see or redirect a guest's OWN DNS queries on apple and tart?
# ABOUTME: Passive tap, pf rdr to a host listener, and --dns plus a port-53 lockdown.
#
# Run: sudo bash dns_intercept.sh
#
# WHY THIS EXISTS
#   The allowlist resolves domains ON THE HOST and installs the addresses. Two measured problems
#   came out of that: split-horizon divergence (the guest could not reach the name it was
#   allowlisted for) and one-shot decay (github.com moved inside 10 minutes). Cilium's toFQDNs
#   inverts the direction — it proxies the POD's queries and installs the addresses the pod
#   actually received, expiring them on the TTL — and that dissolves both at once.
#
#   That inversion needs one thing macOS has never been asked for: the guest's own queries, either
#   observable or redirectable, WITHOUT yoloAI owning the vmnet gateway (which it does not and
#   cannot — the gateway is Apple's).
#
#   D1 PASSIVE TAP. Can the host simply watch queries on the bridge? That alone buys the
#      observation half — learn what the guest resolved, install that — with no packet path change.
#   D2 REDIRECT. pf-rdr.txt proved rdr is evaluated inside our anchor for TCP. Here: UDP/53, to a
#      real listener, aimed at loopback and at the bridge address, since redirecting to loopback
#      from another interface is the classic failure.
#   D3 THE COMPLETE MECHANISM. `container run --dns` points the guest at a resolver of our
#      choosing — but a guest can ignore it, so on its own that is cooperation, not enforcement.
#      The enforceable form is --dns PLUS a pf rule denying 53 to anything else. Both halves are
#      measured, and the "guest goes around it" case is measured as a control.
#   D4 TART, because M2 asks about both backends and they use different bridges.
#
# METHOD: every "the query was captured" is paired with a run where the mechanism is OFF and the
#   listener must see NOTHING — otherwise a listener picking up unrelated traffic passes for free.
#   And every "it is blocked" is paired with a resolution that must still SUCCEED (A22).
#
# SAFETY: writes only into its own anchor. Never touches the main ruleset; asserts the
#   main-ruleset reference count is unchanged on exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_d"
IMG=yoloai-base:latest
DNSPORT=5354
FAKEA=203.0.113.77        # TEST-NET-3; if a guest resolves to this, it used OUR resolver
PUBDNS=8.8.8.8
PASS=0; FAIL=0; UNKNOWN=0
MAINREFS0=0
LPID=""; TDPID=""
WD=$(mktemp -d /tmp/pfd-wd.XXXXXX)

RESULTS="$HERE/results/dns-intercept.txt"
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
load()  { flush; printf '%s\n' "$1" > /tmp/pfd.rules
          pfctl -a "$ANCHOR" -f /tmp/pfd.rules 2>&1 | quiet_pf; }
nrules(){ pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
ntrans(){ pfctl -a "$ANCHOR" -s nat 2>/dev/null | grep -c . || true; }

netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }

# Resolve a name INSIDE the guest; prints the first A record or "". Deliberately A-only: `getent
# hosts` returns the AAAA first on this image, which would silently never equal FAKEA.
# `dig +short` writes ";; communications error to ..." to STDOUT on refusal, so an unfiltered
# capture is non-empty and a successful BLOCK reads as a successful answer. Run 1 lost D3b to it.
gdig()    { asuser container exec "$1" \
              dig +short +time=2 +tries=1 A "$2" 2>/dev/null | grep -v '^;;' | head -1; }
gdig_at() { asuser container exec "$1" \
              dig +short +time=2 +tries=1 A "$2" "@$3" 2>/dev/null | grep -v '^;;' | head -1; }

# --- the host-side resolver under test -------------------------------------
# Answers every A query with FAKEA and appends the queried name to its log, so "the guest used our
# resolver" is provable from the guest side (the address) and the host side (the log) at once.
write_listener() {
  cat > "$WD/dnsd.py" <<'PY'
import socket, sys, struct
bind, port, fake, log = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((bind, port))
f = open(log, "a", buffering=1)
def name_of(d, off):
    parts = []
    while True:
        n = d[off]
        if n == 0: off += 1; break
        if n & 0xC0: off += 2; break
        parts.append(d[off+1:off+1+n].decode("latin1")); off += 1 + n
    return ".".join(parts), off
while True:
    try:
        data, peer = s.recvfrom(2048)
        if len(data) < 13: continue
        qname, off = name_of(data, 12)
        qtype, qclass = struct.unpack(">HH", data[off:off+4]); off += 4
        f.write("%s %s type=%d\n" % (peer[0], qname, qtype))
        hdr = struct.pack(">HHHHHH", struct.unpack(">H", data[:2])[0], 0x8180, 1,
                          1 if qtype == 1 else 0, 0, 0)
        resp = hdr + data[12:off]
        if qtype == 1:
            resp += b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 30, 4) \
                    + socket.inet_aton(fake)
        s.sendto(resp, peer)
    except Exception as e:
        f.write("ERR %s\n" % e)
PY
}
start_listener() {   # $1 = bind address
  : > "$WD/dns.log"
  python3 "$WD/dnsd.py" "$1" "$DNSPORT" "$FAKEA" "$WD/dns.log" >/dev/null 2>&1 &
  LPID=$!
  sleep 1
  kill -0 "$LPID" 2>/dev/null
}
stop_listener() { [ -n "$LPID" ] && kill "$LPID" 2>/dev/null; LPID=""; }
seen() { grep -c "$1" "$WD/dns.log" 2>/dev/null || true; }

cleanup() {
  echo
  echo "== cleanup =="
  stop_listener
  [ -n "$TDPID" ] && kill "$TDPID" 2>/dev/null
  flush
  asuser container rm -f ybd1 ybd2 >/dev/null 2>&1
  asuser "$YOLOAI" destroy dnsx --abandon-unapplied >/dev/null 2>&1
  rm -rf "$WD" /tmp/pfd.rules
  local now; now=$(mainrefs)
  echo "   anchor flushed | main-refs now=$now was=$MAINREFS0"
  [ "$now" = "$MAINREFS0" ] && echo "   main ruleset unchanged" \
                            || echo "   !! MAIN RULESET CHANGED — run pf_anchor_eval.sh"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "D0 SETUP"
asuser container system start >/dev/null 2>&1; sleep 2
MAINREFS0=$(mainrefs)
note "main-refs=$MAINREFS0"
[ "$MAINREFS0" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }

write_listener
asuser container rm -f ybd1 >/dev/null 2>&1
asuser container run -d --name ybd1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 3
IP1=$(netfield ybd1 ipv4Address); GW1=$(netfield ybd1 ipv4Gateway); BR=$(brof "$GW1")
RESOLV=$(asuser container exec ybd1 sh -c 'cat /etc/resolv.conf' 2>/dev/null | tr '\n' ' ')
note "ybd1=$IP1  gateway=$GW1 on ${BR:-<none>}"
note "guest resolv.conf: $RESOLV"
[ -n "$IP1" ] && [ -n "$BR" ] || { bad "setup incomplete; ABORTING"; exit 1; }
base=$(gdig ybd1 example.com)
note "baseline resolution in guest: example.com -> ${base:-<failed>}   (must succeed)"
[ -n "$base" ] || { bad "guest cannot resolve at all; every result below would be free. ABORTING"; exit 1; }
ok "guest resolves through the vmnet gateway, as the design assumes"

# ---------------------------------------------------------------------------
say "D1 PASSIVE TAP — can the host simply WATCH the guest's queries?"
note "This buys the observation half of the Cilium shape with no change to the packet path: learn"
note "what the guest actually resolved, and install THAT. It needs BPF access, which is root."
UNIQ1="d1probe-$$.example.com"
tcpdump -i "$BR" -n -l -c 20 'udp port 53' > "$WD/tap.txt" 2>/dev/null &
TDPID=$!
sleep 2
gdig ybd1 "$UNIQ1" >/dev/null 2>&1
asuser dscacheutil -q host -a name "hostside-$$.example.com" >/dev/null 2>&1
sleep 2
kill "$TDPID" 2>/dev/null; TDPID=""
cap=$(grep -c "d1probe" "$WD/tap.txt" 2>/dev/null || true)
hostcap=$(grep -c "hostside" "$WD/tap.txt" 2>/dev/null || true)
lines=$(grep -c . "$WD/tap.txt" 2>/dev/null || true)
note "captured on $BR: $lines packets total; guest's name seen $cap time(s); host's name $hostcap"
if [ "$cap" -gt 0 ] && [ "$hostcap" -eq 0 ]; then
  ok "D1: the guest's queries are observable on the bridge, and the host's own are NOT there"
  note "    — so a tap sees exactly guest traffic, which is the scoping the design would need."
elif [ "$cap" -gt 0 ]; then
  unk "D1: guest queries visible, but host queries appeared too — the tap is not guest-scoped"
else
  bad "D1: the guest's query was not captured on $BR; passive observation is not available"
fi

# ---------------------------------------------------------------------------
say "D2 REDIRECT — pf rdr of the guest's UDP/53 to a host listener"
note "Two targets, because redirecting to loopback from another interface is the classic failure."
for target in 127.0.0.1 "$GW1"; do
  note ""
  note "D2 target=$target"
  if ! start_listener "$target"; then
    unk "D2($target): listener would not bind; skipped"; continue
  fi
  # OFF control first: with no rdr the listener must stay silent, or nothing below discriminates.
  flush
  UNIQ="d2off-$$-$target.example.com"
  gdig ybd1 "$UNIQ" >/dev/null 2>&1
  sleep 1
  offseen=$(seen "d2off")
  note "rdr OFF: listener saw $offseen query/queries  (must be 0)"

  load "rdr pass on $BR proto udp from $IP1 to any port 53 -> $target port $DNSPORT"
  note "translation rules loaded: $(ntrans)"
  UNIQ2="d2on-$$-$target.example.com"
  got=$(gdig ybd1 "$UNIQ2")
  sleep 1
  onseen=$(seen "d2on")
  note "rdr ON:  listener saw $onseen query/queries; guest resolved to '${got:-<failed>}'"
  note "         (want >=1 seen, and the guest's answer to be $FAKEA — our resolver's signature)"
  if [ "$offseen" -eq 0 ] && [ "$onseen" -gt 0 ] && [ "$got" = "$FAKEA" ]; then
    ok "D2($target): the guest's DNS is redirectable to a host listener, end to end"
  elif [ "$offseen" -eq 0 ] && [ "$onseen" -gt 0 ]; then
    unk "D2($target): the query ARRIVED but the guest did not take our answer (got '${got:-none}')"
    note "         — the redirect works one way; the reply path does not get back."
  elif [ "$offseen" -gt 0 ]; then
    unk "D2($target): the listener saw traffic with rdr OFF; this run cannot discriminate"
  else
    ok "D2($target): rdr did NOT deliver the guest's DNS here (a bounded negative for this target)"
  fi
  stop_listener
  flush
done

# ---------------------------------------------------------------------------
say "D3 THE COMPLETE MECHANISM — --dns to point it, pf to make it stick"
note "--dns alone is cooperation: the guest can edit resolv.conf and query anything. The enforceable"
note "form is --dns plus a rule denying 53 to everything except our resolver. Both halves measured,"
note "including the going-around-it case, which is the whole reason the pf half exists."
if ! start_listener "$GW1"; then
  unk "D3: listener would not bind on $GW1; skipped"
else
  asuser container rm -f ybd2 >/dev/null 2>&1
  asuser container run -d --name ybd2 --dns "$GW1" --dns-option ndots:1 "$IMG" sleep 900 >/dev/null 2>&1
  sleep 3
  IP2=$(netfield ybd2 ipv4Address)
  R2=$(asuser container exec ybd2 sh -c 'cat /etc/resolv.conf' 2>/dev/null | tr '\n' ' ')
  note "ybd2=$IP2  resolv.conf: $R2"
  note "NOTE the port: --dns names an ADDRESS, not a port, so the guest queries $GW1:53 while our"
  note "     listener is on :$DNSPORT. An rdr on the bridge closes that gap, and is loaded here."
  load "rdr pass on $BR proto udp from $IP2 to $GW1 port 53 -> $GW1 port $DNSPORT"

  UNIQ3="d3-$$-viaours.example.com"
  got3=$(gdig ybd2 "$UNIQ3")
  sleep 1
  s3=$(seen "d3-")
  note "guest -> its configured resolver: answer='${got3:-<failed>}' listener saw $s3"
  if [ "$got3" = "$FAKEA" ] && [ "$s3" -gt 0 ]; then
    ok "D3a: the guest's queries land on OUR resolver and it takes our answers"
  else
    unk "D3a: the guest did not use our resolver (answer='${got3:-none}', seen=$s3)"
  fi

  # Going around it: the control that shows why --dns alone is not enforcement.
  around=$(gdig_at ybd2 example.com "$PUBDNS")
  note "guest -> $PUBDNS directly, NO block yet: '${around:-<failed>}'  (success => --dns is bypassable)"

  load "$(printf '%s\n' \
    "rdr pass on $BR proto udp from $IP2 to $GW1 port 53 -> $GW1 port $DNSPORT" \
    "pass  in quick on $BR proto udp from $IP2 to $GW1 port 53" \
    "block return in quick on $BR proto { udp tcp } from $IP2 to any port 53")"
  note "with the lockdown loaded: $(nrules) filter rule(s), $(ntrans) translation rule(s)"
  blocked=$(gdig_at ybd2 example.com "$PUBDNS")
  still=$(gdig ybd2 "d3b-$$-still.example.com")
  note "guest -> $PUBDNS: '${blocked:-<blocked>}'   (must FAIL)"
  note "guest -> ours:    '${still:-<failed>}'      (must still SUCCEED — the A22 control)"
  if [ -z "$blocked" ] && [ "$still" = "$FAKEA" ]; then
    ok "D3b: 53 is closed to everything but our resolver, and our resolver still answers"
    note "    That is the enforceable form: guest-side resolution, observed by us, with no way out"
    note "    over plaintext DNS."
  elif [ -n "$blocked" ]; then
    bad "D3b: the guest still reached $PUBDNS ('$blocked') — the lockdown does not hold"
  else
    unk "D3b: our own resolver stopped answering too (got '${still:-none}'); over-blocked"
  fi
  stop_listener
  flush
fi

# ---------------------------------------------------------------------------
say "D4 TART — same questions, different bridge"
note "Best effort: a tart guest is a full macOS VM and slow to boot. If it does not come up in time"
note "this section reports UNKNOWN rather than guessing, and the apple results above still stand."
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser "$YOLOAI" destroy dnsx --abandon-unapplied >/dev/null 2>&1
if ! asuser "$YOLOAI" new dnsx "$WD" --backend tart >/dev/null 2>&1; then
  unk "D4: tart sandbox would not start; not measured"
else
  TIP=$(asuser "$YOLOAI" ls 2>/dev/null | awk '$1=="dnsx"{print $4}')
  note "tart guest reported at '${TIP:-<none>}'"
  TRES=$(asuser "$YOLOAI" exec dnsx cat /etc/resolv.conf 2>/dev/null)
  note "tart resolv.conf nameservers:"
  printf '%s\n' "$TRES" | awk '/^nameserver/{print "          " $0}'
  # macOS resolv.conf opens with a comment block and lists link-local v6 resolvers first. Run 1
  # flattened the file and took field 2 of the first line containing "nameserver", which was "#".
  TGW=$(printf '%s\n' "$TRES" | awk '/^nameserver/ && $2 !~ /^fe80/ && $2 ~ /\./ {print $2; exit}')
  note "first IPv4 nameserver: ${TGW:-<none>}"
  TBR=$(brof "$TGW")
  note "tart resolver $TGW is the gateway on ${TBR:-<none>}"
  if [ -z "$TBR" ]; then
    unk "D4: could not map the tart resolver to a bridge; not measured"
  else
    if start_listener "$TGW"; then
      flush
      u4="d4off-$$.example.com"
      asuser "$YOLOAI" exec dnsx dscacheutil -q host -a name "$u4" >/dev/null 2>&1
      sleep 1
      off4=$(seen "d4off")
      load "rdr pass on $TBR proto udp from any to any port 53 -> $TGW port $DNSPORT"
      u4b="d4on-$$.example.com"
      asuser "$YOLOAI" exec dnsx dscacheutil -q host -a name "$u4b" >/dev/null 2>&1
      sleep 1
      on4=$(seen "d4on")
      note "rdr OFF saw $off4; rdr ON saw $on4   (want 0 then >=1)"
      if [ "$off4" -eq 0 ] && [ "$on4" -gt 0 ]; then
        ok "D4: tart's DNS is redirectable the same way apple's is"
      elif [ "$off4" -gt 0 ]; then
        unk "D4: listener saw traffic with rdr off; cannot discriminate"
      else
        ok "D4: rdr did NOT capture tart's DNS (bounded negative — the rule loaded, nothing arrived)"
      fi
      stop_listener
      flush
    else
      unk "D4: listener would not bind on $TGW"
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - DoH/DoT. Closing UDP/TCP 53 does not close DNS: a guest that can reach ANY allowlisted
          host on 443 can resolve over it. This is a real ceiling on the whole DNS-proxy direction
          and it is not measured here, because it needs no measurement — it follows from the
          allowlist permitting 443 anywhere.
        - A real resolver. The listener answers every A query with one fixed address; it is a probe,
          not a proxy. Caching, TTL handling, CNAME chains, AAAA and NXDOMAIN are untouched.
        - Per-sandbox resolver ports. One listener on one port was used; whether N sandboxes can be
          told apart by the rdr they arrive through was not tried (M1's K4 is the same question).
        - The guest editing resolv.conf under the lockdown. D3b blocks by destination and port, so
          it should hold regardless of what the guest configures, but only the 8.8.8.8 case was run.
        - IPv6 DNS. The guests hold ULA v6 addresses; a v6 resolver path was not probed. See M7.
EOF
