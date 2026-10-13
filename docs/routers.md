# Supported routers

[← Back to the README](../README.md)

Every release goes through the [router lab](../dev/routerlab), which boots real OpenWrt images in QEMU for each
CPU type and runs the actual installer on them. Each router is checked for the right CPU build, DNS
answering, catching a DHCP request from the LAN, LAN devices resolving and reaching the dashboard, SSH still working, full blocklists loading,
internet names resolving, lookups over DNS over HTTP/3 to a quic-go server the lab runs itself (so no
network in between can get in the way), and an uninstall that gives DNS back with no Fengard firewall rules
left. The x86 ones get rebooted too.

Results for 1.3.1.2:

| Router type | Firmware | CPU build | Result | Memory |
|---|---|---|---|---|
| ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450) | OpenWrt 22.03.7 | linux-arm64 | ✅ all 10 checks | 28 MB |
| ARM64 (Filogic, MT7622, IPQ807x: GL-MT3000, Flint 2, Linksys E8450) | OpenWrt 25.12.5 | linux-arm64 | ✅ all 10 checks | 23 MB |
| ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300) | OpenWrt 21.02.7 | linux-arm | ✅ all 10 checks | 22 MB |
| ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300) | OpenWrt 23.05.6 | linux-arm | ✅ all 10 checks | 20 MB |
| ARMv7 (IPQ40xx, mvebu: Linksys WRT, GL-B1300) | OpenWrt 24.10.8 | linux-arm | ✅ all 10 checks | 21 MB |
| MIPS big-endian (ath79: TP-Link Archer C7, GL-AR750S) | OpenWrt 22.03.7 | linux-mips | ✅ all 10 checks | 21 MB |
| MIPS little-endian (MT7621: Xiaomi 4A, Netgear R6220, GL-MT1300) | OpenWrt 24.10.8 | linux-mipsle | ✅ all 10 checks | 20 MB |
| MIPS64 big-endian (Octeon: EdgeRouter Lite) | OpenWrt 24.10.8 | linux-mips64 | ✅ all 10 checks | 27 MB |
| MIPS64 little-endian (Loongson) | OpenWrt 25.12.5 | linux-mips64le | ✅ all 7 checks (no internet in the VM) | 18 MB |
| x86 32-bit | OpenWrt 23.05.6 | linux-386 | ✅ all 11 checks | 30 MB |
| x86-64 mini PC | OpenWrt 19.07.10 | linux-amd64 | ✅ all 11 checks | 27 MB |
| x86-64 mini PC | OpenWrt 22.03.7 | linux-amd64 | ✅ all 11 checks | 33 MB |
| x86-64, squashfs like router flash | OpenWrt 24.10.8 | linux-amd64 | ✅ all 11 checks | 33 MB |

On real hardware:

| Router | Firmware | Notes |
|---|---|---|
| GL.iNet GL-MT3000 | GL.iNet 4.7 (OpenWrt 21.02, fw3) | In daily use since 1.0.0, upgraded in place |

Got it running on something else? [Open an issue](https://github.com/masaleem-oss/Fengard/issues/new/choose)
and it'll go in this table.

## When things go wrong

These run in the lab on OpenWrt 24.10 x86 squashfs, which has the same flash layout as a real router (the package
rows also on 21.02 and 25.12, and the GL.iNet 4.9 rows on 21.02):

| Situation | Result |
|---|---|
| 96 MB of RAM (under the 100 MB minimum): refuses cleanly, router untouched | ✅ |
| 128 MB of RAM: full blocklists, no out of memory | ✅ |
| Nearly full flash: refuses cleanly, router untouched | ✅ |
| AdGuard Home on port 53: asks first, left alone when the answer is no | ✅ |
| AdGuard Home on port 53: turned off for Fengard and stays off after a reboot | ✅ |
| Removing Fengard turns AdGuard Home back on | ✅ |
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
| GL.iNet 4.9 style firmware (fw3 with an nft that can't read /dev/stdin, like the Beryl 7): uses iptables and the dashboard opens | ✅ |
| GL.iNet 4.9 style firmware: removed cleanly | ✅ |
| Installed from the ipk on OpenWrt 24.10: works like the installer | ✅ |
| Removed with the ipk on OpenWrt 24.10: router back to stock | ✅ |
| Installed from the apk on OpenWrt 25.12: works like the installer | ✅ |
| Removed with the apk on OpenWrt 25.12: router back to stock | ✅ |
| GL.iNet VPN client DNS on its own port: left alone | ✅ |
| Router settings hand port 53 back to dnsmasq (saving GL.iNet's DNS page does this): Fengard takes it back, DNS and DHCP keep working | ✅ |
| Removed afterwards: VPN client DNS keeps its port, DNS back to dnsmasq | ✅ |
