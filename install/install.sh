#!/bin/sh
# fengard setup for mac and linux run sh install.sh for the menu
# or sh install.sh router computer remove-router remove-computer
# SSH_PORT=2222 if the router ssh isnt on 22
# ADGUARD=off lets the router install turn AdGuard Home off when it holds port 53

HERE=$(cd "$(dirname "$0")" && pwd)
if [ -f "$HERE/install/linux-bundle.tar.gz" ]; then KIT=$HERE; else KIT=$(dirname "$HERE"); fi
BUNDLE=$KIT/install/linux-bundle.tar.gz
SSH_PORT=${SSH_PORT:-22}

say() { printf '== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf '\nERROR: %s\n' "$*" >&2; exit 1; }
ask() { printf '%s' "$1" >&2; read -r REPLY </dev/tty || REPLY=; }

need_bundle() {
	[ -f "$BUNDLE" ] || die "the Fengard programs aren't next to this script ($BUNDLE is missing). Use the downloaded Fengard kit, or build it with: go run ./tools/release -version dev"
}

router_address() {
	[ -n "$1" ] && { echo "$1"; return; }
	if [ "$(uname -s)" = Darwin ]; then
		gw=$(route -n get default 2>/dev/null | awk '/gateway:/ { print $2 }')
	else
		gw=$(ip -4 route show default 2>/dev/null | awk '{ print $3; exit }')
	fi
	if [ -n "$gw" ]; then
		ask "Router address [$gw]: "
		echo "${REPLY:-$gw}"
	else
		ask "Router address (for example 192.168.1.1): "
		[ -n "$REPLY" ] || die "no router address given"
		echo "$REPLY"
	fi
}

run_ssh() {
	command -v ssh >/dev/null || die "ssh isn't installed"
	mkdir -p "$HOME/.ssh"
	set -- "$1" "$2" "${3:-/dev/null}"
	ssh -p "$SSH_PORT" -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
		-o UserKnownHostsFile="$HOME/.ssh/fengard_known_hosts" -o HostKeyAlgorithms=+ssh-rsa \
		"root@$1" "$2" <"$3"
}

run_ssh_retry() {
	run_ssh "$@"
	code=$?
	[ "$code" = 255 ] || return "$code"
	echo
	echo "Could not log in to the router. Check the password (on most routers it is the admin page password)."
	echo "If the router was reset or replaced, it has a new identity and SSH refuses it until the old one is forgotten."
	ask "Forget the router's old identity and try again? [y/N] "
	case "$REPLY" in [yY]*) ;; *) return "$code" ;; esac
	if [ "$SSH_PORT" = 22 ]; then key=$1; else key="[$1]:$SSH_PORT"; fi
	ssh-keygen -R "$key" -f "$HOME/.ssh/fengard_known_hosts" >/dev/null 2>&1
	run_ssh "$@"
}

