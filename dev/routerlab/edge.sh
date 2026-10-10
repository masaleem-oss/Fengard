#!/bin/bash
# the situations that would hurt a household on openwrt 24.10 x86 squashfs which has the same layout as router flash
#   edge.sh [test...]   tests lowram ram128 subnet nonet port53 power sysupgrade resilience lowflash
# prints EDGE rows for the readme table
WORK=${WORK:-/var/tmp/routerlab}
LAB=$(cd "$(dirname "$0")" && pwd)
IMG=$WORK/openwrt-24.10.8-x86-64-generic-squashfs-combined.img
V="bash $LAB/vm.sh"
quiet() { grep -v "post-quantum\|store now\|openssh.com/pq\|^\*\* WARNING\|Permanently added"; }
row() { if eval "$2"; then echo "EDGE|$1|✅"; else echo "EDGE|$1|❌"; fi; }
inst() {
	ssh-keygen -R "[127.0.0.1]:$((2300 + $1))" -f ~/.ssh/fengard_known_hosts >/dev/null 2>&1
	(cd "$KIT" && SSH_PORT=$((2300 + $1)) sh install.sh router 127.0.0.1 </dev/null 2>&1) | quiet
}
uninst() { (cd "$KIT" && SSH_PORT=$((2300 + $1)) sh install.sh remove-router 127.0.0.1 --purge </dev/null 2>&1) | quiet | tail -1; }
r() { s=$1; shift; $V ssh "$s" "$@" 2>/dev/null; }
landns() { "$WORK/dnsq" 127.0.0.1:$((15300 + $1)) "${2:-localhost}" | grep -q NOERROR; }
fg_ok() { r "$1" '. /etc/fengard/install.env; pidof fengardd >/dev/null && nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP"'; }
untouched() { r "$1" '! [ -e /etc/init.d/fengard ] && [ "$(uci -q get dhcp.@dnsmasq[0].port)" != 0 ]'; }
waitboot() { for _ in $(seq 90); do r "$1" true && return 0; sleep 2; done; return 1; }
waitdns() { for _ in $(seq 40); do landns "$1" "${2:-localhost}" && return 0; sleep 3; done; return 1; }

