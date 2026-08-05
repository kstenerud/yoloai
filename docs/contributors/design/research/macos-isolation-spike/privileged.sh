#!/bin/bash
# ABOUTME: The single privileged entry point for the macOS isolation spike — host `pf`
# ABOUTME: enforcement per sandbox (apple/tart), the dedicated-gid design, and rule reaping.
#
# Run: sudo bash privileged.sh <apple-A> <tart> [apple-B] [isolated-apple]
#   e.g. sudo bash privileged.sh mac-coh-apple mac-coh-tart mac-coh-apple-b mac-iso-apple
#
# apple-B is the per-VM scoping discriminator; without it P1a proves the rule blocks a VM
# but NOT that it is scoped to one. isolated-apple is required for the tamper test to mean
# anything: a guest without NET_ADMIN cannot attempt tampering, so its failure proves nothing.
#
# WHY IT NEEDS ROOT (audited, not assumed — see unprivileged.sh):
#   /dev/pf is crw------- root:wheel; a non-root `pfctl -a <anchor> -f` fails with
#   "/dev/pf: Permission denied". Only parse-only (-n -f) works unprivileged.
#   setegid() to a supplementary group returns EPERM even for a member, so launching a
#   process under a dedicated gid also requires privilege.
#
# WHAT IT TOUCHES, AND HOW IT UNDOES IT:
#   * pf: loads rules ONLY into the nested anchor com.apple/yoloai_spike, never the main
#     ruleset (flushing that destroys vmnet NAT for every VM on the host — /etc/pf.conf
#     warns about exactly this, and reloading it does NOT put the NAT back). The EXIT trap
#     flushes the anchor. pf enable/disable state is never touched.
#   * a throwaway group `yoloaispike` at a free gid, created with dscl and deleted by the
#     same trap. No existing user, group, or file is modified.
#
# Every block assertion is paired with a positive control, and every rule load is verified
# present in the anchor before anything is measured — a "blocked" reading is otherwise
# indistinguishable from a rule that never loaded (A22, and the reason two earlier
# harness generations produced false negatives).

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 ..."; exit 2; }

U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:-mac-coh-apple}"
T_SB="${2:-mac-coh-tart}"
B_SB="${3:-}"     # second apple sandbox: the per-VM scoping discriminator
ISO_SB="${4:-}"   # apple sandbox created with --network-isolated (guest holds NET_ADMIN)
ANCHOR="com.apple/yoloai_spike"
GRP=yoloaispike
ALLOW=1.1.1.1
DENY=1.0.0.1
CTRL="${CTRL:-1.1.1.2}"  # third address, distinct from ALLOW/DENY: the tart guest blocks this with
                         # its OWN pf as a tamper-efficacy control, so it MUST be reachable from that
                         # guest. 9.9.9.9 was tried first and is unreachable there (000), which would
                         # have made the control pass for the wrong reason; the baseline gate catches
                         # that now, but pick a reachable address. Override with CTRL=<ip>.
GID=""
ONLY="${ONLY:-}"   # comma-separated section list, e.g. ONLY=gid,p1b — empty runs everything
want() {
  [ -n "$ONLY" ] || return 0
  case ",$ONLY," in *",$1,"*) return 0;; *) return 1;; esac
}

asuser() { sudo -u "$U" "$@"; }
hr() { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }

cleanup() {
  pfctl -a "$ANCHOR" -F rules >/dev/null 2>&1
  if [ -n "$GID" ]; then dscl . -delete /Groups/$GRP >/dev/null 2>&1; fi
  printf '\n[restore] anchor flushed; group %s deleted; main ruleset untouched.\n' "$GRP"
  dscl . -read /Groups/$GRP >/dev/null 2>&1 && echo "  WARNING: group $GRP still present" || echo "  verified: group $GRP is gone"
}
trap cleanup EXIT INT TERM

# load <rule>... — loads into the nested anchor and ABORTS the caller if they did not land.
RULES_OK=0
load() {
  local want=$# got
  # Flush first, and compare with -ne rather than -lt. `pfctl -f` aborts BEFORE committing on a
  # parse error, leaving the previous ruleset intact — so without the flush a failed load could
  # find the last experiment's rules still present, satisfy a >= test, and silently measure them.
  pfctl -a "$ANCHOR" -F rules >/dev/null 2>&1
  printf '%s\n' "$@" | pfctl -a "$ANCHOR" -f - 2>&1 | grep -viE 'altq|use of -f|main ruleset|further details' >&2
  got=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c .)
  if [ "$got" -ne "$want" ]; then
    echo "   !! RULE LOAD FAILED (wanted $want, anchor has $got) — readings below are MEANINGLESS"
    RULES_OK=0
  else
    RULES_OK=1
  fi
}
flush() { pfctl -a "$ANCHOR" -F rules >/dev/null 2>&1; }

