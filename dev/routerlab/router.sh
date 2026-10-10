#!/bin/bash
# installs the kit on one emulated router and checks it the way a household would notice
#   router.sh NAME KIND FILE RAM SLOT REBOOT LABEL FIRMWARE
# writes $WORK/results/NAME.log and a ROW line for the readme table
WORK=${WORK:-/var/tmp/routerlab}
LAB=$(cd "$(dirname "$0")" && pwd)
name=$1 kind=$2 file=$3 ram=$4 slot=$5 reboot=$6 label=$7 firmware=$8
V() { bash "$LAB/vm.sh" "$@"; }
r() { V ssh "$slot" "$@" 2>/dev/null; }
mkdir -p "$WORK/results"
exec >"$WORK/results/$name.log" 2>&1
pass=0 fail=0 t0=$(date +%s)
# seconds since the start so the slow steps stand out
step() { echo "[$(($(date +%s) - t0))s] $*"; }
check() { if eval "$2"; then step "PASS  $1"; pass=$((pass + 1)); else step "FAIL  $1"; fail=$((fail + 1)); fi; }
quiet() { grep -v "post-quantum\|store now\|openssh.com/pq\|^\*\* WARNING\|Permanently added"; }
until_ok() { n=$1; shift; for _ in $(seq "$n"); do eval "$*" && return 0; sleep 1; done; return 1; }

V start "$name" "$kind" "$WORK/$file" "$ram" "$slot" || { echo "ROW|$label|$firmware|$kind|vm did not boot|"; exit 1; }
step "router: $(r '. /etc/openwrt_release; echo $DISTRIB_DESCRIPTION $DISTRIB_ARCH')"
until_ok 60 "r 'ping -c1 -W2 1.1.1.1 >/dev/null 2>&1'" && wan=yes || wan=no
step "wan internet: $wan"

lan_ok() {
	"$WORK/dnsq" 127.0.0.1:$((15300 + slot)) fengard.lan | grep -q NOERROR &&
		[ "$(curl -s -m 10 -o /dev/null -w '%{http_code}' http://127.0.0.1:$((18300 + slot))/)" = 200 ]
}
fg_ok() { r '. /etc/fengard/install.env; pidof fengardd >/dev/null && nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP"'; }

# run.sh builds the kit while the vms boot
until_ok 600 "[ -f '$WORK/kit.ok' ] || [ -f '$WORK/kit.fail' ]"
[ -f "$WORK/kit.ok" ] || { echo "ROW|$label|$firmware|$kind|no kit to test|"; exit 1; }
KIT=$(cat "$WORK/kit.ok")
# own known hosts per slot since parallel runs fight over one file, no terminal so a prompt fails not hangs
kitsh() { mkdir -p "$WORK/home-$slot"; (cd "$KIT" && HOME="$WORK/home-$slot" SSH_PORT=$((2300 + slot)) setsid -w sh install.sh "$@" </dev/null 2>&1); }
# a fresh vm has a new host key
rm -f "$WORK/home-$slot/.ssh/fengard_known_hosts"
step "installing"
kitsh router 127.0.0.1 | quiet | tail -8
check "installed the right cpu build" "r '. /etc/fengard/install.env && echo arch=\$ARCH fw=\$FW ip=\$FG_IP' | grep arch="
until_ok 60 fg_ok
check "fengard answering dns" "fg_ok"
until_ok 60 lan_ok
check "lan device resolves and opens the dashboard" "lan_ok"
check "ssh to the router still works" "r true"
if [ "$wan" = yes ]; then
	# emulated cpus parse the lists slowly
	until_ok 600 "r 'logread -e fengardd | grep -q \"blocklists updated\"'"
	check "full blocklists loaded" "r 'logread -e fengardd | grep -q \"blocklists updated: [0-9][0-9][0-9][0-9][0-9]\"'"
	check "lan device resolves internet names" "$WORK/dnsq 127.0.0.1:$((15300 + slot)) example.com | grep -q NOERROR"
fi
rss=$(r 'grep VmRSS /proc/$(pidof fengardd)/status' | awk '{ printf "%d MB", $2 / 1024 }')
build=$(r '. /etc/fengard/install.env; echo linux-$ARCH')
step "memory: $rss"
if [ "$reboot" = yes ]; then
	r reboot
	until_ok 60 "! r true"
	until_ok 120 "r true"
	until_ok 90 "fg_ok && lan_ok"
	check "works after a reboot" "fg_ok && lan_ok"
fi
kitsh remove-router 127.0.0.1 --purge | quiet | tail -1
check "uninstall gives dns back to dnsmasq" "r 'nslookup localhost 127.0.0.1 >/dev/null 2>&1 && ! pidof fengardd >/dev/null'"
check "no fengard firewall rules left" "r '! nft list table inet fengard >/dev/null 2>&1 && ! (iptables-save 2>/dev/null | grep -q FENGARD)'"
V stop "$name"

result="✅ all $pass checks"
[ "$fail" -eq 0 ] || result="❌ $fail of $((pass + fail)) failed"
[ "$wan" = yes ] || result="$result (no internet in the vm)"
step "RESULT $name: $pass passed, $fail failed"
echo "ROW|$label|$firmware|${build:-$kind}|$result|$rss"
