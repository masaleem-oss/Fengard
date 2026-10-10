// some vendor router kernels break mptcp so every listener just hangs
//
//go:debug multipathtcp=0
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/netutil"

	// routers often have no ca bundle so tls downloads need these
	_ "golang.org/x/crypto/x509roots/fallback"
	// routers have no timezone database so named zones in settings would not load
	_ "time/tzdata"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/certs"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/dnsserver"
	"github.com/masaleem-oss/Fengard/internal/firewall"
	"github.com/masaleem-oss/Fengard/internal/internet"
	"github.com/masaleem-oss/Fengard/internal/localdns"
	"github.com/masaleem-oss/Fengard/internal/netscan"
	"github.com/masaleem-oss/Fengard/internal/notify"
	"github.com/masaleem-oss/Fengard/internal/policy"
	"github.com/masaleem-oss/Fengard/internal/presence"
	"github.com/masaleem-oss/Fengard/internal/querylog"
	"github.com/masaleem-oss/Fengard/internal/screentime"
	"github.com/masaleem-oss/Fengard/internal/store"
	"github.com/masaleem-oss/Fengard/internal/sysinfo"
	"github.com/masaleem-oss/Fengard/internal/traffic"
	"github.com/masaleem-oss/Fengard/internal/update"
	"github.com/masaleem-oss/Fengard/internal/vpn"
	"github.com/masaleem-oss/Fengard/internal/web"
	"github.com/masaleem-oss/Fengard/internal/welcome"
)

var version = "0.5.0-dev"

