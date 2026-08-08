#!/bin/bash
# ABOUTME: M7 — is the IPv6 enforcement hole present on tart as well as apple, and how far does it
# ABOUTME: reach on a host with no IPv6 upstream? Measured against a host listener, not the internet.
#
# Run: sudo bash pf_v6_hole.sh
#
# WHY THIS EXISTS
#   DF104: `grep -rn ip6tables` returns nothing repo-wide. The pf pool keys on IPv4 tables, so v6
#   traffic is not filtered by anything. It was found live on apple; tart is unmeasured, and the
#   queue asks whether the gap is per-backend or universal on macOS.
#
#   THE MEASUREMENT PROBLEM, and how it is solved here. This host has no IPv6 upstream, so "curl -6
#   to the internet" fails for a reason that has nothing to do with enforcement — and a failure for
#   the wrong reason reads exactly like enforcement working. Last pass that is why v6 was written
#   off as untestable. It is not: the hole can be demonstrated entirely on the link.
#
#       Put a listener on the HOST. Load the pool so the guest's v4 is blocked. Then have the guest
#       reach the same host, same port, over v6.
#
#   v4 blocked and v6 through, to one destination, in one run — that is the hole, with its own
#   positive control built in. What it does NOT show is global v6 egress, which needs an upstream
#   this host does not have; that is stated rather than guessed at.
#
#   V1 apple: does the guest hold a v6 address, is it routable-scoped, does v6 bypass the pool?
#   V2 tart:  the same three questions on the other backend.
#   V3 does the pool BLOCK v6 if asked? A v6 table in the same anchor — so the finding separates
#      "pf cannot" from "we did not", which decides whether the fix is a table or a redesign.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_6"
IMG=yoloai-base:latest
PORT=18642
SLOTS=4; SLOT=1
ALLOW=1.1.1.1
PASS=0; FAIL=0; UNKNOWN=0
SRVPID=""
WD=$(mktemp -d /tmp/pf6.XXXXXX)

RESULTS="$HERE/results/pf-v6-hole.txt"
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
print(d[0]["status"]["networks"][0].get(sys.argv[1],""))' "$2" 2>/dev/null; }

# A dual-stack HTTP listener on the host: one server, reachable over both families, so "v4 blocked
# and v6 through" is one destination and not two experiments.
start_server() {
  cat > "$WD/srv.py" <<'PY'
import http.server, socket, socketserver, sys
class S(socketserver.TCPServer):
    address_family = socket.AF_INET6
    allow_reuse_address = True
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")
    def log_message(self, *a): pass
srv = S(("::", int(sys.argv[1])), H)
srv.serve_forever()
PY
  python3 "$WD/srv.py" "$PORT" >/dev/null 2>&1 &
  SRVPID=$!
  sleep 1
  kill -0 "$SRVPID" 2>/dev/null
}

load_pool() {   # $1 = "v6" to add a v6 table alongside
  flush
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
      if [ "${1:-}" = v6 ]; then
        echo "table <yb_src6_$i> persist"; echo "table <yb_dst6_$i> persist"
        echo "pass  in quick inet6 from <yb_src6_$i> to <yb_dst6_$i>"
        echo "block return in quick inet6 from <yb_src6_$i> to any"
      fi
    done; } > "$WD/pool.rules"
  pfctl -a "$ANCHOR" -f "$WD/pool.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null
  flush
  asuser container rm -f yb61 >/dev/null 2>&1
  asuser "$YOLOAI" destroy v6x --abandon-unapplied >/dev/null 2>&1
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
say "H0 THE HOST'S OWN IPv6, stated up front so the scope of every result below is clear"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
note "host v6 addresses:"
ifconfig 2>/dev/null | awk '/^[a-z]/{ifc=$1} /inet6/{print "          " ifc " " $2 " " $3 " " $4}' | head -12
V6DEF=$(netstat -rn -f inet6 2>/dev/null | awk '$1=="default"{print $2; exit}')
note "default v6 route: ${V6DEF:-<none>}"
if [ -z "$V6DEF" ]; then
  note "NO IPv6 UPSTREAM. So this run measures the hole ON THE LINK — guest to host — and says"
  note "nothing about global v6 egress. That limit is real and is repeated in the verdict."
fi
start_server || { bad "the host listener would not start; ABORTING"; exit 1; }
note "dual-stack listener up on port $PORT (AF_INET6 socket, so v4-mapped clients reach it too)"

# A link-local v6 address needs a zone index; a ULA or global one must NOT have one, or curl
# rejects it. Getting this wrong makes a reachable address look blocked.
v6url() {   # $1 = address (with or without a zone), $2 = guest interface
  # STRIP any zone the address already carries. macOS's netstat prints "fe80::1%en0" while Linux's
  # `ip -6 route` prints a bare address, so appending a zone unconditionally produced
  # "[fe80::1%en0%en0]" on tart — malformed, curl returned 000, and the run reported that the tart
  # guest had no dual-stack path when the URL was simply invalid.
  local a="${1%%\%*}"
  case "$a" in
    fe80*) printf 'http://[%s%%%s]:%s/' "$a" "$2" "$PORT" ;;
    *)     printf 'http://[%s]:%s/' "$a" "$PORT" ;;
  esac
}

