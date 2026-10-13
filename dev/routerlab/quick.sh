#!/bin/bash
# quick.sh runs before the vms get anything, about a minute, so a broken build stops the lab early.
# on every router cpu at once, through qemu-user with no vm boot:
#   the unit tests that touch memory layout, then the real fengardd on real blocklists answering dns
# needs qemu-user and qemu-user-binfmt (apt install qemu-user qemu-user-binfmt)
#   dev/routerlab/quick.sh            every cpu
#   dev/routerlab/quick.sh arm mips   just these GOARCH values
# exits 1 and prints the failing output on the first problem
set -u
WORK=${WORK:-/var/tmp/routerlab}
LAB=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$LAB/../.." && pwd)
Q=$WORK/quick
# mips64 and mips64le are left to the vms, qemu-user crashes inside go's epoll on them (SIGBUS in netpoll)
ARCHES=${*:-amd64 386 arm64 arm mips mipsle riscv64 loong64}
# packages whose code depends on word size or byte order, kept small so emulated cpus finish fast
PKGS="./internal/catalog ./internal/policy ./internal/update ./internal/querylog ./internal/fengard-h3"
t0=$(date +%s)
say() { echo "[quick $(($(date +%s) - t0))s] $*"; }
mkdir -p "$Q"

# real lists once, then reused so this never waits on the internet
L=$Q/lists
if [ "$(ls "$L" 2>/dev/null | wc -l)" -lt 5 ]; then
	say "downloading blocklists once into $L"
	rm -rf "$Q/seed" && mkdir -p "$Q/seed"
	(cd "$ROOT" && CGO_ENABLED=0 go build -o "$Q/fengardd-host" ./cmd/fengardd) || exit 1
	"$Q/fengardd-host" -dns 127.0.0.1:15999 -http 127.0.0.1:18999 -https "" -block-ip 127.0.0.2 -data "$Q/seed" \
		-leases "$Q/seed/leases" -lan lo -wan eth0 -update-url "" >"$Q/seed.log" 2>&1 &
	p=$!
	for _ in $(seq 120); do grep -q "blocklists updated" "$Q/seed.log" && break; sleep 1; done
	kill $p; wait $p 2>/dev/null
	grep -q "blocklists updated" "$Q/seed.log" || { say "FAIL couldn't download blocklists"; tail -5 "$Q/seed.log"; exit 1; }
	rm -rf "$L" && mv "$Q/seed/lists" "$L"
fi
# a malware domain, which the default profile blocks
BLOCKED=$(cat "$L"/*tif.mini* 2>/dev/null | grep -v '^#' | grep '\.' | sed -n 500p)

one() {
	a=$1 i=$2 d=$Q/$a
	rm -rf "$d" && mkdir -p "$d/data"
	out=$d/out.log
	{
		(cd "$ROOT" && GOARCH=$a CGO_ENABLED=0 go test -short -count=1 $PKGS) || exit 1
		# the kit's own programs when run.sh hands them over, so this tests exactly what ships
		if [ -x "${KITBIN:-}/linux-$a/fengardd" ]; then
			cp "$KITBIN/linux-$a/fengardd" "$d/fengardd"
		else
			(cd "$ROOT" && GOARCH=$a CGO_ENABLED=0 go build -o "$d/fengardd" ./cmd/fengardd) || exit 1
		fi
		ln -s "$L" "$d/data/lists"
		: >"$d/leases"
		port=$((15900 + i))
		"$d/fengardd" -dns 127.0.0.1:$port -http 127.0.0.1:$((18900 + i)) -https "" -block-ip 127.0.0.2 -data "$d/data" \
			-leases "$d/leases" -lan lo -wan eth0 -no-list-updates -update-url "" >"$d/run.log" 2>&1 &
		p=$!
		trap 'kill $p 2>/dev/null' EXIT
		n=
		for _ in $(seq 180); do
			kill -0 $p 2>/dev/null || { echo "fengardd exited:"; tail -5 "$d/run.log"; exit 1; }
			n=$(sed -n 's/.*blocklists: \([0-9]*\) domains loaded.*/\1/p' "$d/run.log")
			[ -n "$n" ] && break
			sleep 1
		done
		[ "${n:-0}" -ge 100000 ] || { echo "blocklists loaded ${n:-nothing}, want 100000+"; tail -5 "$d/run.log"; exit 1; }
		"$WORK/dnsq" 127.0.0.1:$port fengard.lan | grep -q "NOERROR 127.0.0.2" || { echo "fengard.lan didn't answer"; exit 1; }
		"$WORK/dnsq" 127.0.0.1:$port "$BLOCKED" | grep -q "NOERROR 127.0.0.2" || { echo "$BLOCKED wasn't blocked: $("$WORK/dnsq" 127.0.0.1:$port "$BLOCKED")"; exit 1; }
		echo "ok, $n domains"
	} >"$out" 2>&1
}

[ -x "$WORK/dnsq" ] || (cd "$ROOT" && CGO_ENABLED=0 go build -o "$WORK/dnsq" ./tools/dnsq) || exit 1
i=0
pids=
for a in $ARCHES; do
	one "$a" $i &
	pids="$pids $!:$a"
	i=$((i + 1))
done
failed=
for pa in $pids; do
	p=${pa%%:*} a=${pa#*:}
	if wait "$p"; then
		say "PASS $a $(tail -1 "$Q/$a/out.log")"
	else
		say "FAIL $a"
		tail -25 "$Q/$a/out.log" | sed 's/^/    /'
		failed=1
	fi
done
[ -z "$failed" ] || exit 1
say "every cpu passed"