install_router() {
	need_bundle
	addr=$(router_address "$1")
	say "Installing Fengard on the router at $addr"
	note "Enter the router's root password when asked (on most routers it is the admin page password)."
	note "The upload carries Fengard for every router CPU; only the one this router needs is unpacked."
	# router pulls out its own cpu build while it streams in
	# keep in sync with fengard-setup.ps1
	# shellcheck disable=SC2016
	remote='rm -rf /tmp/fengard-install; mkdir -p /tmp/fengard-install && cd /tmp/fengard-install || exit 1; '
	remote=$remote'if [ ! -f /etc/openwrt_release ]; then echo; echo This router does not run OpenWrt-based firmware, which Fengard needs.; cat >/dev/null; exit 3; fi; '
	remote=$remote'a=$(. /etc/openwrt_release; echo $DISTRIB_ARCH); case $a in aarch64*) t=arm64;; arm*) t=arm;; x86_64*) t=amd64;; i?86*) t=386;; '
	remote=$remote'mips64el*) t=mips64le;; mips64*) t=mips64;; mipsel*) t=mipsle;; mips*) t=mips;; riscv64*) t=riscv64;; loongarch64*) t=loong64;; '
	remote=$remote'*) echo Unsupported router CPU: $a; cat >/dev/null; exit 4;; esac; '
	remote=$remote'tar -xzf - router-install.sh router-uninstall.sh bin/linux-$t/fengardd || { echo The upload failed: not enough free memory on the router?; exit 5; }; '
	[ "$ADGUARD" = off ] && remote=$remote'ADGUARD=off '
	remote=$remote'sh router-install.sh'
	run_ssh_retry "$addr" "$remote" "$BUNDLE"
	code=$?
	echo
	# 7 means adguard home has port 53, ask here since the router has no terminal
	if [ "$code" = 7 ] && [ "$ADGUARD" != off ]; then
		ask "Turn AdGuard Home off so Fengard can take over port 53? Removing Fengard turns it back on. [y/N] "
		case "$REPLY" in [yY]*)
			ADGUARD=off
			install_router "$addr"
			return ;;
		esac
	fi
	case $code in
	0) say "Done. Open the dashboard address shown above and create the admin account." ;;
	255) die "could not connect to the router over SSH" ;;
	7) die "AdGuard Home is still using port 53, so nothing was changed" ;;
	*) die "the router install stopped (code $code); see the messages above. The router's own DNS setup was left working." ;;
	esac
}

remove_router() {
	addr=$(router_address "$1")
	say "Removing Fengard from the router at $addr"
	remote="if [ -f /etc/fengard/uninstall.sh ]; then sh /etc/fengard/uninstall.sh $PURGE; elif [ -f /etc/fengard/router-uninstall.sh ]; then sh /etc/fengard/router-uninstall.sh $PURGE; else echo Fengard is not installed on this router.; fi"
	run_ssh_retry "$addr" "$remote" || die "removing Fengard failed"
}

OS=$(uname -s)
if [ "$OS" = Darwin ]; then
	BIN=/usr/local/fengard/fengardd
	DATA="/Library/Application Support/Fengard"
	PLIST=/Library/LaunchDaemons/com.fengard.fengardd.plist
else
	BIN=/usr/local/bin/fengardd
	DATA=/var/lib/fengard
	UNIT=/etc/systemd/system/fengard.service
fi
ENVF=$DATA/install.env

as_root() {
	[ "$(id -u)" = 0 ] && return
	command -v sudo >/dev/null || die "run this as root"
	say "Asking for administrator rights (sudo)"
	exec sudo SSH_PORT="$SSH_PORT" sh "$0" "$@"
}

local_address() {
	if [ "$OS" = Darwin ]; then
		IFACE=$(route -n get default 2>/dev/null | awk '/interface:/ { print $2 }')
		IP=$(ipconfig getifaddr "$IFACE" 2>/dev/null)
	else
		r=$(ip -4 route get 192.0.2.1 2>/dev/null)
		IP=$(echo "$r" | sed -n 's/.* src \([0-9.]*\).*/\1/p')
		IFACE=$(echo "$r" | sed -n 's/.* dev \([^ ]*\).*/\1/p')
	fi
	[ -n "$IP" ] || die "this computer isn't connected to a network"
}

port_user() { # prints whoever already listens on that port
	if [ "$OS" = Darwin ]; then
		if [ "$1" = udp ]; then sel="-iUDP:$2"; else sel="-iTCP:$2 -sTCP:LISTEN"; fi
		# shellcheck disable=SC2086
		lsof -nP $sel 2>/dev/null | awk -v ip="$IP" 'NR > 1 && $1 != "fengardd" {
			n = $9; sub(/:[0-9]+$/, "", n)
			if (n == "*" || n == ip || n == "127.0.0.1" || n == "[::]") { print $1 " (process " $2 ")"; exit } }'
	else
		if [ "$1" = udp ]; then f=-Hlnup; else f=-Hlntp; fi
		ss "$f" "sport = :$2" 2>/dev/null | awk -v ip="$IP" '$0 !~ /"fengardd"/ {
			n = $4; sub(/:[0-9]+$/, "", n); sub(/%.*/, "", n)
			if (n == "0.0.0.0" || n == "*" || n == "[::]" || n == ip || n == "127.0.0.1") {
				p = $0; sub(/.*users:\(\("/, "", p); sub(/".*/, "", p); print (p == $0 ? "another program" : p); exit } }'
	fi
}

