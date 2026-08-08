#!/bin/bash
# ABOUTME: What else rewrites the MAIN ruleset — the object whose loss silently unfilters every
# ABOUTME: sandbox — plus whether a broken host ever heals itself, and what -X does to the count.
#
# Run: sudo bash pf_main_ruleset_writers.sh          (~15 min; THE MACHINE WILL SLEEP TWICE)
#
# WHY THIS EXISTS
#   pf-flush-reference.txt established that the fragile object is not our anchor but the main
#   ruleset's `anchor "com.apple/*"` line: lose it and every rule we own is loaded, correct and
#   never evaluated, with all three of D132's checks reporting healthy. `pfctl -F all` destroys it.
#   That immediately raises three questions nobody has asked, because until now the main ruleset
#   was assumed to be something only we might damage.
#
#   S1/S2 WHO ELSE WRITES IT. /etc/pf.conf says in its own comments that "some system services
#      would dynamically insert anchors into the main ruleset", and the census found
#      com.apple.internet-sharing doing exactly that — it was one of only two lines left in the
#      main ruleset when this host was broken. A service that rewrites that ruleset is a candidate
#      trigger with no relationship to yoloAI, which is the same shape as the Linux hazard.
#   S3 SLEEP/WAKE. The most ordinary event on any laptop, and completely untested. If a wake
#      rebuilds the main ruleset without the anchor reference, every closed lid is a fail-open.
#   S4 DOES A BROKEN HOST HEAL? We have always repaired immediately, so nobody knows how long a
#      user stays exposed. If nothing restores the reference, the answer is "until reboot", and
#      that decides how loud the detector has to be.
#   S5 DOES A WAKE HEAL IT? The interesting cross of S3 and S4: if waking reloads pf.conf, sleep is
#      a repair mechanism rather than a hazard. Worth knowing before designing one.
#   S6 -X AND THE COUNT, properly this time. pf-flush-reference.txt R3 tried to release a token
#      that an earlier `pfctl -d` had already destroyed, got `token invalid`, and recorded a
#      vacuous PASS. Here the token is taken and released with no -d in between.
#
# EVERY VERDICT PAIRS ITS NEGATIVE WITH A POSITIVE CONTROL (A22): allow and deny are both read
# live, and a run where the permitted destination also fails yields UNKNOWN, never PASS.
#
# SAFETY: deliberately breaks the main ruleset in S4, repairs it, and asserts the host is
# enforcing before it exits. If it dies mid-run, pf_anchor_eval.sh repairs.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=4; SLOT=1
SB=mw-w
ALLOW=1.1.1.1; DENY=1.0.0.1
WD=$(mktemp -d /tmp/pfmw2-wd.XXXXXX)
WAKE_AFTER=75          # seconds; the machine auto-wakes so this needs no keyboard
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""; TOKEN=""; CACHE_WAS=""

RESULTS="$HERE/results/pf-main-ruleset-writers.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
tshow()    { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
pfstate()  { pfctl -s info 2>/dev/null | head -1 | awk '{print $2}'; }
showrefs() { pfctl -s References 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
arm() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block drop in quick from <yb_src_$i> to any"
    done; } > /tmp/pfmw2.rules
  pfctl -a "$ANCHOR" -f /tmp/pfmw2.rules >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1
}
LAST_A=""; LAST_D=""
# State first, egress second: the state half is evidence even when the egress half is unattributable.
report() {
  LAST_A=$(egress "$ALLOW"); LAST_D=$(egress "$DENY")
  printf '        pf=%-9s main-refs=%-2s anchor=%-2s rules  slot=%-18s || allow=%s deny=%s\n' \
    "$(pfstate)" "$(mainrefs)" "$(nrules)" "$(tshow "yb_src_$SLOT")" "$LAST_A" "$LAST_D"
}
verdict() {
  local label="$1"
  if [ "$LAST_A" = 000 ]; then
    unk "$label: permitted destination also unreachable — guest has no network, nothing"
    echo "           attributable (DF172). State half: main-refs=$(mainrefs) anchor=$(nrules) rules."
  elif [ "$(mainrefs)" -eq 0 ]; then
    bad "$label: MAIN RULESET REFERENCE GONE — the anchor is loaded and never evaluated. This is"
    echo "           a fail-open trigger, and an ordinary one."
  elif [ "$LAST_D" != 000 ]; then
    bad "$label: denied destination reachable — unenforced"
  else
    ok "$label: SURVIVED — reference, anchor, membership and live enforcement all intact"
  fi
}
repair() {
  pfctl -e >/dev/null 2>&1
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  asuser container system stop  >/dev/null 2>&1; sleep 4
  asuser container system start >/dev/null 2>&1; sleep 6
  asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
  SB_IP=$(ipof "$SB"); [ -n "$SB_IP" ] && arm
}
# Re-establish the guest after an event that may have moved or stopped it. Returns 1 if the guest
# had to be re-armed, so a caller can say whether membership survival was observable.
resettle() {
  local before="$SB_IP" now
  asuser container system start >/dev/null 2>&1
  now=$(ipof "$SB")
  if [ -z "$now" ]; then
    asuser "$YOLOAI" start "$SB" >/dev/null 2>&1; sleep 8; now=$(ipof "$SB")
  fi
  if [ -n "$now" ] && [ "$now" != "$before" ]; then
    echo "        guest address moved $before -> $now; re-arming (membership survival not observable)"
    SB_IP="$now"; arm; return 1
  fi
  return 0
}

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$TOKEN" ] && pfctl -X "$TOKEN" >/dev/null 2>&1
  [ "$CACHE_WAS" = "false" ] && { AssetCacheManagerUtil deactivate >/dev/null 2>&1; echo "   content caching deactivated (restored)"; }
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  pmset schedule cancelall >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed $SB" || echo "   NOTE $SB not destroyed — remove by hand"
  rm -rf "$WD" /tmp/pfmw2.*
  echo "   pf=$(pfstate)  main-refs=$(mainrefs)  references: $(showrefs)"
  [ "$(mainrefs)" -eq 0 ] && echo "   !! HOST IS FAIL-OPEN — run pf_anchor_eval.sh to repair"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "S0 SETUP — an enforcing sandbox on a sound host"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB"; exit 1; }
