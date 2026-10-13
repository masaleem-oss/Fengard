#!/bin/bash
# memory of two fengard builds and adguard home with the same blocklists, the readme chart comes from this
#   OLD_NAME=1.3.1.1 NEW_NAME=1.3.1.3 dev/memcompare/run.sh OLD_FENGARDD NEW_FENGARDD LISTS_DIR
# LISTS_DIR is a fengard data/lists cache. adguard home gets the same files through a local web server.
# linux amd64, needs curl, python3 and dnsq (go build ./tools/dnsq). writes ram.csv here, then:
#   go run dev/memcompare/chart.go ram.csv docs/
set -e
OLD=$1 NEW=$2 LISTS=$3
AGH_VERSION=${AGH_VERSION:-v0.107.79}
DNSQ=${DNSQ:-dnsq}
W=${WORK:-/tmp/memcompare}
mkdir -p "$W"
OUT=$PWD/ram.csv

[ -x "$W/agh/AdGuardHome" ] || {
	curl -sfL -o "$W/agh.tgz" "https://github.com/AdguardTeam/AdGuardHome/releases/download/$AGH_VERSION/AdGuardHome_linux_amd64.tar.gz"
	tar -xzf "$W/agh.tgz" -C "$W" && mv "$W/AdGuardHome" "$W/agh"
}
rm -rf "$W/lists" && cp -r "$LISTS" "$W/lists"
(cd "$W/lists" && python3 -m http.server 8099 --bind 127.0.0.1 >/dev/null 2>&1 &)
srv=$(pgrep -f "http.server 8099" | head -n 1)
trap 'kill $srv 2>/dev/null; pkill -f "$W/" 2>/dev/null' EXIT

# adguard home set up through its own api with only the same list files, the address lists aren't domains
rm -rf "$W/aghwork" && mkdir -p "$W/aghwork"
"$W/agh/AdGuardHome" -w "$W/aghwork" --no-check-update -h 127.0.0.1 -p 3000 >"$W/agh-setup.log" 2>&1 &
p=$!
sleep 3
api() { curl -s -b "$W/jar" -c "$W/jar" -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} "http://127.0.0.1:${PORT:-3001}$2"; }
PORT=3000 api POST /control/install/configure '{"web":{"ip":"127.0.0.1","port":3001},"dns":{"ip":"127.0.0.1","port":5361},"username":"bench","password":"bench-password-2026"}' >/dev/null
sleep 4
api POST /control/login '{"name":"bench","password":"bench-password-2026"}' >/dev/null
for u in $(api GET /control/filtering/status | python3 -c 'import json,sys; [print(f["url"]) for f in json.load(sys.stdin).get("filters") or []]'); do
	api POST /control/filtering/remove_url "{\"url\":\"$u\",\"whitelist\":false}" >/dev/null
done
for f in "$W"/lists/*; do
	n=$(basename "$f")
	case "$n" in *ips_doh* | *tor-nodes*) continue ;; esac
	api POST /control/filtering/add_url "{\"name\":\"$n\",\"url\":\"http://127.0.0.1:8099/$n\",\"whitelist\":false}" >/dev/null
done
sleep 5
api GET /control/filtering/status | python3 -c 'import json,sys; fs=json.load(sys.stdin)["filters"]; print("adguard home:", sum(f["rules_count"] for f in fs), "rules")'
kill $p; wait $p 2>/dev/null || true

# three cold starts side by side, each on 2 cores like a router, fengard with the 96 mb soft limit a
# 512 mb router gets from the installer. rss every 5 seconds for 6 minutes with a few lookups each time
start_fengard() {
	rm -rf "$W/$1" && mkdir -p "$W/$1/data" && cp -r "$W/lists" "$W/$1/data/lists" && : >"$W/$1/leases"
	cp "$2" "$W/$1/fengardd" && chmod +x "$W/$1/fengardd"
	GOMAXPROCS=2 "$W/$1/fengardd" -dns 127.0.0.1:$3 -http 127.0.0.1:$((13000 + $3)) -https "" -block-ip 127.0.0.2 \
		-data "$W/$1/data" -leases "$W/$1/leases" -lan lo -wan eth0 -no-list-updates -update-url "" -mem-limit 96 >"$W/$1/run.log" 2>&1 &
}
start_fengard old "$OLD" 5371
P1=$!
start_fengard new "$NEW" 5373
P2=$!
GOMAXPROCS=2 "$W/agh/AdGuardHome" -w "$W/aghwork" --no-check-update >"$W/agh-run.log" 2>&1 &
P3=$!
echo "t,program,rss_kb,anon_kb" >"$OUT"
for t in $(seq 0 5 360); do
	for pp in "$P1:Fengard ${OLD_NAME:-old}" "$P2:Fengard ${NEW_NAME:-new}" "$P3:AdGuard Home ${AGH_VERSION#v}"; do
		awk -v t="$t" -v n="${pp#*:}" '/VmRSS/ { r = $2 } /RssAnon/ { a = $2 } END { print t "," n "," r "," a }' "/proc/${pp%%:*}/status" >>"$OUT"
	done
	for d in example.com github.com wikipedia.org openwrt.org; do
		for port in 5371 5373 5361; do "$DNSQ" 127.0.0.1:$port $d >/dev/null 2>&1 & done
	done
	sleep 5
done
grep -h "domains loaded" "$W/old/run.log" "$W/new/run.log"
kill $P1 $P2 $P3
awk -F, 'NR > 1 && $1 >= 60 { s[$2] += $3; a[$2] += $4; c[$2]++ } END { for (k in s) printf "%-24s rss %.1f MB, own %.1f MB\n", k, s[k] / c[k] / 1024, a[k] / c[k] / 1024 }' "$OUT"
