#!/bin/bash
# ABOUTME: M3 — the cheapest reliable way to tell "pf evaluates our anchor" from "our anchor is
# ABOUTME: correct but never reached". Three detectors, two different faults, priced.
#
# Run: sudo bash pf_liveness_detect.sh
#
# WHY THIS EXISTS
#   The worst finding of the last pass: the anchor held correct rules, membership was right, pf was
#   enabled, and pf never descended into the anchor because the main ruleset's `anchor "com.apple/*"`
#   line was gone. All three of D132's start-path checks reported healthy while a denied destination
#   answered 301. Verification that cannot see the deciding state is not verification.
#
#   THREE CANDIDATE DETECTORS, and the point is that they are not equivalent:
#     A  MAIN-RULESET INSPECTION — `pfctl -s rules`, count `com.apple/` references. Reads the exact
#        state that was missing. Needs a grant the shipped one does not include.
#     B  EVALUATION COUNTERS — `pfctl -a <anchor> -vvs rules` reports per-rule `Evaluations`. If our
#        rules are not reached, the counter is frozen even while the guest is transmitting. Reads
#        our own anchor, so it is close to the shipped grant — but it needs a DELTA over time, which
#        makes it a liveness poll and not a start-path check.
#     C  BEHAVIOURAL CANARY — ride rule 1b's window: with our address in `src` and `dst` empty, a
#        connection MUST fail (D4). Validated last pass; needs no privilege at all.
#
#   TWO FAULTS, because one of them is the whole reason this item exists:
#     F1 SHADOWED — the anchor reference is PRESENT but a `pass quick all` sits ahead of it, so pf
#        matches and stops before descending. This is not hypothetical: pf.conf's own comments say
#        system services insert anchors into the main ruleset at runtime, and `quick` is first-match.
#        Detector A reports HEALTHY here — the reference it counts is right there. That is the
#        finding: an inspection is still a proxy, and proxies keep passing while the property is false.
#     F2 FLUSHED — `pfctl -F all`, the fault measured last pass, kept as the control that the
#        detectors work at all.
#
# METHOD: enforcement is proven live before each fault (allowed destination reaches, denied one does
#   not) so that "the detector fired" is never confused with a sandbox that had no network (A22).
#
# SAFETY: deliberately breaks the main ruleset twice and repairs it. Ends by asserting the host is
#   enforcing again; if it cannot, it says so loudly.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_v"
IMG=yoloai-base:latest
ALLOW=1.1.1.1; DENY=1.0.0.1
SLOTS=4; SLOT=1
SUDOERS=/etc/sudoers.d/yoloai-pf-spike-v
PASS=0; FAIL=0; UNKNOWN=0
IP=""

RESULTS="$HERE/results/pf-liveness-detect.txt"
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
pfstate()  { pfctl -s info 2>/dev/null | head -1 | awk '{print $2}'; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
evals()    { pfctl -a "$ANCHOR" -vvs rules 2>/dev/null | awk '/Evaluations:/{s+=$3} END{print s+0}'; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
now_ms()   { python3 -c 'import time;print("%.1f"%(time.time()*1000))'; }
sudoers_list() {   # ls|grep trips shellcheck and mangles odd names; glob and loop instead
  local f out=""
  for f in /etc/sudoers.d/*; do [ -e "$f" ] && out="$out ${f##*/}"; done
  printf '%s' "${out# }"
}


# curl writes %{http_code} even when it fails (000) and ALSO exits non-zero. An `|| printf 000`
# fallback therefore appends a second 000, and every "is it blocked?" test silently compares
# "000000" against "000" and reads a successful block as a failure. Run 1 lost four verdicts to it.
try() { local o; o=$(asuser container exec ybv1 curl -s -o /dev/null -w '%{http_code}' \
          --max-time 5 "http://$1/" 2>/dev/null); printf '%s' "${o:-000}"; }

load_pool() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
    done; } > /tmp/pfv.rules
  pfctl -a "$ANCHOR" -f /tmp/pfv.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}
