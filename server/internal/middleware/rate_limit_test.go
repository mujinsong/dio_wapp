package middleware

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rateStoreStub struct {
	allowed bool
	err     error
	key     string
	limit   int
	window  time.Duration
}

func (s *rateStoreStub) AllowRate(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
	s.key = key
	s.limit = limit
	s.window = window
	return s.allowed, s.err
}

func TestFixedWindowLimiterResetsAndBoundsEntries(t *testing.T) {
	limiter := NewFixedWindowLimiter(2, time.Minute, 2)
	now := time.Now()
	if !limiter.allow("user-1", now) || !limiter.allow("user-1", now) {
		t.Fatal("expected requests inside the limit to pass")
	}
	if limiter.allow("user-1", now) {
		t.Fatal("expected request over the limit to be rejected")
	}
	if !limiter.allow("user-2", now) {
		t.Fatal("expected second key to pass")
	}
	if limiter.allow("user-3", now) {
		t.Fatal("expected a new key to be rejected at the entry bound")
	}
	if !limiter.allow("user-1", now.Add(time.Minute)) {
		t.Fatal("expected expired window to reset")
	}
	if !limiter.allow("user-3", now.Add(time.Minute)) {
		t.Fatal("expected expired entries to be cleaned")
	}
}

func TestDistributedLimiterUsesSharedStore(t *testing.T) {
	store := &rateStoreStub{allowed: false}
	limiter := NewDistributedFixedWindowLimiter(7, 2*time.Minute, 10, "dio:rate:test", store)
	if limiter.allowRequest(context.Background(), "user-42", time.Now()) {
		t.Fatal("expected distributed store rejection")
	}
	if store.key != "dio:rate:test:user-42" || store.limit != 7 || store.window != 2*time.Minute {
		t.Fatalf("unexpected distributed request: %+v", store)
	}
}

func TestDistributedLimiterFallsBackLocally(t *testing.T) {
	store := &rateStoreStub{err: errors.New("redis unavailable")}
	limiter := NewDistributedFixedWindowLimiter(1, time.Minute, 10, "dio:rate:test", store)
	now := time.Now()
	if !limiter.allowRequest(context.Background(), "user-42", now) {
		t.Fatal("expected first fallback request to pass")
	}
	if limiter.allowRequest(context.Background(), "user-42", now) {
		t.Fatal("expected local fallback limit to apply")
	}
}
