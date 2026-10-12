package h3

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// real resolvers on the internet, FENGARD_H3_LIVE=1 go test -run Live
var liveServers = []string{
	"https://cloudflare-dns.com/dns-query",
	"https://1.1.1.1/dns-query",
	"https://dns.google/dns-query",
	"https://dns.adguard-dns.com/dns-query",
	"https://unfiltered.adguard-dns.com/dns-query",
	"https://dns.quad9.net/dns-query",
	"https://dns.nextdns.io/dns-query",
}

func TestLive(t *testing.T) {
	if os.Getenv("FENGARD_H3_LIVE") == "" {
		t.Skip("set FENGARD_H3_LIVE=1 to talk to real resolvers")
	}
	servers := liveServers
	if s := os.Getenv("FENGARD_H3_SERVERS"); s != "" {
		servers = strings.Split(s, ",")
	}
	cl := &Client{}
	defer cl.Close()
	for _, u := range servers {
		t.Run(u, func(t *testing.T) {
			ask := func(name string) (time.Duration, error) {
				q := new(dns.Msg)
				q.SetQuestion(name, dns.TypeA)
				q.SetEdns0(1232, false)
				q.Id = 0
				msg, _ := q.Pack()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				start := time.Now()
				body, err := cl.Exchange(ctx, u, msg)
				if err != nil {
					return 0, err
				}
				r := new(dns.Msg)
				if err := r.Unpack(body); err != nil {
					return 0, err
				}
				if r.Rcode != dns.RcodeSuccess || len(r.Answer) == 0 {
					t.Errorf("%s: %s", name, r)
				}
				return time.Since(start), nil
			}
			first, err := ask("example.com.")
			if err != nil {
				t.Fatalf("first query: %v", err)
			}
			second, err := ask("wikipedia.org.")
			if err != nil {
				t.Fatalf("second query: %v", err)
			}
			again, err := ask("wikipedia.org.")
			if err != nil {
				t.Fatalf("cached query: %v", err)
			}
			// a burst at once over the same connection
			var wg sync.WaitGroup
			errs := make(chan error, 20)
			for i := range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := ask([]string{"github.com.", "openwrt.org.", "go.dev.", "debian.org."}[i%4]); err != nil {
						errs <- err
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Errorf("burst: %v", err)
			}
			for _, st := range cl.Status() {
				if strings.Contains(st.Server, u[len("https://"):strings.LastIndex(u, "/")]) {
					t.Logf("handshake+first %v, new name %v, cached %v, rtt %v, sent %d lost %d ptos %d", first.Round(time.Millisecond), second.Round(time.Millisecond), again.Round(time.Millisecond), st.RTT.Round(time.Millisecond), st.Sent, st.Lost, st.PTOs)
				}
			}
		})
	}
}
