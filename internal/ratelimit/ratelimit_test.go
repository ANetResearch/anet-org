package ratelimit

import (
	"testing"
	"time"
)

func TestMinuteAndDayWindows(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 5, 0, time.UTC)
	l := New(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a", 3, 5); !ok {
			t.Fatalf("call %d refused", i)
		}
	}
	ok, retry := l.Allow("a", 3, 5)
	if ok || retry != 55 {
		t.Fatalf("fourth call: ok=%v retry=%d", ok, retry)
	}
	if ok, _ := l.Allow("b", 3, 5); !ok {
		t.Fatal("keys are not independent")
	}
	now = now.Add(time.Minute)
	l.Allow("a", 3, 5)
	l.Allow("a", 3, 5)
	ok, retry = l.Allow("a", 3, 5)
	if ok {
		t.Fatal("day limit not applied")
	}
	if retry <= 60 {
		t.Fatalf("day retry %d", retry)
	}
	now = now.Add(24 * time.Hour)
	if ok, _ := l.Allow("a", 3, 5); !ok {
		t.Fatal("new day not reset")
	}
}

func TestUnlimited(t *testing.T) {
	l := New(nil)
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Allow("x", 0, 0); !ok {
			t.Fatal("unlimited refused")
		}
	}
}

func TestFixedWindowOverAndAdd(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	l := New(func() time.Time { return now })
	w := 10 * time.Minute
	for i := 0; i < 3; i++ {
		if over, _ := l.Over("k", w, 3); over {
			t.Fatalf("over after %d", i)
		}
		l.Add("k", w)
	}
	over, retry := l.Over("k", w, 3)
	if !over || retry != 600 {
		t.Fatalf("over=%v retry=%d", over, retry)
	}
	if over, _ := l.Over("other", w, 3); over {
		t.Fatal("keys not independent")
	}
	if over, _ := l.Over("k", time.Minute, 3); over {
		t.Fatal("windows not independent")
	}
	now = now.Add(w)
	if over, _ := l.Over("k", w, 3); over {
		t.Fatal("window not reset")
	}
}