func main() {
	var (
		dnsAddr      = flag.String("dns", ":53", "comma-separated DNS listen addresses")
		httpAddr     = flag.String("http", ":80", "dashboard and block page listen address")
		httpsAddr    = flag.String("https", ":443", "HTTPS dashboard and block page listen address (empty to disable)")
		blockIP      = flag.String("block-ip", "", "LAN IPv4 address blocked domains resolve to (the block page and dashboard; default: this machine's LAN address)")
		dnsIP        = flag.String("dns-ip", "", "LAN IPv4 address devices' DNS is redirected to (default: the block IP)")
		blockIP6     = flag.String("block-ip6", "", "this box's LAN IPv6 address (optional)")
		dataDir      = flag.String("data", "/etc/fengard", "directory for persistent state")
		leases       = flag.String("leases", "/tmp/dhcp.leases", "dnsmasq DHCP lease file")
		hosts        = flag.String("dashboard-hosts", "fengard.lan", "comma-separated hostnames that open the dashboard")
		lan          = flag.String("lan", "br-lan", "comma-separated LAN interfaces")
		wan          = flag.String("wan", "wan,eth0", "comma-separated WAN interfaces")
		fwOn         = flag.Bool("firewall", false, "apply firewall rules (Linux router only)")
		fwBackend    = flag.String("firewall-backend", "auto", "auto, nftables or iptables")
		netns        = flag.String("netns", "", "apply firewall rules inside this network namespace (testing)")
		harden       = flag.Bool("harden", false, "set kernel network hardening options")
		logMB        = flag.Int("query-log-mb", 4, "query history JSON budget in MiB")
		logRecords   = flag.Int("query-log-records", 10000, "maximum retained detailed queries")
		logFileMB    = flag.Int("log-file-mb", 16, "database high-water threshold in MiB for suspending log writes")
		logReserveMB = flag.Int("log-space-reserve-mb", 4, "free-space reserve in MiB for critical state")
		memMB        = flag.Int("mem-limit", 96, "soft memory limit in MB")
		noUpdate     = flag.Bool("no-list-updates", false, "don't download blocklists (use cached copies only)")
		updateURL    = flag.String("update-url", "https://api.github.com/repos/masaleem-oss/Fengard/releases/latest", "where to check for new Fengard releases (empty to turn checks off)")
		speedURL     = flag.String("speedtest-url", "", "your own speed test server with /__down and /__up (default: fast.com, then LibreSpeed)")
	)
	flag.Parse()
	if *logMB < 1 || *logRecords < 1 || *logFileMB < *logMB+4 || *logReserveMB < 1 {
		log.Fatal("invalid logging storage limits")
	}
	if z := sysinfo.UseRouterTimezone(); z != "" {
		log.Printf("timezone %s from the router settings", z)
	}
	if *blockIP == "" {
		*blockIP = lanIPv4()
	}
	debug.SetMemoryLimit(int64(*memMB) << 20)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)

	ip := net.ParseIP(*blockIP)
	if ip == nil || ip.To4() == nil {
		log.Fatalf("invalid -block-ip %q", *blockIP)
	}
	var ip6 net.IP
	if *blockIP6 != "" {
		if ip6 = net.ParseIP(*blockIP6); ip6 == nil || ip6.To4() != nil {
			log.Fatalf("invalid -block-ip6 %q", *blockIP6)
		}
	}
	if *dnsIP == "" {
		*dnsIP = *blockIP
	}
	if net.ParseIP(*dnsIP) == nil {
		log.Fatalf("invalid -dns-ip %q", *dnsIP)
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatal(err)
	}

	var workers sync.WaitGroup
	run := func(fn func()) {
		workers.Add(1)
		go func() { defer workers.Done(); fn() }()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(filepath.Join(*dataDir, "fengard.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	closeDB := true
	defer func() {
		if closeDB {
			db.Close()
		}
	}()
	db.SetStorageBudget(int64(*logFileMB)<<20, int64(*logReserveMB)<<20)
	queryDB := must(db.Log("queries", store.LogLimits{MaxRecords: *logRecords, MaxBytes: int64(*logMB) << 20}))
	auditDB := must(db.Log("audit"))
	alertDB := must(db.Log("alerts"))
	users := must(db.KV("users"))
	keys := must(db.KV("apikeys"))
	prefs := must(db.KV("prefs"))
	hoursKV := must(db.KV("hours"))
	screenKV := must(db.KV("screentime"))
	welcomeKV := must(db.KV("welcome"))

	cfgStore, err := config.Open(*dataDir)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	alrt := alerts.New(alertDB)
	notifier := notify.New(cfgStore.Get().Settings.BoxName)
	alrt.OnRaise = notifier.Notify
	qlog := querylog.New(5000, queryDB, hoursKV)

	engine := policy.New()
	screen := screentime.New(screenKV, location(cfgStore.Get().Settings.Timezone))
	engine.SetUsage(screen)
	initialSources := customSources(cfgStore.Get())
	cat := catalog.New(filepath.Join(*dataDir, "lists"), initialSources...)
	fwKick := make(chan struct{}, 1)
	kick := func() {
		select {
		case fwKick <- struct{}{}:
		default:
		}
	}
	configKick := make(chan struct{}, 1)
	cat.OnChange = func() {
		// swap now so the old set can be freed, the reconcile below catches config changes
		engine.SetDomains(cat.Set())
		select {
		case configKick <- struct{}{}:
		default:
		}
		kick()
		// give the list loading garbage back to the os now
		go debug.FreeOSMemory()
	}
	// lists take like 30s to load on a router so dns and dashboard start first
	listsReady := make(chan struct{})
	go func() {
		defer close(listsReady)
		if err := cat.Load(); err != nil {
			log.Printf("blocklists: %v", err)
		}
		log.Printf("blocklists: %d domains loaded from cache", cat.Set().Len())
	}()

	tracker := devices.NewTracker(*leases)
	newDevices := make(chan devices.Info, 256)
	tracker.OnNew = func(mac, hostname, ip string) {
		select {
		case newDevices <- devices.Info{MAC: mac, Hostname: hostname, IP: ip}:
		default: // too many new macs at once next scan gets them
		}
	}

	local := localdns.New(tracker)
	gate := welcome.New(cfgStore, welcomeKV)
	dns := &dnsserver.Server{
		Policy: engine, Devices: tracker, Log: qlog, Local: local,
		BlockIP: ip, BlockIPv6: ip6, LocalNames: split(*hosts), Screen: screen,
		Welcome: gate.Want,
	}
	dns.Init()

	vpnMgr := &vpn.Manager{DataDir: *dataDir, WAN: split(*wan)}
	vpnMgr.Detect()

	lastLists := fmt.Sprint(initialSources)
	cfgStore.Subscribe(func(c *config.Config) {
		// only rebuild the domain set when the lists actually change
		src := customSources(c)
		if sig := fmt.Sprint(src); sig != lastLists {
			lastLists = sig
			cat.SetCustom(src)
		}
		screen.SetLocation(location(c.Settings.Timezone))
		engine.Rebuild(c, cat.Set())
		dns.Configure(c.Settings)
		local.Rebuild(c)
		notifier.Configure(c.Settings.BoxName, c.Channels)
		macs := make([]string, len(c.Devices))
		for i, d := range c.Devices {
			macs[i] = d.MAC
		}
		tracker.Known(macs)

		// tunnels only carry ipv4
		var v4only []netip.Prefix
		if c.VPN.Enabled {
			if pfx, err := netip.ParsePrefix(c.VPN.Subnet); err == nil {
				v4only = append(v4only, pfx)
			}
		}
		if vpn.TailscaleSupported() {
			v4only = append(v4only, netip.MustParsePrefix(vpn.TailscaleCGNAT))
		}
		dns.SetIPv4Only(v4only)
		if err := vpnMgr.Apply(c.VPN); err != nil {
			log.Printf("vpn: %v", err)
		}
		tunnelClients(ctx, cfgStore, tracker)
		kick()
	})
	run(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-configKick:
				if ctx.Err() != nil {
					return
				}
				cfgStore.Reconcile()
			}
		}
	})
	if vpn.TailscaleSupported() {
		run(func() {
			tk := time.NewTicker(15 * time.Second)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					tunnelClients(ctx, cfgStore, tracker)
				}
			}
		})
	}
	run(func() { recordNewDevices(ctx, newDevices, cfgStore, alrt) })

	ca, err := certs.LoadOrCreate(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	ca.Allowed = func(host string) bool {
		// only sign names a device is actually blocked from
		return stops(engine.Decide("", host, time.Now())) || anyDeviceBlocked(cfgStore, engine, host)
	}
	ca.DashboardNames = split(*hosts)
	ca.DashboardIPs = []net.IP{ip}
	if ip6 != nil {
		ca.DashboardIPs = append(ca.DashboardIPs, ip6)
	}
	log.Printf("certificate authority: %s", ca.Name())
	log.Printf("CA SHA-256: %s", ca.Fingerprint())

	fw := &firewall.Manager{Enabled: *fwOn, Netns: *netns}
	switch *fwBackend {
	case "iptables":
		fw.Backend = firewall.NewIPTables(*netns)
	case "nftables", "nft":
		fw.Backend = &firewall.NFTables{Netns: *netns}
	default:
		if *fwOn {
			fw.Backend = firewall.Detect(*netns)
		} else {
			fw.Backend = &firewall.NFTables{Netns: *netns}
		}
	}
	fw.Inputs = func() (firewall.Params, error) {
		c := cfgStore.Get()
		now := time.Now()
		q, off := engine.Offline(now)
		// non mac ids are vpn peers so firewall matches them by tunnel ip
		var seen []string
		for mac := range tracker.Seen() {
			seen = append(seen, mac)
		}
		tor := engine.CategoryMACs("tor", seen, now)
		var tunnels []firewall.VPNParams
		if c.VPN.Enabled && vpn.Supported() {
			tunnels = append(tunnels, firewall.VPNParams{Iface: vpn.Iface, Port: c.VPN.Port, Subnet: c.VPN.Subnet})
		}
		if vpn.TailscaleSupported() {
			tunnels = append(tunnels, firewall.VPNParams{Iface: vpn.TailscaleIface, Subnet: vpn.TailscaleCGNAT})
		}
		return firewall.Params{
			LAN: split(*lan), WAN: split(*wan),
			RouterIPv4: *dnsIP, RouterIPv6: *blockIP6,
			ServicePorts: []int{53, 80, 443, port(*httpAddr), port(*httpsAddr)},
			BlockIP:      *blockIP, HTTPPort: port(*httpAddr), HTTPSPort: port(*httpsAddr),
			BlockBypass:  c.Settings.BlockBypass,
			DoHIPs:       cat.DoHIPs(),
			PortForwards: c.PortForwards,
			Quarantined:  q, Offline: off, OfflineIPs: tunnelIPs(tracker, off),
			TorIPs: cat.TorIPs(), TorMACs: tor, TorSrcIPs: tunnelIPs(tracker, tor),
			Tunnels: tunnels,
			// kernel cap sits above the dns per device limit its just for floods
			DNSPerSecond: c.Settings.ClientRateQPS * 2,
		}, nil
	}
	if *harden {
		firewall.Harden()
	}

	go func() {
		for range resyncSignals() {
			log.Printf("firewall: re-sync requested")
			kick()
		}
	}()
	run(func() { tracker.Run(ctx, 15*time.Second) })
	run(func() { screen.Run(ctx) })
	run(func() { fw.Run(ctx, 30*time.Second, fwKick) })
	if !*noUpdate {
		go func() {
			select {
			case <-listsReady:
				cat.Run(ctx, 24*time.Hour)
			case <-ctx.Done():
			}
		}()
	}
	run(func() { maintain(ctx, cfgStore, qlog, alrt, auditDB, dns) })

	run(func() {
		log.Printf("DNS listening on %s", *dnsAddr)
		if err := dns.ListenAndServe(ctx, split(*dnsAddr)...); err != nil {
			log.Fatalf("DNS: %v", err)
		}
	})

	cpu := &sysinfo.Sampler{}
	run(func() { cpu.Run(5*time.Second, ctx.Done()) })

	updater := &update.Updater{
		API: *updateURL, Current: version, Client: cat.HTTPClient(),
		Auto: func() bool { return cfgStore.Get().Settings.AutoUpdate },
		Loc:  func() *time.Location { return location(cfgStore.Get().Settings.Timezone) },
		OnNotice: func(kind, title, detail string) {
			sev := alerts.Info
			if kind == "update_failed" {
				sev = alerts.Warning
			}
			alrt.Raise(kind+":"+title, 0, alerts.Alert{Kind: kind, Severity: sev, Title: title, Detail: detail})
		},
	}
	updater.Status()
	if *updateURL != "" {
		run(func() { updater.Run(ctx) })
	}

	routerLoc := func() *time.Location { return location(cfgStore.Get().Settings.Timezone) }
	named := func(mac string) string { return deviceName(cfgStore.Get(), tracker, mac) }
	inet := &internet.Monitor{
		Speeds:   must(db.Log("speedtests", store.LogLimits{MaxRecords: 200, MaxBytes: 64 << 10})),
		Outages:  must(db.Log("outages", store.LogLimits{MaxRecords: 200, MaxBytes: 64 << 10})),
		Daily:    func() bool { return !cfgStore.Get().Settings.SpeedTestOff },
		Location: routerLoc, Server: *speedURL,
		OnOutage: func(o internet.Outage) {
			loc := routerLoc()
			alrt.Raise("outage:"+o.Start.String(), 0, alerts.Alert{Kind: "internet_down", Severity: alerts.Warning,
				Title:  "Internet was down " + roughly(o.End.Sub(o.Start)),
				Detail: "From " + o.Start.In(loc).Format("3:04 pm") + " to " + o.End.In(loc).Format("3:04 pm") + ". Local names kept working the whole time."})
		},
	}
	run(func() { inet.Run(ctx) })

	meter := &traffic.Meter{
		KV: must(db.KV("traffic")), Location: routerLoc,
		Lookup: func(ip string) (string, bool) { info, ok := tracker.Lookup(ip); return info.MAC, ok && info.MAC != "" },
	}
	run(func() { meter.Run(ctx) })

	home := &presence.Watcher{
		Tracked: func() []string {
			var macs []string
			for _, d := range cfgStore.Get().Devices {
				if d.Presence {
					macs = append(macs, d.MAC)
				}
			}
			return macs
		},
		OnChange: func(mac string, arrived bool, at time.Time) {
			what, kind := "left home", "left_home"
			if arrived {
				what, kind = "got home", "arrived_home"
			}
			alrt.Raise(kind+":"+mac+at.String(), 0, alerts.Alert{Kind: kind, Severity: alerts.Info, MAC: mac,
				Title: named(mac) + " " + what, Detail: "At " + at.In(routerLoc()).Format("3:04 pm") + "."})
		},
	}
	run(func() { home.Run(ctx) })

	scanner := &netscan.Scanner{
		KV: must(db.KV("netscan")),
		Targets: func() []netscan.Target {
			var out []netscan.Target
			lans := lanNets(split(*lan))
			for mac, s := range tracker.Seen() {
				if time.Since(s.LastSeen) > time.Hour || strings.HasPrefix(mac, "vpn:") || strings.HasPrefix(mac, "ts:") {
					continue
				}
				for _, ip := range s.IPs {
					if a, err := netip.ParseAddr(ip); err == nil && a.Is4() && inAny(lans, a) {
						out = append(out, netscan.Target{MAC: mac, IP: ip})
						break
					}
				}
			}
			return out
		},
		OnFinding: func(mac string, f netscan.Finding) {
			alrt.Raise("risk:"+mac+":"+strconv.Itoa(f.Port), 0, alerts.Alert{Kind: "risky_device", Severity: alerts.Warning, MAC: mac,
				Title: f.Title + " on " + named(mac), Detail: f.Detail})
		},
	}
	run(func() { scanner.Run(ctx) })

	authn := auth.New(users, keys)
	authn.Issuer = "Fengard (" + cfgStore.Get().Settings.BoxName + ")"
	webSrv := &web.Server{
		Context: ctx,
		CA:      ca, Config: cfgStore, Catalog: cat, Policy: engine, Log: qlog,
		Devices: tracker, DNS: dns, Firewall: fw,
		Auth: authn, Alerts: alrt, Audit: auditDB, Prefs: prefs, Notifier: notifier,
		CPU: cpu, VPN: vpnMgr, Screen: screen, Version: version, Updater: updater,
		DashboardHosts: split(*hosts), Started: time.Now(),
		Welcome: gate, Internet: inet, Traffic: meter, Presence: home, Scanner: scanner,
		ProbeURL: probeURL(*blockIP, *httpsAddr, *fwOn),
	}
	handler := webSrv.Handler()
	servers := []*http.Server{}

	if *httpsAddr != "" {
		srv := newHTTPServer(*httpsAddr, handler)
		srv.BaseContext = func(net.Listener) context.Context { return ctx }
		srv.TLSConfig = &tls.Config{GetCertificate: ca.GetCertificate, MinVersion: tls.VersionTLS12}
		srv.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are normal here
		servers = append(servers, srv)
		go serve(srv, true)
	}
	srv := newHTTPServer(*httpAddr, handler)
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	servers = append(servers, srv)
	go serve(srv, false)

	if webSrv.Auth.NeedsSetup() {
		log.Printf("first run: open http://%s/ to create the admin account", *blockIP)
	}

	<-ctx.Done()
	log.Printf("shutting down")
	// procd kills us 5s after asking so the important stuff goes first
	shutdown, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	httpCtx, httpCancel := context.WithTimeout(shutdown, time.Second)
	for _, s := range servers {
		if err := s.Shutdown(httpCtx); err != nil {
			s.Close()
		}
	}
	httpCancel()
	persisted := make(chan struct{})
	go func() {
		defer close(persisted)
		if err := screen.Flush(); err != nil {
			log.Printf("final screen time persistence: %v", err)
		}
		if err := qlog.Close(shutdown); err != nil {
			log.Printf("query log shutdown: %v", err)
		}
	}()
	select {
	case <-persisted:
	case <-shutdown.Done():
		closeDB = false
		log.Printf("shutdown deadline reached during final persistence: %v", shutdown.Err())
	}
	// no daemon means no dns so hand the network back to the routers firewall
	if err := fw.Remove(); err != nil {
		log.Printf("firewall cleanup: %v", err)
	}
	vpnMgr.Down()
	if err := webSrv.CloseWorkers(shutdown); err != nil {
		log.Printf("dashboard background shutdown: %v", err)
		closeDB = false
	}
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	select {
	case <-joined:
		// catches notes made while dns was still winding down
		if err := screen.Flush(); err != nil {
			log.Printf("final screen time persistence: %v", err)
		}
	case <-shutdown.Done():
		closeDB = false
		log.Printf("shutdown deadline reached before workers stopped: %v", shutdown.Err())
	}
}

