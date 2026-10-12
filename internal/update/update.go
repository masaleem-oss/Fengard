// checks github for new releases and installs them on routers
package update

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// what a release says it runs on, published as fengard-release.json
type Manifest struct {
	Version    string   `json:"version"`
	Targets    []string `json:"targets"`
	MinOpenWrt string   `json:"minOpenWrt"`
	MinRAMMB   int      `json:"minRamMB"`
}

type Status struct {
	Current    string    `json:"current"`
	Latest     string    `json:"latest,omitempty"`
	Newer      bool      `json:"newer"`
	Supported  bool      `json:"supported"`
	Reason     string    `json:"reason,omitempty"`
	URL        string    `json:"url,omitempty"`
	CheckedAt  time.Time `json:"checkedAt,omitzero"`
	Error      string    `json:"error,omitempty"`
	CanInstall bool      `json:"canInstall"`
	Installing bool      `json:"installing"`
	Target     string    `json:"target"`
	Prerelease bool      `json:"prerelease"` // the newest one found is a preview build
	To         string    `json:"to,omitempty"`       // the version being installed, older when going back
	Previous   string    `json:"previous,omitempty"` // what this router ran before its last update
	Held       string    `json:"held,omitempty"`     // went back from this, so automatic updates skip it
}

// newer and runs here
func (s Status) Available() bool { return s.Newer && s.Supported }

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Page       string  `json:"html_url"`
	Assets     []asset `json:"assets"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
}

type Updater struct {
	API     string // github releases latest url
	Current string
	Client  *http.Client
	Auto    func() bool
	// preview builds are github pre-releases, nobody gets one without switching this on
	Preview func() bool
	Loc     func() *time.Location
	// called once per new version found and after an install finishes
	OnNotice func(kind, title, detail string)

	// router files, tests point these somewhere else
	InitScript string
	StateDir   string
	WorkDir    string

	mu  sync.Mutex
	st  Status
	rel *release
}

func (u *Updater) defaults() {
	if u.InitScript == "" {
		u.InitScript = "/etc/init.d/fengard"
	}
	if u.StateDir == "" {
		u.StateDir = "/etc/fengard"
	}
	if u.WorkDir == "" {
		u.WorkDir = "/tmp/fengard-update"
	}
}

func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

// only router installs can swap their own program
func (u *Updater) canInstall() bool {
	u.defaults()
	if runtime.GOOS != "linux" {
		return false
	}
	_, e1 := os.Stat(u.InitScript)
	_, e2 := os.Stat(filepath.Join(u.StateDir, "install.env"))
	return e1 == nil && e2 == nil
}

func (u *Updater) preview() bool { return u.Preview != nil && u.Preview() }

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.view()
}

// view hides a preview build the moment preview builds are switched off, call with mu held
func (u *Updater) view() Status {
	s := u.st
	if s.Prerelease && !u.preview() {
		s.Latest, s.URL, s.Newer, s.Supported, s.Reason, s.Prerelease = "", "", false, false, "", false
	}
	s.Current = u.Current
	s.Target = Target()
	s.CanInstall = u.canInstall()
	s.Previous = u.readState("previous-version")
	// only means something while the version gone back from is ahead of this one
	if h := u.readState("update-hold"); Compare(h, u.Current) > 0 {
		s.Held = h
	}
	return s
}

func (u *Updater) readState(name string) string {
	u.defaults()
	b, _ := os.ReadFile(filepath.Join(u.StateDir, name))
	v := strings.TrimSpace(string(b))
	if !plainVersion.MatchString(v) {
		return ""
	}
	return v
}

// listURL is every release rather than just the latest, which github never fills with a pre-release
func listURL(api string) string {
	return strings.TrimSuffix(api, "/latest") + "?per_page=20"
}

// newest picks the highest version, pre-releases only when asked for
func newest(rels []release, previews bool) (release, bool) {
	var best release
	found := false
	for _, r := range rels {
		if r.Draft || r.Prerelease && !previews || r.Tag == "" {
			continue
		}
		if !found || Compare(r.Tag, best.Tag) > 0 {
			best, found = r, true
		}
	}
	return best, found
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fengard/"+u.Current)
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (r *release) find(name string) *asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

func (u *Updater) Check(ctx context.Context) Status {
	u.defaults()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	st := Status{CheckedAt: time.Now()}
	previews := u.preview()
	var rel release
	var body []byte
	var err error
	if previews {
		var rels []release
		if body, err = u.get(ctx, listURL(u.API), 4<<20); err == nil {
			if err = json.Unmarshal(body, &rels); err == nil {
				var ok bool
				if rel, ok = newest(rels, true); !ok {
					err = errors.New("no releases found")
				}
			}
		}
	} else if body, err = u.get(ctx, u.API, 1<<20); err == nil {
		err = json.Unmarshal(body, &rel)
	}
	if err != nil {
		// keep what we knew and say why the check failed
		u.mu.Lock()
		u.st.Error, u.st.CheckedAt = "couldn't reach GitHub: "+err.Error(), st.CheckedAt
		u.mu.Unlock()
		return u.Status()
	}
	st.Latest = strings.TrimPrefix(rel.Tag, "v")
	st.URL = rel.Page
	st.Prerelease = rel.Prerelease
	// github's latest never hands out a pre-release, this is the belt to that braces
	st.Newer = Compare(st.Latest, u.Current) > 0 && (!rel.Prerelease || previews)

	var m *Manifest
	if a := rel.find("fengard-release.json"); a != nil {
		if b, err := u.get(ctx, a.URL, 64<<10); err == nil {
			m = &Manifest{}
			if json.Unmarshal(b, m) != nil {
				m = nil
			}
		}
	}
	st.Supported, st.Reason = supported(&rel, m)

	u.mu.Lock()
	notify := st.Newer && st.Latest != u.st.Latest
	u.st, u.rel = st, &rel
	u.mu.Unlock()
	if notify && u.OnNotice != nil {
		name := "Fengard " + st.Latest
		if st.Prerelease {
			name += " preview"
		}
		if st.Supported {
			u.OnNotice("update", name+" is available", "You're on "+u.Current+". Update from the dashboard.")
		} else {
			u.OnNotice("update", name+" doesn't support this router", st.Reason)
		}
	}
	return u.Status()
}

// does this release run on this box
func supported(rel *release, m *Manifest) (bool, string) {
	t := Target()
	if rel.find("fengardd-"+t+".gz") == nil && runtime.GOOS == "linux" {
		return false, "The new release has no build for this router's CPU (" + t + ")."
	}
	if m == nil {
		return true, ""
	}
	if len(m.Targets) > 0 && !contains(m.Targets, t) {
		return false, "The new release no longer supports this router's CPU (" + t + ")."
	}
	if ow := openWrtVersion(); ow != "" && m.MinOpenWrt != "" && Compare(ow, m.MinOpenWrt) < 0 {
		return false, "The new release needs OpenWrt " + m.MinOpenWrt + " or newer, this router has " + ow + "."
	}
	if ram := ramMB(); ram > 0 && m.MinRAMMB > 0 && ram < m.MinRAMMB {
		return false, fmt.Sprintf("The new release needs %d MB of memory, this router has %d MB.", m.MinRAMMB, ram)
	}
	return true, ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func openWrtVersion() string {
	b, err := os.ReadFile("/etc/openwrt_release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "DISTRIB_RELEASE="); ok {
			return strings.Trim(v, `'"`)
		}
	}
	return ""
}

func ramMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.Atoi(f[1])
			return kb / 1024
		}
	}
	return 0
}

// compares versions like 1.0.10, 1.3.0.1 and 1.0.2-rc1, a plain release beats its prereleases
// and missing parts count as 0 so 1.3.0.1 sits between 1.3.0 and 1.3.1
func Compare(a, b string) int {
	pa, ra := parse(a)
	pb, rb := parse(b)
	for i := range max(len(pa), len(pb)) {
		x, y := part(pa, i), part(pb, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ra == rb:
		return 0
	case ra == "":
		return 1
	case rb == "":
		return -1
	case ra < rb:
		return -1
	}
	return 1
}

func parse(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, pre, _ := strings.Cut(v, "-")
	var n []int
	for _, p := range strings.Split(v, ".") {
		digits := strings.TrimRightFunc(p, func(r rune) bool { return r < '0' || r > '9' })
		x, _ := strconv.Atoi(digits)
		n = append(n, x)
	}
	return n, pre
}

func part(n []int, i int) int {
	if i < len(n) {
		return n[i]
	}
	return 0
}

var ErrBusy = errors.New("an update is already running")

// downloads checks and swaps in the new program then restarts
// the swap runs detached so it outlives this process and rolls back if the new one doesnt answer dns
func (u *Updater) Install(ctx context.Context) error {
	u.defaults()
	if !u.canInstall() {
		return errors.New("this install can't update itself, download the new kit from GitHub")
	}
	u.mu.Lock()
	st, rel := u.view(), u.rel
	if st.Installing {
		u.mu.Unlock()
		return ErrBusy
	}
	if rel == nil || !st.Available() {
		u.mu.Unlock()
		return errors.New("no update available for this router")
	}
	u.st.Installing, u.st.To = true, st.Latest
	u.mu.Unlock()
	err := u.install(ctx, rel, st.Latest, false)
	if err != nil {
		u.mu.Lock()
		u.st.Installing, u.st.To = false, ""
		u.mu.Unlock()
	}
	return err
}

// versions from this one keep automatic updates off the version someone went back from
const FirstWithHold = "1.3.1.2"

// going back further than this lands on versions that don't know about previews or four part versions
const OldestRollback = "1.3.0"

type Older struct {
	Version    string `json:"version"`
	Prerelease bool   `json:"prerelease"`
	Supported  bool   `json:"supported"`
	Reason     string `json:"reason,omitempty"`
	URL        string `json:"url,omitempty"`
}

// olderReleases are the ones this router could go back to, newest first
func (u *Updater) olderReleases(ctx context.Context) ([]release, error) {
	body, err := u.get(ctx, listURL(u.API), 4<<20)
	if err != nil {
		return nil, err
	}
	var rels []release
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, err
	}
	out := rels[:0]
	for _, r := range rels {
		v := strings.TrimPrefix(r.Tag, "v")
		if r.Draft || r.Prerelease && !u.preview() || !plainVersion.MatchString(v) ||
			Compare(v, u.Current) >= 0 || Compare(v, OldestRollback) < 0 {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b release) int { return Compare(b.Tag, a.Tag) })
	return out, nil
}

