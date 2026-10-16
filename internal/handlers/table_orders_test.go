package handlers

import (
	"testing"
	"time"
)

func TestOrderLimiter(t *testing.T) {
	l := &orderLimiter{hits: map[string][]time.Time{}}
	now := time.Now()
	for i := 0; i < publicOrderMax; i++ {
		if !l.allow("tbl|1.2.3.4", now) {
			t.Fatalf("order %d should be allowed", i+1)
		}
	}
	if l.allow("tbl|1.2.3.4", now) {
		t.Fatal("order over the limit should be refused")
	}
	if !l.allow("tbl|5.6.7.8", now) {
		t.Fatal("another phone at the same table has its own allowance")
	}
	if !l.allow("tbl|1.2.3.4", now.Add(publicOrderWindow+time.Second)) {
		t.Fatal("allowed again once the window has passed")
	}
}

func TestRandomToken(t *testing.T) {
	a, _ := randomToken(12)
	b, _ := randomToken(12)
	if len(a) != 24 || a == b {
		t.Fatalf("want two different 24-char tokens, got %q and %q", a, b)
	}
}

func TestSaleTableOrderIDs(t *testing.T) {
	one := uint64(7)
	r := createSaleReq{TableOrderIDs: []uint64{7, 3, 3}, TableOrderID: &one}
	got := r.tableOrderIDs()
	if len(got) != 2 || got[0] != 3 || got[1] != 7 {
		t.Fatalf("want [3 7], got %v", got)
	}
	if n := len((createSaleReq{}).tableOrderIDs()); n != 0 {
		t.Fatalf("no table orders: want none, got %d", n)
	}
}
