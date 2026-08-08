#!/bin/bash
# ABOUTME: Can a guest change its own source address and escape or inherit an allowlist?
# ABOUTME: The pf design keys on a value the guest controls; this asks whether that is safe.
#
# Run: sudo bash pf_spoof.sh
#
# WHY THIS EXISTS
#   Every rule in the pool reads `from <yb_src_N>` — a source IP address. The guest owns the
#   interface that address sits on. Nothing in results/ has ever asked the obvious question that
#   follows, and the whole of enforcement-state-reaping.md is built on top of it:
#
#       if a guest can choose its own source address, address keying is not a security boundary.
#
#   It would make yoloAI's isolation a convention that a cooperating guest respects — which is a
#   defensible thing to ship, but a completely different claim from the one D132 makes. And an
#   agent with a shell is exactly the threat model here.
#
#   THREE ESCAPES, in increasing order of how badly they break the model:
#     S1 INHERIT   — take a second address belonging to another sandbox's slot, and use its
#                    allowlist. Steals another sandbox's permissions.
#     S2 REPLACE   — drop our own address and become the other sandbox outright.
#     S3 VANISH    — take an address in NO slot at all. This is the one to watch: the pool's block
#                    rule is `block ... from <yb_src_N> to any`, so it only bites addresses that are
#                    IN a table. A source address in no table matches no rule, falls through the
#                    anchor, and meets pf's default, which is PASS. If S3 works, escaping is easier
#                    than stealing — no reconnaissance needed, just pick an unused address.
#
#     S4 THE FIX   — a bridge-scoped default-deny (`block return in quick on <bridge> from any to
#                    any` after the per-slot passes) would close S3 without naming any address.
#                    Tested here, because a hazard with a measured one-line fix is a different item
#                    from a hazard without one.
#
# METHOD: the enforcement matrix is proven first — each guest reaches its own allowlist and not the
#   other's — so an escape is distinguishable from a host that never enforced (A22). Every escape
#   attempt also reports whether the address change itself succeeded, because "the attack failed"
#   and "the attack never ran" look identical from the outside.
#
#   AND THE CONFIGURATION HAS TO BE THE RIGHT ONE, which took three attempts to get right.
#     run 1  a plain `container run` as an unprivileged user. No capabilities, no escape possible.
#     run 2  a `yoloai new` sandbox. Still no escape — because a PLAIN sandbox has CapEff
#            0x a804 2 5fb, and the isolated one has 0x a804 3 5fb. The single differing bit is
#            number 12, which is CAP_NET_ADMIN. So the capability arrives only with the flag.
#     run 3  `yoloai new --network-isolated`, which is the mode where isolation is the point and
#            the only mode that grants CAP_NET_ADMIN (DF179).
#
#   The full chain is measured and it is the threat model, not a contrivance: the in-guest allowlist
#   blocks a denied destination; the agent flushes its own OUTPUT chain through passwordless sudo
#   (which is DF179's known weakness); egress returns; and `ip addr add` then succeeds. Host pf is
#   the layer that is supposed to survive all of that, so that is the state these escapes run in.
#
#   THE DESIGN QUESTION THIS SETTLES is not really "can a guest spoof". It is whether host-pf
#   enforcement may coexist with a sandbox holding CAP_NET_ADMIN. If an escape works here, the answer
#   is no, and D132 has to say so — the capability and the address key cannot both be present.
#
# SAFETY: writes only into its own anchor; never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_s"
ALLOW_A=1.1.1.1      # A's allowlist
ALLOW_B=1.0.0.1      # B's allowlist — both answer HTTP, so either reaching is unambiguous
SLOTS=4
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfs.XXXXXX)
IPA=""; IPB=""; PFX=24; BR=""

RESULTS="$HERE/results/pf-spoof.txt"
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