// Older lists what Go back can install, at most 8
func (u *Updater) Older(ctx context.Context) ([]Older, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rels, err := u.olderReleases(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach GitHub: %v", err)
	}
	out := []Older{}
	for i := range rels {
		if len(out) == 8 {
			break
		}
		ok, why := supported(&rels[i], nil)
		out = append(out, Older{Version: strings.TrimPrefix(rels[i].Tag, "v"), Prerelease: rels[i].Prerelease,
			Supported: ok, Reason: why, URL: rels[i].Page})
	}
	return out, nil
}

// Rollback installs an older release the same careful way as an update and holds automatic
// updates at it so the version just left doesn't come straight back overnight
func (u *Updater) Rollback(ctx context.Context, version string) error {
	u.defaults()
	if !u.canInstall() {
		return errors.New("this install can't change its own version, install the older kit from GitHub")
	}
	version = strings.TrimPrefix(version, "v")
	u.mu.Lock()
	if u.st.Installing {
		u.mu.Unlock()
		return ErrBusy
	}
	u.st.Installing, u.st.To = true, version
	u.mu.Unlock()
	err := func() error {
		rels, err := u.olderReleases(ctx)
		if err != nil {
			return fmt.Errorf("couldn't reach GitHub: %v", err)
		}
		for i := range rels {
			if strings.TrimPrefix(rels[i].Tag, "v") != version {
				continue
			}
			if ok, why := supported(&rels[i], nil); !ok {
				return errors.New(why)
			}
			return u.install(ctx, &rels[i], version, true)
		}
		return errors.New("Fengard " + version + " isn't one this router can go back to")
	}()
	if err != nil {
		u.mu.Lock()
		u.st.Installing, u.st.To = false, ""
		u.mu.Unlock()
	}
	return err
}

