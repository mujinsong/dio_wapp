package redislock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

const retryInterval = 25 * time.Millisecond

var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

var rateLimitScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return count <= tonumber(ARGV[1])
`)

type Locker struct {
	client *redis.Client
}

func New(addr string, password string, db int) *Locker {
	return &Locker{client: redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})}
}

func (l *Locker) Ping(ctx context.Context) error {
	return l.client.Ping(ctx).Err()
}

func (l *Locker) Close() error {
	return l.client.Close()
}

func (l *Locker) AllowRate(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	result, err := rateLimitScript.Run(ctx, l.client, []string{key}, limit, window.Milliseconds()).Bool()
	if err != nil {
		return false, err
	}
	return result, nil
}

func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration, wait time.Duration) (func(context.Context) error, bool, error) {
	token, err := lockToken()
	if err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(wait)

	for {
		acquired, err := l.client.SetNX(ctx, key, token, ttl).Result()
		if err != nil {
			return nil, false, err
		}
		if acquired {
			release := func(releaseCtx context.Context) error {
				return unlockScript.Run(releaseCtx, l.client, []string{key}, token).Err()
			}
			return release, true, nil
		}
		if wait <= 0 || time.Now().After(deadline) {
			return nil, false, nil
		}

		remaining := time.Until(deadline)
		delay := retryInterval
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, false, ctx.Err()
		case <-timer.C:
		}
	}
}

func lockToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
