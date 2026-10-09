// floods a fengard dns server and checks a normal client still gets fast answers
// only point this at your own box
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

func main() {
	server := flag.String("server", "127.0.0.1:5399", "Fengard DNS address")
	attacker := flag.String("attacker", "127.0.0.1", "source IP for the flood")
	victim := flag.String("victim", "127.0.0.2", "source IP for normal lookups")
	zone := flag.String("zone", "pornhub.com", "blocked zone to flood with random subdomains (answered locally)")
	normal := flag.String("normal", "fengard.lan", "name the normal client looks up")
	seconds := flag.Int("seconds", 10, "test duration")
	workers := flag.Int("workers", 64, "flood sockets")
	flag.Parse()

	stop := time.Now().Add(time.Duration(*seconds) * time.Second)
	var sent, answered atomic.Int64
	var wg sync.WaitGroup

	// fire and forget like a real flood readers just count replies
	dst, err := net.ResolveUDPAddr("udp", *server)
	if err != nil {
		panic(err)
	}
	for range *workers {
		conn, err := net.DialUDP("udp", &net.UDPAddr{IP: net.ParseIP(*attacker)}, dst)
		if err != nil {
			panic(err)
		}
		go func() {
			buf := make([]byte, 1500)
			for {
				conn.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := conn.Read(buf); err != nil {
					if time.Now().After(stop) {
						return
					}
					continue
				}
				answered.Add(1)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := new(dns.Msg)
			for time.Now().Before(stop) {
				m.SetQuestion(fmt.Sprintf("x%d.%s.", rand.Uint64(), *zone), dns.TypeA)
				m.Id = dns.Id()
				pkt, _ := m.Pack()
				if _, err := conn.Write(pkt); err == nil {
					sent.Add(1)
				}
			}
		}()
	}

	var lat []time.Duration
	var fails int
	if *victim == "" { // flood only
		wg.Wait()
		fmt.Printf("flood: %d queries sent (%.0f/s), %d answered\n", sent.Load(), float64(sent.Load())/float64(*seconds), answered.Load())
		return
	}
	c := &dns.Client{Timeout: time.Second, Dialer: &net.Dialer{LocalAddr: &net.UDPAddr{IP: net.ParseIP(*victim)}}}
	for time.Now().Before(stop) {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(*normal), dns.TypeA)
		start := time.Now()
		if _, _, err := c.Exchange(m, *server); err != nil {
			fails++
			fmt.Printf("  lookup #%d at %.1fs failed: %v\n", len(lat)+fails, time.Since(stop.Add(-time.Duration(*seconds)*time.Second)).Seconds(), err)
		} else {
			lat = append(lat, time.Since(start))
		}
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	fmt.Printf("flood:  %d queries sent (%.0f/s), %d answered (%.1f%%), rest rate-limited\n",
		sent.Load(), float64(sent.Load())/float64(*seconds), answered.Load(), 100*float64(answered.Load())/float64(max(1, sent.Load())))
	fmt.Printf("normal: %d lookups, %d failed, latency p50 %v, p99 %v, max %v\n",
		len(lat)+fails, fails, pct(.5), pct(.99), pct(1))
}
