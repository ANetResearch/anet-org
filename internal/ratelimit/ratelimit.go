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
	f     map[string]*fixed
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

// fixed is one fixed window of a key.
type fixed struct {
	start int64
	n     int
}

// Over reports whether key has reached limit events in the current fixed
// window of the given length, without counting anything, and the seconds
// until that window ends. A limit of 0 or less is never reached.
func (l *Limiter) Over(key string, window time.Duration, limit int) (bool, int) {
	if limit <= 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w, start, end := l.fixedWindow(key, window)
	if w == nil || w.start != start {
		return false, 0
	}
	return w.n >= limit, int(end - l.now().UTC().Unix())
}

// Add counts one event for key in its fixed window.
func (l *Limiter) Add(key string, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, start, _ := l.fixedWindow(key, window)
	if w == nil {
		w = &fixed{}
		if l.f == nil {
			l.f = map[string]*fixed{}
		}
		l.f[fixedKey(key, window)] = w
	}
	if w.start != start {
		w.start, w.n = start, 0
	}
	w.n++
	if len(l.f) > 100000 {
		now := l.now().UTC().Unix()
		for k, v := range l.f {
			if now-v.start > 2*86400 {
				delete(l.f, k)
			}
		}
	}
}

func fixedKey(key string, window time.Duration) string {
	return key + "@" + window.String()
}

// fixedWindow returns the stored window of key (or nil) and the bounds of
// the current one.
func (l *Limiter) fixedWindow(key string, window time.Duration) (*fixed, int64, int64) {
	sec := int64(window / time.Second)
	if sec <= 0 {
		sec = 1
	}
	now := l.now().UTC().Unix()
	start := now / sec * sec
	return l.f[fixedKey(key, window)], start, start + sec
}
