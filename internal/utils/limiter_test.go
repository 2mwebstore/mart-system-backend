package utils

import (
	"fmt"
	"testing"
	"time"
)

func TestSlidingWindow(t *testing.T) {
	l := NewSlidingWindow(time.Minute, 2)
	now := time.Now()
	if !l.Allow("a", now) || !l.Allow("a", now) {
		t.Fatal("hits within the limit must pass")
	}
	if l.Allow("a", now) {
		t.Fatal("a hit over the limit must be refused")
	}
	if !l.Allow("b", now) {
		t.Fatal("each key has its own allowance")
	}
	if !l.Allow("a", now.Add(time.Minute)) {
		t.Fatal("allowed again once the window has passed")
	}
}

func TestSlidingWindowRefusedHitsDontExtendTheWait(t *testing.T) {
	l := NewSlidingWindow(time.Minute, 1)
	now := time.Now()
	l.Allow("a", now)
	for i := 1; i < 60; i++ {
		l.Allow("a", now.Add(time.Duration(i)*time.Second))
	}
	if !l.Allow("a", now.Add(time.Minute)) {
		t.Fatal("hammering while refused must not push the reopening back")
	}
}

func TestSlidingWindowDropsIdleKeys(t *testing.T) {
	l := NewSlidingWindow(time.Minute, 5)
	now := time.Now()
	for i := 0; i <= slidingWindowMaxKeys; i++ {
		l.Allow(fmt.Sprintf("key-%d", i), now)
	}
	l.Allow("late", now.Add(2*time.Minute))
	if n := len(l.hits); n > 2 {
		t.Fatalf("idle keys should have been swept, %d remain", n)
	}
}