func location(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}

func tunnelIPs(t *devices.Tracker, ids []string) []string {
	var out []string
	for _, id := range ids {
		if strings.HasPrefix(id, "vpn:") || strings.HasPrefix(id, "ts:") {
			out = append(out, t.IPsOf(id)...)
		}
	}
	return out
}

func tunnelClients(ctx context.Context, cs *config.Store, t *devices.Tracker) {
	c := cs.Get()
	static := map[string]devices.Info{}
	if c.VPN.Enabled {
		for _, p := range c.VPN.Peers {
			static[p.IP] = devices.Info{MAC: p.Identity(), IP: p.IP, Hostname: p.Name}
		}
	}
	if vpn.TailscaleSupported() {
		for _, p := range vpn.TailscaleStatus(ctx).Peers {
			for _, ip := range p.IPs {
				static[ip] = devices.Info{MAC: p.Identity(), IP: ip, Hostname: p.Name}
			}
		}
	}
	t.SetStatic(static)
}

// 443 gets redirected to the https listener when the firewall is on
func probeURL(blockIP, httpsAddr string, fwOn bool) string {
	if httpsAddr == "" {
		return ""
	}
	p := port(httpsAddr)
	if p == 443 || fwOn {
		return "https://" + blockIP + "/__fengard/ping"
	}
	return fmt.Sprintf("https://%s:%d/__fengard/ping", blockIP, p)
}

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func serve(srv *http.Server, tlsOn bool) {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Fatalf("listen %s: %v", srv.Addr, err)
	}
	ln = netutil.LimitListener(ln, 256)
	kind := "dashboard"
	if tlsOn {
		kind = "HTTPS block page"
		err = srv.ServeTLS(ln, "", "")
	} else {
		log.Printf("%s listening on %s", kind, srv.Addr)
		err = srv.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("%s: %v", kind, err)
	}
}