stop_computer() {
	if [ "$OS" = Darwin ]; then
		launchctl bootout system/com.fengard.fengardd >/dev/null 2>&1
	elif [ -f "$UNIT" ]; then
		systemctl stop fengard >/dev/null 2>&1
	fi
	pkill -x fengardd 2>/dev/null && sleep 1
}

install_computer() {
	as_root computer
	need_bundle
	local_address
	case "$OS-$(uname -m)" in
	Darwin-arm64) src="$KIT/bin/darwin-arm64/fengardd" ;;
	Darwin-x86_64) src="$KIT/bin/darwin-amd64/fengardd" ;;
	Linux-x86_64) t=amd64 ;;
	Linux-aarch64 | Linux-arm64) t=arm64 ;;
	Linux-arm*) t=arm ;;
	Linux-i?86) t=386 ;;
	Linux-riscv64) t=riscv64 ;;
	Linux-loongarch64) t=loong64 ;;
	*) die "this computer ($OS $(uname -m)) isn't supported" ;;
	esac
	[ "$OS" = Darwin ] || [ "$OS" = Linux ] || die "this system ($OS) isn't supported"
	[ "$OS" = Linux ] && [ ! -d /run/systemd/system ] &&
		die "this Linux doesn't use systemd, so Fengard can't be set up as a service automatically. Run it by hand: fengardd -dns $IP:53,127.0.0.1:53 -http $IP:80 -https $IP:443 -block-ip $IP -data $DATA -leases ''"

	say "Stopping any running Fengard"
	stop_computer

	say "Checking ports"
	u=$(port_user udp 53)
	[ -z "$u" ] || die "another program already answers DNS on this computer: $u. Turn it off and run this again."
	HTTP_PORT=80 HTTPS_PORT=443
	u=$(port_user tcp 80)
	[ -z "$u" ] || { HTTP_PORT=8080; warn "port 80 is used by $u, so the dashboard moves to port 8080 and blocked sites show a browser error instead of the block page."; }
	u=$(port_user tcp 443)
	[ -z "$u" ] || { HTTPS_PORT=8443; warn "port 443 is used by $u; HTTPS moves to port 8443."; }

	say "Installing to $BIN"
	mkdir -p "$(dirname "$BIN")" "$DATA" || die "can't create folders"
	if [ "$OS" = Darwin ]; then
		[ -f "$src" ] || die "the Fengard program for this Mac isn't in the kit ($src)"
		cp "$src" "$BIN.new"
	else
		tar -xzf "$BUNDLE" -O "bin/linux-$t/fengardd" >"$BIN.new" 2>/dev/null && [ -s "$BIN.new" ] ||
			die "the Fengard program for this computer (linux-$t) isn't in the kit"
	fi
	chmod 755 "$BIN.new" && mv "$BIN.new" "$BIN"
	{
		echo "IP='$IP'"
		echo "IFACE='$IFACE'"
		echo "HTTP_PORT='$HTTP_PORT'"
		echo "HTTPS_PORT='$HTTPS_PORT'"
	} >"$ENVF"

	if [ "$OS" = Darwin ]; then
		xattr -d com.apple.quarantine "$BIN" 2>/dev/null
		say "Starting Fengard now and at every boot (launchd)"
		cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.fengard.fengardd</string>
	<key>ProgramArguments</key>
	<array>
		<string>$BIN</string>
		<string>-dns</string><string>$IP:53,127.0.0.1:53</string>
		<string>-http</string><string>$IP:$HTTP_PORT</string>
		<string>-https</string><string>$IP:$HTTPS_PORT</string>
		<string>-block-ip</string><string>$IP</string>
		<string>-data</string><string>$DATA</string>
		<string>-leases</string><string></string>
	</array>
	<key>WorkingDirectory</key><string>$DATA</string>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardErrorPath</key><string>/Library/Logs/Fengard.log</string>