SB_IP=$(ipof "$SB"); [ -n "$SB_IP" ] || { bad "no address; ABORTING"; exit 1; }
echo "        $SB at $SB_IP | main-refs=$(mainrefs)"
[ "$(mainrefs)" -gt 0 ] || { bad "host is ALREADY fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
ba=$(egress "$ALLOW"); bd=$(egress "$DENY")
echo "        before arming: allow=$ba deny=$bd (both must be reachable)"
{ [ "$ba" != 000 ] && [ "$bd" != 000 ]; } || { bad "no open egress; ABORTING"; exit 1; }
arm; report
{ [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; } || { bad "not enforcing after arm; ABORTING"; exit 1; }
ok "enforcing, on a host whose main ruleset references the anchor"
echo "        the main ruleset as it should look:"
pfctl -s rules 2>/dev/null | sed 's/^/        | /'

# ---------------------------------------------------------------------------
say "S1 CONTENT CACHING activated then deactivated — a system service that rewires networking"
CACHE_WAS=$(AssetCacheManagerUtil status 2>&1 | awk '/Activated:/{print $2}')
echo "        content caching Activated was: ${CACHE_WAS:-unknown}"
if ! command -v AssetCacheManagerUtil >/dev/null 2>&1; then
  unk "S1: AssetCacheManagerUtil absent — NOT TRIED"
elif [ "$CACHE_WAS" != "false" ]; then
  unk "S1: content caching was already active; leaving the host's setting alone — NOT TRIED"
  CACHE_WAS=""
else
  AssetCacheManagerUtil activate 2>&1 | head -3 | sed 's/^/        acmu: /'
  sleep 8
  echo "        activated; main-refs=$(mainrefs)"
  report; verdict "S1a content caching activated"
  AssetCacheManagerUtil deactivate 2>&1 | head -3 | sed 's/^/        acmu: /'
  CACHE_WAS=""
  sleep 5
  report; verdict "S1b content caching deactivated"
fi

# ---------------------------------------------------------------------------
say "S2 INTERNET SHARING restarted — the service the census caught writing the main ruleset"
if launchctl print system/com.apple.InternetSharing >/dev/null 2>&1; then
  launchctl kickstart -k system/com.apple.InternetSharing 2>&1 | head -3 | sed 's/^/        launchctl: /'
  sleep 6
  report; verdict "S2 InternetSharing restarted"
else
  unk "S2: com.apple.InternetSharing is not a loaded service on this host — NOT TRIED."
  echo "           Its ANCHOR is present in the main ruleset regardless (the census saw it), so the"
  echo "           service inserts and removes that anchor on demand. Whether doing so disturbs"
  echo "           com.apple/* is therefore still open, and needs a host with sharing enabled."
fi

# ---------------------------------------------------------------------------
say "S3 SLEEP/WAKE on a healthy host — the most ordinary event there is"
echo "        scheduling an auto-wake ${WAKE_AFTER}s out, then sleeping. No keyboard needed."
pmset relative wake "$WAKE_AFTER" 2>&1 | sed 's/^/        pmset: /'
SLEPT_AT=$(date '+%H:%M:%S')
pmset sleepnow >/dev/null 2>&1
sleep $((WAKE_AFTER + 25))
echo "        slept at $SLEPT_AT, awake at $(date '+%H:%M:%S')"
echo "        immediately after wake: pf=$(pfstate) main-refs=$(mainrefs) anchor=$(nrules) rules"
echo "        slot membership: $(tshow "yb_src_$SLOT")"
resettle; moved=$?
report
verdict "S3 sleep/wake"
[ "$moved" -ne 0 ] && echo "        (the guest moved across the sleep, so only the RULESET half of this is a survival result)"

# ---------------------------------------------------------------------------
say "S4 DOES A BROKEN HOST HEAL ITSELF? — break it, then watch for 4 minutes"
echo "        Every previous run repaired immediately. Nobody knows how long a real user would sit"
echo "        unfiltered, and that decides how loud the detector has to be."
pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
echo "        broken: main-refs=$(mainrefs)"
healed=0
for t in 30 60 90 120 150 180 210 240; do
  sleep 30
  r=$(mainrefs)
  printf '        t=%-4s main-refs=%s\n' "${t}s" "$r"
  if [ "$r" -gt 0 ]; then healed=1; echo "        restored on its own at t=${t}s"; break; fi
done
if [ "$healed" -eq 1 ]; then
  ok "S4: something on the host restored the anchor reference without intervention"
else
  bad "S4: 4 minutes with no reference and nothing restored it. A host broken this way stays"
  echo "           fail-open indefinitely — in practice until a reboot or a manual pf.conf reload."
  echo "           Any sandbox started in that window passes every check and is unfiltered."
fi

# ---------------------------------------------------------------------------
say "S5 DOES A WAKE HEAL IT? — sleep/wake while broken"
echo "        If waking rebuilds the main ruleset from /etc/pf.conf, sleep is a repair rather than"
echo "        a hazard, and that changes what the detector has to cover."
echo "        before: main-refs=$(mainrefs)"
pmset relative wake "$WAKE_AFTER" 2>&1 | sed 's/^/        pmset: /'
pmset sleepnow >/dev/null 2>&1
sleep $((WAKE_AFTER + 25))
echo "        after wake: main-refs=$(mainrefs) | awake at $(date '+%H:%M:%S')"
if [ "$(mainrefs)" -gt 0 ]; then
  ok "S5: a wake DID restore the anchor reference — sleep/wake reloads the main ruleset"
else
  bad "S5: still no reference after a sleep/wake. The host does not heal on wake either, so the"
  echo "           fail-open window is bounded only by a reboot or an explicit repair."
fi

# ---------------------------------------------------------------------------
say "S6 REPAIR, then -X properly — no -d in between this time"
repair; report
if [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; then
  ok "host repaired and enforcing (main-refs=$(mainrefs))"
else
  bad "repair failed (allow=$LAST_A deny=$LAST_D main-refs=$(mainrefs))"
fi
echo "        references before: $(showrefs)"
out=$(pfctl -E 2>&1); echo "$out" | quiet_pf | sed 's/^/        pfctl: /'
TOKEN=$(printf '%s' "$out" | grep -oE 'Token : [0-9]+' | awk '{print $3}')
echo "        our token: ${TOKEN:-<none>} | references: $(showrefs)"
if [ -z "$TOKEN" ]; then
  unk "S6: no token issued — NOT TRIED"
else
  pfctl -X "$TOKEN" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  rc_state=$(pfstate); refs_after=$(showrefs)
  TOKEN=""
  echo "        after releasing it: pf=$rc_state | references: $refs_after"
  if [ "$rc_state" = Enabled ]; then
    ok "S6: releasing our own reference did NOT disable pf — this time the token was valid when"
    echo "           released, so unlike the earlier attempt this is a real result"
  else
    bad "S6: pf went $rc_state when our reference was released. An -X by yoloAI can de-isolate"
    echo "           every VM on the host, which is exactly why this was deferred for so long."
  fi
fi

# ---------------------------------------------------------------------------
say "S7 FINAL VERIFY — the host must leave this run enforcing"
pfctl -e >/dev/null 2>&1
[ "$(mainrefs)" -eq 0 ] && repair
report
if [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; then
  ok "host is sound: pf enabled, anchor referenced, enforcement real"
else
  bad "host did NOT end enforcing — run pf_anchor_eval.sh"
fi
