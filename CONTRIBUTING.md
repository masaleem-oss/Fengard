# Contributing

Thanks for taking a look. Bug reports, router test reports and pull requests are all welcome.

## Testing on your router

The most useful thing right now is hearing which routers Fengard runs on. If you install it, open a
[router report](https://github.com/masaleem-oss/Fengard/issues/new?template=router_report.yml) with the
model, firmware version and whether anything went wrong. Failed installs roll back on their own, so it's
safe to try.

## Bugs and ideas

- **Bugs:** open an [issue](https://github.com/masaleem-oss/Fengard/issues/new?template=bug_report.yml) with
  what you did, what happened and the router or computer it was on. For router installs, the output of
  `logread -e fengardd | tail -50` helps a lot.
- **Security issues:** please don't open a public issue, see [SECURITY.md](SECURITY.md).
- **Ideas:** open an issue first so we can talk it through before you spend time on code.

## Code

```sh
go test ./...          # everything should pass
gofmt -l .             # should print nothing
go vet ./...
```

- Keep it one static binary with no runtime dependencies on the router.
- Anything on the DNS path has to stay bounded and non-blocking (see the design notes in the README).
- The dashboard is plain ES modules and CSS, no build step. Keep it that way.
- Changes to the router installer should be tried on both firewall types: OpenWrt 22+ (fw4, nftables) and
  OpenWrt 21 (fw3, iptables). OpenWrt x86 images run well in QEMU for this.

## Router lab

`dev/routerlab/run.sh` installs Fengard on emulated OpenWrt routers of every CPU type Fengard ships for,
across firmware versions from 19.07 to 25.12. It also runs the situations that would hurt a household:
low memory, a full flash, no internet, another DNS server on port 53, a power cut mid-install, a firmware
upgrade, and Fengard crashing or going missing. At the end it prints the tables used in the README.

```sh
sudo dev/routerlab/run.sh            # everything, about an hour
sudo dev/routerlab/run.sh routers    # just the routers
sudo dev/routerlab/run.sh edge       # just the edge cases
```

It needs Linux or WSL2 with KVM, plus `qemu-system-x86`, `qemu-system-mips`, `qemu-system-arm`, `curl` and
`ssh`. The images are downloaded from downloads.openwrt.org on the first run. To add a router type, add a
line to the list at the top of `run.sh`.

## Releasing

1. Run the router lab and paste its tables into the README's
   [Supported routers](README.md#supported-routers) section. Try to add at least one new router type or
   firmware version each release so the list keeps growing.
2. Add rows for any router reports from real hardware.
3. `go run ./tools/release -version X.Y.Z`, then attach everything in `dist/assets/` to a GitHub release.

By contributing you agree your work is licensed under the Apache License 2.0.
