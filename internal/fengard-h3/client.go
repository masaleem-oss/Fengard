package h3

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Client sends dns queries over http/3, one connection per server shared by every query
type Client struct {
	// RootCAs and the like, the rest is filled in per connection
	TLSConfig *tls.Config
	// Resolve looks up server names, fengard passes plain dns here since it can't ask itself
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)

	mu       sync.Mutex
	conns    map[string]*Conn
	failures map[string]int // which address to try next after a failed connection
	sessions tls.ClientSessionCache
}

func (cl *Client) conn(host string, port uint16, now time.Time) *Conn {
	key := net.JoinHostPort(host, strconv.Itoa(int(port)))
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.conns == nil {
		cl.conns = make(map[string]*Conn)
		cl.failures = make(map[string]int)
		cl.sessions = tls.NewLRUClientSessionCache(8)
	}
	if c := cl.conns[key]; c != nil {
		if c.usable(now) {
			return c
		}
		c.drain()
	}
	conf := &tls.Config{}
	if cl.TLSConfig != nil {
		conf = cl.TLSConfig.Clone()
	}
	// resuming skips the certificate check, which is most of a handshake's cost on a slow router
	conf.ClientSessionCache = cl.sessions
	resolve := cl.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		}
	}
	c := newConn(key, host, port, cl.failures[key], conf, resolve)
	c.onClose = cl.forget
	cl.conns[key] = c
	go c.run()
	return c
}

func (cl *Client) forget(c *Conn) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if !c.handshakeDone {
		cl.failures[c.key]++
	} else {
		delete(cl.failures, c.key)
	}
	if cl.conns[c.key] == c {
		delete(cl.conns, c.key)
	}
}

func parseURL(rawURL string) (*url.URL, uint16, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, 0, errors.New("h3: not an https url")
	}
	port := uint16(443)
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return nil, 0, errors.New("h3: bad port")
		}
		port = uint16(n)
	}
	return u, port, nil
}

// Warm opens the connection to a server ahead of the first lookup
func (cl *Client) Warm(rawURL string) {
	if u, port, err := parseURL(rawURL); err == nil {
		cl.conn(u.Hostname(), port, time.Now())
	}
}

// Exchange posts a dns message to a dns-over-https url over http/3 and returns the answer
func (cl *Client) Exchange(ctx context.Context, rawURL string, msg []byte) ([]byte, error) {
	u, port, err := parseURL(rawURL)
	if err != nil {
		return nil, err
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	req := encodeRequest(u.Host, path, msg)
	for attempt := 0; ; attempt++ {
		c := cl.conn(u.Hostname(), port, time.Now())
		body, err := c.roundTrip(ctx, req)
		if err == nil || attempt == 1 || !retryable(err) || ctx.Err() != nil {
			return body, err
		}
	}
}

// Close hangs up every connection, the client can still be used after
func (cl *Client) Close() {
	cl.mu.Lock()
	conns := make([]*Conn, 0, len(cl.conns))
	for _, c := range cl.conns {
		conns = append(conns, c)
	}
	cl.mu.Unlock()
	for _, c := range conns {
		c.Close()
		<-c.done
	}
}

// Reset moves new queries onto fresh connections, for when the router's routes changed under the old
// ones. queries in flight still finish
func (cl *Client) Reset() {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	for _, c := range cl.conns {
		c.drain()
	}
	clear(cl.conns)
}

// Status lists the open connections
func (cl *Client) Status() []Status {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	out := make([]Status, 0, len(cl.conns))
	for _, c := range cl.conns {
		out = append(out, c.Status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}