func recordNewDevices(ctx context.Context, ch <-chan devices.Info, cs *config.Store, al *alerts.Alerts) {
	for {
		var batch []devices.Info
		select {
		case <-ctx.Done():
			return
		case d := <-ch:
			batch = append(batch, d)
		}
		// batch whatever else comes in right after into one write
		timeout := time.After(500 * time.Millisecond)
	gather:
		for len(batch) < 64 {
			select {
			case d := <-ch:
				batch = append(batch, d)
			case <-timeout:
				break gather
			}
		}
		var added []devices.Info
		quarantine := false
		_, err := cs.Update("system", fmt.Sprintf("Discovered %d new device(s)", len(batch)), func(c *config.Config) error {
			quarantine = c.Settings.QuarantineNew
			for _, d := range batch {
				if c.Device(d.MAC) != nil {
					continue
				}
				c.Devices = append(c.Devices, config.Device{
					MAC: d.MAC, Hostname: d.Hostname, Group: c.Settings.DefaultGroup,
					Approved: !c.Settings.QuarantineNew, FirstSeen: time.Now(),
				})
				added = append(added, d)
			}
			if len(added) == 0 {
				return errNothingNew
			}
			return nil
		})
		if err != nil && !errors.Is(err, errNothingNew) {
			log.Printf("recording new devices: %v", err)
			continue
		}
		for _, d := range added {
			name := d.Hostname
			if name == "" {
				name = d.MAC
			}
			a := alerts.Alert{Kind: "new_device", Severity: alerts.Info, MAC: d.MAC,
				Title: "New device joined: " + name, Detail: "IP " + d.IP}
			if quarantine {
				a.Severity, a.Detail = alerts.Warning, a.Detail+" · waiting for approval"
			}
			al.Raise("new:"+d.MAC, time.Hour, a)
		}
	}
}

