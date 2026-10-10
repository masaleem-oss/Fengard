package web

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDashboardShutdownCancelsAndJoinsBackgroundWork(t *testing.T) {
	s := &Server{}
	started := make(chan struct{})
	finished := make(chan struct{})
	if !s.startWorker(func(ctx context.Context) { close(started); <-ctx.Done(); close(finished) }) {
		t.Fatal("worker refused")
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.CloseWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before worker joined")
	}
	if s.startWorker(func(context.Context) { t.Error("worker started after shutdown") }) {
		t.Fatal("work admitted after shutdown")
	}
}

func TestDashboardShutdownHonorsDeadline(t *testing.T) {
	s := &Server{}
	release := make(chan struct{})
	if !s.startWorker(func(context.Context) { <-release }) {
		t.Fatal("worker refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.CloseWorkers(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown: %v", err)
	}
	close(release)
	if err := s.CloseWorkers(context.Background()); err != nil {
		t.Fatal(err)
	}
}
