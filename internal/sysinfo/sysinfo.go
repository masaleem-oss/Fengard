package sysinfo

import (
	"runtime"
	"sync/atomic"
	"time"
)

// percent of one core so 100 means one core fully busy like top
type Sampler struct {
	percent atomic.Uint64 // x100 fixed point
	lastCPU time.Duration
	lastAt  time.Time
}

func (s *Sampler) Run(every time.Duration, stop <-chan struct{}) {
	s.lastCPU, s.lastAt = cpuTime(), time.Now()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			now, cpu := time.Now(), cpuTime()
			if wall := now.Sub(s.lastAt); wall > 0 && cpu >= s.lastCPU {
				s.percent.Store(uint64(float64(cpu-s.lastCPU) / float64(wall) * 10000))
			}
			s.lastCPU, s.lastAt = cpu, now
		}
	}
}

func (s *Sampler) CPUPercent() float64 { return float64(s.percent.Load()) / 100 }

func CPUTotal() time.Duration { return cpuTime() }

func Cores() int { return runtime.NumCPU() }
