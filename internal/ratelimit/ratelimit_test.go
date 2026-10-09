package ratelimit

import (
	"fmt"
	"testing"
)

func TestBurstThenLimit(t *testing.T) {
	l := New(1, 5, 100)
	ok := 0
	for range 20 {
		if l.Allow("a") {
			ok++
		}
	}
	if ok != 5 {
		t.Fatalf("allowed %d, want burst of 5", ok)
	}
	if !l.Allow("b") {
		t.Fatal("other clients must not be affected")
	}
}

func TestKeyCapBoundsMemory(t *testing.T) {
	l := New(10, 10, 50)
	for i := range 10000 {
		l.Allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if n := l.Len(); n > 50 {
		t.Fatalf("tracking %d keys, cap is 50", n)
	}
}