// automatic updates skip what someone went back from, anything newer still comes
func (u *Updater) held(latest string) bool {
	h := u.readState("update-hold")
	return h != "" && Compare(latest, h) <= 0
}

// the version ends up in a shell script so only plain ones get through
var plainVersion = regexp.MustCompile(`^[0-9A-Za-z.-]{1,32}$`)

func (u *Updater) install(ctx context.Context, rel *release, version string, back bool) error {
	if !plainVersion.MatchString(version) || !plainVersion.MatchString(u.Current) {
		return errors.New("the release has an odd version number, not installing it")
	}
	name := "fengardd-" + Target() + ".gz"
	bin, sums := rel.find(name), rel.find("SHA256SUMS")
	if bin == nil || sums == nil {
		return errors.New("the release is missing " + name + " or SHA256SUMS")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	sumsBody, err := u.get(ctx, sums.URL, 64<<10)
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(sumsBody), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return errors.New("SHA256SUMS has no entry for " + name)
	}
	gz, err := u.get(ctx, bin.URL, 64<<20)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(gz); hex.EncodeToString(sum[:]) != want {
		return errors.New("the download doesn't match its checksum")
	}
	os.RemoveAll(u.WorkDir)
	if err := os.MkdirAll(u.WorkDir, 0o700); err != nil {
		return err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return err
	}
	newBin := filepath.Join(u.WorkDir, "fengardd")
	f, err := os.OpenFile(newBin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(zr, 128<<20))
	f.Close()
	if err != nil {
		return err
	}
	// a build for the wrong cpu or a broken one fails here not after the swap
	if err := exec.CommandContext(ctx, newBin, "-h").Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() > 2 {
			return fmt.Errorf("the new program doesn't run on this router: %v", err)
		}
	}
	// a version that cant answer dns never replaces the running one
	if err := preflight(ctx, newBin, filepath.Join(u.WorkDir, "preflight")); err != nil {
		return fmt.Errorf("the new version didn't start in a test run: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	script := filepath.Join(u.WorkDir, "apply.sh")
	if err := os.WriteFile(script, []byte(applyScript(self, newBin, u.InitScript, u.StateDir, version, u.Current, back)), 0o700); err != nil {
		return err
	}
	return detach(script)
}

// starts the new program on a private port with an empty data dir and waits for a dns answer
func preflight(ctx context.Context, bin, dir string) error {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-dns", addr, "-http", "127.0.0.1:0", "-https", "", "-block-ip", "127.0.0.1",
		"-data", dir, "-leases", "", "-no-list-updates", "-mem-limit", "32", "-update-url", "")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		cmd.Process.Kill()
		<-exited
		os.RemoveAll(dir)
	}()
	q := new(dns.Msg)
	q.SetQuestion("fengard.lan.", dns.TypeA)
	c := &dns.Client{Timeout: time.Second}
	for {
		select {
		case err := <-exited:
			exited <- err
			last := strings.TrimSpace(out.String())
			if i := strings.LastIndexByte(last, '\n'); i >= 0 {
				last = last[i+1:]
			}
			return fmt.Errorf("it exited (%v) %s", err, last)
		case <-ctx.Done():
			return errors.New("it didn't answer dns within a minute")
		case <-time.After(500 * time.Millisecond):
		}
		if r, _, err := c.ExchangeContext(ctx, q, addr); err == nil && len(r.Answer) > 0 {
			return nil
		}
	}
}

