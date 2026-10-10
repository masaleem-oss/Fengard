// package web is the dashboard and api and the block page
// blocked domains resolve to this box so any host thats not the dashboard gets the block page
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/certs"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/dnsserver"
	"github.com/masaleem-oss/Fengard/internal/firewall"
	"github.com/masaleem-oss/Fengard/internal/notify"
	"github.com/masaleem-oss/Fengard/internal/policy"
	"github.com/masaleem-oss/Fengard/internal/querylog"
	"github.com/masaleem-oss/Fengard/internal/ratelimit"
	"github.com/masaleem-oss/Fengard/internal/screentime"
	"github.com/masaleem-oss/Fengard/internal/store"
	"github.com/masaleem-oss/Fengard/internal/sysinfo"
	"github.com/masaleem-oss/Fengard/internal/update"
	"github.com/masaleem-oss/Fengard/internal/vpn"
)

//go:embed static
var staticFS embed.FS

//go:embed blockpage.html
var blockPageHTML string

//go:embed portal.html
var portalHTML string

var portalTmpl = template.Must(template.New("portal").Parse(portalHTML))

var blockTmpl = template.Must(template.New("block").Parse(blockPageHTML))

type Server struct {
	Context      context.Context
	workMu       sync.Mutex
	workWG       sync.WaitGroup
	workerCtx    context.Context
	workerCancel context.CancelFunc
	stopping     bool
	CA           *certs.Authority
	Config       *config.Store
	Catalog      *catalog.Catalog
	Policy       *policy.Engine
	Log          *querylog.Log
	Devices      *devices.Tracker
	DNS          *dnsserver.Server
	Firewall     *firewall.Manager
	Auth         *auth.Auth
	Alerts       *alerts.Alerts
	Audit        *store.Log
	Prefs        *store.KV // per user dashboard state like alerts read up to
	Notifier     *notify.Notifier
	CPU          *sysinfo.Sampler
	VPN          *vpn.Manager
	Screen       *screentime.Tracker
	Updater      *update.Updater
	Version      string

	// https url signed by our ca the dashboard fetches it to see if the browser trusts the cert
	ProbeURL string

	DashboardHosts []string // hostnames that reach the dashboard eg fengard.lan
	Started        time.Time

	loginLimit   *ratelimit.Limiter
	requestLimit *ratelimit.Limiter
}

const sessionCookie = "fengard_session"

