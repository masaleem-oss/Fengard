<p align="center">
  <img src="docs/logo.svg" width="96" alt="Fengard logo">
</p>

<h1 align="center">Fengard</h1>

<p align="center">
  <b>Kernel-enforced firewall and DNS filtering for any OpenWrt router.</b><br>
  One Go binary. Per-device profiles, screen time, a WireGuard VPN and a full dashboard.<br>
  Installs next to your router's firmware with one double-click. Nothing gets reflashed.
</p>

<p align="center">
  Parental controls, ad blocking and screen time for every device on your home network,
  running on the router itself: OpenWrt, GL.iNet and other OpenWrt-based routers.
  A self-hosted alternative to Pi-hole and AdGuard Home that kids can't get around by changing DNS.
</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache 2.0" src="https://img.shields.io/badge/license-Apache%202.0-blue"></a>
  <img alt="Go" src="https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="OpenWrt 19.07+" src="https://img.shields.io/badge/OpenWrt-19.07%2B-00B5E2?logo=openwrt&logoColor=white">
  <img alt="Firewall: nftables and iptables" src="https://img.shields.io/badge/firewall-nftables%20%7C%20iptables-8f78ff">
  <img alt="Runs on Windows, macOS and Linux" src="https://img.shields.io/badge/computer%20mode-Windows%20%7C%20macOS%20%7C%20Linux-555">
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#screenshots">Screenshots</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#features">Features</a> ·
  <a href="#configuration-reference">Reference</a> ·
  <a href="#development">Development</a>
</p>

<p align="center">
  <img src="docs/demo.gif" alt="A walk through the Fengard dashboard: overview, devices, profiles, site check, activity and firewall" width="900">
</p>

---

## Contents

