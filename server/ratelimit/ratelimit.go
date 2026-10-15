package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	count     int
	windowEnd time.Time
}

type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*bucket
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, buckets: map[string]*bucket{}}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok || !now.Before(b.windowEnd) {
		l.buckets[key] = &bucket{count: 1, windowEnd: now.Add(l.window)}
		if len(l.buckets) > 100000 {
			for k, v := range l.buckets {
				if !now.Before(v.windowEnd) {
					delete(l.buckets, k)
				}
			}
		}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}
