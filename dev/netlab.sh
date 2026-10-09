#!/usr/bin/env bash
# fake home network in netns run as root with sudo dev/netlab.sh works in wsl2 too
# fg-wan 10.99.0.1 is the internet and fg-router runs fengardd on 192.168.8.1
# needs iproute2 nftables dnsmasq python3 curl
# FW_BACKEND=iptables to test the iptables backend
set -u
cd "${FENGARD_ROOT:-$(dirname "$0")/..}"
BIN=bin/linux
FW_BACKEND=${FW_BACKEND:-nftables}
LAB=$(mktemp -d /tmp/fglab.XXXX)
PASS=0; FAIL=0
CLIENT_MAC=02:fe:00:00:00:20
CLIENT2_MAC=02:fe:00:00:00:21

ok()   { echo "  PASS  $1"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL  $1"; FAIL=$((FAIL+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
inns() { local ns=$1; shift; ip netns exec "$ns" "$@"; }

cleanup() {
  [ -n "${FG_PID:-}" ] && kill "$FG_PID" 2>/dev/null && wait "$FG_PID" 2>/dev/null
  for p in ${BG_PIDS:-}; do kill "$p" 2>/dev/null; done
  for ns in fg-wan fg-router fg-client fg-client2; do ip netns del "$ns" 2>/dev/null; done
  rm -rf /etc/netns/fg-client /etc/netns/fg-client2 "$LAB"
}
trap cleanup EXIT
cleanup 2>/dev/null; LAB=$(mktemp -d /tmp/fglab.XXXX)

echo "== building topology"
for ns in fg-wan fg-router fg-client fg-client2; do ip netns add $ns; inns $ns ip link set lo up; done
# temp names first since the host might already have eth0 or wan
ip link add fglab-w0 type veth peer name fglab-w1
ip link set fglab-w0 netns fg-router; ip link set fglab-w1 netns fg-wan
ip link add fglab-l0 type veth peer name fglab-l1
ip link set fglab-l0 netns fg-router; ip link set fglab-l1 netns fg-client
ip link add fglab-m0 type veth peer name fglab-m1
ip link set fglab-m0 netns fg-router; ip link set fglab-m1 netns fg-client2
inns fg-router ip link set fglab-w0 name wan
inns fg-wan    ip link set fglab-w1 name wan-peer
inns fg-client ip link set fglab-l1 name eth0
inns fg-client2 ip link set fglab-m1 name eth0
# lan is a bridge like a real router
inns fg-router ip link add br-lan type bridge
for port in fglab-l0 fglab-m0; do inns fg-router ip link set $port master br-lan; inns fg-router ip link set $port up; done
inns fg-client ip link set eth0 address $CLIENT_MAC

inns fg-wan    ip addr add 10.99.0.1/24 dev wan-peer;  inns fg-wan ip link set wan-peer up
inns fg-router ip addr add 10.99.0.2/24 dev wan;       inns fg-router ip link set wan up
inns fg-router ip addr add 192.168.8.1/24 dev br-lan;  inns fg-router ip link set br-lan up
inns fg-router ip route add default via 10.99.0.1
inns fg-router sysctl -qw net.ipv4.ip_forward=1
inns fg-client ip addr add 192.168.8.20/24 dev eth0;   inns fg-client ip link set eth0 up
inns fg-client ip route add default via 192.168.8.1
inns fg-client2 ip link set eth0 address $CLIENT2_MAC
inns fg-client2 ip addr add 192.168.8.21/24 dev eth0;  inns fg-client2 ip link set eth0 up
inns fg-client2 ip route add default via 192.168.8.1
for c in fg-client fg-client2; do mkdir -p /etc/netns/$c && echo "nameserver 192.168.8.1" > /etc/netns/$c/resolv.conf; done

# stand in for fw4 just nat
inns fg-router nft -f - <<'EOF'
table ip labnat {
  chain post { type nat hook postrouting priority srcnat; oifname "wan" masquerade; }
}
EOF

echo "== starting the internet"
inns fg-wan dnsmasq --no-daemon --conf-file=/dev/null --no-resolv --no-hosts --bind-interfaces \
  --listen-address=10.99.0.1 --address=/#/10.99.0.1 --log-facility=/dev/null &
BG_PIDS="$!"
mkdir -p "$LAB/www" && echo "INTERNET OK" > "$LAB/www/index.html"
for port in 80 853; do  # 853 is fake dot
  inns fg-wan python3 -m http.server $port --bind 10.99.0.1 --directory "$LAB/www" >/dev/null 2>&1 &
  BG_PIDS="$BG_PIDS $!"
done

echo "== starting fengard on the router"
mkdir -p "$LAB/data/lists"
cat > "$LAB/leases" <<EOF
0 $CLIENT_MAC 192.168.8.20 lab-client *
0 $CLIENT2_MAC 192.168.8.21 lab-client2 *
EOF
inns fg-router $BIN/fengardd -dns 0.0.0.0:53 -http 0.0.0.0:80 -https 0.0.0.0:443 \
  -block-ip 192.168.8.1 -lan br-lan -wan wan -firewall -firewall-backend "$FW_BACKEND" -data "$LAB/data" -leases "$LAB/leases" \
  -no-list-updates > "$LAB/fengard.log" 2>&1 &
FG_PID=$!
for _ in $(seq 50); do inns fg-client curl -s -o /dev/null http://192.168.8.1/api/session && break; sleep 0.1; done
if ! kill -0 $FG_PID 2>/dev/null || ! inns fg-client curl -s -o /dev/null http://192.168.8.1/api/session; then
  echo "fengard failed to start:"; cat "$LAB/fengard.log"; exit 1
fi

API=http://192.168.8.1
JAR="$LAB/jar"
api() { inns fg-client curl -s -b "$JAR" -c "$JAR" -H 'X-Fengard: 1' -H 'Content-Type: application/json' "$@"; }
api -X POST $API/api/setup -d '{"username":"lab","password":"lab password 123"}' >/dev/null
api -X PUT $API/api/settings -d '{"upstreams":["10.99.0.1:53"],"defaultGroup":"default","quarantineNew":false,"blockBypass":true,"logRetentionDays":7,"clientRateQps":100,"timezone":"UTC"}' >/dev/null
sleep 1
dnsq() { inns "$1" $BIN/dnsq "$2" "$3" | awk '{print $3}'; }

echo "== tests"
check "client resolves through Fengard"            '[ "$(dnsq fg-client 192.168.8.1:53 example.com)" = 10.99.0.1 ]'
check "client reaches the internet through router"  'inns fg-client curl -s -m 3 http://10.99.0.1/ | grep -q "INTERNET OK"'

api -X POST $API/api/rules -d '{"kind":"block","domain":"blocked.test"}' >/dev/null
check "blocked domain points at the block page"     '[ "$(dnsq fg-client 192.168.8.1:53 www.blocked.test)" = 192.168.8.1 ]'
check "browsing a blocked site shows the block page" 'inns fg-client curl -s -m 3 http://www.blocked.test/ | grep -q "This site is blocked"'
check "DNS hijack: asking an outside DNS server still gets filtered" \
  '[ "$(dnsq fg-client 10.99.0.1:53 www.blocked.test)" = 192.168.8.1 ]'
check "control: the router itself can reach port 853" 'inns fg-router curl -s -m 2 http://10.99.0.1:853/ | grep -q "INTERNET OK"'
check "DNS-over-TLS (port 853) from devices is refused" '! inns fg-client curl -s -m 2 -o /dev/null http://10.99.0.1:853/'

api -X POST "$API/api/devices/$CLIENT_MAC/pause" -d '{"minutes":30}' >/dev/null
sleep 1
check "paused device is cut off at the firewall (even by IP)" '! inns fg-client curl -s -m 2 -o /dev/null http://10.99.0.1/'
check "paused device can still open the dashboard"   'inns fg-client curl -s -m 2 -o /dev/null http://192.168.8.1/'
api -X POST "$API/api/devices/$CLIENT_MAC/pause" -d '{"minutes":0}' >/dev/null
sleep 1
check "resumed device is back online"                'inns fg-client curl -s -m 3 http://10.99.0.1/ | grep -q "INTERNET OK"'

mkdir -p "$LAB/game" && echo "GAME SERVER" > "$LAB/game/index.html"
inns fg-client python3 -m http.server 8000 --bind 192.168.8.20 --directory "$LAB/game" >/dev/null 2>&1 &
BG_PIDS="$BG_PIDS $!"
api -X POST $API/api/portforwards -d '{"name":"Game","proto":"tcp","extPort":8080,"destIp":"192.168.8.20","destPort":8000,"enabled":true}' >/dev/null
sleep 1
check "port forward: internet reaches the device"   'inns fg-wan curl -s -m 3 http://10.99.0.2:8080/ | grep -q "GAME SERVER"'
check "dashboard is not reachable from the internet" '! inns fg-wan curl -s -m 2 -o /dev/null http://10.99.0.2/'
check "DNS is not answered for the internet"         '! inns fg-wan $BIN/dnsq 10.99.0.2:53 example.com >/dev/null 2>&1'

echo "== flood: lab-client floods DNS as fast as it can; lab-client2 browses normally"
inns fg-client $BIN/dnsflood -server 192.168.8.1:53 -attacker 192.168.8.20 -victim "" -zone blocked.test -seconds 8 -workers 16 > "$LAB/flood.txt" &
FLOOD=$!
sleep 1
inns fg-client2 $BIN/dnsflood -server 192.168.8.1:53 -victim 192.168.8.21 -workers 0 -normal www.blocked.test -seconds 6 > "$LAB/probe.txt"
wait $FLOOD
sed 's/^/  /' "$LAB/flood.txt"; grep '^normal' "$LAB/probe.txt" | sed 's/^/  /'
if [ "$FW_BACKEND" = iptables ]; then
  DROPPED=$(inns fg-router iptables -S FENGARD_INPUT -v | grep fg_dns | grep -o -- '-c [0-9]*' | awk '{print $2}')
else
  DROPPED=$(inns fg-router nft list chain inet fengard input | grep '@dns4' | grep -o 'counter packets [0-9]*' | awk '{print $3}')
fi
echo "  dropped in the kernel before reaching Fengard: ${DROPPED:-0} packets"
check "other devices keep working during a DNS flood" 'grep -q ", 0 failed" "$LAB/probe.txt"'
check "flood is stopped in the kernel"                '[ "${DROPPED:-0}" -gt 1000 ]'
check "Fengard survived the flood"                     'kill -0 $FG_PID && [ "$(dnsq fg-client2 192.168.8.1:53 example.com)" = 10.99.0.1 ]'

echo "== resources"
RSS=$(awk '/VmRSS/{print $2}' /proc/$FG_PID/status)
echo "  fengardd memory (RSS): $((RSS/1024)) MB"

kill $FG_PID; wait $FG_PID 2>/dev/null; FG_PID=
if [ "$FW_BACKEND" = iptables ]; then
  check "stopping fengard removes its firewall chains" '! inns fg-router iptables -S | grep -q FENGARD'
else
  check "stopping fengard removes its firewall table" '! inns fg-router nft list table inet fengard >/dev/null 2>&1'
fi

echo; echo "== $PASS passed, $FAIL failed"
[ $FAIL -eq 0 ] || { echo "--- fengard log:"; tail -20 "$LAB/fengard.log"; exit 1; }
