#!/bin/sh
# fengard installer for openwrt routers run on the router with sh router-install.sh
# undo it all with sh /etc/fengard/uninstall.sh
# FG_IP spare lan ip for the dashboard
# LAN_NET lan network name default lan
# FG_DIR put data on other storage like usb
# FORCE=1 skip the size checks

RELEASE_URL='@RELEASE_URL@'   # filled in by the release build for web installs
STATE=/etc/fengard/install.env
INIT=/etc/init.d/fengard
WORK=/tmp/fengard-install
SRC=$(cd "$(dirname "$0")" 2>/dev/null && pwd)

say() { printf '== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
getstate() { [ -f "$2" ] && sed -n "s/^$1='\(.*\)'\$/\1/p" "$2" | tail -n 1; }

[ "$(id -u)" = 0 ] || die "run this as root on the router"
[ -f /etc/openwrt_release ] && command -v uci >/dev/null && command -v ubus >/dev/null ||
	die "this router doesn't run OpenWrt-based firmware, which Fengard needs. Stock firmware from most ISPs, Eero, Google and many TP-Link and Asus models can't run it; see the README for the alternative: running Fengard on a computer."

router_arch() {
	a=$(. /etc/openwrt_release 2>/dev/null; echo "$DISTRIB_ARCH")
	[ -n "$a" ] || a=$(uname -m)
	case "$a" in
	aarch64* | arm64*) echo arm64 ;;
	arm*) echo arm ;;
	x86_64* | amd64*) echo amd64 ;;
	i[3-6]86* | x86*) echo 386 ;;
	mips64el* | mips64le*) echo mips64le ;;
	mips64*) echo mips64 ;;
	mipsel* | mipsle*) echo mipsle ;;
	mips*) echo mips ;;
	riscv64*) echo riscv64 ;;
	loongarch64*) echo loong64 ;;
	*) echo "unknown-$a" ;;
	esac
}
ARCH=$(router_arch)
case "$ARCH" in unknown-*) die "unsupported CPU type '${ARCH#unknown-}'" ;; esac

BIN_SRC=
# FG_BIN is set by the ipk and apk packages which already put the program in place
for f in "$SRC/bin/linux-$ARCH/fengardd" "$SRC/fengardd" $FG_BIN; do
	[ -f "$f" ] && { BIN_SRC=$f; break; }
done
if [ -z "$BIN_SRC" ] && [ "${RELEASE_URL#@}" = "$RELEASE_URL" ]; then
	say "downloading Fengard for this router ($ARCH)"
	mkdir -p "$WORK" && rm -f "$WORK/fengardd" "$WORK/fengardd.gz"
	wget -q -O "$WORK/fengardd.gz" "$RELEASE_URL/fengardd-linux-$ARCH.gz" && gunzip -f "$WORK/fengardd.gz" ||
		die "download failed from $RELEASE_URL (does the router have internet and HTTPS support in wget?)"
	[ -f "$SRC/router-uninstall.sh" ] || wget -q -O "$WORK/router-uninstall.sh" "$RELEASE_URL/router-uninstall.sh"
	BIN_SRC=$WORK/fengardd
fi
[ -n "$BIN_SRC" ] || die "no Fengard program for this router's CPU ($ARCH) next to the installer"
UNINSTALL_SRC=$SRC/router-uninstall.sh
[ -f "$UNINSTALL_SRC" ] || UNINSTALL_SRC=$WORK/router-uninstall.sh
[ -f "$UNINSTALL_SRC" ] || die "router-uninstall.sh is missing next to the installer"
chmod 755 "$BIN_SRC"
# ash runs a program for another cpu as a shell script so the exit code alone can pass, check the usage text
"$BIN_SRC" -h 2>&1 | grep -q -- "-block-ip" ||
	die "the Fengard program doesn't run on this router's CPU ($ARCH)"

FG_DIR=${FG_DIR:-$(getstate FG_DIR "$STATE")}
FG_DIR=${FG_DIR:-/etc/fengard}
if [ "$FG_DIR" = /etc/fengard ]; then BIN=/usr/bin/fengardd; else BIN=$FG_DIR/fengardd; fi
mkdir -p "$FG_DIR" /etc/fengard || die "can't create $FG_DIR"

