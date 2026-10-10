#!/bin/sh
# removes fengard from the router and puts dns back to dnsmasq
# sh /etc/fengard/uninstall.sh and add --purge to wipe /etc/fengard too

STATE=/etc/fengard/install.env
INIT=/etc/init.d/fengard
PURGE='' ROLLBACK=''
for a in "$@"; do
	case "$a" in
	--purge) PURGE=1 ;;
	--rollback) ROLLBACK=1 ;;
	esac
done
getstate() { [ -f "$STATE" ] && sed -n "s/^$1='\(.*\)'\$/\1/p" "$STATE" | tail -n 1; }

LEGACY=
[ -f "$STATE" ] || LEGACY=1
FG_IP=$(getstate FG_IP)
BIN=$(getstate BIN)
FG_DIR=$(getstate FG_DIR)
if [ -z "$FG_IP" ] && [ -f "$INIT" ]; then
	FG_IP=$(sed -n 's/.*ip addr add \([0-9.]*\)\/.*/\1/p' "$INIT" | head -n 1)
fi
BIN=${BIN:-/usr/bin/fengardd}
FG_DIR=${FG_DIR:-/etc/fengard}

echo "== stopping Fengard"
[ -x "$INIT" ] && { "$INIT" stop; "$INIT" disable; } >/dev/null 2>&1
killall fengardd 2>/dev/null && sleep 1
# the stand in dns from stopping the service, dnsmasq gets port 53 back below
[ -f /var/run/fengard-rescue.pid ] && kill "$(cat /var/run/fengard-rescue.pid)" 2>/dev/null
rm -f /var/run/fengard-rescue.pid /var/run/fengard-stopped
if [ -n "$FG_IP" ]; then
	ip -o -4 addr show 2>/dev/null | awk -v a="$FG_IP/" 'index($4, a) == 1 { print $2, $4 }' |
		while read -r dev cidr; do ip addr del "$cidr" dev "$dev" 2>/dev/null; done
fi

echo "== firewall rules"
# fengardd cleans up on stop this is for when it crashed
command -v nft >/dev/null 2>&1 && nft delete table inet fengard 2>/dev/null
for ipt in iptables ip6tables; do
	command -v "$ipt" >/dev/null 2>&1 || continue
	for t in raw mangle nat filter; do
		rules=$("$ipt" -t "$t" -S 2>/dev/null) || continue
		echo "$rules" | grep -e '-j FENGARD_' | grep -v '^-A FENGARD_' | sed 's/^-A /-D /' |
			while read -r r; do eval "$ipt -t $t $r" 2>/dev/null; done
		chains=$(echo "$rules" | sed -n 's/^-N \(FENGARD_[A-Za-z0-9_]*\)$/\1/p')
		for c in $chains; do "$ipt" -t "$t" -F "$c" 2>/dev/null; done
		for c in $chains; do "$ipt" -t "$t" -X "$c" 2>/dev/null; done
	done
done
if command -v ipset >/dev/null 2>&1; then
	for s in $(ipset list -n 2>/dev/null | grep '^fengard_'); do ipset destroy "$s" 2>/dev/null; done
fi
uci -q delete firewall.fengard
[ -f /etc/firewall.user ] && sed -i '/Fengard: re-apply/d; /pkill -USR1 fengardd/d' /etc/firewall.user

echo "== VPN interface"
ip link del fgwg0 2>/dev/null
if [ "$(getstate VPN_NET_CREATED)" != 0 ]; then
	uci -q delete network.fgwg0
	i=0
	while uci -q get "firewall.@zone[$i]" >/dev/null; do
		uci -q del_list "firewall.@zone[$i].network=fgwg0"
		i=$((i + 1))
	done
fi
while sec=$(uci show firewall 2>/dev/null | sed -n "s/^firewall\.\([^.]*\)\.name='Fengard-VPN'\$/\1/p" | head -n 1) && [ -n "$sec" ]; do
	uci delete "firewall.$sec" || break
done
uci commit network
uci commit firewall

echo "== DNS back to dnsmasq"
dnsmasq_sections() { uci -X show dhcp 2>/dev/null | sed -n "s/^dhcp\.\([^.=]*\)=dnsmasq\$/\1/p"; }
for s in $(dnsmasq_sections); do
	orig=$(getstate "ORIG_PORT_$s")
	if [ -z "$orig" ] || [ "$orig" = unset ]; then
		uci -q delete "dhcp.$s.port"
	else
		uci set "dhcp.$s.port=$orig"
	fi
done
if [ -n "$LEGACY" ]; then
	opt_sections="lan guest"
else
	opt_sections=$(getstate OPT_SECTIONS)
fi
for s in $opt_sections; do
	uci -q get "dhcp.$s" >/dev/null || continue
	for o in $(uci -q get "dhcp.$s.dhcp_option"); do
		case "$o" in 6,*) uci del_list "dhcp.$s.dhcp_option=$o" ;; esac
	done
	for o in $(getstate "ORIG6_$s"); do uci add_list "dhcp.$s.dhcp_option=$o"; done
done
uci commit dhcp
/etc/init.d/dnsmasq restart >/dev/null 2>&1
/etc/init.d/network reload >/dev/null 2>&1
/etc/init.d/firewall reload >/dev/null 2>&1

echo "== files"
if [ -f /etc/sysupgrade.conf ]; then
	for f in "$BIN" "$INIT" /etc/fengard/ /etc/rc.d/S95fengard /etc/rc.d/K10fengard "$FG_DIR/"; do
		grep -vxF "$f" /etc/sysupgrade.conf >/tmp/fengard-sysupgrade.conf
		cat /tmp/fengard-sysupgrade.conf >/etc/sysupgrade.conf
	done
	rm -f /tmp/fengard-sysupgrade.conf
fi
rm -f "$INIT" "$BIN" /etc/fengard/firewall.include
if [ -n "$PURGE" ]; then
	rm -rf /etc/fengard "$FG_DIR"
	echo "   removed Fengard's settings and history"
else
	# keep ip and ports so a reinstall reuses them and the cert stays trusted
	[ -f "$STATE" ] && mv "$STATE" "$STATE.prev"
	[ -n "$ROLLBACK" ] || echo "   kept settings and history in $FG_DIR (run with --purge to remove them)"
fi
if [ -n "$ROLLBACK" ]; then
	echo "The router is back to its own DNS setup; nothing else was changed."
else
	echo "Fengard removed. The router is back to its stock setup."
fi
