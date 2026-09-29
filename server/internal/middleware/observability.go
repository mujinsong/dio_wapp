package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

const (
	RequestIDKey    = "request_id"
	requestIDHeader = "X-Request-ID"
)

func RequestObservability(logger *slog.Logger) app.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, c *app.RequestContext) {
		startedAt := time.Now()
		requestID := normalizedRequestID(string(c.Request.Header.Peek(requestIDHeader)))
		c.Set(RequestIDKey, requestID)
		c.Response.Header.Set(requestIDHeader, requestID)
		c.Response.Header.Set("Cache-Control", "no-store")
		c.Response.Header.Set("X-Content-Type-Options", "nosniff")

		c.Next(ctx)

		status := c.Response.StatusCode()
		path := c.FullPath()
		if path == "" {
			path = "<unmatched>"
		}
		if (path == "/healthz" || path == "/readyz") && status < 400 {
			return
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		logger.LogAttrs(ctx, level, "http_request",
			slog.String("request_id", requestID),
			slog.String("method", string(c.Method())),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		)
	}
}

func normalizedRequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 8 && len(value) <= 64 {
		valid := true
		for _, char := range value {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
				continue
			}
			valid = false
			break
		}
		if valid {
			return value
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("fallback-%x", time.Now().UnixNano())
}