ram_mb=$(awk '/^MemTotal/ { print int($2 / 1024) }' /proc/meminfo)
if [ "$ram_mb" -lt 100 ] && [ "$FORCE" != 1 ]; then
	die "this router has ${ram_mb} MB of memory; Fengard needs about 128 MB (FORCE=1 to try anyway)"
fi
MEM_MB=$((ram_mb / 4))
[ "$MEM_MB" -le 96 ] || MEM_MB=96
[ "$MEM_MB" -ge 32 ] || MEM_MB=32

free_kb() { df -Pk "$1" 2>/dev/null | awk 'NR == 2 { print $4 }'; }
mount_of() { df -Pk "$1" 2>/dev/null | awk 'NR == 2 { print $6 }'; }
size_kb() { [ -f "$1" ] && echo $(($(wc -c <"$1") / 1024)) || echo 0; }
bin_need=$(($(size_kb "$BIN_SRC") - $(size_kb "$BIN")))
data_have=$(du -sk "$FG_DIR" 2>/dev/null | awk '{ print $1 }')
data_need=$((16384 - ${data_have:-0}))
[ "$data_need" -gt 2048 ] || data_need=2048
if [ "$(mount_of "$(dirname "$BIN")")" = "$(mount_of "$FG_DIR")" ]; then
	set -- "$(dirname "$BIN")" $((bin_need + data_need))
else
	set -- "$(dirname "$BIN")" "$bin_need" "$FG_DIR" "$data_need"
fi
while [ $# -ge 2 ]; do
	have=$(free_kb "$1")
	if [ -n "$have" ] && [ "$have" -lt "$2" ] && [ "$FORCE" != 1 ]; then
		die "not enough free storage at $1: $((have / 1024)) MB free, about $(($2 / 1024)) MB needed. Plug in USB storage and run with FG_DIR=/mnt/<usb>/fengard, or FORCE=1 to try anyway."
	fi
	shift 2
done

istatus() { ubus call "network.interface.$1" status 2>/dev/null; }
iface_ip() { istatus "$1" | jsonfilter -e '@["ipv4-address"][0].address' 2>/dev/null; }
iface_prefix() { istatus "$1" | jsonfilter -e '@["ipv4-address"][0].mask' 2>/dev/null; }
# falls back to static config when its down eg guest wifi off
net_ip() {
	a=$(iface_ip "$1")
	[ -n "$a" ] || [ "$(uci -q get "network.$1.proto")" != static ] || a=$(uci -q get "network.$1.ipaddr" | awk '{ print $1 }')
	echo "${a%/*}"
}
mask_to_prefix() {
	n=0
	for o in $(echo "$1" | tr . ' '); do
		case $o in 255) n=$((n + 8)) ;; 254) n=$((n + 7)) ;; 252) n=$((n + 6)) ;; 248) n=$((n + 5)) ;;
		240) n=$((n + 4)) ;; 224) n=$((n + 3)) ;; 192) n=$((n + 2)) ;; 128) n=$((n + 1)) ;; esac
	done
	echo $n
}