</dict>
</plist>
EOF
		chmod 644 "$PLIST"
		fw=/usr/libexec/ApplicationFirewall/socketfilterfw
		if [ -x "$fw" ] && "$fw" --getglobalstate 2>/dev/null | grep -q enabled; then
			say "Allowing devices on the network to reach Fengard (macOS firewall)"
			"$fw" --add "$BIN" >/dev/null 2>&1
			"$fw" --unblockapp "$BIN" >/dev/null 2>&1
		fi
		launchctl bootstrap system "$PLIST" || die "launchd refused to start Fengard"
	else
		say "Starting Fengard now and at every boot (systemd)"
		cat >"$UNIT" <<EOF
[Unit]
Description=Fengard network protection (DNS-only mode)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN -dns $IP:53,127.0.0.1:53 -http $IP:$HTTP_PORT -https $IP:$HTTPS_PORT -block-ip $IP -data $DATA -leases=
WorkingDirectory=$DATA
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
		systemctl daemon-reload
		systemctl enable --now fengard >/dev/null 2>&1 || die "systemd couldn't start Fengard: $(systemctl status fengard --no-pager 2>&1 | tail -n 5)"
		if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q 'Status: active'; then
			say "Allowing devices on the network to reach Fengard (ufw)"
			ufw allow in on "$IFACE" to any port 53 comment Fengard >/dev/null
			ufw allow in on "$IFACE" to any port "$HTTP_PORT" proto tcp comment Fengard >/dev/null
			ufw allow in on "$IFACE" to any port "$HTTPS_PORT" proto tcp comment Fengard >/dev/null
			echo "FW='ufw'" >>"$ENVF"
		elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
			say "Allowing devices on the network to reach Fengard (firewalld)"
			added=
			for s in dns http https; do
				firewall-cmd --query-service="$s" >/dev/null 2>&1 && continue
				firewall-cmd --permanent --add-service="$s" >/dev/null && added="$added $s"
			done
			[ "$HTTP_PORT" = 80 ] || { firewall-cmd --permanent --add-port="$HTTP_PORT/tcp" >/dev/null && added="$added $HTTP_PORT/tcp"; }
			[ "$HTTPS_PORT" = 443 ] || { firewall-cmd --permanent --add-port="$HTTPS_PORT/tcp" >/dev/null && added="$added $HTTPS_PORT/tcp"; }
			firewall-cmd --reload >/dev/null
			echo "FW='firewalld'" >>"$ENVF"
			echo "FW_ADDED='$added'" >>"$ENVF"
		fi
	fi

	ok=
	for _ in $(seq 1 40); do
		sleep 1
		if command -v dig >/dev/null; then
			dig +short +time=1 +tries=1 @127.0.0.1 fengard.lan 2>/dev/null | grep -qx "$IP" && { ok=1; break; }
		elif [ "$OS" = Linux ]; then
			ss -Hlnu "sport = :53" 2>/dev/null | grep -q "$IP:53" && { ok=1; break; }
		else
			lsof -nP -iUDP:53 2>/dev/null | grep -q "fengardd" && { ok=1; break; }
		fi
	done
	if [ -z "$ok" ]; then
		if [ "$OS" = Darwin ]; then tail -n 20 /Library/Logs/Fengard.log >&2; else journalctl -u fengard -n 20 --no-pager >&2; fi
		die "Fengard didn't start answering DNS"
	fi

	if [ "$HTTP_PORT" = 80 ]; then dash="http://$IP/"; else dash="http://$IP:$HTTP_PORT/"; fi
	echo
	say "Fengard is running on this computer."
	note "Dashboard   $dash  (create the admin account there)"
	note "DNS server  $IP"
	echo
	echo "One step left, in your router's admin page:"
	note "1. Set the DNS server it gives out by DHCP (often under LAN or DHCP settings) to $IP."
	note "   Give only that one address, with no second DNS server, or devices can go around Fengard."
	note "2. Reserve this computer's address so it never changes (DHCP reservation / static lease)."
	note "3. Reconnect your devices (or wait for them to renew) so they pick up the new DNS server."
	echo
	note "Keep this computer on and awake: while it sleeps, devices using it lose internet name lookups."
	note "DNS-only mode filters every device, but it has no firewall: port forwards, bypass blocking"
	note "and the VPN need Fengard installed on the router itself."
}

