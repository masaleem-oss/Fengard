#!/bin/bash
# router lab installs fengard on emulated openwrt routers of every cpu type plus the edge cases
# then prints the rows for the readme tables
# needs linux or wsl2 with kvm plus qemu-system-x86 mips arm misc curl and ssh and runs as root
#   dev/routerlab/run.sh            routers and edge cases
#   dev/routerlab/run.sh routers    just the routers
#   dev/routerlab/run.sh routers mips   just the routers whose name has mips in it
#   dev/routerlab/run.sh edge       just the edge cases
# KIT=path/to/unzipped/kit tests a built kit instead of building one and DNSQ=path/to/linux/dnsq skips building dnsq
LAB=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$LAB/../.." && pwd)
export WORK=${WORK:-/var/tmp/routerlab}
mkdir -p "$WORK/results"
B=https://downloads.openwrt.org/releases
JOBS=${JOBS:-5}

# name kind version target image ram reboot label
ROUTERS='
x86-1907     x86      19.07.10 x86/64      openwrt-19.07.10-x86-64-combined-ext4.img.gz                    256 yes x86-64 mini PC
x86-2203     x86      22.03.7  x86/64      openwrt-22.03.7-x86-64-generic-ext4-combined.img.gz             256 yes x86-64 mini PC
x86-32-2305  x86      23.05.6  x86/generic openwrt-23.05.6-x86-generic-generic-ext4-combined.img.gz        256 yes x86 32-bit
x86-2410     x86      24.10.8  x86/64      openwrt-24.10.8-x86-64-generic-squashfs-combined.img.gz         256 yes x86-64, squashfs like router flash
arm32-2102   arm32    21.02.7  armvirt/32  openwrt-21.02.7-armvirt-32-zImage-initramfs                     256 no  ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300)
arm32-2305   arm32    23.05.6  armsr/armv7 openwrt-23.05.6-armsr-armv7-generic-initramfs-kernel.bin        256 no  ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300)
arm64-2203   arm64    22.03.7  armvirt/64  openwrt-22.03.7-armvirt-64-Image-initramfs                      256 no  ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450)
arm64-2512   arm64    25.12.5  armsr/armv8 openwrt-25.12.5-armsr-armv8-generic-initramfs-kernel.bin        256 no  ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450)
mipsbe-2203  mipsbe   22.03.7  malta/be    openwrt-22.03.7-malta-be-vmlinux-initramfs.elf                  256 no  MIPS big-endian (ath79: TP-Link Archer C7, GL-AR750S)
mipsle-2410  mipsle   24.10.8  malta/le    openwrt-24.10.8-malta-le-vmlinux-initramfs.elf                  256 no  MIPS little-endian (MT7621: Xiaomi 4A, Netgear R6220, GL-MT1300)
mips64-2410  mips64be 24.10.8  malta/be64  openwrt-24.10.8-malta-be64-vmlinux-initramfs.elf                512 no  MIPS64 big-endian (Octeon: EdgeRouter Lite)
mips64le-2512 mips64le 25.12.5 malta/le64  openwrt-25.12.5-malta-le64-vmlinux-initramfs.elf                512 no  MIPS64 little-endian (Loongson)
'

fetch() { # version target image
	[ -s "$WORK/${3%.gz}" ] && return
	curl -sfL -o "$WORK/$3" "$B/$1/targets/$2/$3" || { echo "download failed: $3"; return 1; }
	# openwrt images carry padding gunzip warns about
	case "$3" in *.gz) gunzip -c "$WORK/$3" >"$WORK/${3%.gz}" 2>/dev/null ;; esac
	true
}

if [ -z "$KIT" ]; then
	(cd "$ROOT" && go run ./tools/release -version lab -out dist \
		-only linux-amd64,linux-386,linux-arm,linux-arm64,linux-mips,linux-mipsle,linux-mips64,linux-mips64le,linux-riscv64,linux-loong64) || exit 1
	KIT=$ROOT/dist/fengard-lab
fi
export KIT
if [ -n "$DNSQ" ]; then cp "$DNSQ" "$WORK/dnsq"; else (cd "$ROOT" && GOOS=linux go build -o "$WORK/dnsq" ./tools/dnsq) || exit 1; fi
chmod +x "$WORK/dnsq"

what=${1:-all}
only=${2:-}
# a filtered run keeps the other results so the tables stay whole
[ -n "$only" ] || rm -f "$WORK/results/"*.log
if [ "$what" != edge ]; then
	slot=1
	while read -r name kind ver target image ram reboot label; do
		[ -n "$name" ] || continue
		case "$name" in *"$only"*) ;; *) continue ;; esac
		fetch "$ver" "$target" "$image" || continue
		while [ "$(jobs -r | wc -l)" -ge "$JOBS" ]; do sleep 5; done
		echo "testing $name"
		bash "$LAB/router.sh" "$name" "$kind" "${image%.gz}" "$ram" "$slot" "$reboot" "$label" "OpenWrt $ver" </dev/null &
		slot=$((slot + 1))
		sleep 2
	done <<<"$ROUTERS"
	wait
fi
if [ "$what" != routers ]; then
	# the sysupgrade test needs the compressed image as well
	fetch 24.10.8 x86/64 openwrt-24.10.8-x86-64-generic-squashfs-combined.img.gz
	echo "testing edge cases"
	# each case has its own vm slot so they all run at once
	for t in lowram ram128 subnet nonet port53 power sysupgrade resilience lowflash; do
		bash "$LAB/edge.sh" "$t" >"$WORK/results/edge-$t.out" 2>&1 </dev/null &
	done
	wait
	cat "$WORK"/results/edge-*.out >"$WORK/results/edge.out"
fi

echo
echo "| Router type | Firmware | CPU build | Result | Memory |"
echo "|---|---|---|---|---|"
grep -ah "^ROW|" "$WORK"/results/*.log 2>/dev/null | sort -t'|' -k2,2 -k3,3V | awk -F'|' '{ printf "| %s | %s | %s | %s | %s |\n", $2, $3, $4, $5, $6 }'
echo
echo "| Situation | Result |"
echo "|---|---|"
grep -ah "^EDGE|" "$WORK/results/edge.out" 2>/dev/null | awk -F'|' '{ printf "| %s | %s |\n", $2, $3 }'
