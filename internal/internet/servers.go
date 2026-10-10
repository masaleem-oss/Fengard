package internet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// a place to run the test against and how to ask it for data
type server struct {
	name string
	down func() string
	up   func() string
	ping string
}

// spreads streams over every url the service hands out
func rotate(urls []string, rewrite func(string) string) func() string {
	var n atomic.Int64
	return func() string { return rewrite(urls[int(n.Add(1))%len(urls)]) }
}

// custom is anything that talks like cloudflares speed test, used for -speedtest-url
func custom(base string) server {
	base = strings.TrimRight(base, "/")
	return server{
		name: base,
		down: func() string { return base + "/__down?bytes=25000000" },
		up:   func() string { return base + "/__up" },
		ping: base + "/__down?bytes=0",
	}
}

var (
	fastScript = regexp.MustCompile(`app-[a-z0-9]+\.js`)
	fastToken  = regexp.MustCompile(`token:"([A-Za-z0-9]+)"`)
)

// fast.com hands out netflix servers that usually sit inside your own isp
func fastCom(ctx context.Context, c *http.Client) (server, error) {
	page, err := fetch(ctx, c, "https://fast.com/")
	if err != nil {
		return server{}, err
	}
	script := fastScript.FindString(page)
	if script == "" {
		return server{}, errors.New("fast.com page changed")
	}
	js, err := fetch(ctx, c, "https://fast.com/"+script)
	if err != nil {
		return server{}, err
	}
	tok := fastToken.FindStringSubmatch(js)
	if tok == nil {
		return server{}, errors.New("fast.com token not found")
	}
	body, err := fetch(ctx, c, "https://api.fast.com/netflix/speedtest/v2?https=true&urlCount=5&token="+tok[1])
	if err != nil {
		return server{}, err
	}
	var resp struct {
		Targets []struct {
			URL      string `json:"url"`
			Location struct {
				City string `json:"city"`
			} `json:"location"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.Targets) == 0 {
		return server{}, errors.New("fast.com gave no servers")
	}
	var urls []string
	for _, t := range resp.Targets {
		if strings.Contains(t.URL, "/speedtest?") {
			urls = append(urls, t.URL)
		}
	}
	if len(urls) == 0 {
		return server{}, errors.New("fast.com urls changed")
	}
	at := func(r string) func(string) string {
		return func(u string) string { return strings.Replace(u, "/speedtest?", "/speedtest/range/"+r+"?", 1) }
	}
	name := "Netflix"
	if city := resp.Targets[0].Location.City; city != "" {
		name += " in " + city
	}
	return server{name: name, down: rotate(urls, at("0-26214400")), up: rotate(urls, at("0-0")), ping: at("0-0")(urls[0])}, nil
}

// librespeed runs community servers, this picks whichever answers quickest
func libreSpeed(ctx context.Context, c *http.Client) (server, error) {
	body, err := fetch(ctx, c, "https://librespeed.org/backend-servers/servers.php")
	if err != nil {
		return server{}, err
	}
	var list []struct {
		Name   string `json:"name"`
		Server string `json:"server"`
		Down   string `json:"dlURL"`
		Up     string `json:"ulURL"`
		Ping   string `json:"pingURL"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) == 0 {
		return server{}, errors.New("no librespeed servers")
	}
	best, bestMs := -1, 0.0
	for i, s := range list {
		base := strings.TrimRight(s.Server, "/") + "/"
		t0 := time.Now()
		if _, err := fetch(ctx, c, base+s.Ping); err != nil {
			continue
		}
		if ms := float64(time.Since(t0).Milliseconds()); best < 0 || ms < bestMs {
			best, bestMs = i, ms
		}
	}
	if best < 0 {
		return server{}, errors.New("no librespeed server answered")
	}
	s := list[best]
	base := strings.TrimRight(s.Server, "/") + "/"
	return server{
		name: s.Name,
		down: func() string { return base + s.Down + "?ckSize=25" },
		up:   func() string { return base + s.Up },
		ping: base + s.Ping,
	}, nil
}

func (m *Monitor) pick(ctx context.Context, c *http.Client) (server, error) {
	if m.Server != "" {
		return custom(m.Server), nil
	}
	if s, err := fastCom(ctx, c); err == nil {
		return s, nil
	}
	return libreSpeed(ctx, c)
}

func fetch(ctx context.Context, c *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("status " + strconv.Itoa(resp.StatusCode))
	}
	return string(b), err
}
