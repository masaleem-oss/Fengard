# Changelog

What changed in each Fengard release. Versions with a fourth number, like 1.3.1.1, are preview builds that
routers only get with **Settings → Updates → Preview builds** on. Full notes and downloads for every version
are on the [releases page](https://github.com/masaleem-oss/Fengard/releases).

## Unreleased

### Added
- **Guest Wi-Fi, from the new Guest Wi-Fi page.** One switch sets up a Wi-Fi network for visitors that is kept
  off your LAN, with a name you choose and a long random WPA2 password. Guests scan a big QR code to join, and
  **New password** changes it whenever you like. Every new device on it waits on Fengard's "waiting for
  approval" page, even with **Quarantine new devices** off, and you get an alert. Approve, deny or kick
  guests from the page; kicked and denied devices stay off until you approve them. Guests get their own
  **Guests** profile (malware and adult blocked) that you can change on the Profiles page. Router install only
- Guest Wi-Fi uses the router's own guest network when it has one, like GL.iNet's, and only changes its name,
  password and a firewall rule for the approval page. Other routers get a separate network, firewall zone and
  Wi-Fi that Fengard adds. Turning it off or uninstalling Fengard puts back exactly what was there before

## 1.3.4.2 - 2026-11-26 (preview)

### Added
- **Security check.** A new **Security** page takes a baseline of the router and checks it every night around
  2 am, or when you press **Check now**, for anything new or tampered with: installed packages, programs and
  startup scripts changed since the firmware was installed, SSH keys, accounts that can log in, services and
  scheduled jobs, open ports, and port forwards or firewall rules that let the internet in. New changes raise
  one alert, sent to your notification channels like any other, and you approve them one by one or all at
  once. Each of these can be turned off. A firmware upgrade takes a new baseline instead of listing
  everything it changed. It only notices changes, it doesn't undo them, and it runs on routers only

## 1.3.4.1 - 2026-11-26 (preview)

### Added
- Profiles can be moved up and down the list, from the **⋯** menu on a profile
- The DNS traffic chart on the dashboard shows what share of lookups were blocked when you point at a bar

### Fixed
- **"Using HTTP/2, not HTTP/3" alerts on a busy server or a poor connection.** When an HTTP/3 lookup failed
  Fengard blamed HTTP/3 even if HTTP/2 to the same server failed right after, so a slow resolver, a bad
  mobile connection or a reboot before the internet was up moved lookups to HTTP/2 for up to half an hour and
  raised the alert. HTTP/3 now only counts as failing when HTTP/2 to that server works. A new WAN or VPN
  connection also starts with a clean slate, and the DNS page no longer says HTTP/2 after one slow answer
- The kernel log on GL.iNet and other MediaTek routers filled with `RT_PRIV_IOCTL interface is down` every
  20 seconds. The Presence add-on asked every Wi-Fi interface, including switched-off ones, for its clients.
  It now skips interfaces that are down and doesn't poll at all while no device has presence turned on
- The router's upstream gateway, on the WAN side, showed up under **Devices** and got the default profile.
  Only devices on the LAN side are listed now
- Custom blocklists from a restored backup stayed at "Downloading…" until the daily update or a refresh.
  They download straight away now

## 1.3.4 - 2026-11-25

> [!IMPORTANT]
> **On Fengard 1.3.3?** A bug in 1.3.3 stops the Update button installing this release (it says "the release
> isn't signed by the Fengard release key"). The workaround is to go down one version and back up, from the
> dashboard, with no reinstall:
>
> 1. **Settings → Updates → Go back to an older version → 1.3.2 → Go back.** It takes about a minute and keeps
>    your settings, devices and admin account
> 2. **Settings → Updates → Update now** to install this release
>
> With automatic updates on, step 2 happens on its own overnight. Routers on 1.3.2.2 or older update straight
> to this release as normal

### Added
- **Settings → General → Answer for blocked names** picks what blocked names resolve to: Smart (the new
  default, below), always the block page (how it was before), always 0.0.0.0, or "doesn't exist" (NXDOMAIN)

### Changed
- Releases are signed with a new key, and Fengard also trusts a backup key
- The router installer's last message tells Mac users what to do when the dashboard won't open in their
  browser

### Fixed
- **Apps on iPhones and Macs kept asking for Local Network access.** Every blocked name pointed at the block
  page, which lives on a LAN address, so when an ad or tracker inside an app was blocked, iOS and macOS asked
  whether the app could use the local network. Ads & trackers and your own blocklists now get 0.0.0.0 (and ::),
  like Pi-hole and AdGuard Home, so nothing connects and nothing asks. Everything else still shows the block page
- **Devices connected to a GL.iNet router's own WireGuard or OpenVPN server had no DNS.** GL.iNet's VPN server
  zones masquerade like the WAN, so the installer took them for the internet side and Fengard dropped DNS from
  them, and nothing answered on the tunnel address those devices are told to use. Fengard now finds GL.iNet's
  VPN servers while it runs, so updating fixes routers installed before too. Their devices get Fengard's DNS,
  block page and filtering like the rest of the house, and GL.iNet's own settings still decide whether they
  can reach the LAN
- On GL.iNet routers some firewall rules were written for `@wan`, an alias rather than a real interface

## 1.3.3 - 2026-10-24

The full release after 1.3.2. It has everything from the 1.3.2.1 and 1.3.2.2 previews below, plus:

### Changed
- **Signed updates.** Updates from the dashboard now need a signature from the Fengard release key on top of
  the checksum. The key isn't kept on GitHub, so a taken over GitHub account can't ship an update. Going back
  to a release from before this is still checked, against the checksums each one was published with
- The one line install checks the program it downloads against the release's checksums before running it
- History searches from the dashboard run at most two at a time and stop when the page is closed, so a few
  slow searches can't tie up the router

### Fixed
- **Every blocklist update was reported as failed.** URLhaus shut down its hosts file download (it now
  answers 410 Gone), so the malware list always had one failed source and the log never said the lists
  updated. Malware & phishing now uses HaGeZi's threat list on its own
- A custom blocklist URL with a password or token in it could show up in the log and in the name of the
  cached file. Both now leave the login and the query out

## 1.3.2.2 - 2026-10-19 (preview)

### Fixed
- On GL.iNet routers, devices using a VPN client could lose DNS while the router itself still resolved
  names. Installers before 1.3.1.2 also turned off the DNS GL.iNet runs for each VPN client, and updating
  from the dashboard never turned it back on. Fengard now turns it back on when it starts

## 1.3.2.1 - 2026-10-17 (preview)

### Changed
- The memory check before loading blocklists was too careful. It now uses what loading actually took when
  measured (about 2.5 times the list's file size instead of 3, with 16 MB kept free instead of 24) and counts
  memory Fengard already holds but isn't using. The message gives the total for all your lists, not just one
- A list a profile has on that couldn't load now shows a warning at the top of the profile, and Filtering says
  why next to it instead of only in a tooltip

### Fixed
- On GL.iNet routers, turning off GL.iNet's AdGuard Home during install only stopped it. GL.iNet's own switch
  for it stayed on, which can leave DNS pointing at AdGuard Home after a reboot or when a VPN client comes up.
  The installer now turns the switch off too and removing Fengard turns it back on. Routers installed before
  get the switch turned off once when Fengard starts
- An HTTP/3 alert could show an IPv6 error on a network without IPv6. Addresses the router can't send to are
  now skipped straight away, so the alert shows why HTTP/3 over IPv4 failed

## 1.3.2 - 2026-10-16

The full release after 1.3.1. It has everything from the 1.3.1.1 to 1.3.1.5 previews below, plus:

### Added
- **DNS servers per profile.** A profile can ask its own resolvers, like Cloudflare for Families for Kids, while
  everyone else keeps the ones on the DNS page. Blocklists and rules still apply first
- An alert when a resolver keeps failing over HTTP/3 and lookups to it go back to HTTP/2
- Fengard checks free memory before loading blocklists. A custom list that won't fit isn't loaded, says how
  much it needs, and the rest still load. Loading the HaGeZi TIF list (1.57M domains) peaks around 60 MB
- If the router ran out of memory and killed Fengard while it loaded the lists, the custom lists stay off on the
  next start instead of crashing it again, with an alert. Changing the lists tries them again
- Network-wide rules show 8 at a time with Show all, and a box to find one

### Changed
- The program is about 13% smaller: 11.7 MB instead of 13.4 MB on ARM64, 13.7 MB instead of 15.6 MB on MIPS.
  The block page, captive portal and screen time page no longer use Go's html/template, which kept a lot of
  unused code in the program
- New custom lists start off in every profile and take no memory until a profile turns them on. Filtering
  still shows how many domains they have

### Fixed
- DNS stopped answering for a while after a router VPN client like GL.iNet's OpenVPN client came up. Fengard
  kept sending lookups over connections that went the old way. It now opens fresh ones when the network changes,
  and an HTTP/2 connection that stops answering gets dropped in seconds instead of about 15 minutes
- Making a profile called IoT failed because the built in Smart home profile already had that id

## 1.3.1.5 - 2026-10-14 (preview)

### Added
- **Custom lists per profile.** Lists added in Filtering show up as tiles in each profile, marked Custom, and
  each profile turns them on or off. They used to block for everyone. Lists you already had stay on in every
  profile, and a new list starts on everywhere
- Each profile shows how many blocklist domains it blocks out of all of them, like 156K of 566K, each domain
  counted once. Filtering shows the total too, which is what decides the memory blocklists use

### Fixed
- Sites could load only partly through a router VPN client, like GL.iNet's OpenVPN or WireGuard client.
  Fengard dropped packets that connection tracking marked invalid on all traffic, and hardware acceleration can
  make normal packets look invalid. It now only does that for traffic coming in from the internet, and OpenWrt
  itself doesn't drop them by default either
- On iptables routers the firewall rules could fail to apply when a VPN client came up at the same moment,
  with "Another app is currently holding the xtables lock" in the log. Fengard now waits for the lock

## 1.3.1.4 - 2026-10-13 (preview)

### Added
- **Add-ons.** The VPN, manufacturer names, speed test, device check, who's home and the captive portal can each
  be installed or removed in **Settings → Add-ons**, and setup asks which ones you want. One that isn't installed
  doesn't run and takes no memory or space. They download from the release (under 1 KB to 332 KB each), or come from
  the kit when the router has no internet. Pages of add-ons that aren't installed show in the sidebar with a
  download icon and their size
- **Customise the overview.** Drag cards around, make them wider or narrower, remove them or add them back.
  The layout is saved on the router for each user, so it's the same on your phone and your computer

### Changed
- Manufacturer names take 0.65 MB of memory instead of 2.4 MB, and none at all without the add-on
- Existing installs keep every add-on when they update, nothing gets removed on its own

### Fixed
- The DNS page didn't load with HTTP/3 on and no `https://` resolver

## 1.3.1.3 - 2026-10-13 (preview)

### Added
- **DNS → Ask all at once** sends each lookup to every resolver at the same time and uses the first good answer
- The installer offers to turn off GL.iNet's AdGuard Home when it runs beside DNS, where it only took up memory. Removing Fengard turns it back on

### Changed
- About 19 MB instead of 24 MB on a GL-MT3000 just after starting, with a 45 MB peak instead of 52 MB
- Blocklists take about a third less memory: 535k domains in 6.9 MB instead of 10.7 MB, and loading them peaks at 23 MB instead of 33 MB
- A device flooding DNS can no longer push Fengard's memory up: about 24 MB after a flood instead of 64 MB

### Fixed
- With HTTP/3 on, resolvers after the first showed no status and looked unchecked; they now say they're standbys
- Uninstalling on a slow router could leave a firewall rule behind

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
