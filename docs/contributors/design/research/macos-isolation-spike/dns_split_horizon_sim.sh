#!/bin/bash
# ABOUTME: The DNS case dns-gaps.txt never caught — one name resolving DIFFERENTLY on each side —
# ABOUTME: simulated end to end, plus whether pf will accept a host-relative address into a dst.
#
# Run: sudo bash dns_split_horizon_sim.sh
#
# WHY THIS EXISTS
#   dns-gaps.txt found two real divergences (search-domain names and mDNS names are host-only) but
#   never the worst case: a name that BOTH sides resolve, to DIFFERENT addresses. That is the one
#   that produces a silently broken allowlist rather than an inert one — the host's answer goes
#   into `dst`, the guest sends to its own answer, and the packet is dropped by a rule the user
#   believes permits it.
#
#   It is untestable by waiting for a corporate split-DNS zone to appear, and entirely testable by
#   creating the condition: an /etc/hosts entry makes the HOST's getaddrinfo return one address
#   while the guest's resolver returns the real ones. That is a faithful reproduction of the
#   mechanism — two resolvers, one name, two answers — not an approximation of it.
#
#   H1 THE DIVERGENCE, END TO END. Resolve host-side, install that into `dst` exactly as the pf
#      design would, then have the guest connect to the NAME. It must fail. Paired, in the same
#      run, with the guest reaching the host's answer directly — otherwise "it failed" is just a
#      broken sandbox (A22 / DF172).
#   H2 WILL pf ACCEPT A HOST-RELATIVE ADDRESS? dns-gaps.txt caught the host resolving its own
#      .local name to 127.0.0.1 and to the vmnet gateway. If pf refuses such addresses in a table,
#      the design is protected for free. If it accepts them, the design must validate resolver
#      output itself, and that is a requirement rather than a nicety — 127.0.0.1 in a guest's dst
#      does not allowlist what the user named, it allowlists the guest's own loopback.
#
# SAFETY: edits /etc/hosts and restores it from a backup taken in this run, with the restore in an
# EXIT trap so an interrupted run still puts it back. Never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=4; SLOT=1
SB=sh-a
NAME=example.com          # both sides resolve it today; the point is to make them disagree
HOSTANS=1.1.1.1           # what /etc/hosts will make the HOST believe, and a reachable address
WD=$(mktemp -d /tmp/pfsh-wd.XXXXXX)
HOSTS=/etc/hosts
BACKUP=$(mktemp /tmp/pfsh-hosts.XXXXXX)
MARK="# yoloai-split-horizon-sim"
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""; EDITED=0

RESULTS="$HERE/results/dns-split-horizon-sim.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
tshow()    { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }

PYPROG='
import socket,sys
out=set()
for d in sys.argv[1:]:
    try:
        for r in socket.getaddrinfo(d,None,socket.AF_INET): out.add(r[4][0])
    except OSError: out.add("UNRESOLVED")
