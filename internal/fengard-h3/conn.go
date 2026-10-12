package h3

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	idleTimeout      = 60 * time.Second
	handshakeTimeout = 4 * time.Second
	maxCryptoBuffer  = 64 << 10
	maxPTOs          = 8
	stalePTOs        = 3 // in a row on an open connection before we try a fresh one
	maxOurCIDs       = 4
	maxPeerCIDs      = 2       // the default active_connection_id_limit we never raise
	aeadSendLimit    = 1 << 22 // packets per key before we start over on a new connection
	aeadFailLimit    = 1 << 20
	idleMargin       = 5 * time.Second // stop using a connection a little before the server forgets it
	maxUndecryptable = 4
)

type request struct {
	data   []byte
	done   chan result
	stream *stream
}

type result struct {
	body []byte
	err  error
}

type datagram struct {
	buf *[]byte
	n   int
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 2048); return &b }}

type pnSpace struct {
	next          uint64
	sent          []*sentPacket
	largestAcked  int64
	lossTime      time.Time
	lastEliciting time.Time

	recvd         received
	largestRecvAt time.Time
	ackAt         time.Time // zero when nothing needs acking
	unacked       int       // ack eliciting packets since our last ack

	cryptoOut     []byte
	cryptoSent    uint64
	cryptoLost    []segRng
	cryptoIn      uint64
	cryptoPending []segment
	dropped       bool
}

type peerCID struct {
	seq   uint64
	id    []byte
	token []byte
}

// connection ids we hand the server, spares let it follow us when a nat changes our port
type ourCID struct {
	seq   uint64
	id    []byte
	token []byte
}

type ctrlFrame struct {
	kind  uint8
	id    uint64
	code  uint64
	final uint64
}

// Conn is one quic connection to one server, everything below the channels belongs to run
type Conn struct {
	key      string
	host     string
	port     uint16
	addrPick int
	tlsConf  *tls.Config
	resolve  func(ctx context.Context, host string) ([]netip.Addr, error)
	onClose  func(*Conn)

	reqs     chan *request
	cancel   chan *request
	in       chan datagram
	readErr  chan error
	closeReq chan struct{}
	drainReq chan struct{}
	done     chan struct{}
	err      error

	// for other goroutines
	lastActivity atomic.Int64
	idleNanos    atomic.Int64
	ready        atomic.Bool
	goingAway    atomic.Bool
	dead         atomic.Bool // failed, the loop just hasn't finished closing
	statusMu     sync.Mutex
	status       Status

	udp *net.UDPConn
	tls *tls.QUICConn

	scid, dcid, origDCID  []byte
	serverSCID, retrySCID []byte
	token                 []byte
	gotServerInitial      bool
	retried               bool
	peerCIDs              []peerCID
	dcidSeq               uint64
	retirePriorTo         uint64
	ourCIDs               []ourCID
	nextOurSeq            uint64
	newCIDQ               []uint64

	sp         [numSpaces]*pnSpace
	send       [numSpaces]*keys
	recv       [numSpaces]*keys
	recvNext   *keys
	recvPrev   *keys
	keyPhase   bool
	phaseStart uint64
	sentApp    uint64
	failedApp  uint64

	peer               params
	gotParams          bool
	handshakeDone      bool
	confirmed          bool
	validated          bool // the server has acked one of our handshake packets
	start              time.Time
	idle               time.Duration
	lastActive         time.Time
	elicitingSinceRecv bool

	rtt      rttStats
	cc       newReno
	ptoCount int
	probe    [numSpaces]int

	// flow control, what we may send
	peerMaxData        uint64
	sentData           uint64
	peerMaxStreamsBidi uint64
	peerMaxStreamsUni  uint64
	openedBidi         uint64
	// and what the server may send
	maxData     uint64
	recvData    uint64
	consumed    uint64
	sendMaxData bool
	maxPeerUni  uint64
	peerUni     uint64
	sendMaxUni  bool

	streams     map[uint64]*stream
	sendQ       []*stream
	waiting     []*request
	ctrl        []ctrlFrame
	pathResp    [][8]byte
	retireQ     []uint64
	control     *stream
	peerControl *stream
	gotSettings bool
	goaway      bool
	goawayID    uint64

	undecryptable [][]byte
	closing       bool
	ackScratch    []pnRange
	payload       [numSpaces][]byte
	dgram         []byte
}

func newConn(key, host string, port uint16, pick int, conf *tls.Config, resolve func(context.Context, string) ([]netip.Addr, error)) *Conn {
	c := &Conn{
		key: key, host: host, port: port, addrPick: pick, tlsConf: conf, resolve: resolve,
		reqs: make(chan *request), cancel: make(chan *request), in: make(chan datagram, 64),
		readErr: make(chan error, 1), closeReq: make(chan struct{}, 1), drainReq: make(chan struct{}, 1), done: make(chan struct{}),
		streams: make(map[uint64]*stream), rtt: newRTT(), cc: newCC(),
		maxData: connWindow, maxPeerUni: peerUniStreams, idle: idleTimeout,
		dgram: make([]byte, 0, maxDatagram),
	}
	for i := range c.sp {
		c.sp[i] = &pnSpace{largestAcked: -1}
	}
	c.status = Status{Server: key, State: "connecting", Since: time.Now()}
	c.idleNanos.Store(int64(idleTimeout))
	return c
}

// Status is what a connection is doing, for the dashboard
type Status struct {
	Server string        `json:"server"`
	State  string        `json:"state"`
	RTT    time.Duration `json:"rtt"`
	Since  time.Time     `json:"since"`
	Error  string        `json:"error,omitempty"`
	Sent   uint64        `json:"sent"`
	Lost   uint64        `json:"lost"`
	PTOs   uint64        `json:"ptos"`
	// how often the server rotated its keys
	KeyUpdates uint64 `json:"keyUpdates"`
}

func (c *Conn) setStatus(f func(*Status)) {
	c.statusMu.Lock()
	f(&c.status)
	c.statusMu.Unlock()
}

func (c *Conn) Status() Status {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	return c.status
}