func (s *Server) Handler() http.Handler {
	s.initWorkers()
	s.loginLimit = ratelimit.New(5.0/60, 5, 1024)    // 5 tries then 1 per 12s per ip
	s.requestLimit = ratelimit.New(1.0/120, 3, 1024) // block page access requests per device
	static, _ := fs.Sub(staticFS, "static")
	assets := newAssetServer(static)

	mux := http.NewServeMux()
	// public
	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("POST /api/setup", s.mutating(s.setup))
	mux.HandleFunc("POST /api/login", s.mutating(s.login))
	mux.HandleFunc("POST /api/login/2fa", s.mutating(s.login2FA))
	mux.HandleFunc("POST /api/logout", s.mutating(s.logout))
	mux.HandleFunc("GET /fengard-ca.crt", s.caCert)
	mux.HandleFunc("GET /fengard-ca.der", s.caCertDER)
	mux.HandleFunc("GET /fengard.mobileconfig", s.caMobileConfig)

	// read only any signed in user
	view := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.require(auth.Viewer, h)) }
	view("GET /api/overview", s.overview)
	view("GET /api/queries", s.queries)
	view("GET /api/devices", s.listDevices)
	view("GET /api/devices/{mac}/summary", s.deviceSummary)
	view("GET /api/certificate", s.certificateInfo)
	view("GET /api/vpn", s.vpnInfo)
	view("GET /api/screentime", s.screenTime)
	mux.HandleFunc("GET /api/vpn/peers/{id}/config", s.require(auth.Admin, s.vpnPeerConfig))
	mux.HandleFunc("POST /api/alerts/read", s.mutating(s.require(auth.Viewer, s.markAlertsRead)))
	view("GET /api/groups", s.listGroups)
	view("GET /api/categories", s.listCategories)
	view("GET /api/rules", s.listRules)
	view("GET /api/portforwards", s.listForwards)
	view("GET /api/settings", s.getSettings)
	view("GET /api/alerts", s.listAlerts)
	view("GET /api/firewall", s.firewallStatus)
	view("GET /api/check", s.checkSite)
	view("GET /api/lists", s.listLists)
	view("GET /api/apps", s.listApps)
	view("GET /api/dns", s.dnsInfo)
	view("GET /api/series", s.series)
	view("GET /api/2fa", s.twoFactorStatus)
	view("GET /api/update", s.updateStatus)
	mux.HandleFunc("POST /api/2fa/setup", s.mutating(s.require(auth.Viewer, s.twoFactorSetup)))
	mux.HandleFunc("POST /api/2fa/enable", s.mutating(s.require(auth.Viewer, s.twoFactorEnable)))
	mux.HandleFunc("POST /api/2fa/disable", s.mutating(s.require(auth.Viewer, s.twoFactorDisable)))

	// admins only
	admin := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.mutating(s.require(auth.Admin, h))) }
	admin("PATCH /api/devices/{mac}", s.updateDevice)
	admin("POST /api/devices/{mac}/pause", s.pauseDevice)
	admin("DELETE /api/devices/{mac}", s.forgetDevice)
	admin("POST /api/groups", s.saveGroup)
	admin("PUT /api/groups/{id}", s.saveGroup)
	admin("DELETE /api/groups/{id}", s.deleteGroup)
	admin("POST /api/groups/{id}/pause", s.pauseGroup)
	admin("POST /api/categories/update", s.updateCategories)
	admin("POST /api/rules", s.setRule(true))
	admin("DELETE /api/rules", s.setRule(false))
	admin("POST /api/portforwards", s.saveForward)
	admin("PUT /api/portforwards/{id}", s.saveForward)
	admin("DELETE /api/portforwards/{id}", s.deleteForward)
	admin("PUT /api/settings", s.putSettings)
	admin("POST /api/update/check", s.updateCheck)
	admin("POST /api/update/install", s.updateInstall)
	admin("POST /api/rules/temp", s.addTempAllow)
	admin("DELETE /api/rules/temp", s.deleteTempAllow)
	admin("POST /api/protection/pause", s.pauseProtection)
	admin("POST /api/lists", s.saveList)
	admin("PUT /api/lists/{id}", s.saveList)
	admin("DELETE /api/lists/{id}", s.deleteList)
	admin("POST /api/lists/{id}/update", s.updateList)
	admin("POST /api/records", s.addRecord)
	admin("DELETE /api/records", s.deleteRecord)
	mux.HandleFunc("GET /api/channels", s.require(auth.Admin, s.listChannels))
	admin("POST /api/channels", s.saveChannel)
	admin("PUT /api/channels/{id}", s.saveChannel)
	admin("DELETE /api/channels/{id}", s.deleteChannel)
	admin("POST /api/channels/{id}/test", s.testChannel)
	admin("POST /api/devices/{mac}/wake", s.wakeDevice)
	mux.HandleFunc("GET /api/keys", s.require(auth.Admin, s.listKeys))
	admin("POST /api/keys", s.createKey)
	admin("DELETE /api/keys/{id}", s.deleteKey)
	admin("POST /api/users/{name}/reset-2fa", s.adminReset2FA)
	mux.HandleFunc("GET /api/certificate/export", s.require(auth.Admin, s.certificateExport))
	admin("POST /api/certificate/import", s.certificateImport)
	admin("PUT /api/vpn", s.vpnSettings)
	admin("POST /api/groups/{id}/bonus", s.groupBonus)
	// my time page redirects from the dashboard host too
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/__fengard/me", http.StatusFound) })
	admin("POST /api/vpn/peers", s.vpnAddPeer)
	admin("PUT /api/vpn/peers/{id}", s.vpnUpdatePeer)
	admin("DELETE /api/vpn/peers/{id}", s.vpnDeletePeer)
	mux.HandleFunc("GET /api/config/history", s.require(auth.Admin, s.history))
	admin("POST /api/config/rollback", s.rollback)
	mux.HandleFunc("GET /api/config/export", s.require(auth.Admin, s.export))
	admin("POST /api/config/import", s.importConfig)
	mux.HandleFunc("GET /api/audit", s.require(auth.Admin, s.listAudit))
	mux.HandleFunc("GET /api/users", s.require(auth.Admin, s.listUsers))
	admin("POST /api/users", s.createUser)
	admin("DELETE /api/users/{name}", s.deleteUser)
	mux.HandleFunc("POST /api/password", s.mutating(s.require(auth.Viewer, s.changePassword)))

	mux.Handle("GET /", assets)

	// block page assets have to load on any blocked hostname
	blockAssets := http.StripPrefix("/__fengard/", assets)

	// only cross origin fetch allowed is the https cert trust probe
	connect := "'self'"
	if u, err := url.Parse(s.ProbeURL); err == nil && u.Host != "" {
		connect += " " + u.Scheme + "://" + u.Host
	}
	csp := "default-src 'self'; connect-src " + connect + "; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", csp)
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)

		if r.URL.Path == "/__fengard/request" && r.Method == http.MethodPost {
			s.mutating(s.accessRequest)(w, r)
			return
		}
		switch r.URL.Path {
		case "/__fengard/me":
			s.portal(w, r)
			return
		case "/__fengard/me.json":
			s.portalJSON(w, r)
			return
		case "/__fengard/me/request":
			if r.Method == http.MethodPost {
				s.mutating(s.timeRequest)(w, r)
				return
			}
		}
		if r.URL.Path == "/__fengard/ping" {
			// hit over https from the dashboard to test cert trust
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Cache-Control", "no-store")
			w.Write([]byte("ok"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/__fengard/") {
			if !publicBlockAsset(r.URL.Path) {
				http.NotFound(w, r)
				return
			}
			blockAssets.ServeHTTP(w, r)
			return
		}
		if s.isDashboard(r.Host) {
			if r.TLS == nil && !s.plainHTTPAllowed(r) && !publicCARequest(r) {
				s.httpsBootstrap(w, r)
				return
			}
			mux.ServeHTTP(w, r)
			return
		}
		s.blockPage(w, r)
	})
}

