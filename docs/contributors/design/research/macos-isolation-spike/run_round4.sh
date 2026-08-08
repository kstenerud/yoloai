#!/bin/bash
# ABOUTME: Runs the macOS half of the round-4 verification queue (M1..M8) in a safe order,
# ABOUTME: gating on host health between items so one bad run cannot poison the rest.
#
# Run: sudo bash run_round4.sh            [or: sudo bash run_round4.sh pf_liveness_detect.sh ...]
#
# ORDER IS DELIBERATE. The first five items never touch the main ruleset. The last two break it on
# purpose and repair it. Running them last means a repair that fails cannot invalidate five other
# items — and each script gates on `main-refs > 0` at entry anyway, so a broken host makes the
# remainder ABORT loudly instead of quietly measuring nothing. That failure mode is not theoretical:
# a run in this directory once blamed the slot design for 56 leaks that were really a broken host.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
: "${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE" || exit 1

ITEMS=(
  "M1 non-address keys        pf_nonaddress_key.sh"
  "M2 DNS interception        dns_intercept.sh"
  "M8 pool-size scaling       pf_pool_scaling.sh"
  "M5 uninstall residue       pf_uninstall_residue.sh"
  "M7 IPv6 hole               pf_v6_hole.sh"
  "M3 liveness detectors      pf_liveness_detect.sh"
  "M4 ruleset change signal   pf_change_signal.sh"
)

mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }

if [ "$#" -gt 0 ]; then
  SELECTED=("$@")
else
  SELECTED=()
  for entry in "${ITEMS[@]}"; do SELECTED+=("${entry##* }"); done
fi

echo "================================================================"
echo " macOS verification queue — round 4"
echo " host: macOS $(sw_vers -productVersion) $(uname -m)   $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo " main-refs at start: $(mainrefs)"
echo "================================================================"
if [ "$(mainrefs)" -eq 0 ]; then
  echo "!! The host is ALREADY fail-open: the main ruleset does not reference com.apple/*."
  echo "   Nothing below would measure enforcement. Repair first:"
  echo "       sudo pfctl -f /etc/pf.conf && container system stop && container system start"
  exit 1
fi

FAILED=()
for script in "${SELECTED[@]}"; do
  label=""
  for entry in "${ITEMS[@]}"; do
    [ "${entry##* }" = "$script" ] && label="${entry% *}"
  done
  echo
  echo "----------------------------------------------------------------"
  echo ">>> ${label:-$script}   ($script)"
  echo "----------------------------------------------------------------"
  if [ ! -f "$script" ]; then
    echo "    missing: $script"; FAILED+=("$script (missing)"); continue
  fi
  bash "$script"
  rc=$?
  refs=$(mainrefs)
  echo "<<< $script exited $rc | main-refs now $refs"
  [ "$rc" -ne 0 ] && FAILED+=("$script (exit $rc)")
  if [ "$refs" -eq 0 ]; then
    echo "    !! host left fail-open — repairing before continuing"
    pfctl -f /etc/pf.conf >/dev/null 2>&1
    sudo -u "$SUDO_USER" -H container system stop  >/dev/null 2>&1; sleep 3
    sudo -u "$SUDO_USER" -H container system start >/dev/null 2>&1; sleep 5
    echo "    main-refs after repair: $(mainrefs)"
    [ "$(mainrefs)" -eq 0 ] && { echo "    REPAIR FAILED — stopping"; break; }
  fi
done

echo
echo "================================================================"
echo " done. main-refs: $(mainrefs)   sudoers.d: [$(ls /etc/sudoers.d/ 2>/dev/null | tr '\n' ' ')]"
if [ "${#FAILED[@]}" -gt 0 ]; then
  echo " scripts that did not exit cleanly:"
  printf '   %s\n' "${FAILED[@]}"
else
  echo " all selected scripts exited cleanly"
fi
echo " results written to: $HERE/results/"
echo "================================================================"
