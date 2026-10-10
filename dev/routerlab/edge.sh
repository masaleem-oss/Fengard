#!/bin/bash
# the situations that would hurt a household on openwrt 24.10 x86 squashfs which has the same layout as router flash
#   edge.sh [test...]   tests lowram ram128 subnet nonet port53 power-dns power-start sysupgrade stopped missing crash lowflash
# prints EDGE rows for the readme table
# each test has its own vm slot so they can all run at once
WORK=${WORK:-/var/tmp/routerlab}
LAB=$(cd "$(dirname "$0")" && pwd)
IMG=$WORK/openwrt-24.10.8-x86-64-generic-squashfs-combined.img
V() { bash "$LAB/vm.sh" "$@"; }
quiet() { grep -v "post-quantum\|store now\|openssh.com/pq\|^\*\* WARNING\|Permanently added"; }
t0=$(date +%s)
row() { if eval "$2"; then echo "EDGE|$1|✅"; else echo "EDGE|$1|❌"; fi; echo "  after $(($(date +%s) - t0))s"; }
# run.sh builds the kit while the vms boot
kit() {
	for _ in $(seq 600); do
		[ -f "$WORK/kit.ok" ] && { KIT=$(cat "$WORK/kit.ok"); return 0; }
		[ -f "$WORK/kit.fail" ] && break
		sleep 1
	done
	echo "no kit to test"
	exit 1
}
# own known hosts per slot since parallel runs fight over one file, no terminal so a prompt fails not hangs
kitsh() { s=$1; shift; mkdir -p "$WORK/home-$s"; (cd "$KIT" && HOME="$WORK/home-$s" SSH_PORT=$((2300 + s)) setsid -w sh install.sh "$@" </dev/null 2>&1); }
inst() { rm -f "$WORK/home-$1/.ssh/fengard_known_hosts"; kitsh "$1" router 127.0.0.1 | quiet; }
uninst() { kitsh "$1" remove-router 127.0.0.1 --purge | quiet | tail -1; }
r() { s=$1; shift; V ssh "$s" "$@" 2>/dev/null; }
landns() { "$WORK/dnsq" 127.0.0.1:$((15300 + $1)) "${2:-localhost}" | grep -q NOERROR; }
fg_ok() { r "$1" '. /etc/fengard/install.env; pidof fengardd >/dev/null && nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP"'; }
untouched() { r "$1" '! [ -e /etc/init.d/fengard ] && [ "$(uci -q get dhcp.@dnsmasq[0].port)" != 0 ]'; }
waitboot() { for _ in $(seq 90); do r "$1" true && return 0; sleep 1; done; return 1; }
waitdown() { for _ in $(seq 60); do r "$1" true || return 0; sleep 1; done; return 1; }
# polls a check for up to n seconds
until_ok() { n=$1; shift; for _ in $(seq "$n"); do eval "$*" && return 0; sleep 1; done; return 1; }
# the guard checks every 20 s and waits 10 min before trying fengard again, this makes it 2 s and 1 min
fast_guard() { r "$1" "echo \"GUARD_TICK='2'\" >>/etc/fengard/install.env; /etc/init.d/fengard restart"; until_ok 60 fg_ok "$1"; }

