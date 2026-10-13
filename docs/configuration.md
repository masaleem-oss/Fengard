# Configuration reference

[← Back to the README](../README.md)

The installers set everything up, so these are only needed to run `fengardd` by hand.

| Flag | Default | Meaning |
|---|---|---|
| `-dns` | `:53` | comma-separated DNS listen addresses |
| `-http` | `:80` | dashboard and block page listen address |
| `-https` | `:443` | HTTPS block page listen address, empty to disable |
| `-block-ip` | this machine's LAN address | IPv4 address blocked domains resolve to (the block page and dashboard) |
| `-dns-ip` | the block IP | LAN IPv4 address devices' DNS is redirected to |
| `-block-ip6` | none | the router's LAN IPv6 address |
| `-data` | `/etc/fengard` | directory for persistent state |
| `-leases` | `/tmp/dhcp.leases` | dnsmasq DHCP lease file |
| `-dashboard-hosts` | `fengard.lan` | comma-separated hostnames that open the dashboard |
| `-lan` | `br-lan` | comma-separated LAN interfaces |
| `-wan` | `wan,eth0` | comma-separated WAN interfaces |
| `-firewall` | off | apply firewall rules (Linux router only) |
| `-firewall-backend` | `auto` | `auto`, `nftables` or `iptables` |
| `-netns` | none | apply firewall rules inside a network namespace (testing) |
| `-harden` | off | set kernel network hardening options |
| `-mem-limit` | `96` | soft memory limit in MB |
| `-no-list-updates` | off | don't download blocklists, use cached copies only |
| `-update-url` | the GitHub releases API | where to check for new Fengard releases, empty turns checks off |
| `-speedtest-url` | fast.com, then LibreSpeed | your own speed test server instead, anything with `/__down?bytes=N` and `/__up` |

`SIGUSR1` makes Fengard re-apply its firewall rules; the router installer wires this to firewall reloads.

**API.** Everything the dashboard does goes through a JSON API under `/api`: devices, groups (profiles),
rules, lists, port forwards, DNS records, VPN peers, alerts, settings, updates, users, API keys, config export,
import and rollback. Use an API key from **Settings → API keys** as a bearer token for scripts.
