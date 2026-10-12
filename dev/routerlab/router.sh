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
# a dhcp request from the lan side, chaddr ce:11:22:33:44:55 and hostname fglabtest
dhcp_ask() { { printf '\001\001\006\000'; head -c 24 /dev/zero; printf '\316\021\042\063\104\125'; head -c 202 /dev/zero; printf '\143\202\123\143\065\001\003\014\011fglabtest\377'; } >"$WORK/dhcp-$slot.bin"; socat -u "FILE:$WORK/dhcp-$slot.bin" UDP:127.0.0.1:$((16300 + slot)); }
check "sees devices asking for an address" "until_ok 5 'dhcp_ask; r \"logread -e fengardd | grep -q fglabtest\"'"
until_ok 60 lan_ok
check "lan device resolves and opens the dashboard" "lan_ok"
check "ssh to the router still works" "r true"
if [ "$wan" = yes ]; then
	# emulated cpus parse the lists slowly
	until_ok 600 "r 'logread -e fengardd | grep -q \"blocklists updated\"'"
	check "full blocklists loaded" "r 'logread -e fengardd | grep -q \"blocklists updated: [0-9][0-9][0-9][0-9][0-9]\"'"
	check "lan device resolves internet names" "until_ok 30 '$WORK/dnsq 127.0.0.1:$((15300 + slot)) example.com | grep -q NOERROR'"
fi
# the mips64le vm's wan never comes up, it has no way to the lab machine at all
if [ -s "$WORK/doh3-ca.pem" ] && r 'ip route get 10.0.2.2' >/dev/null; then
	# dns over http/3 to the lab's own server, switched on through the dashboard like a person would.
	# fengard trusts the lab certificate only for this, through SSL_CERT_FILE
	r 'cat >/etc/fengard/lab-ca.pem' <"$WORK/doh3-ca.pem"
	r 'f=/etc/init.d/fengard; awk "!d && /procd_set_param respawn/ { print; print \"	procd_set_param env SSL_CERT_FILE=/etc/fengard/lab-ca.pem\"; d=1; next } 1" $f >$f.new && cat $f.new >$f && rm $f.new && $f restart'
	# an emulated mips cpu takes a minute or two to reload the blocklists after the restart
	until_ok 180 "fg_ok && lan_ok"
	api() { curl -s -m 20 -b "$WORK/jar-$slot" -c "$WORK/jar-$slot" -X "$1" -H 'X-Fengard: 1' -H 'Content-Type: application/json' ${3:+-d "$3"} "http://127.0.0.1:$((18300 + slot))$2"; }
	rm -f "$WORK/jar-$slot"
	api POST /api/setup '{"username":"admin","password":"lab-password-2026"}' >/dev/null
	api POST /api/login '{"username":"admin","password":"lab-password-2026"}' >/dev/null
	api PUT /api/settings "$(api GET /api/settings | python3 -c 'import json,sys; s=json.load(sys.stdin); s["upstreams"]=["https://10.0.2.2:8853/dns-query"]; s["doh3"]=True; print(json.dumps(s))')" >/dev/null
	h3_ok() { for n in openwrt.org wikipedia.org debian.org; do "$WORK/dnsq" 127.0.0.1:$((15300 + slot)) "$n" | grep -q NOERROR || return 1; done; api GET /api/dns | grep -q '"using":"HTTP/3"'; }
	check "lookups go over dns over http/3" "until_ok 60 h3_ok"
	step "http/3: $(api GET /api/dns | grep -o '"transports":\[[^]]*\]') answered $(api GET /api/overview | grep -o '"doh3":[0-9]*')"
fi
# read after the http/3 check so it isn't caught mid way through freeing the blocklist parse
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