netfield() { asuser container inspect "yoloai-cli-$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],""))' "$2" 2>/dev/null; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }

# Every guest is a real yoloAI sandbox. `yoloai exec` parses flags itself, so `--` is REQUIRED
# before any command that has its own flags; without it `curl -s` is read as a yoloai flag.
# Reach from a guest, optionally binding a specific source address.
# curl writes %{http_code} even when it fails (000) and ALSO exits non-zero. An `|| printf 000`
# fallback therefore appends a second 000, and every "is it blocked?" test silently compares
# "000000" against "000" and reads a successful block as a failure. Run 1 lost four verdicts to it.
reach() {   # $1 = sandbox, $2 = dest, [$3 = source address to bind]
  local src="" o
  [ -n "${3:-}" ] && src="--interface $3"
  o=$(asuser "$YOLOAI" exec "$1" -- sh -c \
    "curl -s -o /dev/null -w '%{http_code}' --max-time 5 $src http://$2/ 2>/dev/null" 2>/dev/null)
  printf '%s' "${o:-000}"
}
# Runs as ROOT inside the sandbox, which is what an agent with a shell can do.
gsh()  { asuser "$YOLOAI" exec "$1" -- sudo -n sh -c "$2" 2>&1; }
# --network-isolated is what grants CAP_NET_ADMIN, so it is the only mode in which the escapes are
# even expressible. It also installs an in-guest allowlist, which `disarm` then removes — as the
# agent itself can (DF179) — so that the ONLY thing constraining the guest is host pf.
mksb()   { asuser "$YOLOAI" destroy "$1" --abandon-unapplied >/dev/null 2>&1
           asuser "$YOLOAI" new "$1" "$2" --backend apple --network-isolated >/dev/null 2>&1; }
disarm() { asuser "$YOLOAI" exec "$1" -- sudo -n sh -c \
             'iptables -F OUTPUT 2>/dev/null; iptables -P OUTPUT ACCEPT 2>/dev/null; \
              ip6tables -F OUTPUT 2>/dev/null; echo done' >/dev/null 2>&1; }

load_pool() {   # $1 = "deny" to append the bridge-scoped default-deny of S4
  flush
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
    done
    [ "${1:-}" = deny ] && echo "block return in quick on $BR from any to any"
  } > "$WD/pool.rules"
  pfctl -a "$ANCHOR" -f "$WD/pool.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}
claim() {   # $1 = slot, $2 = address, $3 = allowed dest
  pfctl -a "$ANCHOR" -t "yb_src_$1" -T add "$2" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$1" -T add "$3" >/dev/null 2>&1
}

cleanup() {
  echo
  echo "== cleanup =="
  flush
  for sb in spoofa spoofb spoofx; do
    asuser "$YOLOAI" destroy "$sb" --abandon-unapplied >/dev/null 2>&1
  done
  rm -rf "$WD"
  echo "   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)  main-refs: $(mainrefs)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "   NOTE 'FAIL' here means an escape SUCCEEDED. This script fails when the product is weak,"
  echo "        not when the script is broken."
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "S0 SETUP AND THE ENFORCEMENT MATRIX"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
mksb spoofa "$WD"; mksb spoofb "$WD"
sleep 3
IPA=$(netfield spoofa ipv4Address); PFX=${IPA#*/}; IPA=${IPA%%/*}
IPB=$(netfield spoofb ipv4Address); IPB=${IPB%%/*}
GW=$(netfield spoofa ipv4Gateway); BR=$(brof "$GW")
note "A=spoofa $IPA/$PFX allowed $ALLOW_A"
note "B=spoofb $IPB/$PFX allowed $ALLOW_B     bridge=$BR gateway=$GW"
[ -n "$IPA" ] && [ -n "$IPB" ] && [ -n "$BR" ] || { bad "setup incomplete; ABORTING"; exit 1; }

note ""
note "the in-guest allowlist is present first — confirming it, then removing it the way an agent can"
pre=$(reach spoofa "$ALLOW_A")
note "A -> $ALLOW_A with the in-guest allowlist intact: $pre  (000 expected: it is not allowlisted)"
disarm spoofa; disarm spoofb
post_a=$(reach spoofa "$ALLOW_A"); post_b=$(reach spoofb "$ALLOW_B")
note "after the agent flushes its own OUTPUT chain: A->$ALLOW_A=$post_a  B->$ALLOW_B=$post_b"
if [ "$pre" = 000 ] && [ "$post_a" != 000 ] && [ "$post_b" != 000 ]; then
  bad "S0a: DF179 REPRODUCED — the agent removed its own in-guest allowlist and regained egress."
  note "     That is the reason host pf exists, and it is the state everything below runs in: from"
  note "     here, host pf is the only thing constraining these guests."
elif [ "$post_a" = 000 ] || [ "$post_b" = 000 ]; then
  bad "S0a: egress was not restored after the flush (A=$post_a B=$post_b); the escapes below would"
  note "     be blocked by the GUEST rather than by pf, which proves nothing about pf. ABORTING"
  exit 1
else
  note "     the in-guest allowlist did not block $ALLOW_A to begin with (got $pre); continuing, but"
  note "     DF179 was not reproduced in this run"
fi

load_pool
claim 1 "$IPA" "$ALLOW_A"
claim 2 "$IPB" "$ALLOW_B"
aa=$(reach spoofa "$ALLOW_A"); ab=$(reach spoofa "$ALLOW_B")
ba=$(reach spoofb "$ALLOW_A"); bb=$(reach spoofb "$ALLOW_B")
note "matrix:  A->own=$aa  A->B's=$ab   |   B->A's=$ba  B->own=$bb"
note "         (want non-000, 000, 000, non-000)"
if [ "$aa" != 000 ] && [ "$ab" = 000 ] && [ "$ba" = 000 ] && [ "$bb" != 000 ]; then
  ok "S0: enforcement is real and per-sandbox — escapes below mean something"
else
  bad "S0: the matrix does not hold; nothing below would be interpretable. ABORTING"; exit 1
fi

note ""
note "what can an agent inside this sandbox actually do? Two runs got this wrong; here it is"
note "measured explicitly, because it decides whether the escapes below are even expressible."
note "as the agent user:"
asuser "$YOLOAI" exec spoofa -- sh -c 'id -u; grep CapEff /proc/self/status' 2>&1 | sed 's/^/          /'
note "after sudo (passwordless, and that is the shipped configuration):"
asuser "$YOLOAI" exec spoofa -- sudo -n sh -c 'id -u; grep CapEff /proc/self/status' 2>&1 | sed 's/^/          /'
note "bit 12 of CapEff is CAP_NET_ADMIN. A PLAIN sandbox reports ...a80425fb (bit 12 clear); an"
note "isolated one reports ...a80435fb (bit 12 set). If the value above lacks bit 12, this run is"
note "not testing the mode it means to."
CAP=$(gsh spoofa 'ip addr add 127.0.0.53/32 dev lo >/dev/null 2>&1 && echo CAN || echo CANNOT' | tail -1)
note "interface control as root inside the sandbox: $CAP"
gsh spoofa 'ip addr del 127.0.0.53/32 dev lo' >/dev/null 2>&1
if [ "$CAP" != CAN ]; then
  ok "S0b: even as root the sandbox CANNOT modify its interfaces — address keying rests on that"
else
  bad "S0b: the agent can reach root and root holds CAP_NET_ADMIN, so it CAN change its own source"
  note "     address. Address keying therefore rests on pf alone, with no help from the sandbox."
fi

# ---------------------------------------------------------------------------
say "S1 INHERIT — A adds B's address and uses B's allowlist"
out=$(gsh spoofa "ip addr add $IPB/$PFX dev eth0 2>&1; ip -4 -o addr show eth0 | awk '{print \$4}' | tr '\n' ' '")
note "after 'ip addr add $IPB': $out"
if printf '%s' "$out" | grep -q "$IPB"; then
  r=$(reach spoofa "$ALLOW_B" "$IPB")
  note "A -> B's allowlisted $ALLOW_B, sourced from $IPB: $r   (non-000 = INHERITED)"
  if [ "$r" != 000 ]; then
    bad "S1: ESCAPE. A used B's address and reached B's allowlist. Slots are not isolated from"
    note "     each other by anything the guest cannot defeat."
  else
    ok "S1: taking B's address did not grant B's allowlist"
  fi
else
  ok "S1: the guest could not add a second address (${out:-no output})"
fi
gsh spoofa "ip addr del $IPB/$PFX dev eth0" >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "S3 VANISH — A takes an address in NO slot at all"
note "The pool blocks only what is IN a table. An address in no table matches no rule and meets"
note "pf's default. This needs no knowledge of any other sandbox, which makes it the cheap attack."
SPARE="${IPA%.*}.201"; SPARE_OK=0
out=$(gsh spoofa "ip addr add $SPARE/$PFX dev eth0 2>&1; ip -4 -o addr show eth0 | awk '{print \$4}' | tr '\n' ' '")
note "after 'ip addr add $SPARE': $out"
if printf '%s' "$out" | grep -q "$SPARE"; then
  SPARE_OK=1
  ra=$(reach spoofa "$ALLOW_B" "$SPARE")
  rb=$(reach spoofa 5.5.5.5 "$SPARE")
  note "A -> $ALLOW_B (denied to A) sourced from the spare address: $ra"
  note "A -> 5.5.5.5 (in nobody's allowlist)  sourced from the spare address: $rb"
  note "   (either non-000 = the block was escaped entirely)"
  if [ "$ra" != 000 ] || [ "$rb" != 000 ]; then
    bad "S3: ESCAPE, and the worst one. An unlisted source address is not filtered at all."
    note "     The pool is an allowlist of BLOCKS keyed on address, so it can only constrain"
    note "     addresses it already knows. Changing address leaves the policy behind entirely."
  else
    ok "S3: an unlisted source address was still blocked"
  fi
else
  ok "S3: the guest could not take an unused address (${out:-no output})"
fi

# ---------------------------------------------------------------------------
say "S4 THE FIX — a bridge-scoped default-deny, which names no address"
note "One rule after the per-slot passes: block everything on this bridge that no slot permitted."
note "If it closes S3 while leaving the matrix intact, the hazard has a one-line answer."
load_pool deny
claim 1 "$IPA" "$ALLOW_A"
claim 2 "$IPB" "$ALLOW_B"
note "rules now: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true) (one more than before)"
m1=$(reach spoofa "$ALLOW_A"); m2=$(reach spoofa "$ALLOW_B")
m3=$(reach spoofb "$ALLOW_B"); m4=$(reach spoofb "$ALLOW_A")
note "matrix still: A->own=$m1 A->B's=$m2 | B->own=$m3 B->A's=$m4"
if [ "$SPARE_OK" -eq 0 ]; then
  note "the spare address was never added in S3, so there is nothing to re-test: a 000 here would"
  note "mean 'no such source address', not 'blocked'. The matrix is still checked."
  if [ "$m1" != 000 ] && [ "$m3" != 000 ] && [ "$m2" = 000 ] && [ "$m4" = 000 ]; then
    ok "S4: the default-deny preserves the matrix (the escape it targets never reproduced)"
  else
    bad "S4: the default-deny broke the matrix (A->own=$m1 A->B=$m2 B->own=$m3 B->A=$m4)"
  fi
  sp=n/a; sp2=n/a
fi
if [ "$SPARE_OK" -eq 1 ]; then
sp=$(reach spoofa "$ALLOW_B" "$SPARE")
sp2=$(reach spoofa 5.5.5.5 "$SPARE")
note "spare-address escape retried: ->$ALLOW_B=$sp  ->5.5.5.5=$sp2   (both must be 000)"
if [ "$m1" != 000 ] && [ "$m3" != 000 ] && [ "$m2" = 000 ] && [ "$m4" = 000 ] \
   && [ "$sp" = 000 ] && [ "$sp2" = 000 ]; then
  ok "S4: the bridge default-deny closes the escape AND preserves the matrix. One rule, no new"
  note "    grant surface, no address in it — so it costs nothing per sandbox."
elif [ "$m1" = 000 ] || [ "$m3" = 000 ]; then
  bad "S4: the default-deny broke legitimate traffic (A->own=$m1 B->own=$m3) — over-blocks"
else
  bad "S4: the default-deny did not close the escape (spare -> $sp/$sp2)"
fi
fi
gsh spoofa "ip addr del $SPARE/$PFX dev eth0" >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "S2 REPLACE — A discards its own address and becomes B"
note "Run last: it costs A its network identity, and recovery is not guaranteed."
load_pool
claim 1 "$IPA" "$ALLOW_A"
claim 2 "$IPB" "$ALLOW_B"
gsh spoofa "ip addr add $IPB/$PFX dev eth0; ip addr del $IPA/$PFX dev eth0" >/dev/null 2>&1
now=$(gsh spoofa "ip -4 -o addr show eth0 | awk '{print \$4}' | tr '\n' ' '")
note "A's addresses now: $now"
if printf '%s' "$now" | grep -q "$IPB" && ! printf '%s' "$now" | grep -q "$IPA"; then
  r=$(reach spoofa "$ALLOW_B")
  note "A (now wearing B's address) -> $ALLOW_B: $r"
  if [ "$r" != 000 ]; then
    bad "S2: ESCAPE. A fully assumed B's identity, with B still running."
  else
    ok "S2: assuming B's address did not grant B's allowlist"
  fi
else
  ok "S2: the guest could not replace its address (now: $now)"
fi
gsh spoofa "ip addr add $IPA/$PFX dev eth0" >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "S5 TART — same question, and the repo fact that frames it"
note "yoloAI runs tart as: tart run --no-graphics <vm>  (runtime/tart/tart.go). It does NOT pass"
note "--net-softnet, which is tart's own anti-spoofing mode — softnet exists precisely to prevent"
note "'a class of security issues, such as ARP spoofing'. So on the shipped path there is no"
note "guest-side mitigation, and the question is whether pf alone stops it."
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser "$YOLOAI" destroy spoofx --abandon-unapplied >/dev/null 2>&1
if ! asuser "$YOLOAI" new spoofx "$WD" --backend tart >/dev/null 2>&1; then
  unk "S5: tart sandbox would not start; not measured"
else
  TIP=$(asuser "$YOLOAI" exec spoofx -- /usr/sbin/ipconfig getifaddr en0 2>/dev/null | tr -d '\r')
  note "tart guest at ${TIP:-<none>}"
  if [ -z "$TIP" ]; then
    unk "S5: could not read the tart guest's address; not measured"
  else
    load_pool
    claim 3 "$TIP" "$ALLOW_A"
    t1=$(asuser "$YOLOAI" exec spoofx -- curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$ALLOW_A/" 2>/dev/null)
    t2=$(asuser "$YOLOAI" exec spoofx -- curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$ALLOW_B/" 2>/dev/null)
    note "tart baseline: ->allowed=$t1  ->denied=$t2   (need non-000 then 000)"
    if [ "$t1" != 000 ] && [ "$t2" = 000 ]; then
      TSPARE="${TIP%.*}.202"
      adds=$(asuser "$YOLOAI" exec spoofx -- sudo -n /sbin/ifconfig en0 alias "$TSPARE" netmask 255.255.255.0 2>&1 | tr -d '\r')
      got=$(asuser "$YOLOAI" exec spoofx -- /sbin/ifconfig en0 2>/dev/null | grep -c "$TSPARE" || true)
      note "added alias $TSPARE to en0: $([ "$got" -gt 0 ] && echo yes || echo "no (${adds:-refused})")"
      if [ "$got" -gt 0 ]; then
        t3=$(asuser "$YOLOAI" exec spoofx -- curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
               --interface "$TSPARE" "http://$ALLOW_B/" 2>/dev/null)
        note "tart -> denied $ALLOW_B sourced from the alias: $t3   (non-000 = ESCAPE)"
        if [ "$t3" != 000 ]; then
          bad "S5: ESCAPE on tart too — the hazard is not backend-specific"
        else
          ok "S5: tart's unlisted source address was still blocked"
        fi
        asuser "$YOLOAI" exec spoofx -- sudo -n ifconfig en0 -alias "$TSPARE" >/dev/null 2>&1
      else
        ok "S5: the tart guest could not add an alias"
        note "     NOTE it is not for lack of privilege: the tart guest HAS passwordless sudo to"
        note "     root (measured). Run 2 mis-read this as 'no root' when the real cause was that"
        note "     /sbin is absent from yoloai exec's PATH, so plain \`ifconfig\` was not found."
      fi
    else
      unk "S5: no tart enforcement baseline (allowed=$t1 denied=$t2); escape not attempted"
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - MAC/ARP spoofing. Only the IP was changed. tart's --net-softnet targets the ARP layer
          specifically, and whether pf sees any of that was not examined (M1's K2 shows pf cannot
          match a MAC at all, so it almost certainly cannot).
        - --net-softnet itself. yoloAI does not pass it, so the shipped path was measured. Whether
          softnet would close S5 is untested and would be a backend change, not a pf change.
        - Raw sockets / crafted source addresses without configuring the interface. A guest with
          NET_ADMIN can do this too; only the ip-addr route was tried, which is the easy one.
        - Spoofing the GATEWAY's address, or another host on the LAN. Both are more disruptive than
          this run should be on a machine somebody uses.
        - Whether the S4 default-deny interacts with the vmnet NAT rules or with a second backend
          sharing the bridge. Measured on one bridge with two apple guests only.
EOF