- [Why Fengard](#why-fengard)
- [How it compares](#how-it-compares)
- [Install](#install)
  - [Which routers work](#which-routers-work)
  - [Supported routers](#supported-routers)
  - [What the installer changes](#what-the-installer-changes)
  - [Running on a computer instead](#running-on-a-computer-instead)
  - [Uninstall](#uninstall)
- [Screenshots](#screenshots)
- [How it works](#how-it-works)
- [Features](#features)
- [Performance](#performance)
- [Configuration reference](#configuration-reference)
- [Security](#security)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)
- [Development](#development)
- [Project layout](#project-layout)
- [Contributing](#contributing)
- [License](#license)

## Why Fengard

Most home filtering is either a DNS server on a Raspberry Pi, which any device can route around, or a
subscription box that replaces your router. Fengard does it on the router you already have:

- **It can't be bypassed by changing DNS.** Lookups sent to any other server are redirected back to Fengard,
  and DNS-over-HTTPS, DNS-over-TLS, DNS-over-QUIC and Tor are blocked in the kernel.
- **Every device gets its own rules.** Profiles carry blocked categories, blocked apps, SafeSearch,
  schedules and daily screen-time limits, and a person's limit is counted across all their devices.
- **The kernel does the work.** Filtering decisions live in nftables or iptables rules in Fengard's own
  table. If Fengard stops, it removes them and the router carries on as a normal router.
- **It stays out of your way.** It runs next to the stock firmware, installs and uninstalls in one step,
  and uses about 25 MB of memory.

## How it compares

Pi-hole and AdGuard Home are great DNS blockers. Fengard covers the same ground and adds the parts a family
network needs, enforced by the router's firewall:

| | Fengard | Pi-hole | AdGuard Home |
|---|:---:|:---:|:---:|
| Network-wide ad and tracker blocking | ✅ | ✅ | ✅ |
| Per-device profiles | ✅ | ✅ groups | ✅ per client |
| Runs on the router itself | ✅ any OpenWrt router | ❌ needs a separate machine | ✅ |
| Stops devices using another DNS server, DoH or DoT | ✅ in the kernel | ❌ needs your own firewall rules | ❌ needs your own firewall rules |
| Daily screen-time limits per person, across their devices | ✅ | ❌ | ❌ |
| Block single apps (TikTok, Roblox, Fortnite...) | ✅ | lists only | ✅ |
| Block page with a "request access" button | ✅ | ❌ | ❌ |
| Built-in WireGuard VPN with the same filtering on mobile data | ✅ | ❌ | ❌ |
| Port forwarding and WAN hardening | ✅ | ❌ | ❌ |
| One-click install and clean uninstall | ✅ | ✅ | ✅ |

<sub>Based on each project's out-of-the-box features. Pi-hole and AdGuard Home can do more with extra setup.</sub>

## Install

Download **`fengard-<version>.zip`** from the [latest release](https://github.com/masaleem-oss/Fengard/releases/latest),
unzip it, and run the installer:

| On | Run |
|---|---|
| Windows | Double-click **`Install Fengard.cmd`** |
| macOS or Linux | `sh install.sh` in the unzipped folder |

Both open the same menu:

```
Fengard setup

  1  Install on my router        OpenWrt-based routers, including GL.iNet (recommended)
  2  Run on this computer        protects the whole network through DNS; this computer stays on
  3  Remove from my router
  4  Remove from this computer
```

Choose **1**. The installer finds your router, asks for its root password (on most routers that's the admin
page password), and does everything else. When it finishes it prints the dashboard address, usually
**http://fengard.lan**. Open it and create the admin account.

<a href="docs/videos/install-script.mp4"><img src="docs/videos/install-script.png" width="640" alt="Video: installing with install.sh, from the menu to the dashboard's setup screen"></a>

> Windows may warn about a downloaded file. Choose **More info → Run anyway**, or right-click the zip,
> open **Properties** and tick **Unblock** before unzipping.

**Without a computer.** A router with internet access can install straight from the latest release over SSH:

```sh
wget -O- https://github.com/masaleem-oss/Fengard/releases/latest/download/router-install.sh | sh
```

**From the router's web page (LuCI).** Every release has a package for each CPU type:
`fengard_<version>_<cpu>.ipk` for OpenWrt up to 24.10 (and GL.iNet), `.apk` for OpenWrt 25.12 and later.
The CPU names match the [Supported routers](#supported-routers) table, for example `arm64` for a GL-MT3000 or
`mipsle` for an MT7621 router.

1. In LuCI open **System → Software**, choose **Upload Package…** and pick the `.ipk`.
2. It sets itself up in about a minute, the same way the installer does. Log out of LuCI and back in, then open
   **Services → Fengard** to see how it went, start or stop it, and open the dashboard.

<a href="docs/videos/install-luci.mp4"><img src="docs/videos/install-luci.png" width="640" alt="Video: installing the package from LuCI, from upload to the dashboard's setup screen"></a>

On GL.iNet firmware, LuCI is under **System → Advanced Settings** in the GL admin panel. If you pick the
package for the wrong CPU, it says so and leaves the router as it was.

> **OpenWrt 25.12 and later: the LuCI upload doesn't work.** 25.12 moved to the `apk` package manager, which
> only installs packages signed by a key the router already trusts. LuCI's upload page has no way to allow
> anything else, and there's no way to add a trusted key from LuCI either, so it refuses every package that
> isn't from OpenWrt itself, Fengard included. Signing Fengard's packages wouldn't change that, because the
> router would still need Fengard's key put on it over SSH first. On 25.12 use the one-line install above, or
> install the `.apk` over SSH:

Copy it to the router (`scp -O fengard_<version>_<cpu>.apk root@192.168.1.1:/tmp/`), then run:

```sh
apk add --allow-untrusted /tmp/fengard_<version>_<cpu>.apk
```

The Services → Fengard page works the same after that.

### Which routers work

Fengard needs **OpenWrt-based firmware with SSH access**.

| Works | Doesn't work |
|---|---|
| OpenWrt 19.07 and newer, on any brand | Stock ISP routers |
| GL.iNet (its firmware is OpenWrt-based) | Eero, Google and Nest Wifi |
| Turris and other OpenWrt-based firmware | Most stock TP-Link, Asus and Netgear firmware |

If your router is in the right-hand column, use [computer mode](#running-on-a-computer-instead), or flash
OpenWrt if your model supports it.

| Requirement | Minimum |
|---|---|
| RAM | 128 MB (256 MB or more is comfortable) |
| Free storage | about 30 MB, or USB storage with `FG_DIR=/mnt/<usb>/fengard` |
| CPU | ARM64, ARMv5 and up, MIPS and MIPSEL (32 and 64-bit), x86-64, x86, RISC-V 64, LoongArch64 |

The installer checks memory and storage first and stops cleanly if the router is too small.

### Supported routers

Every release goes through the [router lab](dev/routerlab), which boots real OpenWrt images in QEMU for each
CPU type and runs the actual installer on them. Each router is checked for the right CPU build, DNS
answering, LAN devices resolving and reaching the dashboard, SSH still working, full blocklists loading,
internet names resolving, and an uninstall that gives DNS back with no Fengard firewall rules left. The x86
ones get rebooted too.

Results for 1.2.0:

| Router type | Firmware | CPU build | Result | Memory |
|---|---|---|---|---|
| ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450) | OpenWrt 22.03.7 | linux-arm64 | ✅ all 8 checks | 31 MB |
| ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450) | OpenWrt 25.12.5 | linux-arm64 | ✅ all 8 checks | 32 MB |
| ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300) | OpenWrt 21.02.7 | linux-arm | ✅ all 8 checks | 29 MB |
| ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300) | OpenWrt 23.05.6 | linux-arm | ✅ all 8 checks | 30 MB |
| MIPS big-endian (ath79: TP-Link Archer C7, GL-AR750S) | OpenWrt 22.03.7 | linux-mips | ✅ all 8 checks | 31 MB |
| MIPS little-endian (MT7621: Xiaomi 4A, Netgear R6220, GL-MT1300) | OpenWrt 24.10.8 | linux-mipsle | ✅ all 8 checks | 31 MB |
| MIPS64 big-endian (Octeon: EdgeRouter Lite) | OpenWrt 24.10.8 | linux-mips64 | ✅ all 8 checks | 34 MB |
| MIPS64 little-endian (Loongson) | OpenWrt 25.12.5 | linux-mips64le | ✅ all 6 checks (no internet in the VM) | 18 MB |
| x86 32-bit | OpenWrt 23.05.6 | linux-386 | ✅ all 9 checks | 30 MB |
| x86-64 mini PC | OpenWrt 19.07.10 | linux-amd64 | ✅ all 9 checks | 31 MB |
| x86-64 mini PC | OpenWrt 22.03.7 | linux-amd64 | ✅ all 9 checks | 34 MB |
| x86-64, squashfs like router flash | OpenWrt 24.10.8 | linux-amd64 | ✅ all 9 checks | 35 MB |

On real hardware:

| Router | Firmware | Notes |
|---|---|---|
| GL.iNet GL-MT3000 | GL.iNet 4.7 (OpenWrt 21.02, fw3) | In daily use since 1.0.0, upgraded in place |

Got it running on something else? [Open an issue](https://github.com/masaleem-oss/Fengard/issues/new/choose)
and it'll go in this table.

#### When things go wrong

These run in the lab on OpenWrt 24.10 x86 squashfs, which has the same flash layout as a real router (the package
rows also on 21.02 and 25.12):

| Situation | Result |
|---|---|
| 96 MB of RAM (under the 100 MB minimum): refuses cleanly, router untouched | ✅ |
| 128 MB of RAM: full blocklists, no out of memory | ✅ |
| Nearly full flash: refuses cleanly, router untouched | ✅ |
| LAN on 10.0.0.1/16: picks a spare address in the subnet | ✅ |
| No internet during install: installs and serves local names | ✅ |
| No internet: uninstall gives DNS back | ✅ |
| Another DNS server already on port 53: stops and rolls back | ✅ |
| Power cut at 'DNS:' during install: DNS works after reboot | ✅ |
| Power cut at 'starting' during install: DNS works after reboot | ✅ |
| Firmware upgrade keeping settings: Fengard comes back | ✅ |
| Fengard stopped by hand: the house keeps DNS | ✅ |
| Program missing at boot: the house keeps DNS | ✅ |
| Program crash-looping: the house keeps DNS | ✅ |
| Recovers by itself once the program works again | ✅ |
| Installed from the ipk on OpenWrt 21.02 (fw3, like stock GL.iNet): works like the installer | ✅ |
| Removed with the ipk on OpenWrt 21.02 (fw3, like stock GL.iNet): router back to stock | ✅ |
| Installed from the ipk on OpenWrt 24.10: works like the installer | ✅ |
| Removed with the ipk on OpenWrt 24.10: router back to stock | ✅ |
| Installed from the apk on OpenWrt 25.12: works like the installer | ✅ |
| Removed with the apk on OpenWrt 25.12: router back to stock | ✅ |

### What the installer changes

Everything is read from the router's own configuration, so nothing is hardcoded to one model:

```mermaid
flowchart LR
    A[Computer runs installer] -->|one SSH upload,<br/>every CPU build| B[Router]
    B --> C{Detect}
    C --> C1[CPU type]
    C --> C2[LAN address, subnet,<br/>guest networks]
    C --> C3[WAN interfaces from<br/>the masquerading zone]
    C --> C4[fw3 + iptables<br/>or fw4 + nftables]
    C --> C5[free spare LAN address<br/>and web ports]
    C1 & C2 & C3 & C4 & C5 --> D[Record originals in<br/>/etc/fengard/install.env]
    D --> E[Install service,<br/>move DNS to Fengard]
    E --> F{Answers DNS?}
    F -->|yes| G[Done: dashboard at<br/>http://fengard.lan]
    F -->|no| H[Roll back to the<br/>stock DNS setup]
```

| What | Change | Undone by uninstall |
|---|---|---|
| `/usr/bin/fengardd` | the program, only the one built for this CPU | yes |
| `/etc/fengard/` | settings, database, certificates, blocklists | kept unless `--purge` |
| `/etc/init.d/fengard` | the service; adds one spare LAN address for the dashboard and block page, and runs a small watchdog | yes |
| dnsmasq | stops answering DNS and keeps doing DHCP; devices are told to use the router for DNS | restored exactly |
| firewall | a hook that re-applies Fengard's rules after a firewall reload, plus the VPN interface and port | yes |
| `/etc/sysupgrade.conf` | keeps Fengard across firmware upgrades | yes |

Most firmware keeps its own web interface on ports 80 and 443, so Fengard listens on free ports and the
firewall redirects the spare address's 80 and 443 to them. If any step fails the installer rolls back on its
own, and it never switches DNS over until Fengard is set to start at boot, so even a power cut mid-install
can't leave the household without DNS. Reinstalling keeps the same address, ports and certificate.

**The house stays online if Fengard doesn't.** The watchdog checks Fengard every 20 seconds. If it's stopped,
crashing or missing (say USB storage didn't mount), plain dnsmasq takes over DNS within about a minute and a
half. Every 10 minutes the watchdog gives Fengard the port back, and as soon as Fengard answers again, it's in
charge. Filtering is off while dnsmasq stands in, but nobody loses internet.

Optional settings for the router installer:

| Variable | Use |
|---|---|
| `FG_IP=192.168.1.2` | choose the spare LAN address yourself |
| `LAN_NET=lan` | the main LAN network name in `/etc/config/network` |
| `FG_DIR=/mnt/usb/fengard` | keep data and the program on USB storage |
| `FORCE=1` | install even if the router looks too small |

### Running on a computer instead

For routers that can't run Fengard, option 2 runs it on an always-on computer as a background service:

| System | Runs as | Program | Data |
|---|---|---|---|
| Windows | scheduled task | `C:\Program Files\Fengard` | `C:\ProgramData\Fengard` |
| macOS | launchd daemon | `/usr/local/fengard` | `/Library/Application Support/Fengard` |
| Linux, Raspberry Pi | systemd service | `/usr/local/bin/fengardd` | `/var/lib/fengard` |

It opens the local network through the computer's firewall (Windows Firewall, the macOS firewall, ufw or
firewalld) and then tells you the one step left: set your router's DHCP DNS server to this computer.

Computer mode filters every device by DNS but has limits:

- **No firewall.** Port forwards, bypass blocking, flood limits and the VPN need the router install.
- **The computer must stay on and awake.** While it sleeps, devices lose name lookups.
- **Its address must not change.** Reserve it in the router's DHCP settings.
- **Only one DNS server.** Give devices no second DNS server, or they can go around Fengard.

### Uninstall

- **Router:** option 3 in the menu, or `sh /etc/fengard/uninstall.sh` on the router. Add `--purge` to also delete
  settings and history. If you installed the package, removing `fengard` in **System → Software** (or
  `opkg remove fengard`, `apk del fengard`) does the same, keeping settings for a reinstall.
- **Computer:** option 4 in the menu. Then set your router's DHCP DNS server back to automatic.

## Screenshots

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/dashboard-light.png">
    <img src="docs/screenshots/dashboard.png" alt="Dashboard: queries, blocked share, activity chart, top lists and protection status" width="900">
  </picture>
  <br><sub>The dashboard follows your GitHub theme: dark or light, same as the real one.</sub>
</p>

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/devices.png" alt="Devices list with manufacturer, profile and status"><br><b>Devices.</b> Found automatically, with the manufacturer from the MAC address.</td>
    <td width="50%"><img src="docs/screenshots/device-detail.png" alt="One device's activity and settings"><br><b>One device.</b> Its activity, top sites, profile, pause and Wake-on-LAN.</td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/profiles.png" alt="Profiles with categories, apps and screen time"><br><b>Profiles.</b> Categories, apps, SafeSearch, schedules and screen time per person.</td>
    <td><img src="docs/screenshots/check-site.png" alt="Check a site: what each profile does with tiktok.com"><br><b>Check a site.</b> What every profile would do with a domain, and why.</td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/activity.png" alt="Live and historical query activity"><br><b>Activity.</b> Every lookup, live or historical, filtered by device or domain.</td>
    <td><img src="docs/screenshots/filtering.png" alt="Filtering: categories, custom rules and blocklists"><br><b>Filtering.</b> Categories, block and allow rules, temporary allows and custom lists.</td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/firewall.png" alt="Firewall protections and the generated ruleset"><br><b>Firewall.</b> Bypass blocking, quarantine and WAN hardening, with the live ruleset.</td>
    <td><img src="docs/screenshots/alerts.png" alt="Alerts for new devices, access requests and limits"><br><b>Alerts.</b> New devices, access requests, limits and upstream problems.</td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/forwarding.png" alt="Port forwarding rules"><br><b>Port forwarding.</b> Open a port to one device, enforced in the kernel.</td>
    <td><img src="docs/screenshots/dns.png" alt="DNS settings: upstreams and local records"><br><b>DNS.</b> Plain, DoT or DoH upstreams, DNSSEC and <code>.lan</code> records.</td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/palette.png" alt="Command palette searching for tiktok"><br><b>Command palette.</b> <kbd>Ctrl</kbd> <kbd>K</kbd> jumps to pages, devices or a site check.</td>
    <td><img src="docs/screenshots/settings.png" alt="Settings: accounts, notifications, API keys, backups"><br><b>Settings.</b> Accounts, two-factor, notifications, API keys, backups and history.</td>
  </tr>
</table>

What a device on the network sees:

<table>
  <tr>
    <td width="33%" align="center"><img src="docs/screenshots/blockpage-phone.png" alt="Block page on a phone" width="260"><br><b>Block page</b> with the reason and a request-access form.</td>
    <td width="33%" align="center"><img src="docs/screenshots/mytime-phone.png" alt="My time page on a phone" width="260"><br><b>My time</b> at <code>fengard.lan/me</code>: what's used, what's left, and a "need more time?" button.</td>
    <td width="33%" align="center"><img src="docs/screenshots/blockpage.png" alt="Block page on a laptop"><br><b>On a laptop</b> the same page, served over HTTP and HTTPS.</td>
  </tr>
</table>

<sub>Screenshots are from a simulated home network with demo devices and a month of generated history.</sub>

## How it works

```mermaid
flowchart TB
    subgraph LAN[Home network]
        P[Phones, laptops,<br/>consoles, TVs, IoT]
    end
    subgraph R[Router, stock firmware]
        DHCP[dnsmasq<br/>DHCP only]
        subgraph F[fengardd, one Go binary]
            DNS[DNS server<br/>cache, rate limits]
            POL[Policy engine<br/>per-device decisions]
            WEB[Dashboard, API,<br/>block page]
            FWM[Firewall manager]
            DB[(bbolt database<br/>config, history)]
        end
        K[Kernel: Fengard's own<br/>nftables table or iptables chains]
    end
    UP[Upstream DNS<br/>plain, DoT or DoH]
    NET((Internet))

    P -- DHCP --> DHCP
    P -- every DNS lookup --> K
    K -- redirected to Fengard --> DNS
    DNS <--> POL
    DNS --> UP
    POL --- DB
    WEB --- DB
    FWM -- atomic ruleset --> K
    P -- traffic --> K --> NET
```

A lookup's journey:

```mermaid
sequenceDiagram
    participant D as Device
    participant K as Kernel firewall
    participant F as Fengard DNS
    participant P as Policy engine
    participant U as Upstream
    D->>K: query to 8.8.8.8:53 (any server)
    K->>F: redirected to the router
    F->>F: per-device rate limit
    F->>P: who is this device, what is its profile?
    alt blocked
        P-->>F: block, reason
        F-->>D: address of the block page
    else allowed
        F->>F: cache hit?
        F->>U: forward over DoH/DoT if not cached
        U-->>F: answer
        F-->>D: answer
    end
    F--)F: log, stats and screen time (async, never slows the answer)
```

The policy engine checks each lookup in this order and stops at the first match:

```mermaid
flowchart LR
    A[New device<br/>not approved?] --> B[Device or<br/>profile paused?]
    B --> C[Protection<br/>paused?]
    C --> D[Daily screen<br/>time used up?]
    D --> E[App time<br/>limit reached?]
    E --> F[Schedule<br/>active?]
    F --> G[Allow rule?]
    G --> H[Temporary<br/>allow?]
    H --> I[Block rule?]
    I --> J[Blocked<br/>category?]
    J --> K[Blocked app?]
    K --> L[Custom<br/>blocklist?]
    L --> M[SafeSearch<br/>rewrite?]
    M --> N[Allow]
```

Design choices:

- **The kernel does the heavy lifting.** Packet filtering, NAT, port forwards and flood limits are rules in
  Fengard's own table or chains next to the router's normal firewall. Two backends, nftables (OpenWrt 22 and
  newer, modern Linux) and iptables (OpenWrt 21, GL.iNet 4.x), picked automatically. Rules are applied
  atomically and removed when Fengard stops.
- **Bounded everything.** The DNS cache, rate-limiter keys, stats, certificate cache, device tracking and
  notification queues all have hard caps, so floods of random names or addresses can't grow memory.
- **DNS never waits on slow work.** Query logging, notifications and device discovery are asynchronous and
  drop (and count) rather than slow a lookup down.
- **Compact blocklists.** About 535k domains take about 13 MB: a sorted blob plus offsets instead of a Go map.
- **No build step, no framework.** The dashboard is plain ES modules and CSS embedded in the binary, served
  with ETags and gzip. A first visit downloads about 90 KB, and it works with no internet access.
- **Nothing to install on the router.** One static binary with its own root certificates, so it works even on
  firmware without a CA bundle.

## Features

| Area | What it does |
|---|---|
| Devices | Found from DHCP and the neighbor table (IPv4 and IPv6; the ARP table on Windows and macOS), with the manufacturer from the MAC. Rename, group, pause, block, forget, Wake-on-LAN |
| Profiles | Blocked categories, 55 individual apps (TikTok, Roblox, Discord, Fortnite and more), SafeSearch, custom rules, schedules, pause |
| Categories | Ads, malware, adult, gambling, social, gaming, streaming, AI chatbots, VPN and DNS bypass, Tor. Updated daily |
| Screen time | Profiles marked as a person get a daily online limit and per-app or per-category limits (for example Fortnite 2 h a day), counted once across all their devices |
| My time page | `http://fengard.lan/me` on any of the person's devices: time used and left, each limit, what it went on, and a "need more time?" request the admin can grant from the alert |
| Bypass protection | Redirects all DNS to Fengard, blocks DoH, DoT and DoQ, turns off Firefox DoH and iCloud Private Relay |
| Tor | Blocked by name (Tor Browser, bridges) and by address: the relay list refreshes daily into a firewall set, applied per profile |
| Remote access VPN | Built-in WireGuard server. Add a phone, scan the QR code, and it gets the same filtering on mobile data. Detects double NAT and says what to forward |
| Tailscale | When no port can be forwarded, the router can be a Tailscale exit node. Fengard filters `tailscale0` like the LAN and lists Tailscale devices |
| Custom blocklists | Subscribe to up to 16 hosts-file or domain lists by URL |
| Check a site | What every profile would do with a domain and why, also from the command palette |
| Temporary allow | Allow a site for an hour, or any length, for one profile or everyone. One click from an access request |
| Pause protection | Turn all filtering off for a few minutes when something breaks. Device pauses still apply |
| Local DNS | Every device answers as `<name>.lan`, with custom A, AAAA and CNAME records and reverse lookups. `.lan` never leaks upstream |
| Upstreams | Plain, DNS-over-TLS and DNS-over-HTTPS, with an optional DNSSEC-record request (validation depends on the upstream) |
| Block page | Branded, over HTTP and HTTPS (a per-router CA with signing restricted in software to blocked names and the dashboard), with an access-request form |
| Firewall | Port forwards, WAN hardening (service ports closed, SYN and ICMP limits, invalid packets dropped), per-device DNS flood limit, quarantine for new devices |
| DNS resilience | Cache, serve-stale when the upstream is down, merged duplicate lookups, per-device and global rate limits |
| Notifications | Discord, Slack, Telegram, ntfy and generic webhooks, by severity |
| Accounts | Admin and viewer roles, two-factor (TOTP and recovery codes), API keys, idle and absolute session timeouts |
| Dashboard HTTPS | Served with a CA-signed certificate for its names and IP, so installing the CA makes it green |
| Certificate portability | The CA exports, imports and travels in backups, so a replacement router keeps the trust already installed on devices |
| Admin | Audit log, config history with rollback, backup and restore |
| Updates | Checks GitHub for new releases, makes sure a release still supports this router's CPU, firmware and memory, then updates from the dashboard in about a minute or overnight on its own. A new version is test-run before it replaces the old one, and the old one comes back if it doesn't start |
| Monitoring | Live and historical activity per device, 24 h, 7 d and 30 d charts with comparison, top lists, alerts |
| Internet | A green bar with the line's speed, a speed test every night around 4 am (or any time from the dashboard), and an alert like "Internet was down 14 min" after an outage |
| Live activity | Live speed and today's data for each device, read from the router's own connection table with no extra firewall rules |
| Who's home | Turn on arrival alerts for a phone and Fengard shows whether it's home and messages you when it arrives or leaves |
| Device check | Every week Fengard checks the devices on your network for risky open doors like Telnet, Android debugging on TV boxes, or a camera stream with no password, and for ports a device opened to the internet with UPnP, and explains each one in plain words |
| Captive portal | Optional. The first time a new iPhone or iPad joins, it shows a sign-in page saying the network is protected by Fengard, with a Join network button. You can swap in your own HTML and CSS (scripts never run). Computers and smart devices never see it, and anything that doesn't tap Join gets through after 10 minutes |

## Performance

Measured on the network lab ([`dev/netlab.sh`](dev/netlab.sh)) with both firewall backends:

| Measure | Result |
|---|---|
| Memory | 22 to 26 MB RSS |
| Idle CPU | 0 ms over 20 s |
| Cost per query under sustained load | about 16 µs |
| A busy home at about 20 queries per second | about 0.03% of one core |
| One device flooding 1.4 million queries per second | dropped in the kernel; other devices keep resolving with 0 failures and single-digit-ms latency |
| Blocklists | about 535k domains in about 13 MB |

## Configuration reference

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

## Security

- **Accounts.** Passwords are hashed with bcrypt. Two-factor uses TOTP with recovery codes. Sessions have
  idle and absolute timeouts, and login attempts are rate limited.
- **CSRF.** Every write needs a custom header that browsers won't send cross-origin, and the Origin is checked.
- **Dashboard transport.** The dashboard works over HTTP from your own network (private, link-local and
  Tailscale addresses) and over HTTPS from anywhere. Anything else gets a page pointing at the HTTPS address.
  Turn on **Settings → Account → Require HTTPS** to stop plain HTTP on the LAN too. It can only be switched
  on from an HTTPS session, so you can't lock yourself out. Session cookies set over HTTPS are Secure,
  HttpOnly and SameSite Strict. To check the CA before installing it, compare its SHA-256 fingerprint with
  `logread -e 'CA SHA-256'` over SSH.
- **Certificates.** Each router makes its own CA. Fengard restricts signing in software to blocked names and
  the dashboard. Installing this root trusts its private key for any website, so protect the router and CA
  exports. New versions store the key and certificate together in `ca.bundle` (mode 0600). Existing `ca.crt`
  and `ca.key` files are migrated without changing trust and retained for older binaries. After importing a
  different CA, rolling back to an older binary uses the previous legacy identity and requires restoring that
  identity on clients. Include `ca.bundle` in filesystem backups; the dashboard CA export remains portable.
- **DNSSEC.** The DNSSEC setting sends the DO bit to request DNSSEC records. Fengard does not validate
  signatures locally. Validation depends on a trusted validating upstream; use authenticated DoH or DoT
  transport. Plain DNS and an untrusted upstream do not establish validation assurance.
- **History storage.** Detailed queries retain at most 10,000 records and 4 MiB of serialized records by
  default, with the oldest removed transactionally. Audit and alert logs each retain at most 2,000 records and
  512 KiB. Time retention still applies and these budgets can shorten it. Override query budgets with
  `-query-log-records` and `-query-log-mb`. Detailed log writes suspend near the database high-water threshold
  (`-log-file-mb`, default 16 MiB) or free-space reserve (`-log-space-reserve-mb`, default 4 MiB), while aggregate
  counters and critical writes continue. The threshold is an admission guard, not a filesystem quota: bbolt
  pages, transaction overhead and concurrent filesystem activity affect actual allocation. Query stats expose
  `droppedWrites` and `evictedRecords`; alert responses expose dropped counts. Deleting records reuses pages
  but does not shrink an existing database. Back up and compact an oversized legacy database offline with
  enough temporary space; never delete it to recover space because it also contains account and usage state.
- **Kernel enforcement.** Bypass blocking, quarantine and flood limits are firewall rules, so they hold even
  while Fengard is busy.

If you find a security issue, please report it privately through
[GitHub security advisories](https://github.com/masaleem-oss/Fengard/security/advisories/new) rather than an issue.

## Troubleshooting

| Problem | Fix |
|---|---|
| "Can't reach SSH on the router" | Turn on SSH in the router's settings. OpenWrt: **System → Administration → SSH Access**. GL.iNet has it on by default |
| "Router has a new identity" | The router was reset or replaced. Answer yes when the installer asks to forget the old one |
| "Not enough free storage" | Use USB storage with `FG_DIR=/mnt/<usb>/fengard`, or a router with more flash |
| "Another program uses DNS port 53" | Turn off AdGuard Home, unbound or similar in the router's settings and run the installer again |
| Blocklists show only a few hundred domains | The router has no internet yet. Lists download as soon as it does |
| VPN page says WireGuard isn't installed | Install the `kmod-wireguard` and `wireguard-tools` packages. Everything else works without them |
| Phones show "no internet" on Wi-Fi | Check that devices get the router as their DNS server. The installer sets DHCP option 6 for every LAN network |
| Something is wrongly blocked | Use **Check a site**, then add an allow rule or a temporary allow. **Pause protection** turns filtering off for a few minutes |

## FAQ

**Is Fengard a Pi-hole or AdGuard Home alternative?**
Yes. It blocks ads, trackers and malware for the whole network like they do, and adds per-person screen
time, app blocking, a block page and firewall enforcement. See [How it compares](#how-it-compares).

**Can kids get around it with DNS-over-HTTPS, a VPN or a different DNS server?**
Changing DNS doesn't help: all lookups are redirected back to Fengard, and DoH, DoT, DoQ and Tor are blocked
in the router's firewall. Known VPN and proxy services are blocked by the "VPN, proxy & DNS bypass" category.
No filter catches every VPN, so for younger kids pair it with a schedule or a device pause.

**Do I have to flash my router?**
No. If it already runs OpenWrt or GL.iNet firmware, Fengard installs next to it and uninstalls cleanly.
If it runs stock firmware that isn't OpenWrt-based, use computer mode or flash OpenWrt.

**Does it work on GL.iNet routers?**
Yes. GL.iNet firmware is OpenWrt-based, and Fengard is used daily on a GL-MT3000 (Beryl AX). The installer
handles GL.iNet's own web server and firewall setup.

**Will it slow my internet down?**
No. Only DNS lookups go through Fengard, answered in milliseconds and mostly from cache. Everything else is
plain kernel routing. It uses about 25 MB of memory.

**Can it brick my router, or take the internet down?**
It doesn't touch the firmware or bootloader, so there's nothing to brick, and uninstall puts everything back.
For the internet, the installer never moves DNS until Fengard is set to start at boot, and a watchdog hands
DNS to the router's own dnsmasq if Fengard ever stops answering. Both are tested in the
[router lab](#supported-routers), including a power cut mid-install and a firmware upgrade.

**How do updates work?**
Fengard checks GitHub twice a day. When a new release is out, it reads the release's list of supported CPUs,
the oldest OpenWrt it runs on and the memory it needs, and only offers it if this router qualifies. You get
an alert and a banner with **Update now**. It downloads the update, checks its checksum, test-runs it on a
private port and only then swaps it in. If the new version doesn't answer DNS, the old one comes back on
its own. Turn on **Settings → Install updates automatically** to have this happen overnight, between 3 and 5 am.

**Is it free?**
Yes. Fengard is open source under the Apache 2.0 license, with no accounts, cloud or subscription.
Everything stays on your router.

## Development

Requires Go (see [`go.mod`](go.mod)).

```sh
go test ./...                              # unit and integration tests
go build -o bin/fengardd ./cmd/fengardd    # the daemon for this machine
go run ./tools/release -version 1.0.0      # the full kit for every platform, into dist/
```

The tests cover DNS end to end against fake plain and DoH upstreams, policy, local DNS, two-factor, API keys,
web auth and firewall rendering for both backends.

**Try it on Windows.** Build `bin/fengardd.exe`, run `dev\run.ps1`, and open http://127.0.0.1. State lives in
`dev\data`, and `dev\leases.txt` makes this PC appear as a device. To filter this PC's own browsing, run
`dev\test-mode-on.ps1` as Administrator; `dev\test-mode-off.ps1` undoes it. The firewall isn't applied on
Windows; the Firewall page shows the generated ruleset instead.

**Network lab.** [`dev/netlab.sh`](dev/netlab.sh) builds a simulated home network in network namespaces and
checks the real firewall: DNS hijack, DoT blocking, pause at the firewall, port forwarding, WAN exposure and a
DNS flood. It needs root on Linux or WSL2, and `FW_BACKEND=iptables` runs it with the iptables backend.

```sh
GOOS=linux GOARCH=amd64 go build -o bin/linux/fengardd ./cmd/fengardd
GOOS=linux GOARCH=amd64 go build -o bin/linux/dnsq ./tools/dnsq
GOOS=linux GOARCH=amd64 go build -o bin/linux/dnsflood ./tools/dnsflood
sudo FENGARD_ROOT=. bash dev/netlab.sh
```

**Router lab.** [`dev/routerlab/run.sh`](dev/routerlab) installs Fengard on emulated OpenWrt routers of every
CPU type and firmware generation, runs the edge cases, and prints the tables in
[Supported routers](#supported-routers), in a few minutes. It needs root on Linux or WSL2 with KVM and QEMU. See
[CONTRIBUTING](CONTRIBUTING.md#router-lab) for details.

**Releasing.** Run the router lab first and update the [Supported routers](#supported-routers) tables,
adding a new router type or firmware version when you can. Then `go run ./tools/release -version 1.0.0` writes
`dist/assets/`. Attach all of it to a GitHub release: the kit zip, `router-install.sh`, `router-uninstall.sh`,
one `fengardd-linux-<cpu>.gz` per router CPU, and `SHA256SUMS`. The router installer downloads from the latest
release, which is what makes the one-line install work.

## Project layout

| Path | What's there |
|---|---|
| [`cmd/fengardd`](cmd/fengardd) | the daemon: flags and wiring |
| [`install/`](install) | installers: `Install Fengard.cmd` and `fengard-setup.ps1` (Windows), `install.sh` (macOS, Linux), `router-install.sh` and `router-uninstall.sh` (run on the router) |
| [`internal/config`](internal/config) | config model, validation, versioned store with rollback |
| [`internal/catalog`](internal/catalog) | category blocklists, custom lists, app catalog, compact domain set |
| [`internal/policy`](internal/policy) | compiled per-device decision engine |
| [`internal/dnsserver`](internal/dnsserver) | resolver, cache, rate limiting, DoH and DoT upstreams, local answers |
| [`internal/localdns`](internal/localdns) | `.lan` names, records, reverse lookups |
| [`internal/devices`](internal/devices) | IP to device tracking, MAC vendor table, Wake-on-LAN |
| [`internal/firewall`](internal/firewall) | ruleset generation and atomic apply, nftables and iptables |
| [`internal/certs`](internal/certs) | per-router CA: block page and dashboard certificates, Apple profile, export and import |
| [`internal/vpn`](internal/vpn) | WireGuard keys, client profiles, kernel interface, public address detection, Tailscale status |
| [`internal/screentime`](internal/screentime) | minutes online per person and per app, persisted, with bonus minutes |
| [`internal/auth`](internal/auth) | accounts, sessions, TOTP two-factor, API keys |
| [`internal/notify`](internal/notify) | alert delivery to chat apps and webhooks |
| [`internal/web`](internal/web) | API, embedded dashboard (`static/`), asset server, block page |
| [`internal/store`](internal/store), [`querylog`](internal/querylog), [`alerts`](internal/alerts) | embedded database, query history and hourly series, alerts |
| [`tools/`](tools) | `release` (builds the kit), `dnsq` (test lookups), `dnsflood` (resilience test), `memcheck`, `genoui` (vendor table), `wgkey` |
| [`dev/`](dev) | local test scripts, the network lab and the [router lab](dev/routerlab) |

## Contributing

Router test reports are the most useful thing right now: if you try Fengard on a router, please
[tell us how it went](https://github.com/masaleem-oss/Fengard/issues/new?template=router_report.yml).
Bug reports and pull requests are welcome too, see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Fengard is licensed under the [Apache License 2.0](LICENSE). Third-party components are listed in [NOTICE](NOTICE):
IBM Plex Sans and Mono (OFL), Lucide icons (ISC), Simple Icons logos (CC0), qrcode-generator (MIT) and the
IEEE MA-L vendor registry, all bundled so the dashboard works offline. Blocklists aren't distributed with
Fengard; each installation downloads them from their publishers under the publishers' licenses.

<p align="center"><sub>Made by <a href="https://github.com/masaleem-oss">masaleem-oss</a></sub></p>
