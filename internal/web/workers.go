package web

import "context"

func (s *Server) initWorkers() {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	if s.workerCtx != nil {
		return
	}
	parent := s.Context
	if parent == nil {
		parent = context.Background()
	}
	s.workerCtx, s.workerCancel = context.WithCancel(parent)
}

func (s *Server) startWorker(fn func(context.Context)) bool {
	s.initWorkers()
	s.workMu.Lock()
	defer s.workMu.Unlock()
	if s.stopping || s.workerCtx.Err() != nil {
		return false
	}
	s.workWG.Add(1)
	go func() { defer s.workWG.Done(); fn(s.workerCtx) }()
	return true
}

func (s *Server) CloseWorkers(ctx context.Context) error {
	s.workMu.Lock()
	s.stopping = true
	if s.workerCancel != nil {
		s.workerCancel()
	}
	s.workMu.Unlock()
	done := make(chan struct{})
	go func() { s.workWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
