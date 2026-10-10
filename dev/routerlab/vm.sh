#!/bin/bash
# boots openwrt test routers in qemu
#   vm.sh start NAME KIND FILE RAM SLOT [keep]
#   vm.sh stop NAME
#   vm.sh ssh SLOT cmd
# slot n gives ssh 2300+n web 18300+n and dns 15300+n on localhost
# kinds x86 mipsbe mipsle mips64be mips64le arm32 arm64
# LANNET RIP SHOST FGIP change the lan subnet and WANOPT=restrict=on cuts the internet
WORK=${WORK:-/var/tmp/routerlab}
cd "$WORK" || exit 1
LANNET=${LANNET:-192.168.1.0/24} RIP=${RIP:-192.168.1.1} SHOST=${SHOST:-192.168.1.2} FGIP=${FGIP:-192.168.1.4}
SSHOPT=(-q -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
lan() { echo "user,id=lan,net=$LANNET,host=$SHOST,dhcpstart=${RIP%.*}.240,hostfwd=tcp:0.0.0.0:$((2300 + $1))-$RIP:22,hostfwd=tcp:0.0.0.0:$((18300 + $1))-$FGIP:80,hostfwd=udp:0.0.0.0:$((15300 + $1))-$RIP:53"; }

case "$1" in
start)
	name=$2 kind=$3 file=$4 ram=$5 slot=$6
	pkill -f "name vm-$name " 2>/dev/null
	sleep 1
	common=(-name "vm-$name " -m "$ram" -display none -serial "file:$WORK/serial-$name.log" -daemonize
		-netdev "$(lan "$slot")" -netdev "user,id=wan${WANOPT:+,$WANOPT}")
	case "$kind" in
	x86)
		# overlay so every run starts from a clean image
		[ "$7" = keep ] && [ -f "ovl-$name.qcow2" ] || { rm -f "ovl-$name.qcow2"; qemu-img create -q -f qcow2 -F raw -b "$file" "ovl-$name.qcow2"; }
		qemu-system-x86_64 "${common[@]}" -enable-kvm -cpu host -smp 2 -drive file="ovl-$name.qcow2",if=virtio \
			-device virtio-net-pci,netdev=lan -device virtio-net-pci,netdev=wan ;;
	mipsbe | mipsle | mips64be | mips64le)
		bin=qemu-system-mips cpu=24Kc
		case "$kind" in
		mipsle) bin=qemu-system-mipsel ;;
		mips64be) bin=qemu-system-mips64 cpu=MIPS64R2-generic ;;
		mips64le) bin=qemu-system-mips64el cpu=MIPS64R2-generic ;;
		esac
		$bin "${common[@]}" -M malta -cpu $cpu -kernel "$file" -append "console=ttyS0" \
			-device pcnet,netdev=lan -device pcnet,netdev=wan ;;
	arm32 | arm64)
		bin=qemu-system-arm cpu=cortex-a15
		[ "$kind" = arm64 ] && bin=qemu-system-aarch64 cpu=cortex-a53
		$bin "${common[@]}" -M virt -cpu $cpu -smp 2 -kernel "$file" -append "console=ttyAMA0" \
			-device virtio-net-pci,netdev=lan -device virtio-net-pci,netdev=wan ;;
	esac
	for _ in $(seq 150); do
		ssh "${SSHOPT[@]}" -o ConnectTimeout=3 -p $((2300 + slot)) root@127.0.0.1 true 2>/dev/null && exit 0
		sleep 2
	done
	echo "vm-$name did not come up"
	exit 1 ;;
stop) pkill -f "name vm-$2 " ;;
ssh)
	s=$2
	shift 2
	exec ssh "${SSHOPT[@]}" -o ConnectTimeout=10 -p $((2300 + s)) root@127.0.0.1 "$@" ;;
esac