print(" ".join(sorted(out)) if out else "UNRESOLVED")'
hostres()  { python3 -c "$PYPROG" "$@" 2>/dev/null; }
guestres() { asuser container exec "yoloai-cli-$SB" python3 -c "$PYPROG" "$@" 2>/dev/null; }
reach() {
  local c
  c=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

cleanup() {
  echo
  echo "== cleanup =="
  if [ "$EDITED" -eq 1 ]; then
    cp "$BACKUP" "$HOSTS" && echo "   /etc/hosts restored from backup"
    dscacheutil -flushcache 2>/dev/null; killall -HUP mDNSResponder 2>/dev/null
    if grep -q "$MARK" "$HOSTS" 2>/dev/null; then
      echo "   !! MARKER STILL PRESENT in $HOSTS — remove the yoloai-split-horizon-sim lines by hand"
    else
      echo "   verified: no simulation lines remain in $HOSTS"
    fi
    echo "   host now resolves $NAME to: $(hostres "$NAME")"
  fi
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed $SB" || echo "   NOTE $SB not destroyed — remove by hand"
  rm -rf "$WD" "$BACKUP" /tmp/pfsh.*
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | simulated name=$NAME"

# ---------------------------------------------------------------------------
say "H0 SETUP — a sandbox, and the two sides agreeing before anything is faked"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open (main-refs=0); run pf_anchor_eval.sh. ABORTING"; exit 1; }
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB"; exit 1; }
SB_IP=$(ipof "$SB"); [ -n "$SB_IP" ] || { bad "no address; ABORTING"; exit 1; }
H0=$(hostres "$NAME"); G0=$(guestres "$NAME")
echo "        $SB at $SB_IP"
echo "        host  resolves $NAME -> $H0"
echo "        guest resolves $NAME -> $G0"
if [ "$H0" = "$G0" ] && [ "$H0" != UNRESOLVED ]; then
  ok "both sides agree before the simulation, so a later disagreement is the thing we introduced"
else
  unk "the two sides already differ or one failed; the simulation still runs but the 'before'"
  echo "           control is weaker than intended"
fi

# ---------------------------------------------------------------------------
say "H1 CREATE THE DIVERGENCE — /etc/hosts makes the HOST answer differently"
cp "$HOSTS" "$BACKUP"; EDITED=1
printf '%s\n%s %s\n' "$MARK" "$HOSTANS" "$NAME" >> "$HOSTS"
# NO FLUSH FIRST. Two earlier runs flushed and restarted mDNSResponder to make the host notice the
# new entry, and both took the GUEST's DNS down with it — still dead 25 seconds later, which is a
# fact about this topology rather than a flake: the guest resolves through the vmnet gateway, which
# forwards through the host's mDNSResponder. macOS watches /etc/hosts, so the host usually picks the
# entry up on its own, and the flush is only worth its side effect if that fails.
sleep 2
H1=$(hostres "$NAME")
echo "        host after edit, no flush: $H1"
if [ "$H1" != "$HOSTANS" ]; then
  echo "        host did not pick it up unaided; flushing (this is known to disturb guest DNS)"
  dscacheutil -flushcache 2>/dev/null; killall -HUP mDNSResponder 2>/dev/null
  sleep 8
  H1=$(hostres "$NAME")
fi
G1=UNRESOLVED
for attempt in 1 2 3 4 5; do
  G1=$(guestres "$NAME")
  [ -n "$G1" ] && [ "$G1" != UNRESOLVED ] && break
  echo "        guest resolver not answering yet (attempt $attempt)"
  sleep 4
done
echo "        host  now resolves $NAME -> $H1   (expected $HOSTANS)"
echo "        guest still resolves $NAME -> $G1"
if [ "$H1" = "$HOSTANS" ] && [ "$G1" != "$H1" ] && [ "$G1" != UNRESOLVED ]; then
  ok "the two sides now disagree about one name — split horizon, reproduced"
elif [ "$G1" = UNRESOLVED ]; then
  bad "the guest's resolver did not recover from the mDNSResponder restart within 25s. That is a"
  echo "           finding about the topology — guest DNS depends on the host's resolver daemon — but"
  echo "           it is NOT the divergence this run exists to test. ABORTING rather than reporting"
  echo "           a dead resolver as a split-horizon result."
  exit 1
else
  bad "could not create the divergence (host=$H1 guest=$G1); the rest proves nothing. ABORTING"
  exit 1
fi

say "H1b INSTALL THE HOST'S ANSWER, AS THE pf DESIGN WOULD, AND SEE WHO IS RIGHT"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block return in quick from <yb_src_$i> to any"
  done; } > /tmp/pfsh.rules
pfctl -a "$ANCHOR" -f /tmp/pfsh.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1
for a in $H1; do pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$a" >/dev/null 2>&1; done
echo "        dst now holds the HOST's answer: $(tshow "yb_dst_$SLOT")"
echo "        the user asked to allow '$NAME'. Two questions follow."
byname=$(reach "$NAME")
byhost=$(reach "$HOSTANS")
echo "        guest -> $NAME (resolves to its OWN answer): $byname"
echo "        guest -> $HOSTANS (the host's answer, direct): $byhost   <-- positive control"
if [ "$byhost" = 000 ]; then
  unk "H1b: the control failed — the guest cannot reach the allowlisted address either, so the"
  echo "           block is not attributable to the divergence (DF172)"
elif [ "$byname" = 000 ] && [ "$byhost" != 000 ]; then
  bad "H1b: CONFIRMED, and it is the silent kind. The user allowlisted $NAME; the guest cannot"
  echo "           reach $NAME, while the address that WAS installed is reachable. Nothing is"
  echo "           misconfigured, no component erred, and no error is produced anywhere — the two"
  echo "           sides simply mean different things by one name. Host-side resolution is not a"
  echo "           transparent substitution for guest-side resolution."
else
  ok "H1b: the guest reached $NAME anyway ($byname) — on this host the divergence did not bite"
fi

# ---------------------------------------------------------------------------
say "H2 WILL pf ACCEPT A HOST-RELATIVE ADDRESS INTO A dst TABLE?"
echo "        dns-gaps.txt caught the host resolving its own .local name to 127.0.0.1 and to the"
echo "        vmnet gateway. Those mean something DIFFERENT inside a guest: 127.0.0.1 in a guest's"
echo "        allowlist permits the guest to reach ITSELF, not the host the user meant. The"
echo "        question is whether pf refuses such entries, or whether the design must."
pfctl -a "$ANCHOR" -t "yb_dst_2" -T flush >/dev/null 2>&1
accepted=""
for a in 127.0.0.1 169.254.1.1 224.0.0.1 0.0.0.0 192.168.64.1; do
  out=$(pfctl -a "$ANCHOR" -t "yb_dst_2" -T add "$a" 2>&1); rc=$?
  present=$(pfctl -a "$ANCHOR" -t yb_dst_2 -T show 2>/dev/null | tr -d ' ' | grep -c "^$a$" || true)
  printf '        %-16s rc=%d present=%s  %s\n' "$a" "$rc" "$present" "$(printf '%s' "$out" | head -1)"
  [ "$present" -gt 0 ] && accepted="$accepted $a"
done
echo "        accepted into the table:$accepted"
if [ -n "$accepted" ]; then
  bad "H2: pf accepts host-relative and non-routable addresses into a dst table without complaint."
  echo "           So nothing below yoloAI will catch a resolver answer that is meaningless or"
  echo "           actively wrong inside a guest. Validating resolver output — rejecting loopback,"
  echo "           link-local, multicast and the guest's own gateway — is the design's job, and it"
  echo "           is a requirement rather than a refinement."
else
  ok "H2: pf rejects these addresses itself, so the design is protected without doing anything"
fi