[ $# -gt 0 ] || set -- lowram ram128 subnet nonet port53 power-dns power-start sysupgrade stopped missing crash lowflash
for t in "$@"; do
	case $t in
	lowram)
		V start lowram x86 "$IMG" 96 20 >/dev/null
		kit; inst 20 >/dev/null
		row "96 MB of RAM (under the 100 MB minimum): refuses cleanly, router untouched" "untouched 20 && landns 20"
		V stop lowram ;;
	ram128)
		V start ram128 x86 "$IMG" 128 21 >/dev/null
		kit; inst 21 >/dev/null
		until_ok 600 "r 21 'logread -e fengardd | grep -q \"blocklists updated\"'"
		row "128 MB of RAM: full blocklists, no out of memory" "fg_ok 21 && landns 21 example.com && ! r 21 'dmesg | grep -qi \"out of memory\|oom-kill\"'"
		uninst 21 >/dev/null
		V stop ram128 ;;
	subnet)
		V start subnet x86 "$IMG" 256 22 >/dev/null
		# early in boot dropbear can restart under us so keep at it until the change reads back
		until_ok 30 "r 22 'uci set network.lan.ipaddr=10.0.0.1; uci set network.lan.netmask=255.255.0.0; uci commit network; sync; uci get network.lan.ipaddr | grep -qx 10.0.0.1'"
		r 22 poweroff
		until_ok 30 "! pgrep -f 'name vm-subnet ' >/dev/null"
		V stop subnet; sleep 1
		LANNET=10.0.0.0/16 RIP=10.0.0.1 SHOST=10.0.0.2 FGIP=10.0.0.4 V start subnet x86 "$IMG" 256 22 keep >/dev/null
		kit; inst 22 >/dev/null
		until_ok 60 landns 22 fengard.lan
		row "LAN on 10.0.0.1/16: picks a spare address in the subnet" "fg_ok 22 && landns 22 fengard.lan && r 22 '. /etc/fengard/install.env; wget -q -O /dev/null http://\$FG_IP/'"
		uninst 22 >/dev/null
		V stop subnet ;;
	nonet)
		WANOPT=restrict=on V start nonet x86 "$IMG" 256 23 >/dev/null
		kit; inst 23 >/dev/null
		row "No internet during install: installs and serves local names" "fg_ok 23 && landns 23 fengard.lan"
		uninst 23 >/dev/null
		row "No internet: uninstall gives DNS back" "until_ok 30 'untouched 23 && landns 23'"
		V stop nonet ;;
	port53)
		V start port53 x86 "$IMG" 256 24 >/dev/null
		r 24 'dnsmasq --port=53 --listen-address=127.0.0.2 --bind-interfaces --conf-file=/dev/null --no-resolv --pid-file=/tmp/other-dns.pid'
		kit; inst 24 >/dev/null
		row "Another DNS server already on port 53: stops and rolls back" "until_ok 30 landns 24 && r 24 '! pidof fengardd >/dev/null && [ \"\$(uci -q get dhcp.@dnsmasq[0].port)\" != 0 ]'"
		V stop port53 ;;
	power-dns | power-start)
		# pulls the plug as the installer prints this stage
		stage="== DNS:" slot=25
		[ "$t" = power-start ] && stage="== starting" slot=29
		V start "$t" x86 "$IMG" 256 $slot >/dev/null
		kit
		inst $slot | while read -r line; do
			case "$line" in "$stage"*) sleep 1; pkill -9 -f "name vm-$t "; break ;; esac
		done
		pkill -9 -f "name vm-$t "; sleep 1
		V start "$t" x86 "$IMG" 256 $slot keep >/dev/null
		until_ok 120 landns $slot
		row "Power cut at '${stage#== }' during install: DNS works after reboot" "landns $slot"
		V stop "$t" ;;
	sysupgrade)
		V start sysup x86 "$IMG" 256 26 >/dev/null
		kit; inst 26 >/dev/null
		r 26 'cat > /tmp/fw.img.gz' <"$IMG.gz"
		r 26 'sysupgrade /tmp/fw.img.gz' >/dev/null 2>&1
		waitdown 26; waitboot 26
		until_ok 120 fg_ok 26
		row "Firmware upgrade keeping settings: Fengard comes back" "fg_ok 26 && until_ok 60 landns 26"
		V stop sysup ;;
	stopped)
		V start stopped x86 "$IMG" 256 27 >/dev/null
		kit; inst 27 >/dev/null
		r 27 '/etc/init.d/fengard stop'
		row "Fengard stopped by hand: the house keeps DNS" "until_ok 30 landns 27"
		uninst 27 >/dev/null
		V stop stopped ;;
	missing)
		V start missing x86 "$IMG" 256 30 >/dev/null
		kit; inst 30 >/dev/null
		fast_guard 30
		r 30 'mv /usr/bin/fengardd /usr/bin/fengardd.gone; reboot'
		waitdown 30; waitboot 30
		row "Program missing at boot: the house keeps DNS" "until_ok 90 landns 30"
		V stop missing ;;
	crash)
		V start crash x86 "$IMG" 256 31 >/dev/null
		kit; inst 31 >/dev/null
		fast_guard 31
		r 31 'printf "#!/bin/sh\nexit 1\n" > /tmp/crash; chmod +x /tmp/crash; mount --bind /tmp/crash /usr/bin/fengardd; /etc/init.d/fengard restart'
		# fengard is gone for good once the guard has taken over
		until_ok 60 "r 31 '[ -f /var/run/fengard-rescue.pid ]'"
		row "Program crash-looping: the house keeps DNS" "until_ok 30 landns 31"
		r 31 'umount /usr/bin/fengardd'
		row "Recovers by itself once the program works again" "until_ok 240 fg_ok 31 && landns 31"
		uninst 31 >/dev/null
		V stop crash ;;
	lowflash)
		V start lowflash x86 "$IMG" 256 28 >/dev/null
		r 28 'free=$(df -k /overlay | awk "NR==2{print \$4}"); dd if=/dev/zero of=/overlay/filler bs=1024 count=$((free - 12000)) 2>/dev/null'
		kit; inst 28 >/dev/null
		row "Nearly full flash: refuses cleanly, router untouched" "untouched 28 && landns 28"
		V stop lowflash ;;
	esac
done