# tamper_verdict <probe-fn> <label> — call AFTER a guest tamper attempt, with host rules
# still loaded. A bare "still blocked" is not evidence: the guest's own tampering can break
# its networking outright, and a dead guest is blocked for every destination. So require the
# allowlisted destination to still succeed, and when it does not, flush and re-probe to say
# WHICH of the two happened instead of quietly claiming the rule held.
tamper_verdict() {
  local probe=$1 label=$2 d a d2 a2
  d=$($probe $DENY); a=$($probe $ALLOW)
  note "after tamper: $label -> $DENY = $d (want 000)   -> $ALLOW = $a (want 301)"
  if [ "$d" = "000" ] && [ "$a" = "301" ]; then
    note "VERDICT: host rule SURVIVED the tamper attempt, and the guest is still on the network."
    return 0
  fi
  if [ "$a" != "301" ]; then
    note "VERDICT: INDETERMINATE — the positive control failed, so '$DENY blocked' proves nothing."
    flush
    d2=$($probe $DENY); a2=$($probe $ALLOW)
    note "  with host rules flushed: -> $DENY = $d2   -> $ALLOW = $a2"
    if [ "$a2" = "301" ]; then
      note "  ...egress returns once the host rule is gone => the host rule WAS the cause; the"
      note "     control failed because the rule also covers $ALLOW under this ruleset."
    else
      note "  ...still dead with no host rules => THE GUEST BROKE ITS OWN NETWORKING."
      note "     Nothing about host-rule tamper-resistance can be concluded from this run."
    fi
    return 1
  fi
  note "VERDICT: the rule did NOT hold — $DENY was reachable after tampering."
  return 1
}


