package main

import (
	"sync"
	"time"
)

type rssSampler struct {
	stop chan struct{}
	done chan uint64
	once sync.Once
	peak uint64
}

func startRSSSampler() *rssSampler {
	s := &rssSampler{stop: make(chan struct{}), done: make(chan uint64, 1)}
	go func() {
		peak := currentRSSBytes()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if value := currentRSSBytes(); value > peak {
					peak = value
				}
			case <-s.stop:
				if value := currentRSSBytes(); value > peak {
					peak = value
				}
				s.done <- peak
				return
			}
		}
	}()
	return s
}

func (s *rssSampler) Stop() uint64 {
	s.once.Do(func() { close(s.stop); s.peak = <-s.done })
	return s.peak
}
