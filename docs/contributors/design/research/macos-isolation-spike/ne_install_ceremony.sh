#!/bin/bash
# ABOUTME: M6 — what a NetworkExtension content-filter system extension would cost to install,
# ABOUTME: probed on this host and read from Apple's docs. Establishes the ceremony; builds nothing.
#
# Run: bash ne_install_ceremony.sh          (no privilege needed, and that is part of the point)
#
# WHY THIS EXISTS
#   prior-art-egress-enforcement.md §4 says the sanctioned macOS mechanism for filtering a HOST
#   PROCESS GROUP — which is what seatbelt is — is `NEFilterDataProvider`, not pf with a dedicated
#   gid. seatbelt-host-pf-enforcement.md is parked with the reason "not worth building yet"; if the
#   sanctioned mechanism is out of reach for a different and permanent reason, the parking has a
#   better justification and D132 should say per backend which mechanism it means.
#
#   TIMEBOXED BY INSTRUCTION: establish the install ceremony and stop. Nothing is built here.
#
# EVIDENCE LEVEL, stated up front because this file mixes two kinds and they must not blur:
#   MEASURED  — everything under P1..P4. Run on this host, reproducible by re-running this script.
#   READ      — everything under R1. Apple's documentation, summarised. Not verified here, and it
#               cannot be: verifying it means obtaining a Developer ID, which is the finding.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
RESULTS="$HERE/results/ne-install-ceremony.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
note(){ printf '        %s\n' "$*"; }

echo "host: macOS $(sw_vers -productVersion) ($(sw_vers -buildVersion)) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"

say "P1 MEASURED — is any system extension installed on this host at all?"
systemextensionsctl list 2>&1 | sed 's/^/        /'
note "Baseline. yoloAI installs none today, and neither does anything else here."

say "P2 MEASURED — can this host load an UNNOTARIZED extension for development?"
note "This is the question that decides whether the mechanism can even be prototyped locally."
note "\`systemextensionsctl developer on\` is the documented way to run an unsigned/unnotarized"
note "system extension during development. Asking this host:"
systemextensionsctl developer 2>&1 | sed 's/^/          /'
note ""
note "and the state it depends on:"
csrutil status 2>&1 | sed 's/^/          /'
note ""
note "So developer mode is unavailable while SIP is on, and SIP is on. Prototyping a system"
note "extension on a normal machine costs a REBOOT INTO RECOVERY AND SIP DISABLED — for the"
note "developer, before any user is involved. That is a measured fact about this host, not a"
note "documentation claim, and it is the cheapest thing in the whole ceremony to have gotten wrong."

say "P3 MEASURED — could a signed extension be produced here?"
ids=$(security find-identity -v -p codesigning 2>&1 | tail -1)
note "codesigning identities available: $ids"
note "A system extension must be signed with a Developer ID. With zero identities present, the"
note "artifact cannot be produced on this machine at all — the blocker is an Apple Developer"
note "Program membership (a paid annual account), not a build step."

say "P4 MEASURED — where would the containing app have to live?"
note "A system extension is not installed on its own. It ships INSIDE an app bundle, and the app"
note "requests activation at runtime. Current state of the two locations that matter:"
napps=0; for a in /Applications/*[Yy]olo*; do [ -e "$a" ] && napps=$((napps+1)); done
nsysext=0; for e in /Library/SystemExtensions/*; do [ -e "$e" ] && nsysext=$((nsysext+1)); done
note "  /Applications entries matching yolo   : $napps"
note "  /Library/SystemExtensions             : $nsysext entries"
note "yoloAI ships as a single CLI binary, typically to a Homebrew prefix. It has no app bundle."
note "That is a packaging change, not a code change, and it is the part most likely to be"
note "underestimated: the deliverable stops being 'a binary' and becomes 'a signed, notarized"
note "macOS application that happens to contain a CLI'."

say "R1 READ — the rest of the ceremony, from Apple's documentation"
cat <<'EOF'
        Not verified on this host. Sources listed at the end.

        1. ENTITLEMENT  com.apple.developer.networking.networkextension, with the value
           `content-filter-provider-systemextension` — the -systemextension suffix is the one
           required for Developer ID distribution outside the Mac App Store.

           NOTE, and it corrects the impression prior-art-egress-enforcement.md leaves: this
           entitlement has NOT required Apple's case-by-case approval since November 2016. Any
           developer can enable the Network Extension capability like any other. (App *push*
           providers, a different thing, still need authorization.) So "Apple has to say yes" is
           NOT among the barriers. The barriers are the four below, and they are enough.

        2. DEVELOPER ID  The app and the extension must be signed with a Developer ID certificate,
           which requires paid Apple Developer Program membership. See P3: unavailable here.

        3. NOTARIZATION  macOS 10.15+ requires notarization for software distributed outside the
           App Store. This is a per-release submission to Apple, so it lands in the release process
           permanently, not once at the start.

        4. LOCATION  sysextd refuses to activate an extension whose containing app is outside
           /Applications. A Homebrew-installed CLI cannot host one.

        5. USER APPROVAL  Activation prompts the user, who must approve the extension in System
           Settings. It cannot be done silently, and it cannot be done from a script.

        6. THE MDM ESCAPE HATCH  A device-management service can pre-authorize all extensions from
           a given developer or of a given type, removing step 5 — for managed fleets only. This is
           the one path where the ceremony collapses, and it is worth naming because a team
           deploying yoloAI across managed Macs is a plausible user, unlike a solo developer.

        WHAT IT WOULD BUY, so the cost is weighed against something. NEFilterDataProvider gives
        per-flow verdicts with the OWNING PROCESS attached — exactly the identity M1 measured pf as
        unable to supply. For seatbelt, whose target is a host process group, that is the right
        shape and the gid mechanism is the workaround. For tart and apple it buys nothing: those
        targets are VMs with their own addresses, filtered at the packet layer, and the same
        reading notes NetworkExtension sits ABOVE pf ("if pf blocks a packet the application
        firewall will never see it"). So the split D132 should record is per backend, not global.
EOF

say "VERDICT — the shape of it, for synthesis to decide on"
cat <<'EOF'
        The blocker is NOT that Apple must permit it. It is that the deliverable changes:
        a notarized, Developer-ID-signed .app in /Applications, plus a user approval step, plus a
        SIP-disabled machine to develop it on. yoloAI ships an unsigned CLI binary today.

        That is a permanent structural cost, not a "not yet" — which is a better reason to keep
        seatbelt-host-pf-enforcement.md parked than the one currently recorded, and a real route if
        it is ever unparked for a managed-fleet deployment where item 6 applies.

        NOT TRIED, and deliberately: building an extension, obtaining a Developer ID, disabling SIP,
        or measuring NEFilterDataProvider's runtime behaviour. The instruction was to establish the
        ceremony and stop, and every one of those costs money, a reboot, or both.

        SOURCES (read, not run):
          https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.networking.networkextension
          https://developer.apple.com/documentation/networkextension/content-filter-providers
          https://developer.apple.com/documentation/technotes/tn3134-network-extension-provider-deployment
          https://developer.apple.com/documentation/systemextensions
          https://support.apple.com/guide/deployment/system-extensions-in-macos-depa5fb8376f/web
          https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution
EOF