func (s *Server) isDashboard(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.ToLower(host)
	if host == "localhost" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return true
	}
	for _, h := range s.DashboardHosts {
		if host == h {
			return true
		}
	}
	return false
}

// middleware

type ctxKey struct{}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) currentSession(r *http.Request) *auth.Session {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return s.Auth.KeySession(strings.TrimSpace(tok))
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.Auth.Session(c.Value)
}

func isBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) require(role auth.Role, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.currentSession(r)
		if sess == nil {
			httpError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if role == auth.Admin && sess.Role != auth.Admin {
			httpError(w, http.StatusForbidden, "admin access required")
			return
		}
		h(w, r.WithContext(contextWith(r, sess)))
	}
}

// csrf guard needs a custom header browsers wont send cross origin without a preflight
func (s *Server) mutating(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// api keys dont need the csrf header a browser cant be tricked into sending them
		if isBearer(r) {
			h(w, r)
			return
		}
		if r.Header.Get("X-Fengard") != "1" {
			httpError(w, http.StatusForbidden, "missing request header")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !sameHost(o, r.Host) {
			httpError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		h(w, r)
	}
}

func sameHost(origin, host string) bool {
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	return strings.EqualFold(o, host)
}

func (s *Server) actor(r *http.Request) string {
	if sess := sessionFrom(r); sess != nil {
		return sess.Username
	}
	return "unknown"
}

func (s *Server) audit(r *http.Request, action, detail string) {
	if s.Audit == nil {
		return
	}
	s.Audit.Append(store.Record{Time: time.Now(), Value: AuditEntry{
		Time: time.Now(), User: s.actor(r), IP: clientIP(r), Action: action, Detail: detail,
	}})
}

type AuditEntry struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	IP     string    `json:"ip"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
}

// helpers

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpError(w, http.StatusRequestEntityTooLarge, "request too large")
		} else {
			httpError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		}
		return false
	}
	return true
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, summary string, fn func(*config.Config) error) bool {
	if _, err := s.Config.Update(s.actor(r), summary, fn); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return false
	}
	s.audit(r, summary, "")
	return true
}

// block page

func (s *Server) blockPage(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.ToLower(host)
	client := clientIP(r)
	info, _ := s.Devices.Lookup(client)
	dec := s.Policy.Decide(info.MAC, host, time.Now())

	data := map[string]any{
		"Kind":    "blocked",
		"Title":   "This site is blocked",
		"Message": "The network you're connected to doesn't allow this site.",
		"Domain":  host,
		"Reason":  dec.Reason,
		"Device":  firstNonEmpty(dec.Device, info.Hostname, client),
		"Group":   dec.Group,
		"Time":    time.Now().Format("2 Jan 2006, 15:04"),
		"Network": s.Config.Get().Settings.BoxName,
		"CanAsk":  true,
	}
	if dec.Person && dec.Action == policy.Paused {
		// a persons pause or used up day is explained on their own page
		http.Redirect(w, r, "/__fengard/me", http.StatusFound)
		return
	}
	data["Person"] = dec.Person
	switch dec.Action {
	case policy.Paused:
		data["Kind"], data["Title"] = "paused", "Internet is paused"
		data["Message"] = "Internet access for this device has been paused by the network admin."
	case policy.Quarantine:
		data["Kind"], data["Title"] = "pending", "Waiting for approval"
		data["Message"] = "This device is new to the network. The admin has been notified and needs to approve it before it can go online."
		data["CanAsk"] = false
	case policy.Allow, policy.SafeSearch:
		data["Kind"], data["Title"] = "allowed", "This site is now allowed"
		data["Message"] = "It was unblocked a moment ago. Reload the page to continue."
		data["Reason"], data["CanAsk"] = "Recently unblocked", false
	}
	if data["Reason"] == "" {
		data["Reason"] = "Network policy"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	blockTmpl.Execute(w, data)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (s *Server) caCert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="fengard-ca.crt"`)
	w.Write(s.CA.CertPEM())
}