hostcurl()  { curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null || true; }
appcurl()   { asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null || true; }
tartcurl()  { asuser tart exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null || true; }
HERE=$(cd "$(dirname "$0")" && pwd)
UID_N=$(id -u "$U")
asgid()     { python3 "$HERE/as_gid.py" "$1" "$UID_N" "${@:2}"; }
gcurl()     { asgid "$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null || true; }
gping()     { asgid "$1" ping -c1 -W2000 "$2" >/dev/null 2>&1 && echo REACHED || echo blocked; }
cip()       { asuser container ls 2>/dev/null | awk -v n="yoloai-cli-$1" '$1==n {print $6}' | cut -d/ -f1; }
iface_for() { route -n get "$1" 2>/dev/null | awk '/interface:/{print $2}'; }

echo "==================================================================="
echo " macOS isolation spike — privileged half"
echo " pf: $(pfctl -s info 2>/dev/null | head -1)"
echo "==================================================================="

# ---------------------------------------------------------------- CONTROL 0
hr "CONTROL 0 — are rules in the nested anchor even evaluated?"
flush
note "baseline host -> $DENY : $(hostcurl $DENY)   (expect 301)"
load "block drop quick inet proto tcp from any to $DENY"
if [ $RULES_OK = 1 ]; then
  note "blocked  host -> $DENY : $(hostcurl $DENY)   (expect 000 => anchor IS evaluated)"
  note "control  host -> $ALLOW: $(hostcurl $ALLOW)  (expect 301 => rule is scoped)"
fi
flush
note "flushed  host -> $DENY : $(hostcurl $DENY)   (expect 301)"

# ------------------------------------------------------------- P1: per-VM pf
vm_case() { # vm_case <label> <probe-fn> <ip> <second-probe-or-empty> <second-label>
  local label=$1 probe=$2 ip=$3 probe2=${4:-} lab2=${5:-}
  local ifc b1 b2
  ifc=$(iface_for "$ip")
  note "$label = $ip via ${ifc:-?}"
  flush
  b1=$($probe $DENY)
  note "BASELINE $label -> $DENY = $b1   (expect 301)"
  if [ "$b1" != "301" ]; then note "*** SKIP: no egress at baseline; any 'blocked' would be vacuous."; return 1; fi
  load "block drop in quick on $ifc from $ip to any"
  [ $RULES_OK = 1 ] || return 1
  note "blocked  $label -> $DENY = $($probe $DENY)   (want 000)"
  note "control  host  -> $DENY = $(hostcurl $DENY)  (want 301, host unaffected)"
  if [ -n "$probe2" ]; then
    b2=$($probe2 $DENY); note "scoping  $lab2 -> $DENY = $b2  (want 301 = per-VM, not per-backend)"
  fi
  load "pass in quick on $ifc proto tcp from $ip to $ALLOW port 80" \
       "block drop in quick on $ifc from $ip to any"
  [ $RULES_OK = 1 ] || return 1
  note "allowlist $label -> $ALLOW = $($probe $ALLOW) (want 301)   -> $DENY = $($probe $DENY) (want 000)"
  flush
  note "flushed  $label -> $DENY = $($probe $DENY)   (expect 301)"
  return 0
}

A_IP=$(cip "$A_SB")
if want p1a; then
hr "P1a — apple: per-VM scoping and the allowlist shape"
if [ -z "$A_IP" ]; then note "SKIP: no IP for $A_SB"; else
  probe_a() { appcurl "$A_SB" "$1"; }
  probe_b() { appcurl "$B_SB" "$1"; }
  if [ -n "$B_SB" ] && [ -n "$(cip "$B_SB")" ]; then
    vm_case "appleA" probe_a "$A_IP" probe_b "appleB"
  else
    note "NOTE: no apple-B supplied — this run shows the rule BLOCKS a VM but not that it"
    note "      is SCOPED to one. Per-VM scoping is the claim the plans actually make."
    vm_case "appleA" probe_a "$A_IP"
  fi
fi
fi

# ------------------------------------------- P4: privileged in-guest tamper
if want p4; then
hr "P4 — can a guest that genuinely HOLDS NET_ADMIN defeat the host rule?"
if [ -z "$ISO_SB" ] || [ -z "$(cip "$ISO_SB")" ]; then
  note "SKIP: no --network-isolated apple sandbox supplied (arg 4)."
  note "Without it this proves nothing: an unprivileged iptables failure is not a tamper attempt."
else
  I_IP=$(cip "$ISO_SB"); I_IF=$(iface_for "$I_IP")
  note "isolated sandbox $ISO_SB = $I_IP via ${I_IF:-?}"
  flush
  note "PRECONDITION 1: the guest is privileged (else a failed tamper proves nothing):"
  asuser container exec "yoloai-cli-$ISO_SB" sh -c 'sudo iptables -L OUTPUT >/dev/null 2>&1 && echo "      iptables readable -> guest HAS NET_ADMIN" || echo "      NO NET_ADMIN -> vacuous"'
  # This sandbox is --network-isolated, so its OWN in-guest firewall already blocks
  # non-allowlisted destinations. Using one of those as the tamper target measures the
  # guest firewall, not the host rule — the first run did exactly that and read 000 at
  # baseline. So the target must be a destination the guest CAN reach: $ALLOW.
  # The host rule's target must be something this guest's OWN allowlist already permits,
  # otherwise a later "blocked" is just its in-guest firewall. $ALLOW is usually not on the
  # allowlist, so fall back to an allowlisted domain's address. Override with P4_TARGET=<ip>.
  TGT="${P4_TARGET:-}"
  if [ -z "$TGT" ]; then
    if [ "$(appcurl "$ISO_SB" $ALLOW)" != "000" ]; then TGT=$ALLOW
    else TGT=$(asuser dig +short api.anthropic.com A 2>/dev/null | grep -E '^[0-9.]+$' | head -1); fi
  fi
  i_tgt=$(appcurl "$ISO_SB" "$TGT"); i_deny=$(appcurl "$ISO_SB" $DENY)
  note "PRECONDITION 2: target=$TGT  baseline iso -> $TGT = $i_tgt (need non-000: the rule's target)"
  note "                baseline iso -> $DENY = $i_deny  (expect 000: blocked by its OWN firewall)"
  if [ -z "$TGT" ] || [ "$i_tgt" = "000" ]; then
    note "*** SKIP P4: the guest cannot reach $TGT even with no host rule loaded, so any later"
    note "    'blocked' would be its own firewall rather than evidence about host pf."
    note "    Pass P4_TARGET=<an ip this sandbox's allowlist permits> to fix."
  elif [ "$i_deny" != "000" ]; then
    note "*** SKIP P4: $DENY is NOT blocked in-guest at baseline, so it cannot serve as the"
    note "    positive control for 'the guest's flush actually did something'."
  else
    load "block drop in quick on $I_IF from $I_IP to $TGT"
    if [ $RULES_OK = 1 ]; then
      note "host pf now blocks $TGT: iso -> $TGT = $(appcurl "$ISO_SB" "$TGT")  (want 000)"
      note "guest now flushes ALL of its own firewall state:"
      asuser container exec "yoloai-cli-$ISO_SB" sh -c \
        'sudo iptables -F 2>&1|head -1; sudo iptables -P OUTPUT ACCEPT 2>&1|head -1; sudo iptables -P INPUT ACCEPT 2>&1|head -1; echo "      (flush attempted)"'
      t_deny=$(appcurl "$ISO_SB" $DENY); t_allow=$(appcurl "$ISO_SB" "$TGT")
      note "POSITIVE CONTROL — did the tamper actually DO anything?"
      note "  iso -> $DENY = $t_deny   (301 = the guest really did defeat its own firewall)"
      note "RESULT — did the host rule survive that same privileged flush?"
      note "  iso -> $TGT = $t_allow  (000 = host pf held)"
      if [ "$t_deny" = "301" ] && [ "$t_allow" = "000" ]; then
        note "VERDICT: the guest demonstrably tore down its OWN firewall and still could not"
        note "         reach past the host rule. That is the property the plan claims."
      elif [ "$t_deny" != "301" ]; then
        note "VERDICT: INCONCLUSIVE — the flush had no observable effect, so the guest never"
        note "         actually tampered; nothing follows about the host rule."
      else
        note "VERDICT: the host rule did NOT hold."
      fi
    fi
  fi
  flush
fi
fi

# -------------------------------------------------------- Reaping behaviour
if want reaping; then
hr "REAPING — what happens to an address-keyed rule when the sandbox dies?"
if [ -z "$A_IP" ]; then note "SKIP: no apple sandbox IP."; else
  A_IF=$(iface_for "$A_IP")
  load "block drop in quick on $A_IF from $A_IP to any"
  if [ $RULES_OK = 1 ]; then
    note "rule installed for $A_IP; destroying the sandbox out from under it..."
    asuser /Users/karlstenerud/Projects/yoloai/yoloai destroy "$A_SB" >/dev/null 2>&1
    sleep 3
    note "anchor contents after the sandbox is gone:"
    pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/        /'
    note "^ nothing reaped it. A later sandbox issued $A_IP inherits this silently."
    note "(that is the per-sandbox-lifetime cost the plans name as undesigned)"
  fi
  flush
fi
fi

# ---------------------------------------------- P2/P3: the dedicated-gid design
if want gid; then
hr "P2 — the ACTUAL dedicated-gid design (not borrowed admin/staff)"
for g in $(seq 700 760); do
  if ! dscl . -list /Groups PrimaryGroupID 2>/dev/null | awk '{print $2}' | grep -qx "$g"; then GID=$g; break; fi
done
[ -n "$GID" ] || { echo "   no free gid in 700-760"; exit 1; }
dscl . -create /Groups/$GRP >/dev/null 2>&1
dscl . -create /Groups/$GRP PrimaryGroupID "$GID" >/dev/null 2>&1
dscl . -create /Groups/$GRP RealName "yoloAI spike throwaway" >/dev/null 2>&1
note "created group $GRP gid=$GID (deleted on exit)"
note "NOTE: $U is deliberately NOT a member — a sandbox gid should not be one the user holds."

flush
GID_BASE=$(gcurl "$GID" $DENY)
note "baseline gid=$GRP($GID) -> $DENY : [$GID_BASE]  (expect 301)"
note "baseline gid=staff(20)   -> $DENY : [$(gcurl 20 $DENY)]  (expect 301)"
if [ "$GID_BASE" != "301" ]; then
  note "*** ABORT P2/P3: the gid probe cannot reach the network even with NO rules loaded."
  note "    Everything below would read 'blocked' for the wrong reason. Fix the probe first."
  note "    (a first run hit exactly this: sudo refused the runas group and returned empty)"
else
  load "pass out quick inet proto tcp from any to $ALLOW port 80 group $GID" \
       "block drop quick inet proto tcp from any to any group $GID"
  if [ $RULES_OK = 1 ]; then
    note "allowlist gid=$GRP -> $ALLOW : [$(gcurl "$GID" $ALLOW)]  (want 301)"
    note "allowlist gid=$GRP -> $DENY  : [$(gcurl "$GID" $DENY)]   (want 000)"
    note "unaffected gid=staff -> $DENY: [$(gcurl 20 $DENY)]  (want 301)"
  fi
fi

hr "P2b — can a process in that gid shed it? (the whole design rests on 'no')"
asgid "$GID" python3 - <<'PY'
import ctypes, os
libc = ctypes.CDLL(None, use_errno=True)
print("      inside: rgid=%d egid=%d" % (os.getgid(), os.getegid()))
for target in (20, 0):
    for fn in ("setgid", "setegid"):
        ctypes.set_errno(0); r = getattr(libc, fn)(target); e = ctypes.get_errno()
        print("      %s(%d) -> %s" % (fn, target, "ESCAPED" if r == 0 else "refused errno=%d(%s)" % (e, os.strerror(e))))
PY

hr "P3 — the ICMP hole: does 'group' filter non-TCP/UDP at all?"
note "man 5 pf.conf: 'Only TCP and UDP packets can be associated with users; for other"
note "protocols these parameters are ignored.' Three outcomes discriminate what 'ignored' means."
note "ping setuid? $(stat -f '%Sp %Su' /sbin/ping)  (a setuid-root ping would confound this)"
flush
PING_BASE=$(gping "$GID" $DENY)
note "baseline ping as gid=$GRP : $PING_BASE   (expect REACHED; 'blocked' here means the probe is broken)"
load "block drop quick inet proto icmp from any to $DENY group $GID"
if [ $RULES_OK = 1 ]; then
  r1=$(gping "$GID" $DENY); r2=$(gping 20 $DENY)
  note "icmp+group rule loaded:  gid=$GRP -> $r1    gid=staff -> $r2"
  note "  both REACHED  => the rule never matches ICMP: gid allowlists leak ICMP entirely"
  note "  both blocked  => 'group' is ignored and the rule hits EVERY process (over-blocks)"
  note "  only $GRP blocked => group works for ICMP, contradicting the man page"
fi
flush
note "the shape a real gid allowlist would use: block rule with NO protocol qualifier"
# The block rule below deliberately carries no `proto`. An earlier version scoped it to
# `proto tcp`, which made "ICMP still gets through" a restatement of the qualifier rather
# than a result — and a later edit relabelled the output "PROTOCOL-AGNOSTIC" while leaving
# the `proto tcp` rule in place, which is worse: a true-sounding label on a circular test.
load "pass out quick inet proto tcp from any to $ALLOW port 80 group $GID" \
     "block drop quick inet from any to any group $GID"
if [ $RULES_OK = 1 ]; then
  note "  tcp -> $DENY  as gid=$GRP : [$(gcurl "$GID" $DENY)]   (000 = the block bites TCP)"
  note "  tcp -> $ALLOW as gid=$GRP : [$(gcurl "$GID" $ALLOW)]  (301 = the pass still works)"
  note "  OVER-BLOCK CONTROL, tcp -> $DENY as gid=staff : [$(gcurl 20 $DENY)]"
  note "    301 => the rule is correctly scoped to gid $GID"
  note "    000 => 'group' is being IGNORED and the rule hits EVERY process — which would make"
  note "           the whole mechanism unusable, and is the outcome this control exists to catch"
  note "  ICMP -> $DENY as gid=$GRP : $(gping "$GID" $DENY)"
  note "    REACHED => group never matches ICMP; a gid allowlist leaks every non-TCP/UDP protocol"
  note "    blocked => group DOES constrain ICMP, contradicting man 5 pf.conf"
fi
flush
fi

if want p1b; then
hr "P1b — tart (run last: the handoff restarts tart, which can re-lease apple's subnet)"
note "restarting tart so it holds a live lease (they can coexist; see DF172)..."
asuser /Users/karlstenerud/Projects/yoloai/yoloai stop "$T_SB" >/dev/null 2>&1
sleep 2
asuser /Users/karlstenerud/Projects/yoloai/yoloai start "$T_SB" >/dev/null 2>&1
sleep 20
T_IP=$(asuser tart ip "yoloai-cli-$T_SB" 2>/dev/null | tr -d '[:space:]')
case "$T_IP" in ""|*noIP*) note "SKIP: tart has no IP";; *)
  probe_t() { tartcurl "$T_SB" "$1"; }
  vm_case "tart" probe_t "$T_IP"
  # TAMPER, with an efficacy control. A bare `pfctl -d` proves nothing: nothing establishes
  # the guest's pf was enabled or carried a rule, so "still blocked" is consistent with the
  # tamper having done nothing at all. So: have the guest block a THIRD address with its own
  # pf first, confirm that works, then require that address to come back after the tamper.
  # That is the apple-P4 shape — show the agent destroying the policy it controls, in the
  # same breath as failing to touch the one it does not.
  # BASELINE FIRST. A previous version checked only that $CTRL was blocked AFTER the guest
  # installed its rule — and passed that check on a run where `pfctl` was not even on the
  # guest's PATH, because $CTRL happened to be unreachable anyway. A control with no baseline
  # is the exact defect this harness exists to refuse.
  g0=$(probe_t "$CTRL")
  note "[TAMPER] baseline: guest -> $CTRL = $g0  (need 301, else $CTRL is useless as a control)"
  note "  guest installs its OWN pf rule blocking $CTRL, to prove a tamper can be detected"
  # /sbin/pfctl, not pfctl: the tart guest's non-login PATH does not include /sbin, and a
  # `command not found` here silently produced an unenforced rule last run.
  asuser tart exec "yoloai-cli-$T_SB" bash -c \
    "printf 'block drop out proto tcp from any to $CTRL\n' | sudo /sbin/pfctl -f - 2>&1 | head -2; sudo /sbin/pfctl -E 2>&1 | head -2" 2>&1 | sed 's/^/      /'
  g1=$(probe_t "$CTRL")
  note "  guest -> $CTRL with its own rule loaded: $g1  (need 000)"
  if [ "$g0" != "301" ] || [ "$g1" != "000" ]; then
    note "  *** SKIP the tamper test: need $CTRL reachable at baseline AND blocked by the guest's"
    note "      own rule. Got baseline=$g0 after-rule=$g1. Without both, 'it came back' proves"
    note "      nothing — which is exactly how the previous run reported INCONCLUSIVE."
  else
    load "pass in quick on $(iface_for "$T_IP") proto tcp from $T_IP to $ALLOW port 80" \
         "block drop in quick on $(iface_for "$T_IP") from $T_IP to any"
    if [ $RULES_OK = 1 ]; then
      note "  host pf loaded: guest -> $DENY = $(probe_t $DENY)  (want 000)"
      note "  guest now disables its own pf:"
      asuser tart exec "yoloai-cli-$T_SB" bash -c 'sudo /sbin/pfctl -d 2>&1 | head -2' 2>&1 | sed 's/^/      /'
      t_ctrl=$(probe_t "$CTRL"); t_deny=$(probe_t $DENY); t_allow=$(probe_t $ALLOW)
      note "  CONTROL  guest -> $CTRL = $t_ctrl  (301 = the guest really did tear down its own pf)"
      note "  RESULT   guest -> $DENY  = $t_deny   (000 = the host rule survived that)"
      note "  alive    guest -> $ALLOW = $t_allow  (301 = still on the network)"
      if [ "$t_ctrl" = "301" ] && [ "$t_deny" = "000" ]; then
        note "  VERDICT: guest defeated its OWN firewall and still could not pass the host rule."
      elif [ "$t_ctrl" != "301" ]; then
        note "  VERDICT: INCONCLUSIVE — the tamper had no observable effect; nothing follows."
      else
        note "  VERDICT: the host rule did NOT hold."
      fi
    fi
  fi
  asuser tart exec "yoloai-cli-$T_SB" bash -c 'sudo /sbin/pfctl -F rules 2>&1|head -1; sudo /sbin/pfctl -d 2>&1|head -1' >/dev/null 2>&1
  flush;;
esac
fi

hr "FINAL"
note "host -> $DENY : $(hostcurl $DENY)  (expect 301)"
pfctl -a "$ANCHOR" -s rules 2>/dev/null | sed 's/^/   leftover: /'
echo "done."