LAN_NET=${LAN_NET:-lan}
LAN_IP=$(iface_ip "$LAN_NET")
LAN_PREFIX=$(iface_prefix "$LAN_NET")
if [ -z "$LAN_IP" ]; then
	LAN_IP=$(uci -q get "network.$LAN_NET.ipaddr" | awk '{ print $1 }')
	case "$LAN_IP" in */*) LAN_PREFIX=${LAN_IP#*/} LAN_IP=${LAN_IP%/*} ;; esac
fi
[ -n "$LAN_PREFIX" ] || LAN_PREFIX=$(mask_to_prefix "$(uci -q get "network.$LAN_NET.netmask")")
[ -n "$LAN_PREFIX" ] && [ "$LAN_PREFIX" -gt 0 ] || LAN_PREFIX=24
[ -n "$LAN_IP" ] || die "couldn't find the router's LAN address (network '$LAN_NET'); set LAN_NET to the LAN network's name"
[ "$LAN_PREFIX" -ge 16 ] && [ "$LAN_PREFIX" -le 30 ] || die "the LAN subnet /$LAN_PREFIX isn't supported (needs /16 to /30)"

# wan zone is whichever one masquerades
WAN_NETS='' WAN_ZONE='' LAN_ZONE=''
i=0
while uci -q get "firewall.@zone[$i]" >/dev/null; do
	name=$(uci -q get "firewall.@zone[$i].name")
	nets=$(uci -q get "firewall.@zone[$i].network")
	if [ "$(uci -q get "firewall.@zone[$i].masq")" = 1 ] || [ "$name" = wan ]; then
		WAN_NETS="$WAN_NETS $nets"
		[ -n "$WAN_ZONE" ] || WAN_ZONE=$name
	fi
	case " $nets " in *" $LAN_NET "*) [ -n "$LAN_ZONE" ] || LAN_ZONE=$name ;; esac
	i=$((i + 1))
done
[ -n "$LAN_ZONE" ] || LAN_ZONE=lan
[ -n "$WAN_ZONE" ] || WAN_ZONE=wan
[ -n "$WAN_NETS" ] || WAN_NETS="wan wan6"
is_wan_net() { case " $WAN_NETS " in *" $1 "*) return 0 ;; esac; return 1; }

# other lan side nets like guest or iot
dhcp_sections() { uci -X show dhcp 2>/dev/null | sed -n "s/^dhcp\.\([^.=]*\)=dhcp\$/\1/p"; }
dhcp_section_of() {
	for s in $(dhcp_sections); do
		[ "$(uci -q get "dhcp.$s.interface")" = "$1" ] && { echo "$s"; return; }
	done
}
EXTRA_NETS=
for s in $(dhcp_sections); do
	n=$(uci -q get "dhcp.$s.interface")
	[ -n "$n" ] && [ "$n" != "$LAN_NET" ] && [ "$(uci -q get "dhcp.$s.ignore")" != 1 ] || continue
	is_wan_net "$n" && continue
	[ -n "$(net_ip "$n")" ] || continue
	EXTRA_NETS="$EXTRA_NETS $n"
done
LAN_DHCP=$(dhcp_section_of "$LAN_NET")

ACTIVE=
[ -f "$STATE" ] && ACTIVE=1
PREV=$STATE
[ -f "$PREV" ] || PREV=$STATE.prev
LEGACY=
if [ -z "$ACTIVE" ] && [ -f "$INIT" ]; then
	# old installer didnt write install.env
	LEGACY=1
	FG_IP=${FG_IP:-$(sed -n 's/.*ip addr add \([0-9.]*\)\/.*/\1/p' "$INIT" | head -n 1)}
	HTTP_PORT=${HTTP_PORT:-$(sed -n 's/.*-http "[0-9.]*:\([0-9]*\)".*/\1/p' "$INIT" | head -n 1)}
	HTTPS_PORT=${HTTPS_PORT:-$(sed -n 's/.*-https "[0-9.]*:\([0-9]*\)".*/\1/p' "$INIT" | head -n 1)}
fi
FG_IP=${FG_IP:-$(getstate FG_IP "$PREV")}
HTTP_PORT=${HTTP_PORT:-$(getstate HTTP_PORT "$PREV")}
HTTPS_PORT=${HTTPS_PORT:-$(getstate HTTPS_PORT "$PREV")}

# adguard home holding port 53, ADGUARD=off lets fengard turn it off and uninstall turns it back on
agh_on_53() {
	pids=$(pidof AdGuardHome) || return 1
	socks=$(awk '$4 == "07" && substr($2, length($2) - 4) == ":0035" { print $10 }' /proc/net/udp /proc/net/udp6 2>/dev/null)
	for p in $pids; do
		for i in $socks; do ls -l "/proc/$p/fd" 2>/dev/null | grep -qF "socket:[$i]" && return 0; done
	done
	return 1
}
AGH_INIT=$(getstate AGH_INIT "$STATE")
if agh_on_53; then
	AGH_INIT=$(grep -l AdGuardHome /etc/init.d/* 2>/dev/null | head -n 1)
	[ -n "$AGH_INIT" ] || die "AdGuard Home is using DNS port 53 and has no service to turn it off with. Stop it yourself and run the installer again."
	# the computer installers have no terminal here so they ask and rerun with ADGUARD=off
	if [ "$ADGUARD" != off ] && ( : </dev/tty ) 2>/dev/null; then
		printf '\nAdGuard Home is using DNS port 53. Turn it off so Fengard can take over?\nRemoving Fengard turns it back on. [y/N] ' >/dev/tty
		read -r a </dev/tty
		case "$a" in [yY]*) ADGUARD=off ;; esac
	fi
	if [ "$ADGUARD" != off ]; then
		printf '\nERROR: AdGuard Home is using DNS port 53. Run the installer again and let it turn AdGuard Home off (ADGUARD=off), it comes back on when Fengard is removed. Nothing was changed.\n' >&2
		exit 7
	fi
fi

DONE='' TOUCHED=''
rollback() {
	[ -z "$DONE" ] && [ -n "$TOUCHED" ] || return
	printf '\n== install did not finish; putting the router back to its stock DNS setup\n' >&2
	sh "$UNINSTALL_SRC" --rollback >&2
}
trap rollback EXIT
trap 'exit 1' INT TERM HUP

say "stopping any running Fengard"
[ -x "$INIT" ] && TOUCHED=1
[ -x "$INIT" ] && "$INIT" stop >/dev/null 2>&1
for _ in 1 2 3 4 5 6 7 8 9 10; do pidof fengardd >/dev/null 2>&1 || break; sleep 1; done
killall fengardd 2>/dev/null && sleep 1
# stopping starts the stand in dns, the install takes port 53 itself
[ -f /var/run/fengard-rescue.pid ] && kill "$(cat /var/run/fengard-rescue.pid)" 2>/dev/null
rm -f /var/run/fengard-rescue.pid

set -- $(echo "$LAN_IP" | tr . ' ')
A=$1 B=$2 C=$3 D=$4
mask_octet() {
	bits=$((LAN_PREFIX - ($1 - 1) * 8))
	if [ "$bits" -ge 8 ]; then echo 255; elif [ "$bits" -le 0 ]; then echo 0; else echo $(((255 << (8 - bits)) & 255)); fi
}
M2=$(mask_octet 2) M3=$(mask_octet 3) M4=$(mask_octet 4)
N2=$((B & M2)) N3=$((C & M3)) N4=$((D & M4))
in_lan() {
	set -- $(echo "$1" | tr . ' ')
	[ "$1" = "$A" ] && [ $(($2 & M2)) = "$N2" ] && [ $(($3 & M3)) = "$N3" ] && [ $(($4 & M4)) = "$N4" ]
}
in_use() {
	ping -c 1 -W 1 "$1" >/dev/null 2>&1 && return 0
	awk -v ip="$1" '$1 == ip && $3 == "0x2" { f = 1 } END { exit !f }' /proc/net/arp
}
pick_fg_ip() {
	if [ "$LAN_PREFIX" -ge 24 ]; then lo=$N4 hi=$((N4 + (1 << (32 - LAN_PREFIX)) - 1)); else lo=0 hi=255; fi
	start=$(uci -q get "dhcp.$LAN_DHCP.start")
	limit=$(uci -q get "dhcp.$LAN_DHCP.limit")
	[ "$(uci -q get "dhcp.$LAN_DHCP.ignore")" = 1 ] && start=0 limit=0
	start=${start:-100} limit=${limit:-150}
	statics=$(uci show dhcp 2>/dev/null | sed -n "s/^dhcp\..*\.ip='\(.*\)'\$/\1/p")
	for x in $(seq $((D + 1)) $((D + 30))) $(seq $((hi - 1)) -1 $((hi - 30))); do
		[ "$x" -gt "$lo" ] && [ "$x" -lt "$hi" ] && [ "$x" -gt 0 ] && [ "$x" -lt 255 ] && [ "$x" != "$D" ] || continue
		off=$(((B - N2) * 65536 + (C - N3) * 256 + x - N4))
		[ "$off" -ge "$start" ] && [ "$off" -lt $((start + limit)) ] && continue
		ip=$A.$B.$C.$x
		case " $(echo $statics) " in *" $ip "*) continue ;; esac
		in_use "$ip" && continue
		echo "$ip"
		return
	done
}
if [ -n "$FG_IP" ] && ! in_lan "$FG_IP"; then
	note "the LAN subnet changed since the last install; picking a new address for Fengard"
	FG_IP=
fi
if [ -z "$FG_IP" ]; then
	say "finding a free LAN address for the dashboard"
	FG_IP=$(pick_fg_ip)
	[ -n "$FG_IP" ] || die "no free address found next to $LAN_IP outside the DHCP range; set FG_IP to a free LAN address"
fi
[ "$FG_IP" != "$LAN_IP" ] || die "FG_IP must be a spare address, not the router's own"

port_used() {
	hex=$(printf '%04X' "$1")
	awk -v p=":$hex" '$4 == "0A" && substr($2, length($2) - 4) == p { f = 1 } END { exit !f }' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}
free_port() { for p in "$@"; do port_used "$p" || { echo "$p"; return; }; done; }
[ -n "$HTTP_PORT" ] && port_used "$HTTP_PORT" && HTTP_PORT=
[ -n "$HTTPS_PORT" ] && port_used "$HTTPS_PORT" && HTTPS_PORT=
HTTP_PORT=${HTTP_PORT:-$(free_port 8880 8180 9080 8008 18080)}
HTTPS_PORT=${HTTPS_PORT:-$(free_port 8843 8143 9443 4430 18443)}
[ -n "$HTTP_PORT" ] && [ -n "$HTTPS_PORT" ] || die "no free ports for the dashboard"

LEASES=$(uci -q get dhcp.@dnsmasq[0].leasefile)
LEASES=${LEASES:-/tmp/dhcp.leases}
if [ -x /sbin/fw4 ]; then FW=fw4; else FW=fw3; fi

DNSMASQ=$(uci -X show dhcp 2>/dev/null | sed -n "s/^dhcp\.\([^.=]*\)=dnsmasq\$/\1/p")
{
	echo "# Written by the Fengard installer; read by the service and the uninstaller."
	for k in FG_IP LAN_PREFIX HTTP_PORT HTTPS_PORT LAN_NET LAN_IP EXTRA_NETS WAN_NETS FG_DIR BIN LEASES MEM_MB ARCH FW; do
		eval "v=\$$k"
		printf "%s='%s'\n" "$k" "$(echo $v)"
	done
	echo "FG_PREFIX='$LAN_PREFIX'"
	echo "DNSMASQ='$(echo $DNSMASQ)'"
	for s in $DNSMASQ; do
		v=$(getstate "ORIG_PORT_$s" "$STATE")
		if [ -z "$v" ]; then
			v=$(uci -q get "dhcp.$s.port")
			[ -n "$LEGACY" ] && v=
			v=${v:-unset}
		fi
		echo "ORIG_PORT_$s='$v'"
	done
	opt_sections=
	for n in $LAN_NET $EXTRA_NETS; do
		s=$(dhcp_section_of "$n")
		[ -n "$s" ] || continue
		opt_sections="$opt_sections $s"
		if grep -q "^ORIG6_$s=" "$STATE" 2>/dev/null; then
			v=$(getstate "ORIG6_$s" "$STATE")
		else
			v=
			[ -n "$LEGACY" ] || for o in $(uci -q get "dhcp.$s.dhcp_option"); do case "$o" in 6,*) v="$v $o" ;; esac; done
		fi
		echo "ORIG6_$s='$(echo $v)'"
	done
	echo "OPT_SECTIONS='$(echo $opt_sections)'"
	v=$(getstate VPN_NET_CREATED "$STATE")
	if [ -z "$v" ]; then
		if [ -n "$LEGACY" ] || ! uci -q get network.fgwg0 >/dev/null; then v=1; else v=0; fi
	fi
	echo "VPN_NET_CREATED='$v'"
	echo "AGH_INIT='$AGH_INIT'"
} >"$STATE.new" && mv "$STATE.new" "$STATE" || die "can't write $STATE"
rm -f "$STATE.prev"
TOUCHED=1

say "installing Fengard for $ARCH"
if [ "$BIN_SRC" != "$BIN" ]; then
	cp "$BIN_SRC" "$BIN.new" && chmod 755 "$BIN.new" && mv "$BIN.new" "$BIN" || die "can't write $BIN"
fi
cp "$UNINSTALL_SRC" /etc/fengard/uninstall.sh && chmod 755 /etc/fengard/uninstall.sh
rm -f /etc/fengard/router-uninstall.sh
if [ -d "$SRC/lists" ] && [ "$FG_DIR" != "$SRC" ]; then
	mkdir -p "$FG_DIR/lists" && cp "$SRC"/lists/* "$FG_DIR/lists/" 2>/dev/null && note "copied the blocklist cache"
fi

say "service"
cat >"$INIT" <<'EOF'
#!/bin/sh /etc/rc.common
# fengard service settings come from /etc/fengard/install.env
START=95
STOP=10
USE_PROCD=1

[ -f /etc/fengard/install.env ] && . /etc/fengard/install.env

fg_status() { ubus call "network.interface.$1" status 2>/dev/null; }
fg_ip() { fg_status "$1" | jsonfilter -e '@["ipv4-address"][0].address' 2>/dev/null; }
fg_dev() {
	d=$(fg_status "$1" | jsonfilter -e '@.l3_device' 2>/dev/null)
	[ -n "$d" ] || { [ "$(uci -q get "network.$1.type")" = bridge ] && d=br-$1; }
	[ -n "$d" ] || d=$(uci -q get "network.$1.device" || uci -q get "network.$1.ifname")
	echo "${d%% *}"
}
fg_join() {
	out=
	for x in "$@"; do
		[ -n "$x" ] || continue
		case ",$out," in *",$x,"*) ;; *) out=${out:+$out,}$x ;; esac
	done
	echo "$out"
}

start_service() {
	# after a manual stop take port 53 back, network reloads leave the guard in charge
	[ -f /var/run/fengard-stopped ] && { rm -f /var/run/fengard-stopped; /bin/sh /etc/fengard/guard.sh off; }
	lan_dev=$(fg_dev "$LAN_NET")
	lan_ip=$(fg_ip "$LAN_NET")
	lan_ip=${lan_ip:-$LAN_IP}
	ip addr add "$FG_IP/$FG_PREFIX" dev "$lan_dev" 2>/dev/null
	lans=$lan_dev
	dns="$lan_ip:53,$FG_IP:53,127.0.0.1:53"
	for n in $EXTRA_NETS; do
		lans="$lans $(fg_dev "$n")"
		i=$(fg_ip "$n")
		[ -n "$i" ] && dns="$dns,$i:53"
	done
	wans=
	for n in $WAN_NETS; do wans="$wans $(fg_dev "$n")"; done
	wans=$(fg_join $wans)
	# phones use the routers ipv6 for dns too
	lan6=$(ip -6 addr show dev "$lan_dev" scope global 2>/dev/null | awk '/inet6/ { print $2 }' | cut -d/ -f1 | head -n 1)
	[ -n "$lan6" ] && dns="$dns,[$lan6]:53,[::1]:53"
	procd_open_instance
	procd_set_param command "$BIN" \
		-dns "$dns" \
		-http "$FG_IP:$HTTP_PORT" -https "$FG_IP:$HTTPS_PORT" \
		-block-ip "$FG_IP" -dns-ip "$lan_ip" \
		-lan "$(fg_join $lans)" -wan "${wans:-fg-nowan}" \
		-leases "$LEASES" -data "$FG_DIR" \
		-dashboard-hosts fengard.lan -firewall -harden -mem-limit "$MEM_MB"
	procd_set_param respawn 3600 5 0
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_set_param limits nofile="16384 16384"
	procd_close_instance
	# keeps the house online if fengard ever stops answering dns
	procd_open_instance guard
	procd_set_param command /bin/sh /etc/fengard/guard.sh
	procd_set_param respawn 3600 5 0
	procd_close_instance
}

service_triggers() {
	for n in $LAN_NET $EXTRA_NETS $WAN_NETS; do
		procd_add_reload_interface_trigger "$n"
	done
}

stop_service() {
	for d in $(ip -o -4 addr show 2>/dev/null | awk -v a="$FG_IP/" 'index($4, a) == 1 { print $2 }'); do
		ip addr del "$FG_IP/$FG_PREFIX" dev "$d" 2>/dev/null
	done
	# port 53 has to be free before the stand in dns can take it
	killall fengardd 2>/dev/null
	for _ in 1 2 3 4 5 6 7 8 9 10; do pidof fengardd >/dev/null || break; sleep 1; done
	touch /var/run/fengard-stopped
	/bin/sh /etc/fengard/guard.sh on
}
EOF
chmod 755 "$INIT"

# plain dnsmasq stands in on port 53 whenever fengard is stopped or not answering
cat >/etc/fengard/guard.sh <<'EOF'
#!/bin/sh
# guard.sh       watch fengard and stand in for it when its down
# guard.sh on    start the stand in dns now
# guard.sh off   stop it so fengard can have port 53
PID=/var/run/fengard-rescue.pid
. /etc/fengard/install.env
# seconds between checks, the router lab turns it down
T=${GUARD_TICK:-20}

answering() { nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP"; }
rescuing() { [ -f $PID ] && kill -0 "$(cat $PID)" 2>/dev/null; }
rescue_on() {
	rescuing && return
	resolv=/tmp/resolv.conf.d/resolv.conf.auto
	[ -f $resolv ] || resolv=/tmp/resolv.conf.auto
	addrs=127.0.0.1
	for n in $LAN_NET $EXTRA_NETS; do
		a=$(ubus call "network.interface.$n" status 2>/dev/null | jsonfilter -e '@["ipv4-address"][0].address' 2>/dev/null)
		[ -n "$a" ] && addrs=$addrs,$a
	done
	logger -t fengard "not answering dns, plain dnsmasq is standing in until it recovers"
	dnsmasq --port=53 --conf-file=/dev/null --no-hosts --resolv-file="$resolv" --bind-interfaces \
		--listen-address="$addrs" --cache-size=1000 --pid-file=$PID
}
rescue_off() {
	rescuing && kill "$(cat $PID)" 2>/dev/null
	rm -f $PID
	sleep 1
}

case "$1" in
on) rescue_on; exit 0 ;;
off) rescue_off; exit 0 ;;
esac

sleep $((T * 3 / 2))
bad=0 n=0
while :; do
	if rescuing; then
		# every 10 minutes hand the port back and see if fengard copes now
		n=$((n + 1))
		if [ $n -ge 30 ]; then
			n=0
			rescue_off
			killall fengardd 2>/dev/null
			for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do sleep 5; answering && break; done
			if answering; then logger -t fengard "answering dns again"; else rescue_on; fi
		fi
	elif answering; then
		bad=0
	else
		bad=$((bad + 1))
		[ $bad -ge 3 ] && { rescue_on; bad=0 n=0; }
	fi
	sleep $T
done
EOF
chmod 755 /etc/fengard/guard.sh

say "firewall ($FW): re-apply Fengard's rules whenever the router's firewall reloads"
cat >/etc/fengard/firewall.include <<'EOF'
#!/bin/sh
# firewall runs this after a reload so fengard puts its rules back
killall -USR1 fengardd 2>/dev/null
exit 0
EOF
chmod 755 /etc/fengard/firewall.include
[ -f /etc/firewall.user ] && sed -i '/Fengard: re-apply/d; /pkill -USR1 fengardd/d' /etc/firewall.user
uci -q delete firewall.fengard
uci set firewall.fengard=include
uci set firewall.fengard.type=script
uci set firewall.fengard.path=/etc/fengard/firewall.include
uci set firewall.fengard.reload=1
uci set firewall.fengard.fw4_compatible=1

say "VPN: register Fengard's WireGuard interface with the router's firewall"
if ! uci -q get network.fgwg0 >/dev/null; then
	uci set network.fgwg0=interface
	uci set network.fgwg0.proto=none
	case "$(. /etc/openwrt_release; echo "$DISTRIB_RELEASE")" in
	1[0-9].*) uci set network.fgwg0.ifname=fgwg0 ;;
	*) uci set network.fgwg0.device=fgwg0 ;;
	esac
fi
i=0
while uci -q get "firewall.@zone[$i]" >/dev/null; do
	if [ "$(uci -q get "firewall.@zone[$i].name")" = "$LAN_ZONE" ]; then
		case " $(uci -q get "firewall.@zone[$i].network") " in
		*" fgwg0 "*) ;;
		*) uci add_list "firewall.@zone[$i].network=fgwg0" ;;
		esac
		break
	fi
	i=$((i + 1))
done
uci show firewall 2>/dev/null | grep -q "\.name='Fengard-VPN'" || {
	uci add firewall rule >/dev/null
	uci set firewall.@rule[-1].name=Fengard-VPN
	uci set firewall.@rule[-1].src="$WAN_ZONE"
	uci set firewall.@rule[-1].proto=udp
	uci set firewall.@rule[-1].dest_port=51820
	uci set firewall.@rule[-1].target=ACCEPT
}
uci commit network
uci commit firewall
/etc/init.d/network reload >/dev/null 2>&1
/etc/init.d/firewall reload >/dev/null 2>&1
if ! { [ -d /sys/module/wireguard ] || modprobe wireguard 2>/dev/null; } || ! command -v wg >/dev/null; then
	note "WireGuard isn't installed, so the VPN page won't run a tunnel yet. Install the"
	note "kmod-wireguard and wireguard-tools packages to use it; everything else works without."
fi

say "keep Fengard across firmware upgrades"
touch /etc/sysupgrade.conf
for f in "$BIN" "$INIT" /etc/fengard/ /etc/rc.d/S95fengard /etc/rc.d/K10fengard; do
	grep -qxF "$f" /etc/sysupgrade.conf || echo "$f" >>/etc/sysupgrade.conf
done
[ "$FG_DIR" = /etc/fengard ] || grep -qxF "$FG_DIR/" /etc/sysupgrade.conf || echo "$FG_DIR/" >>/etc/sysupgrade.conf
# enabled and on flash before dns moves so a power cut never leaves the router without dns
"$INIT" enable
sync

say "DNS: Fengard answers on port 53, dnsmasq keeps doing DHCP"
if [ -n "$AGH_INIT" ] && pidof AdGuardHome >/dev/null; then
	"$AGH_INIT" disable
	"$AGH_INIT" stop >/dev/null 2>&1
	for _ in 1 2 3 4 5 6 7 8 9 10; do pidof AdGuardHome >/dev/null || break; sleep 1; done
	note "turned AdGuard Home off, removing Fengard turns it back on"
fi
for s in $DNSMASQ; do uci set "dhcp.$s.port=0"; done
for n in $LAN_NET $EXTRA_NETS; do
	s=$(dhcp_section_of "$n")
	ip=$(net_ip "$n")
	[ -n "$s" ] && [ -n "$ip" ] || continue
	for o in $(uci -q get "dhcp.$s.dhcp_option"); do
		case "$o" in 6,*) uci del_list "dhcp.$s.dhcp_option=$o" ;; esac
	done
	uci add_list "dhcp.$s.dhcp_option=6,$ip"
done
uci commit dhcp
/etc/init.d/dnsmasq restart >/dev/null 2>&1
sleep 1
if awk '$4 == "07" && substr($2, length($2) - 4) == ":0035" { f = 1 } END { exit !f }' /proc/net/udp /proc/net/udp6 2>/dev/null; then
	die "another program still uses DNS port 53 (AdGuard Home, unbound or similar). Turn it off in the router's settings and run the installer again."
fi

say "starting"
"$INIT" start
ok=
for _ in $(seq 1 45); do
	sleep 1
	pidof fengardd >/dev/null 2>&1 || continue
	nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP" && { ok=1; break; }
done
if [ -z "$ok" ]; then
	echo "Fengard didn't start answering DNS. Recent log:" >&2
	logread -e fengardd 2>/dev/null | tail -n 20 >&2
	exit 1
fi
DONE=1
rm -rf "$WORK"

if pidof AdGuardHome >/dev/null 2>&1; then
	note "AdGuard Home is running but no longer used for DNS; you can turn it off in the router's settings."
fi
echo
echo "Fengard is running."
echo "  Dashboard   http://$FG_IP/  or  http://fengard.lan/"
echo "  DNS         $LAN_IP (devices need no changes)"
echo "  Uninstall   sh /etc/fengard/uninstall.sh"