var errNothingNew = errors.New("no new devices")

func maintain(ctx context.Context, cs *config.Store, ql *querylog.Log, al *alerts.Alerts, audit *store.Log, dns *dnsserver.Server) {
	prune := func() {
		ql.Prune(cs.Get().Settings.LogRetentionDay)
		al.Prune(90)
		audit.Prune(time.Now().AddDate(-1, 0, 0))
	}
	prune()
	lastLimited, lastUp, lastErr := uint64(0), uint64(0), uint64(0)
	minute := time.NewTicker(time.Minute)
	hour := time.NewTicker(time.Hour)
	defer minute.Stop()
	defer hour.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hour.C:
			prune()
		case <-minute.C:
			c := &dns.Counters
			limited, up, upErr := c.RateLimited.Load(), c.Upstream.Load(), c.UpstreamErr.Load()
			if limited-lastLimited > 1000 {
				al.Raise("dns-flood", 15*time.Minute, alerts.Alert{Kind: "dns_flood", Severity: alerts.Critical,
					Title: "DNS flood detected", Detail: fmt.Sprintf("%d queries rate-limited in the last minute", limited-lastLimited)})
			}
			if tries := up - lastUp; tries > 10 && upErr-lastErr == tries {
				al.Raise("upstream-down", 15*time.Minute, alerts.Alert{Kind: "upstream_down", Severity: alerts.Critical,
					Title: "Upstream DNS unreachable", Detail: "Answering from cache until it recovers"})
			}
			lastLimited, lastUp, lastErr = limited, up, upErr
		}
	}
}