// usable says whether new requests should go here
func (c *Conn) usable(now time.Time) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	if c.goingAway.Load() || c.dead.Load() {
		return false
	}
	last := c.lastActivity.Load()
	if last == 0 {
		return true
	}
	idle := time.Duration(c.idleNanos.Load())
	return now.Sub(time.Unix(0, last)) < idle-min(idleMargin, idle/4)
}

// Close sends the server a goodbye and frees everything
func (c *Conn) Close() {
	select {
	case c.closeReq <- struct{}{}:
	default:
	}
}

// drain lets queries in flight finish then closes, new ones go to another connection
func (c *Conn) drain() {
	select {
	case c.drainReq <- struct{}{}:
	default:
	}
}

func (c *Conn) roundTrip(ctx context.Context, data []byte) ([]byte, error) {
	r := &request{data: data, done: make(chan result, 1)}
	select {
	case c.reqs <- r:
	case <-c.done:
		return nil, c.closedErr()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case res := <-r.done:
		return res.body, res.err
	case <-c.done:
		select {
		case res := <-r.done:
			return res.body, res.err
		default:
			return nil, c.closedErr()
		}
	case <-ctx.Done():
		select {
		case c.cancel <- r:
		case <-c.done:
		}
		return nil, ctx.Err()
	}
}

func (c *Conn) closedErr() error {
	if c.err != nil {
		return c.err
	}
	return errClosed
}

func (c *Conn) run() {
	c.start = time.Now()
	defer func() {
		if c.udp != nil {
			c.udp.Close()
		}
		if c.tls != nil {
			c.tls.Close()
		}
		c.setStatus(func(s *Status) {
			s.State, s.Since = "closed", time.Now()
			if c.err != nil {
				s.Error = c.err.Error()
			}
		})
		close(c.done)
		if c.onClose != nil {
			c.onClose(c)
		}
	}()
	if err := c.dial(); err != nil {
		c.fail(err)
		return
	}
	if err := c.startTLS(); err != nil {
		c.fail(err)
		return
	}
	go c.readLoop()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		now := time.Now()
		c.flush(now)
		if c.closing {
			return
		}
		timer.Reset(max(c.nextWake(now).Sub(now), 0))
		select {
		case d := <-c.in:
			c.datagram(d, time.Now())
			// take whatever else already arrived before answering
			for more := true; more && !c.closing; {
				select {
				case d := <-c.in:
					c.datagram(d, time.Now())
				default:
					more = false
				}
			}
		case r := <-c.reqs:
			c.queue(r)
		case r := <-c.cancel:
			c.abandon(r)
		case err := <-c.readErr:
			if !c.handshakeDone {
				c.fail(err)
			}
		case <-c.closeReq:
			c.closeWith(appErr(h3NoError, ""))
		case <-c.drainReq:
			c.goingAway.Store(true)
			c.onGoaway(c.openedBidi * 4)
		case <-timer.C:
			c.onTimer(time.Now())
		}
	}
}

func (c *Conn) dial() error {
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(c.host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
		addrs, err = c.resolve(ctx, c.host)
		cancel()
		if err != nil {
			return err
		}
	}
	// ipv4 first, routers rarely have working ipv6 upstream
	var v4, v6 []netip.Addr
	for _, a := range addrs {
		if a.Unmap().Is4() {
			v4 = append(v4, a.Unmap())
		} else {
			v6 = append(v6, a)
		}
	}
	addrs = append(v4, v6...)
	if len(addrs) == 0 {
		return errors.New("h3: no address for " + c.host)
	}
	a := addrs[c.addrPick%len(addrs)]
	udp, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, c.port)))
	if err != nil {
		return err
	}
	c.udp = udp
	c.setStatus(func(s *Status) { s.Server = net.JoinHostPort(a.String(), itoa(int(c.port))) + " (" + c.host + ")" })
	return nil
}

func (c *Conn) readLoop() {
	fails := 0
	for {
		b := bufPool.Get().(*[]byte)
		n, err := c.udp.Read(*b)
		if err != nil {
			bufPool.Put(b)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// icmp port unreachable shows up here on a connected socket
			select {
			case c.readErr <- ErrUnreachable:
			default:
			}
			if fails++; fails > 100 {
				return
			}
			continue
		}
		fails = 0
		select {
		case c.in <- datagram{b, n}:
		case <-c.done:
			bufPool.Put(b)
			return
		}
	}
}

