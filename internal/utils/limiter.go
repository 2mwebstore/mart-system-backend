package utils

import (
	"sync"
	"time"
)

// slidingWindowMaxKeys is how many keys a limiter holds before it sweeps out
// idle ones, so a stream of made-up keys can't grow the map without bound.
const slidingWindowMaxKeys = 5000

// SlidingWindow allows at most max hits per key in any window-long span. It
// lives in this process's memory, so it resets on restart and isn't shared
// between replicas: it slows abuse down, and isn't the only control on anything.
type SlidingWindow struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	hits   map[string][]time.Time
}

func NewSlidingWindow(window time.Duration, max int) *SlidingWindow {
	return &SlidingWindow{window: window, max: max, hits: map[string][]time.Time{}}
}

// Allow records a hit for key and reports whether it was within the limit. A
// refused hit isn't counted, so waiting out the window always works.
func (l *SlidingWindow) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > slidingWindowMaxKeys {
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= l.window {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// Window is the span the limit applies over.
func (l *SlidingWindow) Window() time.Duration { return l.window }
