#!/bin/bash
# installs the kit on one emulated router and checks it the way a household would notice
#   router.sh NAME KIND FILE RAM SLOT REBOOT LABEL FIRMWARE
# writes $WORK/results/NAME.log and a ROW line for the readme table
WORK=${WORK:-/var/tmp/routerlab}
LAB=$(cd "$(dirname "$0")" && pwd)
name=$1 kind=$2 file=$3 ram=$4 slot=$5 reboot=$6 label=$7 firmware=$8
V="bash $LAB/vm.sh"
r() { $V ssh "$slot" "$@" 2>/dev/null; }
mkdir -p "$WORK/results"
exec >"$WORK/results/$name.log" 2>&1
pass=0 fail=0
check() { if eval "$2"; then echo "PASS  $1"; pass=$((pass + 1)); else echo "FAIL  $1"; fail=$((fail + 1)); fi; }
quiet() { grep -v "post-quantum\|store now\|openssh.com/pq\|^\*\* WARNING\|Permanently added"; }

$V start "$name" "$kind" "$WORK/$file" "$ram" "$slot" || { echo "ROW|$label|$firmware|$kind|vm did not boot|"; exit 1; }
echo "router: $(r '. /etc/openwrt_release; echo $DISTRIB_DESCRIPTION $DISTRIB_ARCH' )"
for _ in $(seq 20); do r 'ping -c1 -W2 1.1.1.1 >/dev/null 2>&1' && break; sleep 3; done
wan=$(r 'ping -c1 -W2 1.1.1.1 >/dev/null 2>&1 && echo yes || echo no')
echo "wan internet: $wan"

lan_ok() {
	"$WORK/dnsq" 127.0.0.1:$((15300 + slot)) fengard.lan | grep -q NOERROR &&
		[ "$(curl -s -m 10 -o /dev/null -w '%{http_code}' http://127.0.0.1:$((18300 + slot))/)" = 200 ]
}
fg_ok() { r '. /etc/fengard/install.env; pidof fengardd >/dev/null && nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP"'; }

# a fresh vm has a new host key so forget the last one on this port
ssh-keygen -R "[127.0.0.1]:$((2300 + slot))" -f ~/.ssh/fengard_known_hosts >/dev/null 2>&1
(cd "$KIT" && SSH_PORT=$((2300 + slot)) sh install.sh router 127.0.0.1 </dev/null 2>&1) | quiet | tail -8
check "installed the right cpu build" "r '. /etc/fengard/install.env && echo arch=\$ARCH fw=\$FW ip=\$FG_IP' | grep arch="
for _ in $(seq 30); do fg_ok && break; sleep 2; done
check "fengard answering dns" "fg_ok"
for _ in $(seq 30); do lan_ok && break; sleep 2; done
check "lan device resolves and opens the dashboard" "lan_ok"
check "ssh to the router still works" "r true"
if [ "$wan" = yes ]; then
	# emulated cpus parse the lists slowly so this can take a few minutes
	for _ in $(seq 150); do r 'logread -e fengardd | grep -q "blocklists updated"' && break; sleep 4; done
	check "full blocklists loaded" "r 'logread -e fengardd | grep -q \"blocklists updated: [0-9][0-9][0-9][0-9][0-9]\"'"
	check "lan device resolves internet names" "$WORK/dnsq 127.0.0.1:$((15300 + slot)) example.com | grep -q NOERROR"
fi
rss=$(r 'grep VmRSS /proc/$(pidof fengardd)/status' | awk '{ printf "%d MB", $2 / 1024 }')
build=$(r '. /etc/fengard/install.env; echo linux-$ARCH')
echo "memory: $rss"
if [ "$reboot" = yes ]; then
	r reboot
	sleep 15
	for _ in $(seq 90); do r true && break; sleep 2; done
	for _ in $(seq 45); do fg_ok && lan_ok && break; sleep 2; done
	check "works after a reboot" "fg_ok && lan_ok"
fi
(cd "$KIT" && SSH_PORT=$((2300 + slot)) sh install.sh remove-router 127.0.0.1 --purge </dev/null 2>&1) | quiet | tail -1
check "uninstall gives dns back to dnsmasq" "r 'nslookup localhost 127.0.0.1 >/dev/null 2>&1 && ! pidof fengardd >/dev/null'"
check "no fengard firewall rules left" "r '! nft list table inet fengard >/dev/null 2>&1 && ! (iptables-save 2>/dev/null | grep -q FENGARD)'"
$V stop "$name"

result="✅ all $pass checks"
[ "$fail" -eq 0 ] || result="❌ $fail of $((pass + fail)) failed"
[ "$wan" = yes ] || result="$result (no internet in the vm)"
echo "RESULT $name: $pass passed, $fail failed"
echo "ROW|$label|$firmware|${build:-$kind}|$result|$rss"
