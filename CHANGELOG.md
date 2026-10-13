# Changelog

What changed in each Fengard release. Versions with a fourth number, like 1.3.1.1, are preview builds that
routers only get with **Settings → Updates → Preview builds** on. Full notes and downloads for every version
are on the [releases page](https://github.com/masaleem-oss/Fengard/releases).

## Unreleased

### Added
- **DNS → Ask all at once** sends each lookup to every resolver at the same time and uses the first good answer

### Changed
- A device flooding DNS can no longer push Fengard's memory up: about 24 MB after a flood instead of 64 MB

### Fixed
- With HTTP/3 on, resolvers after the first showed no status and looked unchecked; they now say they're standbys

## 1.3.1.2 - 2026-10-13 (preview)

### Changed
- Much less memory: about 24 MB instead of 43 MB on a GL-MT3000, and a 52 MB peak while loading
  blocklists instead of 77 MB
- 535k blocked domains take about 11 MB instead of 13 MB, with slightly faster lookups
- Fengard's own root certificates only load on routers without a certificate bundle

### Added
- **Settings → Updates → Go back…** installs an older release with the same checks as an update, backs up
  settings first and keeps automatic updates off the version you left

### Fixed
- Saving GL.iNet's DNS page gave port 53 back to dnsmasq and Fengard kept restarting; it now takes the port
  back within 20 seconds
- The installer switched off GL.iNet's per-VPN-client DNS along with the main one
- The installer asked to create an admin account after an upgrade that kept it

## 1.3.1.1 - 2026-10-12 (preview)

### Changed
- DNS over HTTP/3 keeps connections warm, connects early and resumes TLS sessions, so lookups after a quiet
  spell don't wait for a new handshake
- Less work per packet on routers without AES hardware

### Fixed
- A preview's own `router-install.sh` installed the latest full release instead of the preview

## 1.3.1 - 2026-10-12

### Added
- **Settings → Updates → Preview builds** to get pre-releases early

### Fixed
- Four-part version numbers were compared as three parts

## 1.3.0 - 2026-10-12

### Added
- DNS over HTTP/3 (**DNS → Use HTTP/3**) through fengard-h3, Fengard's own small QUIC client, falling back to
  HTTP/2 on its own when UDP is blocked
- Google DoH and AdGuard DoH presets

## 1.2.6 - 2026-10-11

### Fixed
- Dashboard and blocking on GL.iNet 4.9 firmware (Beryl 7), which ships an `nft` that can't read its rules;
  fw3 routers now always use iptables and leftover nftables rules are removed

## 1.2.5 - 2026-10-11

### Added
- A switch to turn the block page off, and your own block page HTML with placeholders like `{site}`

### Fixed
- Schedules, daily data totals and the block page used the router's idea of "Local" time instead of your
  city

## 1.2.4 - 2026-10-11

### Added
- Devices that come back with a new private Wi-Fi address are recognized and keep their profile
- **Same device as…** to link two devices by hand, and **Firewall → Recognize returning devices**

## 1.2.3 - 2026-10-11

### Added
- Popular blocklists to add with one click (HaGeZi, OISD, AdGuard, Steven Black and more)
- Data used per device over any date range

### Changed
- Passwords need 8 characters instead of 10

### Fixed
- DoH upstreams given by name (including the Quad9 and Cloudflare presets) cut the internet
- VPN blocking blocked Tailscale on the router itself
- "Used today" could count too high on a busy router

## 1.2.2 - 2026-10-10

### Fixed
- Quad9 DoH, which refuses DoH over HTTP/1.1

## 1.2.1 - 2026-10-10

### Added
- The installer asks before turning off AdGuard Home when it holds DNS port 53, and removing Fengard turns
  it back on

## 1.2.0 - 2026-10-10

### Added
- `.ipk` and `.apk` packages for every CPU to install from LuCI, and a **Services → Fengard** page

## 1.1.1 - 2026-10-10

### Added
- Speed test, internet down and slow alerts, arrival alerts, a weekly device check for risky open ports,
  live bandwidth and an optional captive portal for new iPhones and iPads

## 1.1.0 - 2026-10-10

### Added
- Updates from the dashboard, checked against the router's CPU, OpenWrt version and memory first
- **Require HTTPS** for the dashboard

### Changed
- A watchdog hands DNS to the router's own dnsmasq if Fengard stops, so the house stays online
- The installer enables Fengard at boot before moving DNS, so a power cut mid-install can't leave the router
  without DNS

## 1.0.0 - 2026-10-09

First release: DNS filtering with category and custom blocklists, per-person profiles with schedules and
screen time, app blocking, a block page with Request access, bypass blocking for DoH, DoT, DoQ and Tor, a
built-in WireGuard VPN and a one-step installer for OpenWrt and GL.iNet routers.
