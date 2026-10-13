# Development

[← Back to the README](../README.md)

Requires Go (see [`go.mod`](../go.mod)).

```sh
go test ./...                              # unit and integration tests
go build -o bin/fengardd ./cmd/fengardd    # the daemon for this machine
go run ./tools/release -version 1.0.0      # the full kit for every platform, into dist/
```

The tests cover DNS end to end against fake plain and DoH upstreams, policy, local DNS, two-factor, API keys,
web auth and firewall rendering for both backends.

**HTTP/3 lab.** [`dev/h3lab/run.sh`](../dev/h3lab) runs fengard-h3 against [quic-go](https://github.com/quic-go/quic-go),
a separate QUIC implementation, through a proxy that loses, reorders, duplicates and delays packets: up to 30%
loss, Retry, 60 KB answers, server restarts, NAT rebinding, key updates, GOAWAY and stream limits, in under a
minute. `run.sh long` adds a 250,000 query soak and fuzzes every parser, and `run.sh live` asks Cloudflare,
Google, Quad9 and AdGuard for real. It's its own Go module so quic-go never becomes a Fengard dependency.

**Try it on Windows.** Build `bin/fengardd.exe`, run `dev\run.ps1`, and open http://127.0.0.1. State lives in
`dev\data`, and `dev\leases.txt` makes this PC appear as a device. To filter this PC's own browsing, run
`dev\test-mode-on.ps1` as Administrator; `dev\test-mode-off.ps1` undoes it. The firewall isn't applied on
Windows; the Firewall page shows the generated ruleset instead.

**Network lab.** [`dev/netlab.sh`](../dev/netlab.sh) builds a simulated home network in network namespaces and
checks the real firewall: DNS hijack, DoT blocking, pause at the firewall, port forwarding, WAN exposure and a
DNS flood. It needs root on Linux or WSL2, and `FW_BACKEND=iptables` runs it with the iptables backend.

```sh
GOOS=linux GOARCH=amd64 go build -o bin/linux/fengardd ./cmd/fengardd
GOOS=linux GOARCH=amd64 go build -o bin/linux/dnsq ./tools/dnsq
GOOS=linux GOARCH=amd64 go build -o bin/linux/dnsflood ./tools/dnsflood
sudo FENGARD_ROOT=. bash dev/netlab.sh
```

**Router lab.** [`dev/routerlab/run.sh`](../dev/routerlab) installs Fengard on emulated OpenWrt routers of every
CPU type and firmware generation, runs the edge cases, and prints the tables in
[Supported routers](routers.md#supported-routers), in a few minutes. It needs root on Linux or WSL2 with KVM and QEMU. See
[CONTRIBUTING](../CONTRIBUTING.md#router-lab) for details.

**Releasing.** Run the router lab first and update the [Supported routers](routers.md#supported-routers) tables,
adding a new router type or firmware version when you can. Then `go run ./tools/release -version 1.0.0` writes
`dist/assets/`. Attach all of it to a GitHub release: the kit zip, `router-install.sh`, `router-uninstall.sh`,
one `fengardd-linux-<cpu>.gz` per router CPU, and `SHA256SUMS`. The router installer downloads from the latest
release, which is what makes the one-line install work.

## Project layout

| Path | What's there |
|---|---|
| [`cmd/fengardd`](../cmd/fengardd) | the daemon: flags and wiring |
| [`install/`](../install) | installers: `Install Fengard.cmd` and `fengard-setup.ps1` (Windows), `install.sh` (macOS, Linux), `router-install.sh` and `router-uninstall.sh` (run on the router) |
| [`internal/config`](../internal/config) | config model, validation, versioned store with rollback |
| [`internal/catalog`](../internal/catalog) | category blocklists, custom lists, app catalog, compact domain set |
| [`internal/policy`](../internal/policy) | compiled per-device decision engine |
| [`internal/dnsserver`](../internal/dnsserver) | resolver, cache, rate limiting, DoH, DoH3 and DoT upstreams, local answers |
| [`internal/fengard-h3`](../internal/fengard-h3) | fengard-h3: QUIC v1, HTTP/3 and QPACK, just enough for DNS over HTTP/3 |
| [`internal/localdns`](../internal/localdns) | `.lan` names, records, reverse lookups |
| [`internal/devices`](../internal/devices) | IP to device tracking, MAC vendor table, Wake-on-LAN |
| [`internal/firewall`](../internal/firewall) | ruleset generation and atomic apply, nftables and iptables |
| [`internal/certs`](../internal/certs) | per-router CA: block page and dashboard certificates, Apple profile, export and import |
| [`internal/vpn`](../internal/vpn) | WireGuard keys, client profiles, kernel interface, public address detection, Tailscale status |
| [`internal/screentime`](../internal/screentime) | minutes online per person and per app, persisted, with bonus minutes |
| [`internal/auth`](../internal/auth) | accounts, sessions, TOTP two-factor, API keys |
| [`internal/notify`](../internal/notify) | alert delivery to chat apps and webhooks |
| [`internal/web`](../internal/web) | API, embedded dashboard (`static/`), asset server, block page |
| [`internal/features`](../internal/features) | add-ons: the manifest, installing them from the kit or the release, checking their files |
| [`features/`](../features) | the add-ons' own files, one folder each. After changing one run `go run ./tools/featurepack` |
| [`internal/store`](../internal/store), [`querylog`](../internal/querylog), [`alerts`](../internal/alerts) | embedded database, query history and hourly series, alerts |
| [`tools/`](../tools) | `release` (builds the kit), `dnsq` (test lookups), `dnsflood` (resilience test), `memcheck`, `genoui` (vendor table), `featurepack` (add-on manifest), `wgkey` |
| [`dev/`](../dev) | local test scripts, the network lab and the [router lab](../dev/routerlab) |
