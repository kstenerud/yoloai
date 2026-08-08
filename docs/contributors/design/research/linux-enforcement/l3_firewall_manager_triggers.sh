#!/usr/bin/env bash
# ABOUTME: L3 — what do other firewall managers do to a custom nft table? Checks
# ABOUTME: table presence AND live enforcement after each trigger, because the
# ABOUTME: interesting failure is state that still looks right and no longer applies.
set -uo pipefail

TABLE=yb_l3
CT=yb_l3_guest
WATCHDOG_SECS=300
say() { printf '\n=== %s ===\n' "$*"; }

BACKUP=$(mktemp -d)
WATCHDOG_PID=""
cleanup() {
	[ -n "$WATCHDOG_PID" ] && sudo kill "$WATCHDOG_PID" 2>/dev/null
	sudo ufw --force disable >/dev/null 2>&1
	sudo cp "$BACKUP/user.rules" /etc/ufw/user.rules 2>/dev/null
	sudo cp "$BACKUP/user6.rules" /etc/ufw/user6.rules 2>/dev/null
	sudo nft delete table inet $TABLE 2>/dev/null
	docker rm -f $CT >/dev/null 2>&1
	rm -rf "$BACKUP"
}
trap cleanup EXIT

say "SAFETY: this host is reached over SSH — back up ufw config, allow 22, arm a watchdog"
sudo cp /etc/ufw/user.rules /etc/ufw/user6.rules "$BACKUP/"
echo "ufw config backed up to $BACKUP"
echo "ufw state before: $(sudo ufw status | head -1)"
sudo ufw allow 22/tcp >/dev/null 2>&1
sudo ufw allow from 192.168.111.0/24 >/dev/null 2>&1
sudo setsid bash -c "sleep $WATCHDOG_SECS; ufw --force disable" >/dev/null 2>&1 &
WATCHDOG_PID=$!
echo "watchdog armed: ufw force-disabled in ${WATCHDOG_SECS}s if this script dies (pid $WATCHDOG_PID)"

say "install an enforcing sandbox"
docker run -d --name $CT alpine sleep 900 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
echo "guest=$G  allowlisted=$ALLOW (example.com)  denied=1.1.1.1"
sudo nft -f - <<NFT
table inet $TABLE {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G ct state established,related accept
		ip saddr $G udp dport 53 accept
		ip saddr $G ip daddr @allowed counter accept
		ip saddr $G counter drop
	}
}
NFT

check() { # label
	local present enforce allowed
	if sudo nft list table inet $TABLE >/dev/null 2>&1; then present=yes; else present=NO; fi
	if docker exec $CT nc -z -w 4 1.1.1.1 80 >/dev/null 2>&1; then enforce="FAIL-OPEN (denied host reachable)"; else enforce="enforcing"; fi
	if docker exec $CT nc -z -w 4 "$ALLOW" 80 >/dev/null 2>&1; then allowed=reachable; else allowed="BROKEN (allowlisted host unreachable)"; fi
	printf '%-28s table=%-3s  denied-dst=%-32s  allowed-dst=%s\n' "$1" "$present" "$enforce" "$allowed"
}

say "baseline — both controls must be right before any trigger means anything"
check "baseline"

say "TRIGGER: ufw enable"
sudo ufw --force enable >/dev/null 2>&1; sleep 2
check "after ufw enable"

say "TRIGGER: ufw reload"
sudo ufw reload >/dev/null 2>&1; sleep 2
check "after ufw reload"

say "TRIGGER: ufw disable"
sudo ufw --force disable >/dev/null 2>&1; sleep 2
check "after ufw disable"

say "TRIGGER: docker restart (rewrites its own chains)"
sudo systemctl restart docker; sleep 6
docker start $CT >/dev/null 2>&1; sleep 2
G2=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
echo "guest address after docker restart: $G2 (was $G)"
check "after docker restart"

say "TRIGGER: nft flush ruleset (the unconditional case, for reference)"
echo "not run — it would drop this SSH session's host networking along with everything else."
echo "/etc/nftables.conf on this host begins with 'flush ruleset', but nftables.service is"
echo "$(systemctl is-enabled nftables 2>&1) and $(systemctl is-active nftables 2>&1), so that trigger is latent here, not live."

say "final state"
sudo nft list table inet $TABLE 2>&1 | head -20
echo "ufw: $(sudo ufw status | head -1)"