// plain http is fine on the lan unless the admin turned on require https
func (s *Server) plainHTTPAllowed(r *http.Request) bool {
	ip, err := netip.ParseAddr(clientIP(r))
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return true
	}
	if s.Config == nil || s.Config.Get().Settings.RequireHTTPS {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || tunnelRange.Contains(ip)
}

// tailscale addresses come in over its own encrypted tunnel
var tunnelRange = netip.MustParsePrefix("100.64.0.0/10")

func publicCARequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/fengard-ca.crt", "/fengard-ca.der", "/fengard.mobileconfig":
		return true
	}
	return false
}

var bootstrapTmpl = template.Must(template.New("bootstrap").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Fengard secure dashboard</title><body><main><h1>Open the secure dashboard</h1><p>Administration requires HTTPS. Do not enter passwords or API keys over HTTP.</p>{{if .URL}}<p>Before installing the certificate, compare its SHA-256 fingerprint with the value shown by <code>logread -e 'CA SHA-256'</code> on your router over SSH or its trusted console. This HTTP page and downloads do not prove the certificate's identity.</p><p>Installing this root certificate trusts its private key for any website. Keep the key and router secure.</p><p><a href="/fengard-ca.crt">Download the public CA certificate</a> · <a href="/fengard-ca.der">Android certificate</a> · <a href="/fengard.mobileconfig">Apple profile</a></p><p>Install the verified certificate, then <a href="{{.URL}}">continue to the HTTPS dashboard</a>.</p>{{else}}<p>The HTTPS listener is disabled. Enable it on the router, or use a trusted SSH tunnel to the loopback dashboard for recovery.</p>{{end}}</main></body></html>`))

func (s *Server) httpsBootstrap(w http.ResponseWriter, r *http.Request) {
	dashboardURL := ""
	if u, err := url.Parse(s.ProbeURL); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		dashboardURL = "https://" + u.Host + "/"
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != "/index.html") {
		httpError(w, http.StatusUpgradeRequired, "administration requires HTTPS; open the router's secure dashboard")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	bootstrapTmpl.Execute(w, struct{ URL string }{dashboardURL})
}

func publicBlockAsset(path string) bool {
	switch path {
	case "/__fengard/js/block.js", "/__fengard/js/me.js", "/__fengard/img/logo.svg", "/__fengard/fonts/plex-sans-latin.woff2":
		return true
	}
	return false
}

func loopbackClient(r *http.Request) bool {
	ip, err := netip.ParseAddr(clientIP(r))
	return err == nil && ip.Unmap().IsLoopback()
}
