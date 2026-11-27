package h3

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// a server that never answers is told apart from a slow one, and the context's error is still there
func TestTimeoutSaysNoReply(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close() // reads nothing, so the handshake goes unanswered
	cl := &Client{}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = cl.Exchange(ctx, "https://"+pc.LocalAddr().String()+"/dns-query", []byte{0, 0})
	var te *TimeoutError
	if !errors.As(err, &te) || !te.NoReply() {
		t.Fatalf("want no reply, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost the context's error: %v", err)
	}
	if err.Error() != "h3: no reply from the server over UDP" {
		t.Fatalf("message %q", err)
	}
}

func TestTimeoutStages(t *testing.T) {
	ctxErr := context.DeadlineExceeded
	c := &Conn{}
	if err := c.timedOut(ctxErr); err.Error() != msgNoReply {
		t.Errorf("nothing heard: %v", err)
	}
	c.heard.Store(true)
	if err := c.timedOut(ctxErr); err.Error() != msgHandshake {
		t.Errorf("heard, no handshake: %v", err)
	}
	c.ready.Store(true)
	if err := c.timedOut(ctxErr); err.Error() != msgSlow {
		t.Errorf("connected: %v", err)
	}
}