claim()    { pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$IP"    >/dev/null 2>&1; }
populate() { pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1; }
empty()    { pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T flush        >/dev/null 2>&1; }

# --- the three detectors, each returning HEALTHY or BROKEN plus its own elapsed ms -------------
detA() {   # main-ruleset inspection
  local t0 t1 n
  t0=$(now_ms); n=$(mainrefs); t1=$(now_ms)
  printf '%s %s' "$([ "$n" -gt 0 ] && echo HEALTHY || echo BROKEN)" \
                 "$(python3 -c "print('%.1f' % ($t1-$t0))")"
}
detB() {   # evaluation-counter delta across a traffic burst
  local before after t0 t1
  before=$(evals)
  t0=$(now_ms)
  try "$ALLOW" >/dev/null 2>&1; try "$ALLOW" >/dev/null 2>&1
  after=$(evals)
  t1=$(now_ms)
  printf '%s %s (evals %s->%s)' \
    "$([ "$after" -gt "$before" ] && echo HEALTHY || echo BROKEN)" \
    "$(python3 -c "print('%.1f' % ($t1-$t0))")" "$before" "$after"
}
detC() {   # behavioural canary on rule 1b's empty-dst window
  local t0 t1 c
  empty
  t0=$(now_ms); c=$(try "$ALLOW"); t1=$(now_ms)
  populate
  printf '%s %s (code %s)' \
    "$([ "$c" = 000 ] && echo HEALTHY || echo BROKEN)" \
    "$(python3 -c "print('%.1f' % ($t1-$t0))")" "$c"
}
# Runs all three and leaves the verdicts in DA/DB/DC. Deliberately NOT via command substitution:
# the human-readable lines and the verdicts go to different places, and capturing stdout would
# swallow the former into the latter.
DA=""; DB=""; DC=""
run_detectors() {
  local a b c
  a=$(detA); b=$(detB); c=$(detC)
  note "A main-ruleset inspection : $a ms"
  note "B evaluation counters     : $b ms"
  note "C behavioural canary      : $c ms"
  DA=${a%% *}
  DB=$(printf '%s' "$b" | awk '{print $1}')
  DC=$(printf '%s' "$c" | awk '{print $1}')
}

repair() {
  pfctl -e >/dev/null 2>&1
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  asuser container system stop  >/dev/null 2>&1; sleep 4
  asuser container system start >/dev/null 2>&1; sleep 6
  asuser container rm -f ybv1 >/dev/null 2>&1
  asuser container run -d --name ybv1 "$IMG" sleep 900 >/dev/null 2>&1
  sleep 4
  IP=$(netfield ybv1 ipv4Address)
  [ -n "$IP" ] && { load_pool >/dev/null; claim; populate; }
}

cleanup() {
  echo
  echo "== cleanup =="
  rm -f "$SUDOERS" /tmp/pfv.rules /tmp/pfv.conf
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser container rm -f ybv1 >/dev/null 2>&1
  echo "   pf=$(pfstate)  main-refs=$(mainrefs)  sudoers: [$(sudoers_list)]"
  if [ "$(mainrefs)" -eq 0 ]; then
    echo "   !! HOST IS FAIL-OPEN — repairing"
    pfctl -f /etc/pf.conf >/dev/null 2>&1
    asuser container system stop >/dev/null 2>&1; sleep 3
    asuser container system start >/dev/null 2>&1; sleep 4
    echo "   after repair: main-refs=$(mainrefs)"
  fi
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "V0 SETUP — and proof that enforcement is real before anything is broken"
asuser container system start >/dev/null 2>&1; sleep 2
[ "$(mainrefs)" -gt 0 ] || { bad "host is already fail-open; run pf_anchor_eval.sh. ABORTING"; exit 1; }
asuser container rm -f ybv1 >/dev/null 2>&1
asuser container run -d --name ybv1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybv1 ipv4Address)
note "guest=$IP  pf=$(pfstate)  main-refs=$(mainrefs)"
[ -n "$IP" ] || { bad "no guest address; ABORTING"; exit 1; }
load_pool; claim; populate
a=$(try "$ALLOW"); d=$(try "$DENY")
note "allowed->$ALLOW=$a   denied->$DENY=$d   (need non-000 then 000)"
if [ "$a" != 000 ] && [ "$d" = 000 ]; then
  ok "enforcement is live: the allowlist passes and a denied destination is refused"
else
  bad "enforcement not live at the start (allow=$a deny=$d); nothing below would mean anything. ABORTING"
  exit 1
fi

# ---------------------------------------------------------------------------
say "V1 ALL THREE DETECTORS ON A HEALTHY HOST"
note "Each must say HEALTHY here. A detector that says BROKEN on a working host is worse than none."
run_detectors
if [ "$DA|$DB|$DC" = "HEALTHY|HEALTHY|HEALTHY" ]; then
  ok "V1: all three agree the host is healthy"
else
  bad "V1: disagreement on a healthy host ($DA|$DB|$DC) — a false positive is disqualifying"
fi

# ---------------------------------------------------------------------------
say "V2 FAULT F1 'SHADOWED' — the reference is PRESENT and pf still never descends"
note "pf.conf's own comments warn that system services insert anchors into the main ruleset at"
note "runtime. Rules are first-match with 'quick', so anything inserted ahead of the com.apple"
note "anchor point shadows it. Here that is simulated with one 'pass quick all' line, loaded from a"
note "copy of /etc/pf.conf — so the reference detector A counts is still right there in the ruleset."
# Two edits, and the second is load-bearing. /etc/pf.anchors/com.apple contains ONLY the AirDrop
# and ApplicationFirewall anchors — not vmnet's NAT, which the backend inserts at runtime. So
# `load anchor "com.apple" from ...` REPLACES the anchor's live contents and destroys that NAT.
# Run 1 kept the line, the guest lost all egress, and both destinations returned 000: the fault was
# masked by NAT death, which is the same trap that forced last pass's fail-closed retraction.
# Dropping the line leaves the anchor's dynamic contents intact, so what is tested is shadowing
# alone.
awk '/^anchor "com\.apple\/\*"/ && !done {print "pass quick all"; done=1}
     /^load anchor/ {next}
     {print}' /etc/pf.conf > /tmp/pfv.conf
note "the injected ruleset, in full:"
sed 's/^/          | /' /tmp/pfv.conf | grep -v '^          | *#' | grep -v '^          | *$'
pfctl -f /tmp/pfv.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
note "main-refs after injection = $(mainrefs)  (still non-zero: that is the entire point)"
note ""
note "Dropping the 'load anchor' line was not enough. ANY 'pfctl -f' of the main ruleset re-declares"
note "nat-anchor \"com.apple/*\" with no body, and a declared-but-unfilled anchor loads EMPTY — so"
note "vmnet's runtime NAT goes with it either way, and run 2 still saw both destinations refused."
note "Restarting the backend daemon re-inserts that NAT while leaving the injected main ruleset"
note "alone, which is the same asymmetry the repair procedure already relies on."
asuser container system stop  >/dev/null 2>&1; sleep 4
asuser container system start >/dev/null 2>&1; sleep 6
asuser container rm -f ybv1 >/dev/null 2>&1
asuser container run -d --name ybv1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybv1 ipv4Address)
note "guest re-created at ${IP:-<none>} | main-refs=$(mainrefs) (the injection must have survived)"
if [ -n "$IP" ]; then load_pool >/dev/null; claim; populate; fi
sa=$(try "$ALLOW"); sd=$(try "$DENY")
note "allowed->$ALLOW=$sa   denied->$DENY=$sd   (denied reaching => enforcement is dead)"
if [ "$sa" = 000 ] && [ "$sd" = 000 ]; then
  unk "V2: BOTH destinations refused, so the guest has no egress at all and nothing was tested."
  note "     That is NAT death, not enforcement — the classic masking failure. Check that the"
  note "     injected ruleset above still omits the 'load anchor' line."
