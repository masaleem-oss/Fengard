package h3

import (
	"errors"
	"fmt"
)

// quic transport error codes, rfc 9000 section 20.1
const (
	errNoError            = 0x00
	errInternal           = 0x01
	errFlowControl        = 0x03
	errStreamLimit        = 0x04
	errStreamState        = 0x05
	errFinalSize          = 0x06
	errFrameEncoding      = 0x07
	errTransportParameter = 0x08
	errConnectionIDLimit  = 0x09
	errProtocolViolation  = 0x0a
	errCryptoBuffer       = 0x0d
	errKeyUpdate          = 0x0e
	errAEADLimit          = 0x0f
	errCrypto             = 0x100 // plus the tls alert
)

// http/3 and qpack error codes, rfc 9114 section 8.1 and rfc 9204 section 6
const (
	h3NoError              = 0x100
	h3GeneralProtocol      = 0x101
	h3StreamCreation       = 0x103
	h3ClosedCriticalStream = 0x104
	h3FrameUnexpected      = 0x105
	h3Frame                = 0x106
	h3MissingSettings      = 0x10a
	h3RequestCancelled     = 0x10c
	h3MessageError         = 0x10e
	qpackDecompression     = 0x200
)

// connError closes the whole connection
type connError struct {
	app    bool // an http/3 code rather than a quic one
	code   uint64
	reason string
	remote bool // the server closed it
}

func (e *connError) Error() string {
	who := "closed"
	if e.remote {
		who = "closed by the server"
	}
	kind := "quic"
	if e.app {
		kind = "http/3"
	}
	if e.reason != "" {
		return fmt.Sprintf("h3: connection %s (%s error 0x%x: %s)", who, kind, e.code, e.reason)
	}
	return fmt.Sprintf("h3: connection %s (%s error 0x%x)", who, kind, e.code)
}

func transportErr(code uint64, reason string) error { return &connError{code: code, reason: reason} }
func appErr(code uint64, reason string) error {
	return &connError{app: true, code: code, reason: reason}
}

var (
	// these mean the request never reached the server so another connection can take it
	ErrIdle      = errors.New("h3: connection went idle")
	ErrReset     = errors.New("h3: connection reset by the server")
	errGoingAway = errors.New("h3: server is going away")
	errClosed    = errors.New("h3: connection closed")
	errStale     = errors.New("h3: connection stopped answering")

	// the server or the path doesn't do http/3
	ErrNoVersion   = errors.New("h3: server doesn't speak QUIC v1")
	ErrUnreachable = errors.New("h3: UDP to the server is blocked or refused")
	ErrHandshake   = errors.New("h3: no answer to the QUIC handshake")
)

// retryable is true when the request can safely go out again on a fresh connection
func retryable(err error) bool {
	var ce *connError
	if errors.As(err, &ce) {
		// a clean close from either side, nothing was wrong with the request
		return ce.app && ce.code == h3NoError || ce.remote && !ce.app && ce.code == errNoError
	}
	return errors.Is(err, ErrIdle) || errors.Is(err, ErrReset) || errors.Is(err, errGoingAway) || errors.Is(err, errClosed) || errors.Is(err, errStale)
}
