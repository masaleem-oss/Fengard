package h3

import "time"

// loss detection and congestion control from rfc 9002, sized for dns which sends little and often

const (
	initialRTT     = 333 * time.Millisecond
	granularity    = time.Millisecond
	packetThresh   = 3
	minWindow      = 2 * maxDatagram
	initialWindow  = 10 * maxDatagram
	ourMaxAckDelay = 25 * time.Millisecond
)

// what went into a packet that has to be sent again if the packet is lost
type sentFrame struct {
	kind uint8
	id   uint64 // stream id or connection id sequence
	off  uint64
	n    uint64
	fin  bool
}

const (
	sfCrypto uint8 = iota
	sfStream
	sfMaxData
	sfMaxStreamData
	sfMaxStreamsUni
	sfRetireCID
	sfStopSending
	sfResetStream
	sfNewCID
)

type sentPacket struct {
	pn           uint64
	at           time.Time
	size         int
	ackEliciting bool
	inFlight     bool
	frames       []sentFrame
}

type rttStats struct {
	latest, smoothed, variance, min time.Duration
	has                             bool
}

func newRTT() rttStats { return rttStats{smoothed: initialRTT, variance: initialRTT / 2} }

func (r *rttStats) update(sample, ackDelay, maxAckDelay time.Duration, confirmed bool) {
	r.latest = sample
	if !r.has {
		r.has = true
		r.min, r.smoothed, r.variance = sample, sample, sample/2
		return
	}
	r.min = min(r.min, sample)
	if confirmed {
		ackDelay = min(ackDelay, maxAckDelay)
	}
	adjusted := sample
	if sample >= r.min+ackDelay {
		adjusted = sample - ackDelay
	}
	diff := r.smoothed - adjusted
	if diff < 0 {
		diff = -diff
	}
	r.variance = (3*r.variance + diff) / 4
	r.smoothed = (7*r.smoothed + adjusted) / 8
}

func (r *rttStats) pto() time.Duration {
	return r.smoothed + max(4*r.variance, granularity)
}

func (r *rttStats) lossDelay() time.Duration {
	return max(9*max(r.latest, r.smoothed)/8, granularity)
}

// newReno is the rfc 9002 congestion controller
type newReno struct {
	window, inFlight, ssthresh int
	recoveryStart              time.Time
}

func newCC() newReno { return newReno{window: initialWindow, ssthresh: 1 << 30} }

func (c *newReno) canSend() bool { return c.inFlight < c.window }

func (c *newReno) onAck(p *sentPacket) {
	if !p.inFlight {
		return
	}
	c.inFlight -= p.size
	if !p.at.After(c.recoveryStart) {
		return
	}
	if c.window < c.ssthresh {
		c.window += p.size
	} else {
		c.window += maxDatagram * p.size / c.window
	}
}

func (c *newReno) onLost(p *sentPacket, now time.Time) {
	if !p.inFlight {
		return
	}
	c.inFlight -= p.size
	if !p.at.After(c.recoveryStart) {
		return
	}
	c.recoveryStart = now
	c.ssthresh = max(c.window/2, minWindow)
	c.window = c.ssthresh
}

func (c *newReno) forget(p *sentPacket) {
	if p.inFlight {
		c.inFlight -= p.size
	}
}
