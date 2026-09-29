package wechat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"dio_wapp/server/internal/xerr"
)

func TestCode2SessionValidatesAndParsesBoundedResponse(t *testing.T) {
	client := testClient(func(request *http.Request) *http.Response {
		if request.URL.Query().Get("appid") != "test-app" || request.URL.Query().Get("secret") != "test-secret" || request.URL.Query().Get("js_code") != "login-code" {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
		}
		return testResponse(http.StatusOK, `{"openid":"openid-1","unionid":"unionid-1","session_key":"session-1"}`)
	})

	session, err := client.Code2Session(context.Background(), "  login-code  ")
	if err != nil {
		t.Fatalf("code to session: %v", err)
	}
	if session.OpenID != "openid-1" || session.UnionID != "unionid-1" || session.SessionKey != "session-1" {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestCode2SessionRejectsInvalidCodeBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	client := testClient(func(_ *http.Request) *http.Response {
		requests.Add(1)
		return testResponse(http.StatusOK, `{"openid":"unexpected"}`)
	})

	_, err := client.Code2Session(context.Background(), strings.Repeat("x", maxLoginCodeBytes+1))
	assertWeChatErrorCode(t, err, "invalid_code")
	if requests.Load() != 0 {
		t.Fatalf("invalid code reached upstream %d times", requests.Load())
	}
}

func TestCode2SessionRejectsOversizedOrInvalidUpstreamResponse(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", maxSessionResponseSize+1), code: "wechat_response_too_large"},
		{name: "bad status", status: http.StatusServiceUnavailable, body: `{}`, code: "wechat_request_failed"},
		{name: "trailing json", status: http.StatusOK, body: `{"openid":"openid-1"} trailing`, code: "wechat_response_error"},
		{name: "oversized openid", status: http.StatusOK, body: fmt.Sprintf(`{"openid":%q}`, strings.Repeat("x", maxWeChatIdentifierRunes+1)), code: "wechat_response_error"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			client := testClient(func(_ *http.Request) *http.Response {
				return testResponse(testCase.status, testCase.body)
			})
			_, err := client.Code2Session(context.Background(), "login-code")
			assertWeChatErrorCode(t, err, testCase.code)
		})
	}
}

func TestMockCodeRejectsInvalidOpenID(t *testing.T) {
	client := NewClient("local", "", "", true)
	_, err := client.Code2Session(context.Background(), "mock:invalid openid")
	assertWeChatErrorCode(t, err, "invalid_code")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testClient(response func(*http.Request) *http.Response) *Client {
	client := NewClient("production", "test-app", "test-secret", false)
	client.endpoint = "https://wechat.test/session"
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request), nil
		}),
	}
	return client
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertWeChatErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