elif [ "$sd" != 000 ]; then
  ok "V2 precondition: enforcement IS dead — the denied destination answered $sd"
  run_detectors
  if [ "$DA" = HEALTHY ]; then
    ok "V2 THE FINDING: detector A reports HEALTHY on a host that is not enforcing."
    note "    An inspection of the main ruleset is STILL a proxy. It counts the reference and cannot"
    note "    see that something matched first. This is the same failure class as last pass, one"
    note "    level up — so 'just read the main ruleset' does not close the gap."
  else
    note "    detector A reported $DA — it caught this fault too; record and re-read the ruleset above"
  fi
  if [ "$DB" = BROKEN ] && [ "$DC" = BROKEN ]; then
    ok "V2: both the counter delta and the canary detect it"
  else
    bad "V2: a detector missed the shadowed fault (B=$DB C=$DC)"
  fi
else
  unk "V2: the injection did not actually stop enforcement (denied=$sd); nothing tested"
fi
note "restoring the real /etc/pf.conf"
pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
sleep 1
rd=$(try "$DENY")
note "denied->$DENY=$rd after restore  (000 = enforcement back, no daemon restart needed)"

# ---------------------------------------------------------------------------
say "V3 FAULT F2 'FLUSHED' — the control, and the fault measured last pass"
pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
asuser container system stop  >/dev/null 2>&1; sleep 4
asuser container system start >/dev/null 2>&1; sleep 6
asuser container rm -f ybv1 >/dev/null 2>&1
asuser container run -d --name ybv1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(netfield ybv1 ipv4Address)
note "guest now $IP | main-refs=$(mainrefs) | pf=$(pfstate)"
if [ -z "$IP" ]; then
  unk "V3: no guest address after the flush; cannot run the detectors"