# ---------------------------------------------------------------------------
probe_backend() {   # $1 = label, $2 = exec-prefix fn, $3 = v4 addr, $4 = v6 addr, $5 = guest iface
  local label="$1" ex="$2" v4="$3" v6="$4" iface="$5"
  note ""
  note "--- $label ---"
  note "guest v4=$v4"
  note "guest v6=${v6:-<none>}"
  if [ -z "$v6" ]; then
    unk "$label: the guest holds no IPv6 address at all — no hole here, and no v6 to filter"
    return
  fi
  case "$v6" in
    fe80*) note "scope: LINK-LOCAL only" ;;
    fd*|fc*) note "scope: unique-local (ULA) — routable on the link, not globally" ;;
    2*|3*)   note "scope: GLOBAL — this address is internet-routable" ;;
  esac

  # Which host address does the guest use to reach us? Its own gateway, on both families.
  local hv4 hv6
  hv4=$($ex "ip route | awk '/default/{print \$3}'" | tr -d '\r')
  hv6=$($ex "ip -6 route | awk '/default/{print \$3}'" | tr -d '\r')
  [ -z "$hv6" ] && hv6=$($ex "ip -6 neigh | awk '{print \$1; exit}'" | tr -d '\r')
  note "host as seen from the guest: v4=$hv4  v6=${hv6:-<none>}"

  # Baseline: both families must reach the listener BEFORE any rule, or a later block is free.
  local b4 b6
  b4=$($ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://$hv4:$PORT/")
  b6=$([ -n "$hv6" ] && $ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 '$(v6url "$hv6" "$iface")'" || echo 000)
  note "baseline: v4->$b4   v6->$b6   (both must be non-000 for the comparison to mean anything)"
  if [ "$b4" = 000 ] || [ "$b6" = 000 ]; then
    unk "$label: no dual-stack baseline to the host (v4=$b4 v6=$b6); the hole cannot be shown here"
    return
  fi
  ok "$label: dual-stack baseline to the host listener"

  # Now block the guest's v4 with the pool, and try both families again.
  load_pool >/dev/null
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$v4" >/dev/null 2>&1
  local a4 a6
  a4=$($ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://$hv4:$PORT/")
  a6=$($ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 '$(v6url "$hv6" "$iface")'")
  note "pool loaded, empty allowlist: v4->$a4   v6->$a6"
  note "   (want v4=000 — enforcement bites — and v6 non-000 — enforcement does not see it)"
  if [ "$a4" = 000 ] && [ "$a6" != 000 ]; then
    bad "$label: THE HOLE IS LIVE. v4 refused, v6 reached the same host on the same port."
  elif [ "$a4" != 000 ]; then
    unk "$label: v4 was not blocked ($a4), so nothing is demonstrated about v6"
  else
    ok "$label: v6 was blocked too ($a6) — no hole on this backend"
  fi
  flush
}

say "V1 APPLE"
asuser container system start >/dev/null 2>&1; sleep 2
asuser container rm -f yb61 >/dev/null 2>&1
asuser container run -d --name yb61 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
A4=$(netfield yb61 ipv4Address | cut -d/ -f1)
A6=$(netfield yb61 ipv6Address | cut -d/ -f1)
apple_ex() { asuser container exec yb61 sh -c "$1" 2>/dev/null; }
if [ -n "$A4" ]; then
  probe_backend "apple" apple_ex "$A4" "$A6" eth0
else
  unk "V1: no apple guest address; not measured"
fi

say "V2 TART"
note "A tart guest is a full macOS VM; if it does not come up this reports UNKNOWN rather than"
note "guessing, and V1 still stands on its own."
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser "$YOLOAI" destroy v6x --abandon-unapplied >/dev/null 2>&1
if ! asuser "$YOLOAI" new v6x "$WD" --backend tart >/dev/null 2>&1; then
  unk "V2: tart sandbox would not start; not measured"
else
  # ABSOLUTE paths. `yoloai exec` runs with PATH=/bin:/usr/bin:/usr/sbin:/usr/local/bin:
  # /opt/homebrew/bin — /sbin is NOT on it, so plain `ifconfig` is "not found". Run 2 read that as
  # "the tart guest holds no IPv6 address", which would have made the v6 hole look apple-only. The
  # guest in fact holds a ULA and a link-local; the probe simply could not see them.
  T4=$(asuser "$YOLOAI" exec v6x -- /usr/sbin/ipconfig getifaddr en0 2>/dev/null | tr -d '\r')
  TALL=$(asuser "$YOLOAI" exec v6x -- /sbin/ifconfig en0 2>/dev/null | tr -d '\r' \
         | awk '/inet6/{print $2}' | cut -d% -f1)
  note "tart en0 IPv6 addresses:"; printf '%s\n' "$TALL" | sed 's/^/          /'
  T6=$(printf '%s\n' "$TALL" | grep -v '^fe80' | head -1)
  [ -z "$T6" ] && T6=$(printf '%s\n' "$TALL" | head -1)
  note "tart guest: v4=${T4:-<none>} v6=${T6:-<none>}"
  if [ -z "$T4" ]; then
    unk "V2: could not read the tart guest's address; not measured"
  else
    # macOS guests: route lookup differs from Linux, so the host address is derived from the subnet.
    tart_ex2() {
      case "$1" in
        *"ip route"*)    asuser "$YOLOAI" exec v6x -- /bin/sh -c "/usr/sbin/netstat -rn -f inet | awk '/^default/{print \$2; exit}'" 2>/dev/null | tr -d '\r' ;;
        *"ip -6 route"*) asuser "$YOLOAI" exec v6x -- /bin/sh -c "/usr/sbin/netstat -rn -f inet6 | awk '/^default/{print \$2; exit}'" 2>/dev/null | tr -d '\r' ;;
        *"ip -6 neigh"*) printf '' ;;
        *) asuser "$YOLOAI" exec v6x -- /bin/sh -c "$1" 2>/dev/null | tr -d '\r' ;;
      esac
    }
    probe_backend "tart" tart_ex2 "$T4" "$T6" en0
  fi
