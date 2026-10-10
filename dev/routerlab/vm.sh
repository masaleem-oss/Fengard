#!/bin/bash
# boots openwrt test routers in qemu
#   vm.sh start NAME KIND FILE RAM SLOT [keep]
#   vm.sh stop NAME
#   vm.sh ssh SLOT cmd
# slot n gives ssh 2300+n web 18300+n and dns 15300+n on localhost
# kinds x86 mipsbe mipsle mips64be mips64le arm32 arm64
# LANNET RIP SHOST FGIP change the lan subnet and WANOPT=restrict=on cuts the internet
# emulated cpus boot slowly so the first boot is saved once its up and later runs carry on from there
# SNAP=no boots them from scratch
# mips is the slowest to emulate so it gets the cpu first then arm then the kvm ones
WORK=${WORK:-/var/tmp/routerlab}
cd "$WORK" || exit 1
LANNET=${LANNET:-192.168.1.0/24} RIP=${RIP:-192.168.1.1} SHOST=${SHOST:-192.168.1.2} FGIP=${FGIP:-192.168.1.4}
SSHOPT=(-q -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
lan() { echo "user,id=lan,net=$LANNET,host=$SHOST,dhcpstart=${RIP%.*}.240,hostfwd=tcp:0.0.0.0:$((2300 + $1))-$RIP:22,hostfwd=tcp:0.0.0.0:$((18300 + $1))-$FGIP:80,hostfwd=udp:0.0.0.0:$((15300 + $1))-$RIP:53"; }
r() { ssh "${SSHOPT[@]}" -o ConnectTimeout=3 -p $((2300 + slot)) root@127.0.0.1 "$@" 2>/dev/null; }
mon() { echo "$1" | socat - "UNIX-CONNECT:$WORK/mon-$name.sock" 2>/dev/null; }
# waits until qemu is really gone, a new vm on the same slot cant get its ports till then
kill_vm() {
	pkill -f "name vm-$1 " 2>/dev/null || return 0
	for _ in $(seq 30); do pgrep -f "name vm-$1 " >/dev/null || return 0; sleep 0.5; done
	pkill -9 -f "name vm-$1 "; sleep 1
}
up() {
	for _ in $(seq "$1"); do r true && return 0; sleep 1; done
	return 1
}

case "$1" in
start)
	name=$2 kind=$3 file=$4 ram=$5 slot=$6
	kill_vm "$name"
	common=(-name "vm-$name " -m "$ram" -display none -serial "file:$WORK/serial-$name.log" -daemonize
		-monitor "unix:$WORK/mon-$name.sock,server,nowait"
		-netdev "$(lan "$slot")" -netdev "user,id=wan${WANOPT:+,$WANOPT}")
	case "$kind" in
	x86)
		# overlay so every run starts from a clean image
		[ "$7" = keep ] && [ -f "ovl-$name.qcow2" ] || { rm -f "ovl-$name.qcow2"; qemu-img create -q -f qcow2 -F raw -b "$file" "ovl-$name.qcow2"; }
		nice -n 10 qemu-system-x86_64 "${common[@]}" -enable-kvm -cpu host -smp 2 -drive file="ovl-$name.qcow2",if=virtio \
			-device virtio-net-pci,netdev=lan -device virtio-net-pci,netdev=wan
		up 90 || { echo "vm-$name did not come up"; exit 1; }
		# on a first boot squashfs writes go to ram while the flash overlay is formatted, and a power off
		# before the overlay is marked ready makes the next boot throw it away, ssh is up long before that
		for _ in $(seq 90); do
			r 'if grep -q "^overlayfs:/overlay / " /proc/mounts; then [ "$(readlink /overlay/.fs_state)" = 2 ]; else ! grep -q "^overlayfs:/tmp/root / " /proc/mounts; fi' && exit 0
			sleep 1
		done
		echo "vm-$name never finished booting"
		exit 1 ;;
	mipsbe | mipsle | mips64be | mips64le)
		bin=qemu-system-mips cpu=24Kc
		case "$kind" in
		mipsle) bin=qemu-system-mipsel ;;
		mips64be) bin=qemu-system-mips64 cpu=MIPS64R2-generic ;;
		mips64le) bin=qemu-system-mips64el cpu=MIPS64R2-generic ;;
		esac
		cmd=(nice -n 0 $bin -M malta -cpu $cpu -kernel "$file" -append "console=ttyS0" -device pcnet,netdev=lan -device pcnet,netdev=wan) ;;
	arm32 | arm64)
		bin=qemu-system-arm cpu=cortex-a15
		[ "$kind" = arm64 ] && bin=qemu-system-aarch64 cpu=cortex-a53
		cmd=(nice -n 5 $bin -M virt -cpu $cpu -smp 2 -kernel "$file" -append "console=ttyAMA0" -device virtio-net-pci,netdev=lan -device virtio-net-pci,netdev=wan) ;;
	*) echo "unknown kind $kind"; exit 1 ;;
	esac
	# these run from ram so a saved boot is the whole router
	snap=$WORK/snap-$name
	key="$($bin --version | head -1) ${cmd[*]} $ram $slot $LANNET ${WANOPT:-} $(stat -c %s.%Y "$file")"
	if [ "$SNAP" != no ] && [ -s "$snap.state" ] && [ "$(cat "$snap.key" 2>/dev/null)" = "$key" ]; then
		"${cmd[@]}" "${common[@]}" -incoming "exec:cat $snap.state"
		# it was paused for saving so it comes back paused
		for _ in $(seq 60); do mon "info migrate" | grep -qi "status:[[:space:]]*completed" && break; sleep 0.5; done
		mon cont >/dev/null
		# the saved clock is from when it was saved
		up 30 && r "date -u -s '$(date -u '+%Y-%m-%d %H:%M:%S')' >/dev/null" && exit 0
		echo "vm-$name did not carry on from its saved boot, booting it again"
		kill_vm "$name"
	fi
	rm -f "$snap.state" "$snap.key"
	"${cmd[@]}" "${common[@]}"
	up 300 || { echo "vm-$name did not come up"; exit 1; }
	[ "$SNAP" != no ] || exit 0
	# save it once the wan is up so later runs have internet straight away
	if [ -z "$WANOPT" ]; then
		for _ in $(seq 30); do r 'ping -c1 -W2 1.1.1.1 >/dev/null 2>&1' && break; sleep 2; done
	fi
	mon stop >/dev/null
	mon "migrate exec:cat>$snap.tmp" >/dev/null
	for _ in $(seq 120); do mon "info migrate" | grep -qi "status:[[:space:]]*completed" && break; sleep 0.5; done
	if mon "info migrate" | grep -qi "status:[[:space:]]*completed"; then mv "$snap.tmp" "$snap.state" && echo "$key" >"$snap.key"; else rm -f "$snap.tmp"; fi
	mon cont >/dev/null
	up 30 && exit 0
	echo "vm-$name stopped answering after saving its boot"
	exit 1 ;;
stop) kill_vm "$2" ;;
ssh)
	s=$2
	shift 2
	exec ssh "${SSHOPT[@]}" -o ConnectTimeout=10 -p $((2300 + s)) root@127.0.0.1 "$@" ;;
esac