remove_computer() {
	as_root remove-computer $PURGE
	say "Removing Fengard from this computer"
	stop_computer
	FW= FW_ADDED= IFACE= HTTP_PORT= HTTPS_PORT=
	# shellcheck disable=SC1090
	[ -f "$ENVF" ] && . "$ENVF"
	if [ "$OS" = Darwin ]; then
		/usr/libexec/ApplicationFirewall/socketfilterfw --remove "$BIN" >/dev/null 2>&1
		rm -f "$PLIST"
		rm -rf "$(dirname "$BIN")"
	else
		systemctl disable fengard >/dev/null 2>&1
		rm -f "$UNIT"
		systemctl daemon-reload 2>/dev/null
		case "$FW" in
		ufw)
			ufw delete allow in on "$IFACE" to any port 53 >/dev/null 2>&1
			ufw delete allow in on "$IFACE" to any port "$HTTP_PORT" proto tcp >/dev/null 2>&1
			ufw delete allow in on "$IFACE" to any port "$HTTPS_PORT" proto tcp >/dev/null 2>&1 ;;
		firewalld)
			for s in $FW_ADDED; do
				case "$s" in */tcp) firewall-cmd --permanent --remove-port="$s" ;; *) firewall-cmd --permanent --remove-service="$s" ;; esac >/dev/null 2>&1
			done
			firewall-cmd --reload >/dev/null 2>&1 ;;
		esac
		rm -f "$BIN"
	fi
	if [ -n "$PURGE" ]; then
		rm -rf "$DATA"
		note "removed Fengard's settings and history"
	else
		rm -f "$ENVF"
		[ -d "$DATA" ] && note "kept settings and history in $DATA (add --purge to remove them)"
	fi
	note "Remember to set your router's DHCP DNS server back to automatic."
}

PURGE=
ARGS=
for a in "$@"; do
	case "$a" in --purge) PURGE=--purge ;; *) ARGS="$ARGS $a" ;; esac
done
# shellcheck disable=SC2086
set -- $ARGS
action=$1
if [ -z "$action" ]; then
	echo
	echo "Fengard setup"
	echo
	echo "  1  Install on my router        OpenWrt-based routers, including GL.iNet (recommended)"
	echo "  2  Run on this computer        protects the whole network through DNS; this computer stays on"
	echo "  3  Remove from my router"
	echo "  4  Remove from this computer"
	echo
	ask "Choose 1-4: "
	case "$REPLY" in 1) action=router ;; 2) action=computer ;; 3) action=remove-router ;; 4) action=remove-computer ;; *) die "nothing chosen" ;; esac
fi
case "$action" in
router) install_router "$2" ;;
remove-router) remove_router "$2" ;;
computer) install_computer ;;
remove-computer) remove_computer ;;
*) die "unknown action '$action' (router, computer, remove-router, remove-computer)" ;;
esac