fi

# ---------------------------------------------------------------------------
say "V3 CAN pf BLOCK v6 AT ALL IN THIS ANCHOR? — 'we did not' vs 'pf cannot'"
note "If a v6 table in the same anchor enforces, the fix is a second table pair and some plumbing."
note "If it does not load or does not bite, the fix is larger. Very different sizes of problem."
load_pool v6
n=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
note "pool with v6 tables loaded: $n rules (expect twice the v4-only count)"
note "the v6 rules, as loaded:"
pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep inet6 | head -4 | sed 's/^/          /'
if [ -n "${A6:-}" ] && [ -n "${A4:-}" ]; then
  note "Run 1 added ONLY the ULA the backend reports, and the block did not bite. The reason is"
  note "source selection: talking to a LINK-LOCAL host address, the guest sources from its own"
  note "link-local, not its ULA — so the table held an address the traffic never used. That is a"
  note "finding in its own right: a v6 table keyed on 'the address the backend reports' is"
  note "insufficient, because a guest holds several v6 addresses and picks per destination scope."
  A6ALL=$(apple_ex "ip -6 -o addr show eth0 | awk '{print \$4}' | cut -d/ -f1" | tr -d '\r')
  note "every v6 address the guest holds:"
  for a in $A6ALL; do
    note "  $a"
    pfctl -a "$ANCHOR" -t "yb_src6_$SLOT" -T add "$a" >/dev/null 2>&1
  done
  held=$(pfctl -a "$ANCHOR" -t "yb_src6_$SLOT" -T show 2>/dev/null | tr -d ' ' | grep -c . || true)
  note "v6 src table now holds $held entr(y/ies)"
  HV6=$(apple_ex "ip -6 route | awk '/default/{print \$3}'" | tr -d '\r')
  [ -z "$HV6" ] && HV6=$(apple_ex "ip -6 neigh | awk '{print \$1; exit}'" | tr -d '\r')
  if [ -n "$HV6" ]; then
    r6=$(apple_ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 '$(v6url "$HV6" eth0)'")
    r4=$(apple_ex "curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://$ALLOW/")
    note "with the v6 block loaded: guest v6->host=$r6   (want 000)"
    note "                          guest v4->$ALLOW=$r4  (unclaimed slot, so this is unaffected)"
    if [ "$r6" = 000 ]; then
      ok "V3: pf DOES enforce on IPv6 in this anchor. The hole is an omission, not a limitation —"
      note "    but closing it costs more than 'a second table pair': the table must hold EVERY v6"
      note "    address the guest holds, and a guest can acquire more at any time (SLAAC, privacy"
      note "    addresses), which a v4 table never has to contend with."
    else
      bad "V3: the v6 rules loaded and the table held every address the guest has, and it STILL"
      note "    did not bite ($r6). The fix is larger than a table pair."
    fi
  else
    unk "V3: no host v6 address reachable from the guest; not measured"
  fi
else
  unk "V3: no guest v6 address available to test with"
fi

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Global IPv6 egress. This host has no v6 default route, so guest-to-internet over v6 could
          not be attempted at all. Everything above is guest-to-host on the link. If a user's host
          HAS v6 upstream, the hole is wider than what is shown here, not narrower.
        - v6 address recycling. Whether a guest's v6 address is reused across recreate the way its
          v4 lease is — which decides whether a v6 table needs rules 1/1b/1c too — was not measured.
        - SLAAC/privacy addresses. A guest may hold several v6 addresses and rotate them, which
          would defeat a table keyed on one. Only the address the backend reports was used.
        - ICMPv6. Neighbour discovery must keep working under any v6 block; blocking it wholesale
          would break the link. The rules above are TCP-reachability only and were not audited for it.
EOF
