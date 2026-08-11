#!/usr/bin/env python3
# ABOUTME: root-only helper — drop to a given gid/uid and exec a command, so `pf` group
# ABOUTME: rules can be probed from a credential the invoking user is not a member of.
#
# Usage (as root): as_gid.py <gid> <uid> <argv...>
#
# Why not `sudo -u <user> -g <group>`: sudo's policy governs the runas *group*, and on a
# default macOS sudoers root is refused a group the target user does not belong to —
#   "Sorry, user root is not allowed to execute '...' as karlstenerud:yoloaispike"
# That refusal is silent in the sense that matters: the probe returns an empty string, which
# reads exactly like a blocked connection. A first run of privileged.sh reported an entire
# gid allowlist section as blank for this reason. Dropping privilege directly avoids the
# policy layer, and a dedicated sandbox gid is by definition one the user is NOT in.

import os
import sys


def main() -> None:
    if len(sys.argv) < 4:
        sys.exit("usage: as_gid.py <gid> <uid> <argv...>")
    gid, uid, argv = int(sys.argv[1]), int(sys.argv[2]), sys.argv[3:]
    if os.geteuid() != 0:
        sys.exit("as_gid.py must run as root")
    # Order matters: setgid and setgroups need the privilege that setuid discards.
    os.setgid(gid)
    os.setgroups([gid])
    os.setuid(uid)
    if os.getegid() != gid or os.geteuid() != uid:
        sys.exit("failed to assume %d:%d" % (uid, gid))
    os.execvp(argv[0], argv)


if __name__ == "__main__":
    main()