func anyDeviceBlocked(cs *config.Store, e *policy.Engine, host string) bool {
	now := time.Now()
	for _, d := range cs.Get().Devices {
		if stops(e.Decide(d.MAC, host, now)) {
			return true
		}
	}
	return false
}

func stops(d policy.Decision) bool {
	return d.Action == policy.Block || d.Action == policy.Paused || d.Action == policy.Quarantine
}

func port(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ye udp dial sends nothing it just asks the kernel which local ip routes out
func lanIPv4() string {
	c, err := net.Dial("udp4", "192.0.2.1:53")
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func customSources(c *config.Config) []catalog.CustomSource {
	var sources []catalog.CustomSource
	for _, list := range c.Lists {
		if list.Enabled {
			sources = append(sources, catalog.CustomSource{ID: list.ID, Name: list.Name, URL: list.URL})
		}
	}
	return sources
}

// the name people gave a device, else what it calls itself
func deviceName(c *config.Config, t *devices.Tracker, mac string) string {
	if d := c.Device(mac); d != nil && d.Name != "" {
		return d.Name
	}
	if s, ok := t.Seen()[mac]; ok && s.Hostname != "" {
		return s.Hostname
	}
	if d := c.Device(mac); d != nil && d.Hostname != "" {
		return d.Hostname
	}
	return mac
}

// 14 min or 2 h 5 min, good enough for an alert title
func roughly(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	if m < 60 {
		return strconv.Itoa(max(m, 1)) + " min"
	}
	if m%60 == 0 {
		return strconv.Itoa(m/60) + " h"
	}
	return strconv.Itoa(m/60) + " h " + strconv.Itoa(m%60) + " min"
}

// the subnets on our own lan side so a repeater doesnt scan the network upstream
func lanNets(ifaces []string) []netip.Prefix {
	var out []netip.Prefix
	for _, name := range ifaces {
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
				out = append(out, p.Masked())
			}
		}
	}
	return out
}

// no lan interfaces found means a computer install so any private address will do
func inAny(nets []netip.Prefix, a netip.Addr) bool {
	if len(nets) == 0 {
		return a.IsPrivate()
	}
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
