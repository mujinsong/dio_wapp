package middleware

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestNormalizedRequestIDAcceptsSafeValueAndReplacesUnsafeValue(t *testing.T) {
	if got := normalizedRequestID("request-1234"); got != "request-1234" {
		t.Fatalf("safe request id changed: %s", got)
	}
	for _, unsafe := range []string{"short", "request id with spaces", "request-id\nforged-log"} {
		got := normalizedRequestID(unsafe)
		if got == unsafe || len(got) < 8 {
			t.Fatalf("unsafe request id was not replaced: %q", got)
		}
	}
}

func TestRequestObservabilitySetsCorrelationAndSecurityHeaders(t *testing.T) {
	requestContext := app.NewContext(0)
	requestContext.Request.Header.Set(requestIDHeader, "client-request-123")
	requestContext.Request.Header.SetMethod("GET")
	requestContext.Request.SetRequestURI("/api/v1/test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	RequestObservability(logger)(context.Background(), requestContext)

	if got := string(requestContext.Response.Header.Peek(requestIDHeader)); got != "client-request-123" {
		t.Fatalf("unexpected response request id: %q", got)
	}
	if got := string(requestContext.Response.Header.Peek("Cache-Control")); got != "no-store" {
		t.Fatalf("unexpected cache control: %q", got)
	}
	if got := string(requestContext.Response.Header.Peek("X-Content-Type-Options")); got != "nosniff" {
		t.Fatalf("unexpected content type protection: %q", got)
	}
}