func (c *Conn) startTLS() error {
	c.scid = randomBytes(cidLen)
	c.ourCIDs, c.nextOurSeq = []ourCID{{seq: 0, id: c.scid}}, 1
	c.dcid = randomBytes(cidLen)
	c.origDCID = c.dcid
	c.send[spInitial], c.recv[spInitial] = initialKeys(c.dcid)
	conf := c.tlsConf.Clone()
	conf.MinVersion = tls.VersionTLS13
	conf.NextProtos = []string{"h3"}
	if conf.ServerName == "" {
		conf.ServerName = c.host
	}
	c.tls = tls.QUICClient(&tls.QUICConfig{TLSConfig: conf})
	c.tls.SetTransportParameters(appendOurParams(nil, c.scid))
	if err := c.tls.Start(context.Background()); err != nil {
		return err
	}
	return c.tlsEvents(time.Now())
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func levelSpace(l tls.QUICEncryptionLevel) (space, bool) {
	switch l {
	case tls.QUICEncryptionLevelInitial:
		return spInitial, true
	case tls.QUICEncryptionLevelHandshake:
		return spHandshake, true
	case tls.QUICEncryptionLevelApplication:
		return spApp, true
	}
	return 0, false
}

func spaceLevel(sp space) tls.QUICEncryptionLevel {
	return [...]tls.QUICEncryptionLevel{tls.QUICEncryptionLevelInitial, tls.QUICEncryptionLevelHandshake, tls.QUICEncryptionLevelApplication}[sp]
}

func (c *Conn) tlsEvents(now time.Time) error {
	for {
		e := c.tls.NextEvent()
		switch e.Kind {
		case tls.QUICNoEvent:
			return nil
		case tls.QUICSetReadSecret, tls.QUICSetWriteSecret:
			sp, ok := levelSpace(e.Level)
			if !ok {
				continue
			}
			k, err := newKeys(e.Suite, bytes.Clone(e.Data))
			if err != nil {
				return transportErr(errInternal, err.Error())
			}
			if e.Kind == tls.QUICSetWriteSecret {
				c.send[sp] = k
				continue
			}
			c.recv[sp] = k
			if sp == spApp {
				c.recvNext = k.next()
			}
			c.retryBuffered(now)
		case tls.QUICWriteData:
			if sp, ok := levelSpace(e.Level); ok {
				c.sp[sp].cryptoOut = append(c.sp[sp].cryptoOut, e.Data...)
			}
		case tls.QUICTransportParametersRequired:
			c.tls.SetTransportParameters(appendOurParams(nil, c.scid))
		case tls.QUICTransportParameters:
			p, err := parseParams(e.Data)
			if err == nil {
				err = p.checkCIDs(c.origDCID, c.serverSCID, c.retrySCID)
			}
			if err != nil {
				return err
			}
			c.applyParams(p)
		case tls.QUICHandshakeDone:
			if err := c.onHandshakeDone(now); err != nil {
				return err
			}
		}
	}
}

func (c *Conn) applyParams(p params) {
	c.peer, c.gotParams = p, true
	c.peerMaxData = p.maxData
	c.peerMaxStreamsBidi = p.maxStreamsBidi
	c.peerMaxStreamsUni = p.maxStreamsUni
	if p.idle > 0 && p.idle < c.idle {
		c.idle = p.idle
	}
	c.idleNanos.Store(int64(c.idle))
	c.peerCIDs = []peerCID{{seq: 0, id: c.dcid, token: p.resetToken}}
}

func (c *Conn) onHandshakeDone(now time.Time) error {
	if c.tls.ConnectionState().NegotiatedProtocol != "h3" || !c.gotParams {
		return transportErr(errCrypto+120, "server didn't agree to http/3")
	}
	c.handshakeDone = true
	c.ready.Store(true)
	c.lastActive = now
	c.lastActivity.Store(now.UnixNano())
	c.setStatus(func(s *Status) { s.State, s.Since = "connected", now })
	// our control stream with an almost empty settings frame
	settings := appendVarint(appendVarint(nil, 0x06), maxFieldSection) // max field section size
	c.control = newStream(2, kindControl, 0)
	c.control.out = appendFrame([]byte{0x00}, frameSettings, settings)
	c.control.peerLimit = c.peer.maxStreamUni
	c.streams[2] = c.control
	c.sendQ = append(c.sendQ, c.control)
	for range min(c.peer.cidLimit, maxOurCIDs) - 1 {
		c.issueCID()
	}
	c.openWaiting()
	return nil
}

// datagram handles one udp payload, which can hold several coalesced packets
func (c *Conn) datagram(d datagram, now time.Time) {
	defer bufPool.Put(d.buf)
	b := (*d.buf)[:d.n]
	if d.n >= len(*d.buf) {
		return // bigger than we said we take
	}
	var tail [16]byte
	canReset := len(b) >= 21
	if canReset {
		copy(tail[:], b[len(b)-16:])
	}
	processed, first := false, true
	for len(b) > 0 && !c.closing {
		h, err := parseHeader(b, len(c.scid))
		if err != nil {
			break
		}
		pkt := b[:h.length]
		b = b[h.length:]
		var sp space
		if h.long {
			if h.version == 0 {
				// it has to echo our connection ids or it's a forgery
				if first && bytes.Equal(h.dcid, c.scid) && bytes.Equal(h.scid, c.origDCID) {
					c.versionNegotiation(pkt[h.pnOffset:])
				}
				return
			}
			if h.version != version1 || !c.ownsCID(h.dcid) {
				continue
			}
			switch h.typ {
			case typeRetry:
				if first {
					c.onRetry(h, pkt, now)
				}
				return
			case typeInitial:
				sp = spInitial
			case typeHandshake:
				sp = spHandshake
			default:
				continue
			}
		} else {
			if !c.ownsCID(h.dcid) {
				break
			}
			sp = spApp
		}
		first = false
		if c.packet(sp, h, pkt, now) {
			processed = true
		}
	}
	if !processed && canReset && c.isReset(tail) {
		c.fail(ErrReset)
	}
}

func (c *Conn) ownsCID(id []byte) bool {
	for _, o := range c.ourCIDs {
		if bytes.Equal(o.id, id) {
			return true
		}
	}
	return false
}

func (c *Conn) issueCID() {
	o := ourCID{seq: c.nextOurSeq, id: randomBytes(cidLen), token: randomBytes(16)}
	c.nextOurSeq++
	c.ourCIDs = append(c.ourCIDs, o)
	c.newCIDQ = append(c.newCIDQ, o.seq)
}

// the server is done with one of our connection ids, give it another so it always has a spare
func (c *Conn) onRetireCID(seq uint64) error {
	if seq >= c.nextOurSeq {
		return transportErr(errProtocolViolation, "retired a connection id we never issued")
	}
	for i, o := range c.ourCIDs {
		if o.seq == seq {
			c.ourCIDs = append(c.ourCIDs[:i], c.ourCIDs[i+1:]...)
			c.issueCID()
			return nil
		}
	}
	return nil
}

func (c *Conn) isReset(tail [16]byte) bool {
	for _, p := range c.peerCIDs {
		if len(p.token) == 16 && subtle.ConstantTimeCompare(p.token, tail[:]) == 1 {
			return true
		}
	}
	return false
}

// packet decrypts and handles one packet, false when it couldn't be read
func (c *Conn) packet(sp space, h header, pkt []byte, now time.Time) bool {
	s := c.sp[sp]
	k := c.recv[sp]
	if s.dropped {
		return false
	}
	if k == nil {
		// handshake or 1-rtt that overtook the packet with its keys
		if len(c.undecryptable) < maxUndecryptable {
			c.undecryptable = append(c.undecryptable, bytes.Clone(pkt))
		}
		return false
	}
	pnl, err := unmask(pkt, h.pnOffset, k, h.long)
	if err != nil {
		return false
	}
	largest := int64(-1)
	if len(s.recvd.r) > 0 {
		largest = int64(s.recvd.largest())
	}
	pn := decodePN(largest, truncatedPN(pkt, h.pnOffset, pnl), pnl)
	hdrEnd := h.pnOffset + pnl
	key, rotate := k, false
	if sp == spApp && (pkt[0]&0x04 != 0) != c.keyPhase {
		if c.recvPrev != nil && pn < c.phaseStart {
			key = c.recvPrev
		} else {
			key, rotate = c.recvNext, true
		}
	}
	payload, err := key.open(pkt[hdrEnd:hdrEnd], pn, pkt[:hdrEnd], pkt[hdrEnd:])
	if err != nil {
		if sp == spApp {
			if c.failedApp++; c.failedApp > aeadFailLimit {
				c.closeWith(transportErr(errAEADLimit, "too many forged packets"))
			}
		}
		return false
	}
	if h.long && pkt[0]&0x0c != 0 || !h.long && pkt[0]&0x18 != 0 {
		c.closeWith(transportErr(errProtocolViolation, "reserved bits set"))
		return true
	}
	if s.recvd.has(pn) {
		return true
	}
	if rotate {
		c.recvPrev, c.recv[spApp] = c.recv[spApp], c.recvNext
		c.recvNext = c.recv[spApp].next()
		c.send[spApp] = c.send[spApp].next()
		c.keyPhase = !c.keyPhase
		c.phaseStart = pn
		c.setStatus(func(st *Status) { st.KeyUpdates++ })
		c.sentApp = 0
	}
	if sp == spInitial && !c.gotServerInitial {
		c.gotServerInitial = true
		c.serverSCID = bytes.Clone(h.scid)
		c.dcid = c.serverSCID
	}
	if sp == spHandshake {
		c.validated = true
	}
	s.recvd.add(pn)
	if s.recvd.largest() == pn {
		s.largestRecvAt = now
	}
	c.lastActive, c.elicitingSinceRecv = now, false
	c.lastActivity.Store(now.UnixNano())
	eliciting, err := c.frames(sp, payload, now)
	if err != nil {
		c.closeWith(err)
		return true
	}
	if eliciting {
		s.unacked++
		switch {
		case sp != spApp || s.unacked >= 2 || int64(pn) < largest:
			s.ackAt = now
		case s.ackAt.IsZero():
			s.ackAt = now.Add(ourMaxAckDelay)
		}
	}
	return true
}

func (c *Conn) retryBuffered(now time.Time) {
	pending := c.undecryptable
	c.undecryptable = nil
	for _, p := range pending {
		d := bufPool.Get().(*[]byte)
		n := copy(*d, p)
		c.datagram(datagram{d, n}, now)
	}
}

func (c *Conn) versionNegotiation(list []byte) {
	if c.gotServerInitial || c.retried {
		return
	}
	for len(list) >= 4 {
		if binary.BigEndian.Uint32(list) == version1 {
			return // a forgery, a real server would just talk v1
		}
		list = list[4:]
	}
	c.fail(ErrNoVersion)
}

func (c *Conn) onRetry(h header, pkt []byte, now time.Time) {
	if c.gotServerInitial || c.retried || bytes.Equal(h.scid, c.dcid) {
		return
	}
	if len(pkt) <= h.pnOffset+tagLen {
		return
	}
	token := pkt[h.pnOffset : len(pkt)-tagLen]
	if !retryValid(pkt, c.origDCID) {
		return
	}
	c.retried = true
	c.retrySCID = bytes.Clone(h.scid)
	c.dcid = c.retrySCID
	c.token = bytes.Clone(token)
	c.send[spInitial], c.recv[spInitial] = initialKeys(c.dcid)
	// everything in the first initial goes again with the token
	s := c.sp[spInitial]
	for _, p := range s.sent {
		c.cc.forget(p)
	}
	s.sent = nil
	s.cryptoSent = 0
	s.cryptoLost = nil
	s.lossTime = time.Time{}
	c.ptoCount = 0
}

// frames handles a packet's payload and says whether it needs an ack
func (c *Conn) frames(sp space, b []byte, now time.Time) (bool, error) {
	r := reader{b: b}
	eliciting := false
	for len(r.b) > 0 {
		typ := r.varint()
		if r.err != nil {
			break
		}
		if sp != spApp && typ > 0x03 && typ != 0x06 && typ != 0x1c {
			return eliciting, transportErr(errProtocolViolation, "frame not allowed before 1-rtt")
		}
		if typ != 0x00 && typ != 0x02 && typ != 0x03 && typ != 0x1c && typ != 0x1d {
			eliciting = true
		}
		var err error
		switch {
		case typ == 0x00:
			for len(r.b) > 0 && r.b[0] == 0 {
				r.b = r.b[1:]
			}
		case typ == 0x01:
		case typ == 0x02 || typ == 0x03:
			err = c.onAck(sp, &r, typ == 0x03, now)
		case typ == 0x04:
			id, _, final := r.varint(), r.varint(), r.varint()
			if r.err == nil {
				err = c.onResetStream(id, final)
			}
		case typ == 0x05:
			id, _ := r.varint(), r.varint()
			if r.err == nil {
				err = c.onStopSending(id)
			}
		case typ == 0x06:
			off := r.varint()
			data := r.bytes(r.varint())
			if r.err == nil {
				err = c.onCrypto(sp, off, data, now)
			}
		case typ == 0x07:
			r.bytes(r.varint())
		case typ >= 0x08 && typ <= 0x0f:
			id := r.varint()
			var off uint64
			if typ&0x04 != 0 {
				off = r.varint()
			}
			var data []byte
			if typ&0x02 != 0 {
				data = r.bytes(r.varint())
			} else {
				data, r.b = r.b, nil
			}
			if r.err == nil {
				if off+uint64(len(data)) > maxVarint {
					return eliciting, transportErr(errFrameEncoding, "stream offset too large")
				}
				err = c.onStream(id, off, data, typ&0x01 != 0)
			}
		case typ == 0x10:
			c.peerMaxData = max(c.peerMaxData, r.varint())
		case typ == 0x11:
			id, v := r.varint(), r.varint()
			if r.err == nil {
				err = c.onMaxStreamData(id, v)
			}
		case typ == 0x12 || typ == 0x13:
			v := r.varint()
			if v > 1<<60 {
				return eliciting, transportErr(errFrameEncoding, "too many streams")
			}
			if typ == 0x12 {
				c.peerMaxStreamsBidi = max(c.peerMaxStreamsBidi, v)
				c.openWaiting()
			} else {
				c.peerMaxStreamsUni = max(c.peerMaxStreamsUni, v)
			}
		case typ == 0x14, typ == 0x16, typ == 0x17:
			r.varint()
		case typ == 0x15:
			r.varint()
			r.varint()
		case typ == 0x18:
			seq, rpt := r.varint(), r.varint()
			id := r.bytes(uint64(r.byte()))
			token := r.bytes(16)
			if r.err == nil {
				if len(id) == 0 || len(id) > maxCIDLen || rpt > seq {
					return eliciting, transportErr(errFrameEncoding, "bad new connection id")
				}
				err = c.onNewCID(seq, rpt, id, token)
			}
		case typ == 0x19:
			if seq := r.varint(); r.err == nil {
				err = c.onRetireCID(seq)
			}
		case typ == 0x1a:
			if d := r.bytes(8); r.err == nil {
				if len(c.pathResp) >= 2 {
					c.pathResp = c.pathResp[1:]
				}
				c.pathResp = append(c.pathResp, [8]byte(d))
			}
		case typ == 0x1b:
			r.bytes(8)
		case typ == 0x1c:
			code := r.varint()
			r.varint()
			reason := r.bytes(r.varint())
			if r.err == nil {
				c.fail(&connError{code: code, reason: string(reason), remote: true})
				return false, nil
			}
		case typ == 0x1d:
			code := r.varint()
			reason := r.bytes(r.varint())
			if r.err == nil {
				c.fail(&connError{app: true, code: code, reason: string(reason), remote: true})
				return false, nil
			}
		case typ == 0x1e:
			c.onConfirmed()
		default:
			return eliciting, transportErr(errFrameEncoding, "unknown frame type")
		}
		if err != nil {
			return eliciting, err
		}
		if r.err != nil {
			break
		}
	}
	if r.err != nil {
		return eliciting, transportErr(errFrameEncoding, "truncated frame")
	}
	return eliciting, nil
}

func (c *Conn) onConfirmed() {
	if c.confirmed {
		return
	}
	c.confirmed, c.validated = true, true
	c.drop(spHandshake)
}

func (c *Conn) drop(sp space) {
	s := c.sp[sp]
	if s.dropped {
		return
	}
	for _, p := range s.sent {
		c.cc.forget(p)
	}
	*s = pnSpace{dropped: true, largestAcked: -1, next: s.next}
	c.send[sp], c.recv[sp] = nil, nil
	c.probe[sp] = 0
	c.ptoCount = 0
}

func (c *Conn) onCrypto(sp space, off uint64, data []byte, now time.Time) error {
	s := c.sp[sp]
	end := off + uint64(len(data))
	if end <= s.cryptoIn {
		return nil
	}
	if end > s.cryptoIn+maxCryptoBuffer {
		return transportErr(errCryptoBuffer, "too much handshake data at once")
	}
	if off > s.cryptoIn {
		if len(s.cryptoPending) < maxBuffered {
			s.cryptoPending = append(s.cryptoPending, segment{off, bytes.Clone(data)})
		}
		return nil
	}
	if err := c.feedTLS(sp, data[s.cryptoIn-off:]); err != nil {
		return err
	}
	s.cryptoIn = end
	for progress := true; progress; {
		progress = false
		for i := 0; i < len(s.cryptoPending); i++ {
			g := s.cryptoPending[i]
			gEnd := g.off + uint64(len(g.data))
			if g.off > s.cryptoIn {
				continue
			}
			s.cryptoPending = append(s.cryptoPending[:i], s.cryptoPending[i+1:]...)
			i--
			if gEnd > s.cryptoIn {
				if err := c.feedTLS(sp, g.data[s.cryptoIn-g.off:]); err != nil {
					return err
				}
				s.cryptoIn = gEnd
				progress = true
			}
		}
	}
	return c.tlsEvents(now)
}

func (c *Conn) feedTLS(sp space, data []byte) error {
	if err := c.tls.HandleData(spaceLevel(sp), data); err != nil {
		var alert tls.AlertError
		if errors.As(err, &alert) {
			return transportErr(errCrypto+uint64(alert), err.Error())
		}
		return transportErr(errCrypto+80, err.Error())
	}
	return nil
}

// retire queues a retire_connection_id, a server that makes us retire too many at once is broken
func (c *Conn) retire(seq uint64) error {
	for _, q := range c.retireQ {
		if q == seq {
			return nil
		}
	}
	if len(c.retireQ) >= 16 {
		return transportErr(errConnectionIDLimit, "too many connection ids to retire")
	}
	c.retireQ = append(c.retireQ, seq)
	return nil
}

func (c *Conn) onNewCID(seq, rpt uint64, id, token []byte) error {
	if seq < c.retirePriorTo {
		return c.retire(seq)
	}
	for _, p := range c.peerCIDs {
		if p.seq == seq {
			if !bytes.Equal(p.id, id) {
				return transportErr(errProtocolViolation, "connection id sequence reused")
			}
			return nil
		}
	}
	c.peerCIDs = append(c.peerCIDs, peerCID{seq, bytes.Clone(id), bytes.Clone(token)})
	if rpt > c.retirePriorTo {
		c.retirePriorTo = rpt
		keep := c.peerCIDs[:0]
		for _, p := range c.peerCIDs {
			if p.seq < rpt {
				if err := c.retire(p.seq); err != nil {
					return err
				}
				continue
			}
			keep = append(keep, p)
		}
		c.peerCIDs = keep
		if c.dcidSeq < rpt && len(keep) > 0 {
			next := keep[0]
			for _, p := range keep {
				if p.seq < next.seq {
					next = p
				}
			}
			c.dcid, c.dcidSeq = next.id, next.seq
		}
	}
	if len(c.peerCIDs) > maxPeerCIDs {
		return transportErr(errConnectionIDLimit, "too many connection ids")
	}
	return nil
}

func (c *Conn) onAck(sp space, r *reader, ecn bool, now time.Time) error {
	ranges, delay, err := parseAck(r, ecn, c.ackScratch)
	if err != nil {
		return transportErr(errFrameEncoding, "bad ack frame")
	}
	c.ackScratch = ranges
	s := c.sp[sp]
	if ranges[0].hi >= s.next {
		return transportErr(errProtocolViolation, "ack for a packet never sent")
	}
	var newest *sentPacket
	eliciting := false
	keep := s.sent[:0]
	for _, p := range s.sent {
		if !inRanges(p.pn, ranges) {
			keep = append(keep, p)
			continue
		}
		if p.pn == ranges[0].hi {
			newest = p
		}
		eliciting = eliciting || p.ackEliciting
		c.acked(p)
	}
	acked := len(keep) != len(s.sent)
	clear(s.sent[len(keep):])
	s.sent = keep
	if !acked {
		return nil
	}
	if int64(ranges[0].hi) > s.largestAcked {
		s.largestAcked = int64(ranges[0].hi)
	}
	if newest != nil && eliciting {
		var ackDelay time.Duration
		if sp == spApp {
			ackDelay = time.Duration(min(delay, 1<<24)<<c.peer.ackDelayExp) * time.Microsecond
		}
		c.rtt.update(now.Sub(newest.at), ackDelay, c.peer.maxAckDelay, c.confirmed)
		c.setStatus(func(st *Status) { st.RTT = c.rtt.smoothed })
	}
	if sp == spHandshake {
		c.validated = true
	}
	if sp == spApp && c.handshakeDone {
		c.onConfirmed()
	}
	c.ptoCount = 0
	c.detectLoss(sp, now)
	return nil
}

func inRanges(pn uint64, rs []pnRange) bool {
	for _, g := range rs {
		if pn > g.hi {
			return false
		}
		if pn >= g.lo {
			return true
		}
	}
	return false
}

func (c *Conn) acked(p *sentPacket) {
	c.cc.onAck(p)
	for _, f := range p.frames {
		if f.kind != sfStream {
			continue
		}
		if s := c.streams[f.id]; s != nil {
			s.onAcked(f.off, f.n, f.fin)
		}
	}
}

func (c *Conn) detectLoss(sp space, now time.Time) {
	s := c.sp[sp]
	s.lossTime = time.Time{}
	if s.largestAcked < 0 {
		return
	}
	delay := c.rtt.lossDelay()
	lostBefore := now.Add(-delay)
	keep := s.sent[:0]
	for _, p := range s.sent {
		if int64(p.pn) > s.largestAcked {
			keep = append(keep, p)
			continue
		}
		if !p.at.After(lostBefore) || s.largestAcked-int64(p.pn) >= packetThresh {
			c.setStatus(func(st *Status) { st.Lost++ })
			c.cc.onLost(p, now)
			c.resend(sp, p.frames)
			continue
		}
		if t := p.at.Add(delay); s.lossTime.IsZero() || t.Before(s.lossTime) {
			s.lossTime = t
		}
		keep = append(keep, p)
	}
	clear(s.sent[len(keep):])
	s.sent = keep
}

// resend puts what a lost packet carried back in line
func (c *Conn) resend(sp space, frames []sentFrame) {
	for _, f := range frames {
		switch f.kind {
		case sfCrypto:
			c.sp[sp].cryptoLost = append(c.sp[sp].cryptoLost, segRng{f.off, f.n})
		case sfStream:
			if s := c.streams[f.id]; s != nil && !s.sendDone() {
				s.onLost(f.off, f.n, f.fin)
				c.wantSend(s)
			}
		case sfMaxData:
			c.sendMaxData = true
		case sfMaxStreamsUni:
			c.sendMaxUni = true
		case sfMaxStreamData:
			if s := c.streams[f.id]; s != nil && !s.complete() {
				c.ctrl = append(c.ctrl, ctrlFrame{kind: sfMaxStreamData, id: f.id})
			}
		case sfRetireCID:
			c.retire(f.id)
		case sfNewCID:
			c.newCIDQ = append(c.newCIDQ, f.id)
		case sfStopSending, sfResetStream:
			if _, ok := c.streams[f.id]; ok {
				c.ctrl = append(c.ctrl, ctrlFrame{kind: f.kind, id: f.id, code: h3RequestCancelled, final: f.off})
			}
		}
	}
}

func (c *Conn) wantSend(s *stream) {
	for _, q := range c.sendQ {
		if q == s {
			return
		}
	}
	c.sendQ = append(c.sendQ, s)
}

// streams

func (c *Conn) onStream(id, off uint64, data []byte, fin bool) error {
	s, err := c.recvStream(id)
	if s == nil || err != nil {
		return err
	}
	before := s.inOff
	grew, err := s.receive(off, data, fin)
	if err != nil {
		return err
	}
	if c.recvData += grew; c.recvData > c.maxData {
		return transportErr(errFlowControl, "connection data over the limit")
	}
	c.consumed += s.inOff - before
	if c.maxData-c.consumed < connWindow/2 {
		c.maxData = c.consumed + connWindow
		c.sendMaxData = true
	}
	return c.process(s)
}

// recvStream finds or opens the stream a frame from the server is for, nil when it's already gone
func (c *Conn) recvStream(id uint64) (*stream, error) {
	if s := c.streams[id]; s != nil {
		return s, nil
	}
	switch id % 4 {
	case 0:
		if id/4 >= c.openedBidi {
			return nil, transportErr(errStreamState, "data on a stream we never opened")
		}
		return nil, nil
	case 1:
		return nil, transportErr(errStreamLimit, "server opened a bidirectional stream")
	case 2:
		return nil, transportErr(errStreamState, "data on our own unidirectional stream")
	}
	idx := id / 4
	if idx >= c.maxPeerUni {
		return nil, transportErr(errStreamLimit, "too many streams from the server")
	}
	if idx < c.peerUni {
		return nil, nil // closed already
	}
	for ; c.peerUni <= idx; c.peerUni++ {
		sid := c.peerUni*4 + 3
		c.streams[sid] = newStream(sid, kindPeerUni, uniWindow)
	}
	return c.streams[id], nil
}

func (c *Conn) process(s *stream) error {
	switch s.kind {
	case kindRequest:
		if !s.complete() {
			return nil
		}
		body, status, err := readResponse(s.in)
		if err == nil && status != 200 {
			err = errors.New("h3: server answered HTTP " + itoa(status))
		}
		c.finish(s.req, body, err)
		c.closeStream(s)
		return nil
	case kindPeerUni:
		if len(s.in) == 0 {
			break
		}
		typ, n, err := readVarint(s.in)
		if err != nil {
			break
		}
		s.in = s.in[n:]
		switch typ {
		case 0x00:
			if c.peerControl != nil {
				return appErr(h3StreamCreation, "second control stream")
			}
			s.kind, c.peerControl = kindPeerControl, s
			return c.process(s)
		default:
			// qpack encoder and decoder streams carry nothing for a decoder with no table, unknown types get ignored
			s.kind = kindDiscard
		}
	case kindPeerControl:
		if err := c.controlFrames(s); err != nil {
			return err
		}
		if s.finalSize >= 0 || s.peerReset {
			return appErr(h3ClosedCriticalStream, "server closed its control stream")
		}
	}
	if s.kind == kindDiscard {
		s.in = s.in[:0]
		if s.complete() {
			c.closeStream(s)
			return nil
		}
	}
	// keep long lived server streams open for more
	if s.id%4 == 3 && s.finalSize < 0 && s.limit-s.inOff < uniWindow/2 {
		s.limit = s.inOff + uniWindow
		c.ctrl = append(c.ctrl, ctrlFrame{kind: sfMaxStreamData, id: s.id})
	}
	return nil
}

func (c *Conn) controlFrames(s *stream) error {
	for len(s.in) > 0 {
		typ, payload, n, err := nextFrame(s.in)
		if errors.Is(err, errShort) {
			if len(s.in) > 64<<10 {
				return appErr(h3Frame, "control frame too large")
			}
			return nil
		}
		if err != nil {
			return appErr(h3Frame, "bad control frame")
		}
		if !c.gotSettings && typ != frameSettings {
			return appErr(h3MissingSettings, "control stream didn't start with settings")
		}
		switch {
		case typ == frameSettings:
			if c.gotSettings {
				return appErr(h3FrameUnexpected, "second settings frame")
			}
			c.gotSettings = true
		case typ == frameGoaway:
			id, _, err := readVarint(payload)
			if err != nil {
				return appErr(h3Frame, "bad goaway")
			}
			c.onGoaway(id)
		case typ == frameData || typ == frameHeaders || typ == framePush || typ == 0x0d || reservedFrame(typ):
			return appErr(h3FrameUnexpected, "frame not allowed on the control stream")
		}
		s.in = s.in[n:]
	}
	s.in = s.in[:0]
	return nil
}

func (c *Conn) onGoaway(id uint64) {
	c.goaway, c.goawayID = true, id
	c.goingAway.Store(true)
	for _, r := range c.waiting {
		r.done <- result{err: errGoingAway}
	}
	c.waiting = nil
	for _, s := range c.streams {
		if s.kind == kindRequest && s.id >= id && s.req != nil {
			c.finish(s.req, nil, errGoingAway)
			c.abandonStream(s)
		}
	}
	c.closeIfIdle()
}

// closeIfIdle hangs up once a going away connection has nothing left in flight
func (c *Conn) closeIfIdle() {
	if !c.goaway {
		return
	}
	for _, s := range c.streams {
		if s.kind == kindRequest && s.req != nil {
			return
		}
	}
	c.closeWith(appErr(h3NoError, ""))
}

func (c *Conn) onResetStream(id, final uint64) error {
	s, err := c.recvStream(id)
	if s == nil || err != nil {
		return err
	}
	if final < s.highest || s.finalSize >= 0 && uint64(s.finalSize) != final || final > s.limit {
		return transportErr(errFinalSize, "reset changed the final size")
	}
	if c.recvData += final - s.highest; c.recvData > c.maxData {
		return transportErr(errFlowControl, "connection data over the limit")
	}
	c.consumed += final - s.inOff
	s.highest, s.finalSize, s.peerReset = final, int64(final), true
	if s == c.peerControl {
		return appErr(h3ClosedCriticalStream, "server reset its control stream")
	}
	if s.req != nil {
		c.finish(s.req, nil, errors.New("h3: server reset the request"))
	}
	c.closeStream(s)
	return nil
}

func (c *Conn) onStopSending(id uint64) error {
	if id%4 == 3 || id%4 == 1 {
		return transportErr(errStreamState, "stop sending for a stream we can't send on")
	}
	s := c.streams[id]
	if s == nil {
		if id%4 == 0 && id/4 >= c.openedBidi {
			return transportErr(errStreamState, "stop sending for a stream we never opened")
		}
		return nil
	}
	if s == c.control {
		return appErr(h3ClosedCriticalStream, "server stopped our control stream")
	}
	if !s.sendDone() {
		s.reset = true
		c.ctrl = append(c.ctrl, ctrlFrame{kind: sfResetStream, id: id, code: h3RequestCancelled, final: s.sent})
	}
	if s.req != nil {
		// the server turned it down before answering, another connection can try
		c.finish(s.req, nil, errGoingAway)
	}
	return nil
}

func (c *Conn) onMaxStreamData(id, v uint64) error {
	if id%4 == 3 || id%4 == 1 {
		return transportErr(errStreamState, "max stream data for a stream we don't send on")
	}
	if s := c.streams[id]; s != nil && v > s.peerLimit {
		s.peerLimit = v
		c.wantSend(s)
	}
	return nil
}

func (c *Conn) closeStream(s *stream) {
	delete(c.streams, s.id)
	if s.id%4 == 3 {
		c.maxPeerUni++
		c.sendMaxUni = true
	}
	c.closeIfIdle()
}

// requests

func (c *Conn) queue(r *request) {
	if c.goaway {
		r.done <- result{err: errGoingAway}
		return
	}
	c.waiting = append(c.waiting, r)
	c.openWaiting()
}

func (c *Conn) openWaiting() {
	for c.handshakeDone && len(c.waiting) > 0 && c.openedBidi < c.peerMaxStreamsBidi {
		r := c.waiting[0]
		c.waiting = c.waiting[1:]
		s := newStream(c.openedBidi*4, kindRequest, streamWindow)
		c.openedBidi++
		s.out, s.fin, s.req = r.data, true, r
		s.peerLimit = c.peer.maxStreamBidi
		r.stream = s
		c.streams[s.id] = s
		c.sendQ = append(c.sendQ, s)
	}
}

func (c *Conn) finish(r *request, body []byte, err error) {
	if r == nil || r.stream == nil && err == nil {
		return
	}
	select {
	case r.done <- result{body, err}:
	default:
	}
	if r.stream != nil {
		r.stream.req = nil
	}
}

// abandon is a request whose caller gave up
func (c *Conn) abandon(r *request) {
	for i, w := range c.waiting {
		if w == r {
			c.waiting = append(c.waiting[:i], c.waiting[i+1:]...)
			return
		}
	}
	if s := r.stream; s != nil && c.streams[s.id] == s {
		s.req = nil
		c.abandonStream(s)
	}
}

func (c *Conn) abandonStream(s *stream) {
	s.req = nil
	if s.complete() {
		c.closeStream(s)
		return
	}
	s.kind = kindDiscard
	s.in = nil
	c.ctrl = append(c.ctrl, ctrlFrame{kind: sfStopSending, id: s.id, code: h3RequestCancelled})
	if !s.sendDone() {
		s.reset = true
		c.ctrl = append(c.ctrl, ctrlFrame{kind: sfResetStream, id: s.id, code: h3RequestCancelled, final: s.sent})
	}
}

// failing and closing

// fail ends the connection without telling the server
func (c *Conn) fail(err error) {
	if c.closing {
		return
	}
	c.closing = true
	c.err = err
	c.ready.Store(false)
	c.dead.Store(true)
	for _, r := range c.waiting {
		r.done <- result{err: err}
	}
	c.waiting = nil
	for _, s := range c.streams {
		if s.req != nil {
			c.finish(s.req, nil, err)
		}
	}
}

// closeWith tells the server why we're leaving then ends the connection
func (c *Conn) closeWith(err error) {
	if c.closing {
		return
	}
	var ce *connError
	if !errors.As(err, &ce) {
		ce = &connError{code: errInternal, reason: err.Error()}
	}
	reason := ce.reason
	if len(reason) > 64 {
		reason = reason[:64]
	}
	now := time.Now()
	for sp := spInitial; sp < numSpaces; sp++ {
		if c.send[sp] == nil || c.sp[sp].dropped {
			continue
		}
		var f []byte
		if ce.app && sp == spApp {
			f = appendVarint(appendVarint([]byte{0x1d}, ce.code), uint64(len(reason)))
			f = append(f, reason...)
		} else {
			code := ce.code
			if ce.app {
				code, reason = 0x0c, "" // application_error, the real reason stays private before 1-rtt
			}
			f = appendVarint(appendVarint(appendVarint([]byte{0x1c}, code), 0), uint64(len(reason)))
			f = append(f, reason...)
		}
		c.writeDatagram(now, []part{{sp: sp, payload: f}})
	}
	c.fail(err)
}

// timers

func (c *Conn) nextWake(now time.Time) time.Time {
	wake := now.Add(time.Hour)
	set := func(t time.Time) {
		if !t.IsZero() && t.Before(wake) {
			wake = t
		}
	}
	for sp, s := range c.sp {
		if !s.ackAt.IsZero() && c.send[sp] != nil {
			set(s.ackAt)
		}
	}
	t, _, _ := c.lossTimer(now)
	set(t)
	if c.handshakeDone {
		set(c.lastActive.Add(max(c.idle, 3*c.rtt.pto())))
	} else {
		set(c.start.Add(handshakeTimeout))
	}
	return wake
}

// lossTimer is the earliest loss time, or the probe timeout when nothing is waiting on time
func (c *Conn) lossTimer(now time.Time) (time.Time, space, bool) {
	var at time.Time
	var where space
	for sp, s := range c.sp {
		if !s.lossTime.IsZero() && (at.IsZero() || s.lossTime.Before(at)) {
			at, where = s.lossTime, space(sp)
		}
	}
	if !at.IsZero() {
		return at, where, false
	}
	backoff := time.Duration(1) << min(c.ptoCount, 16)
	pto := c.rtt.pto() * backoff
	inFlight := false
	for sp, s := range c.sp {
		if s.dropped || !hasEliciting(s) {
			continue
		}
		inFlight = true
		d := pto
		if space(sp) == spApp {
			if !c.confirmed {
				continue
			}
			d += c.peer.maxAckDelay * backoff
		}
		if t := s.lastEliciting.Add(d); at.IsZero() || t.Before(at) {
			at, where = t, space(sp)
		}
	}
	if !inFlight && !c.validated && !c.closing {
		// the server may be waiting on us to unblock it, rfc 9002 section 6.2.2.1
		base := c.start
		for _, sp := range []space{spInitial, spHandshake} {
			if s := c.sp[sp]; s.lastEliciting.After(base) {
				base = s.lastEliciting
			}
		}
		where = spInitial
		if c.send[spHandshake] != nil {
			where = spHandshake
		}
		return base.Add(pto), where, true
	}
	return at, where, true
}

func hasEliciting(s *pnSpace) bool {
	for _, p := range s.sent {
		if p.ackEliciting {
			return true
		}
	}
	return false
}

func (c *Conn) onTimer(now time.Time) {
	if !c.handshakeDone && !now.Before(c.start.Add(handshakeTimeout)) {
		c.fail(ErrHandshake)
		return
	}
	if c.handshakeDone && !now.Before(c.lastActive.Add(max(c.idle, 3*c.rtt.pto()))) {
		c.fail(ErrIdle)
		return
	}
	at, sp, isPTO := c.lossTimer(now)
	if at.IsZero() || now.Before(at) {
		return
	}
	if !isPTO {
		c.detectLoss(sp, now)
		return
	}
	c.ptoCount++
	c.setStatus(func(st *Status) { st.PTOs++ })
	if c.handshakeDone && c.ptoCount >= stalePTOs || c.ptoCount > maxPTOs {
		// the path died or the server forgot us, a fresh connection is quicker than waiting it out
		c.fail(errStale)
		return
	}
	// send again what the oldest packets carried, without calling them lost
	s := c.sp[sp]
	n := 0
	for _, p := range s.sent {
		if p.ackEliciting && n < 2 {
			c.resend(sp, p.frames)
			n++
		}
	}
	c.probe[sp] = 1
	if n > 1 {
		c.probe[sp] = 2
	}
}
