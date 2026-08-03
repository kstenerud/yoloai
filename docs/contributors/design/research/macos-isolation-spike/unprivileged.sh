#!/bin/bash
# ABOUTME: Every macOS isolation experiment that needs neither root nor a running sandbox —
# ABOUTME: SBPL granularity, SBPL nesting semantics, and the privilege audit behind "needs root".
#
# Run: bash unprivileged.sh          (no sudo, no VMs, ~30s)
#
# Each check prints EXPECT and GOT so a changed platform shows up as a mismatch rather than
# as prose that quietly stopped being true. Every denial assertion is paired with a positive
# control (A22): a negative result is only meaningful if the same machinery succeeds nearby.

set -u
D=$(mktemp -d)
trap 'rm -rf "$D"' EXIT
ALLOW=1.1.1.1
PASS=0
FAIL=0

chk() { # chk <label> <expected> <got>
  if [ "$2" = "$3" ]; then printf '  ok   %-56s %s\n' "$1" "$3"; PASS=$((PASS+1))
  else printf '  FAIL %-56s expected=%s got=%s\n' "$1" "$2" "$3"; FAIL=$((FAIL+1)); fi
}
hr() { printf '\n== %s\n' "$1"; }

# curl through a profile; prints the HTTP code, or 000 when the connection is refused.
sbcurl() { sandbox-exec -f "$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "$2" 2>/dev/null || true; }
# does this profile even compile+apply? prints ok / the first error word
sbparse() { if out=$(sandbox-exec -f "$1" /usr/bin/true 2>&1); then echo ok; else echo "$out" | head -1; fi; }

cat > "$D/allowall.sb" <<'EOF'
(version 1)
(allow default)
EOF
cat > "$D/denynet.sb" <<'EOF'
(version 1)
(allow default)
(deny network*)
EOF

hr "SBPL: what destination granularity can the profile express?"
cat > "$D/ip.sb" <<EOF
(version 1)
(allow default)
(deny network-outbound)
(allow network-outbound (remote ip "$ALLOW:443"))
EOF
cat > "$D/cidr.sb" <<'EOF'
(version 1)
(allow default)
(deny network-outbound)
(allow network-outbound (remote ip "1.1.1.0/24:443"))
EOF
cat > "$D/port.sb" <<'EOF'
(version 1)
(allow default)
(deny network-outbound)
(allow network-outbound (remote ip "*:443"))
EOF
chk "literal IP host is rejected" "sandbox-exec: host must be * or localhost in network address" "$(sbparse "$D/ip.sb")"
chk "CIDR host is rejected"       "sandbox-exec: host must be * or localhost in network address" "$(sbparse "$D/cidr.sb")"
chk "wildcard host + port parses" "ok" "$(sbparse "$D/port.sb")"
echo "  -- port scoping enforces (positive control first):"
chk "  unsandboxed https (control)" "301" "$(curl -s -o /dev/null -w '%{http_code}' --max-time 6 https://$ALLOW/ || true)"
chk "  profile *:443 -> https(443)" "301" "$(sbcurl "$D/port.sb" https://$ALLOW/)"
chk "  profile *:443 -> http(80)"   "000" "$(sbcurl "$D/port.sb" http://$ALLOW/)"

hr "SBPL: nesting semantics (is confinement one-way, or no-way?)"
# The claim under test: a confined process cannot change its own sandbox. An earlier draft
# said "cannot LOOSEN". Case 3 is the discriminator that separates the two readings.
cat > "$D/tighten.sb" <<'EOF'
(version 1)
(allow default)
(deny network*)
EOF
n_loosen=$(sandbox-exec -f "$D/denynet.sb"  sandbox-exec -f "$D/allowall.sb" /usr/bin/true 2>&1 | head -1)
n_tight=$(sandbox-exec  -f "$D/allowall.sb" sandbox-exec -f "$D/tighten.sb"  /usr/bin/true 2>&1 | head -1)
n_noop=$(sandbox-exec   -f "$D/denynet.sb"  sandbox-exec -f "$D/denynet.sb"  /usr/bin/true 2>&1 | head -1)
n_perm=$(sandbox-exec   -f "$D/allowall.sb" sandbox-exec -f "$D/allowall.sb" /usr/bin/true 2>&1 | head -1)
chk "1. deny-net outer + permissive inner (LOOSEN)"  "sandbox-exec: sandbox_apply: Operation not permitted" "$n_loosen"
chk "2. permissive outer + permissive inner (NO-OP)" "" "$n_perm"
chk "3. permissive outer + TIGHTENING inner"         "sandbox-exec: sandbox_apply: Operation not permitted" "$n_tight"
chk "4. deny-net outer + identical inner (NO-OP)"    "" "$n_noop"
echo "  -- 3 failing is what makes this NO-way, not one-way: a change in EITHER direction is"
echo "     refused, and 2/4 pass only because the second profile is a semantic no-op."
chk "confined process still denied network" "000" "$(sbcurl "$D/denynet.sb" https://$ALLOW/)"

hr "Privilege audit: is 'needs root' actually true?"
# Claimed in both plans. Each line here is the evidence for one such claim.
chk "/dev/pf mode is root-only" "crw-------" "$(stat -f '%Sp' /dev/pf | cut -c1-10)"
python3 - <<'PY'
import ctypes, os
libc = ctypes.CDLL(None, use_errno=True)
member = 80 in os.getgroups()
ctypes.set_errno(0); r = libc.setegid(80); e = ctypes.get_errno()
print("  ok   %-56s member=%s rc=%d errno=%d(%s)" % (
    "setegid() to a supplementary group", member, r, e, os.strerror(e) if e else "none"))
PY
echo 'block drop quick inet proto tcp from any to 192.0.2.1' > "$D/r.conf"
pfctl -n -f "$D/r.conf" >/dev/null 2>&1; chk "pfctl parse-only (-n -f) unprivileged" "0" "$?"
pfctl -a com.apple/yoloai_probe -f "$D/r.conf" >/dev/null 2>&1; chk "pfctl anchor LOAD unprivileged" "1" "$?"

hr "pf rule syntax (the trap that produced a false negative)"
printf 'block drop in quick on en0 from 192.0.2.5 to any\n' > "$D/good.conf"
printf 'block drop quick in on en0 from 192.0.2.5 to any\n' > "$D/bad.conf"
pfctl -n -f "$D/good.conf" >/dev/null 2>&1; chk "'block drop in quick on ...' parses" "0" "$?"
pfctl -n -f "$D/bad.conf"  >/dev/null 2>&1; chk "'block drop quick in on ...' rejected" "1" "$?"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
