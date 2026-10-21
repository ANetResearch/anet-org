// Package ratelimit counts calls in fixed windows, in memory.
//
// A restart forgets the counts. That is acceptable: the anet daemon in
// front of the program keeps its own per-caller quotas, and these limits
// exist to keep one member from flooding the others, not to bill anyone.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter counts by key in a one-minute and a one-day (UTC) window.
type Limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	m     map[string]*window
	calls int
}

type window struct {
	minute    int64 // unix minute of the current minute window
	minCount  int
	day       int64 // unix day of the current day window
	dayCount  int
	lastTouch int64
}

// New returns a limiter. now may be nil (time.Now).
func New(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, m: map[string]*window{}}
}

// Allow counts one call for key and reports whether it is within perMinute
// and perDay (a limit of 0 or less is unlimited). A refused call is not
// counted. retryAfter is the number of seconds until the window that
// refused it ends.
func (l *Limiter) Allow(key string, perMinute, perDay int) (ok bool, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now().UTC()
	sec := t.Unix()
	minute, day := sec/60, sec/86400
	w := l.m[key]
	if w == nil {
		w = &window{}
		l.m[key] = w
	}
	if w.minute != minute {
		w.minute, w.minCount = minute, 0
	}
	if w.day != day {
		w.day, w.dayCount = day, 0
	}
	w.lastTouch = sec
	if perDay > 0 && w.dayCount >= perDay {
		return false, int((day+1)*86400 - sec)
	}
	if perMinute > 0 && w.minCount >= perMinute {
		return false, int((minute+1)*60 - sec)
	}
	w.minCount++
	w.dayCount++
	l.calls++
	if l.calls%1024 == 0 {
		l.sweep(sec)
	}
	return true, 0
}

// sweep drops keys untouched for two days.
func (l *Limiter) sweep(now int64) {
	for k, w := range l.m {
		if now-w.lastTouch > 2*86400 {
			delete(l.m, k)
		}
	}
}

// Len reports the number of keys held (for tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}