// runs after this process is gone so it can restart the service and roll back
func applyScript(bin, newBin, initScript, stateDir, version, current string, back bool) string {
	// going back holds automatic updates at the version left, going forward lets them run again
	after := `rm -f "$STATE/update-hold"`
	if back {
		after = fmt.Sprintf(`echo %q >"$STATE/update-hold"`, current)
	}
	return fmt.Sprintf(`#!/bin/sh
BIN=%[1]q NEW=%[2]q INIT=%[3]q STATE=%[4]q
. "$STATE/install.env"
sleep 2
logger -t fengard "switching from %[6]s to %[5]s"
# settings as they were on %[6]s, so a version that reads them differently never loses anything
D=${FG_DIR:-$STATE}
if [ -f "$D/config.json" ]; then
	cp "$D/config.json" "$D/config.%[6]s.json"
	ls -t "$D"/config.*.json 2>/dev/null | tail -n +4 | while read -r f; do rm -f "$f"; done
fi
cp "$BIN" "$NEW.prev" && cp "$NEW" "$BIN.new" && chmod 755 "$BIN.new" && mv "$BIN.new" "$BIN" || {
	rm -f "$BIN.new"
	echo "failed %[5]s couldn't write the new program, is the flash full" >"$STATE/update-result"
	exit 1
}
sync
# package installs show this version on the luci page
V=/usr/share/fengard/version
[ -f "$V" ] && echo %[5]s >"$V"
"$INIT" restart
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18; do
	sleep 5
	nslookup fengard.lan 127.0.0.1 2>/dev/null | grep -q "$FG_IP" && {
		echo "ok %[5]s" >"$STATE/update-result"
		echo %[6]s >"$STATE/previous-version"
		%[7]s
		exit 0
	}
done
logger -t fengard "%[5]s didn't answer dns, going back to %[6]s"
cp "$NEW.prev" "$BIN.new" && mv "$BIN.new" "$BIN"
[ -f "$V" ] && echo %[6]s >"$V"
sync
echo "failed %[5]s didn't start, went back to %[6]s" >"$STATE/update-result"
"$INIT" restart
`, bin, newBin, initScript, stateDir, version, current, after)
}

// reports how the last update went, called once at startup
func (u *Updater) ReportLast() {
	u.defaults()
	p := filepath.Join(u.StateDir, "update-result")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	os.Remove(p)
	res := strings.TrimSpace(string(b))
	if u.OnNotice == nil {
		return
	}
	if v, ok := strings.CutPrefix(res, "ok "); ok {
		u.OnNotice("update", "Updated to Fengard "+v, "The update installed and Fengard is running normally.")
	} else if rest, ok := strings.CutPrefix(res, "failed "); ok {
		v, why, _ := strings.Cut(rest, " ")
		if why == "" {
			why = "the new version didn't start"
		}
		u.OnNotice("update_failed", "Update to Fengard "+v+" failed", strings.ToUpper(why[:1])+why[1:]+".")
	}
}

// checks a few minutes after start then twice a day, installing overnight when auto updates are on
func (u *Updater) Run(ctx context.Context) {
	t := time.NewTimer(3 * time.Minute)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// the update script writes its result after this new process is already up
		u.ReportLast()
		if time.Since(last) >= 12*time.Hour {
			u.Check(ctx)
			last = time.Now()
		}
		if st := u.Status(); u.Auto != nil && u.Auto() && st.Available() && !u.held(st.Latest) {
			loc := time.Local
			if u.Loc != nil {
				loc = u.Loc()
			}
			if h := time.Now().In(loc).Hour(); h >= 3 && h < 5 {
				st := u.Status()
				if err := u.Install(ctx); err != nil && err != ErrBusy && u.OnNotice != nil {
					msg := err.Error()
					u.OnNotice("update_failed", "Update to Fengard "+st.Latest+" failed", strings.ToUpper(msg[:1])+msg[1:]+".")
				}
			}
		}
		t.Reset(20 * time.Minute)
	}
}