else
  load_pool >/dev/null; claim; populate
  fa=$(try "$ALLOW"); fd=$(try "$DENY")
  note "allowed->$ALLOW=$fa   denied->$DENY=$fd"
  if [ "$fd" != 000 ]; then
    run_detectors
    if [ "$DA|$DB|$DC" = "BROKEN|BROKEN|BROKEN" ]; then
      ok "V3: all three detect the flushed fault"
    else
      bad "V3: not all detectors caught the flushed fault ($DA|$DB|$DC)"
    fi
  else
    unk "V3: the flush did not disable enforcement (denied=$fd)"
  fi
fi

# ---------------------------------------------------------------------------
say "V4 GRANT SURFACE — what each detector would cost at the security boundary"
note "The shipped D132 grant permits exactly four forms. Installing it here in its real shape (with"
note "this run's anchor root) and asking for each detector's command is the only way to know."
cat > "$SUDOERS" <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_v -t yb_(src|dst)_([0-9]|[12][0-9]|3[01]) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_v -f /etc/yoloai/pf-pool\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_v -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
chmod 440 "$SUDOERS"
if ! visudo -c -f "$SUDOERS" >/dev/null 2>&1; then
  unk "V4: the grant file did not validate; skipping"
else
  note "grant installed and validated"
  probe() {   # $1 = label, $* = argv
    local label="$1"; shift
    if asuser sudo -n /sbin/pfctl "$@" >/dev/null 2>&1; then
      printf '        PERMITTED  %-34s %s\n' "$label" "pfctl $*"
    else
      printf '        refused    %-34s %s\n' "$label" "pfctl $*"
    fi
  }
  probe "detector C (none needed)"      -s info
  probe "detector A wants"              -s rules
  probe "detector B wants"              -a "$ANCHOR" -vvs rules
  probe "(the permitted anchor read)"   -a "$ANCHOR" -s rules
  note ""
  note "So detector A needs a NEW grant line for the WHOLE main ruleset, and detector B needs the"
  note "existing anchor read widened to -vv. Both are security-boundary changes: D132's permit/refuse"
  note "matrix has to be re-run, not just a regex edited. Detector C needs nothing."
  rm -f "$SUDOERS"
  if asuser sudo -n /sbin/pfctl -s info >/dev/null 2>&1; then
    bad "V4: the grant survived removal — check /etc/sudoers.d"
  else
    ok "V4: grant removed and refusal re-verified"
  fi
fi

# ---------------------------------------------------------------------------
say "V5 REPAIR, THEN PRICE THE DETECTORS ON A HEALTHY HOST"
repair
note "after repair: main-refs=$(mainrefs) pf=$(pfstate) guest=$IP"
ra=$(try "$ALLOW"); rdd=$(try "$DENY")
note "allowed=$ra denied=$rdd  (need non-000 then 000)"
if [ "$ra" != 000 ] && [ "$rdd" = 000 ]; then
  ok "host is sound again"
else
  bad "host did NOT end enforcing (allow=$ra deny=$rdd) — run pf_anchor_eval.sh"
fi
note ""
note "Five repetitions of each, on a healthy host, since that is the case that runs every time:"
for i in 1 2 3 4 5; do
  printf '        run %s   A=%s   B=%s   C=%s\n' "$i" "$(detA)" "$(detB)" "$(detC)"
done
note ""
note "Read the middle column of each: that is the millisecond cost added per check. Compare against"
note "a ~2.4s sandbox start. Detector B's figure includes the two requests it needs to see a delta,"
note "which is why it is a poll and not a start-path check — at start there is no prior traffic to"
note "compare against."

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - A third fault: pf DISABLED (`pfctl -d`). D132's check 1 already covers it and `-s info` is
          in the shipped grant, so it is the one state existing verification does see.
        - Shadowing by a real system service. `pass quick all` was injected by hand; no service was
          induced to insert its own anchor ahead of com.apple. The mechanism is pf.conf's own
          documented behaviour, but the specific trigger is not demonstrated here.
        - Detector cost under load. All timings are on an idle host with one guest.
        - Whether the canary can run concurrently with a real acquisition. It rides rule 1b's window,
          so two sandboxes starting at once share that window; not tested.
EOF