[ $# -gt 0 ] || set -- lowram ram128 subnet nonet port53 power sysupgrade resilience lowflash
for t in "$@"; do
	case $t in
	lowram)
		$V start lowram x86 "$IMG" 96 20 >/dev/null
		inst 20 >/dev/null
		row "96 MB of RAM (under the 100 MB minimum): refuses cleanly, router untouched" "untouched 20 && landns 20"
		$V stop lowram ;;
	ram128)
		$V start ram128 x86 "$IMG" 128 21 >/dev/null
		inst 21 >/dev/null
		for _ in $(seq 60); do r 21 'logread -e fengardd | grep -q "blocklists updated"' && break; sleep 3; done
		row "128 MB of RAM: full blocklists, no out of memory" "fg_ok 21 && landns 21 example.com && ! r 21 'dmesg | grep -qi \"out of memory\|oom-kill\"'"
		uninst 21 >/dev/null
		$V stop ram128 ;;
	subnet)
		$V start subnet x86 "$IMG" 256 22 >/dev/null
		r 22 'uci set network.lan.ipaddr=10.0.0.1; uci set network.lan.netmask=255.255.0.0; uci commit network; poweroff'
		sleep 8; $V stop subnet; sleep 2
		LANNET=10.0.0.0/16 RIP=10.0.0.1 SHOST=10.0.0.2 FGIP=10.0.0.4 $V start subnet x86 "$IMG" 256 22 keep >/dev/null
		inst 22 >/dev/null
		waitdns 22 fengard.lan
		row "LAN on 10.0.0.1/16: picks a spare address in the subnet" "fg_ok 22 && landns 22 fengard.lan && r 22 '. /etc/fengard/install.env; wget -q -O /dev/null http://\$FG_IP/'"
		uninst 22 >/dev/null
		$V stop subnet ;;
	nonet)
		WANOPT=restrict=on $V start nonet x86 "$IMG" 256 23 >/dev/null
		inst 23 >/dev/null
		row "No internet during install: installs and serves local names" "fg_ok 23 && landns 23 fengard.lan"
		uninst 23 >/dev/null
		row "No internet: uninstall gives DNS back" "untouched 23 && landns 23"
		$V stop nonet ;;
	port53)
		$V start port53 x86 "$IMG" 256 24 >/dev/null
		r 24 'dnsmasq --port=53 --listen-address=127.0.0.2 --bind-interfaces --conf-file=/dev/null --no-resolv --pid-file=/tmp/other-dns.pid'
		inst 24 >/dev/null
		row "Another DNS server already on port 53: stops and rolls back" "landns 24 && r 24 '! pidof fengardd >/dev/null && [ \"\$(uci -q get dhcp.@dnsmasq[0].port)\" != 0 ]'"
		$V stop port53 ;;
	power)
		for stage in "== DNS:" "== starting"; do
			$V start power x86 "$IMG" 256 25 >/dev/null
			inst 25 | while read -r line; do
				case "$line" in "$stage"*) sleep 1; pkill -9 -f "name vm-power "; break ;; esac
			done
			sleep 3; pkill -9 -f "name vm-power "; sleep 2
			$V start power x86 "$IMG" 256 25 keep >/dev/null
			waitdns 25
			row "Power cut at '${stage#== }' during install: DNS works after reboot" "landns 25"
			$V stop power; sleep 2
		done ;;
	sysupgrade)
		$V start sysup x86 "$IMG" 256 26 >/dev/null
		inst 26 >/dev/null
		r 26 'cat > /tmp/fw.img.gz' <"$IMG.gz"
		r 26 'sysupgrade /tmp/fw.img.gz' >/dev/null 2>&1
		sleep 30; waitboot 26
		for _ in $(seq 40); do fg_ok 26 && break; sleep 3; done
		row "Firmware upgrade keeping settings: Fengard comes back" "fg_ok 26 && waitdns 26"
		$V stop sysup ;;
	resilience)
		$V start resil x86 "$IMG" 256 27 >/dev/null
		inst 27 >/dev/null
		r 27 '/etc/init.d/fengard stop'
		sleep 15
		row "Fengard stopped by hand: the house keeps DNS" "landns 27"
		r 27 '/etc/init.d/fengard start'; sleep 20
		r 27 'mv /usr/bin/fengardd /usr/bin/fengardd.gone; reboot'
		sleep 20; waitboot 27; sleep 120
		row "Program missing at boot: the house keeps DNS" "landns 27"
		r 27 'mv /usr/bin/fengardd.gone /usr/bin/fengardd'
		r 27 'printf "#!/bin/sh\nexit 1\n" > /tmp/crash; chmod +x /tmp/crash; mount --bind /tmp/crash /usr/bin/fengardd; /etc/init.d/fengard restart'
		sleep 150
		row "Program crash-looping: the house keeps DNS" "landns 27"
		r 27 'umount /usr/bin/fengardd'
		for _ in $(seq 160); do fg_ok 27 && break; sleep 5; done
		row "Recovers by itself once the program works again" "fg_ok 27 && landns 27"
		uninst 27 >/dev/null
		$V stop resil ;;
	lowflash)
		$V start lowflash x86 "$IMG" 256 28 >/dev/null
		r 28 'free=$(df -k /overlay | awk "NR==2{print \$4}"); dd if=/dev/zero of=/overlay/filler bs=1024 count=$((free - 12000)) 2>/dev/null'
		inst 28 >/dev/null
		row "Nearly full flash: refuses cleanly, router untouched" "untouched 28 && landns 28"
		$V stop lowflash ;;
	esac
done
