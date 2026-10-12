#!/bin/bash
# fengard-h3 against quic-go through a misbehaving network, see lab_test.go
#   dev/h3lab/run.sh        the interop lab, under a minute, with the race detector
#   dev/h3lab/run.sh long   adds the 250k query soak with key updates and fuzzes every parser for a minute each
#   dev/h3lab/run.sh live   asks the real resolvers on the internet
set -e
LAB=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$LAB/../.." && pwd)
cd "$LAB"
go mod download
case $1 in
live)
	# by ip so a network that blocks doh names doesn't get in the way
	cd "$ROOT" && FENGARD_H3_LIVE=1 FENGARD_H3_SERVERS=${SERVERS:-https://1.1.1.1/dns-query,https://8.8.8.8/dns-query,https://9.9.9.9/dns-query,https://94.140.14.140/dns-query} \
		go test -run Live -v -count=1 ./internal/fengard-h3/ | grep -E "^(---|ok|FAIL)|handshake"
	;;
long)
	go test -count=1 -v -run . . | grep -E "^(---|ok|FAIL|PASS)|lab_test"
	cd "$ROOT"
	for f in FuzzHeader FuzzParams FuzzAck FuzzQPACK FuzzFrames FuzzStreamReassembly; do
		echo "$f: $(go test -run XXX -fuzz "^$f\$" -fuzztime "${FUZZTIME:-60s}" ./internal/fengard-h3/ 2>&1 | tail -1)"
	done
	;;
*)
	go test -race -short -count=1 -v -run . . | grep -E "^(---|ok|FAIL|PASS)|lab_test"
	;;
esac
