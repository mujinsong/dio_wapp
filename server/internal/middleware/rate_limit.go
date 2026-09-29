package middleware

import (
	"context"
	"strconv"
	"sync"
	"time"

	"dio_wapp/server/internal/response"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type rateWindow struct {
	Count     int
	ExpiresAt time.Time
}

type DistributedRateStore interface {
	AllowRate(context.Context, string, int, time.Duration) (bool, error)
}

type FixedWindowLimiter struct {
	mu         sync.Mutex
	limit      int
	window     time.Duration
	maxEntries int
	entries    map[string]rateWindow
	namespace  string
	store      DistributedRateStore
}

func NewFixedWindowLimiter(limit int, window time.Duration, maxEntries int) *FixedWindowLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	return &FixedWindowLimiter{
		limit:      limit,
		window:     window,
		maxEntries: maxEntries,
		entries:    make(map[string]rateWindow),
	}
}

func NewDistributedFixedWindowLimiter(limit int, window time.Duration, maxEntries int, namespace string, store DistributedRateStore) *FixedWindowLimiter {
	limiter := NewFixedWindowLimiter(limit, window, maxEntries)
	limiter.namespace = namespace
	limiter.store = store
	return limiter
}

func (l *FixedWindowLimiter) ByIP() app.HandlerFunc {
	return l.middleware(func(c *app.RequestContext) string {
		key := c.ClientIP()
		if key == "" {
			return "unknown"
		}
		return key
	})
}

func (l *FixedWindowLimiter) ByUser() app.HandlerFunc {
	return l.middleware(func(c *app.RequestContext) string {
		if userID, ok := CurrentUserID(c); ok {
			return strconv.FormatUint(userID, 10)
		}
		key := c.ClientIP()
		if key == "" {
			return "unknown"
		}
		return "ip:" + key
	})
}

func (l *FixedWindowLimiter) middleware(keyFn func(*app.RequestContext) string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !l.allowRequest(ctx, keyFn(c), time.Now()) {
			response.Error(c, consts.StatusTooManyRequests, "rate_limited", "too many requests, please retry later")
			c.Abort()
			return
		}
		c.Next(ctx)
	}
}

func (l *FixedWindowLimiter) allowRequest(ctx context.Context, key string, now time.Time) bool {
	if l.store != nil && l.namespace != "" {
		allowed, err := l.store.AllowRate(ctx, l.namespace+":"+key, l.limit, l.window)
		if err == nil {
			return allowed
		}
	}
	return l.allow(key, now)
}

func (l *FixedWindowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, exists := l.entries[key]
	if exists && now.Before(current.ExpiresAt) {
		if current.Count >= l.limit {
			return false
		}
		current.Count++
		l.entries[key] = current
		return true
	}

	if !exists && len(l.entries) >= l.maxEntries {
		for entryKey, entry := range l.entries {
			if !now.Before(entry.ExpiresAt) {
				delete(l.entries, entryKey)
			}
		}
		if len(l.entries) >= l.maxEntries {
			return false
		}
	}
	l.entries[key] = rateWindow{Count: 1, ExpiresAt: now.Add(l.window)}
	return true
}
