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

By contributing you agree your work is licensed under the Apache License 2.0.
