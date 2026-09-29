package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"dio_wapp/server/internal/xerr"
)

const (
	defaultSessionEndpoint   = "https://api.weixin.qq.com/sns/jscode2session"
	maxLoginCodeBytes        = 256
	maxSessionResponseSize   = 64 << 10
	maxWeChatIdentifierRunes = 128
)

type Session struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid"`
	SessionKey string `json:"session_key"`
}

type Client struct {
	appEnv     string
	appID      string
	secret     string
	allowMock  bool
	endpoint   string
	httpClient *http.Client
}

func NewClient(appEnv string, appID string, secret string, allowMock bool) *Client {
	return &Client{
		appEnv:    appEnv,
		appID:     appID,
		secret:    secret,
		allowMock: allowMock,
		endpoint:  defaultSessionEndpoint,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) Code2Session(ctx context.Context, code string) (Session, error) {
	code = strings.TrimSpace(code)
	if code == "" || !utf8.ValidString(code) || len(code) > maxLoginCodeBytes {
		return Session{}, xerr.New(400, "invalid_code", "wechat login code is invalid")
	}
	if c.appEnv == "local" && c.allowMock && strings.HasPrefix(code, "mock:") {
		openID := strings.TrimPrefix(code, "mock:")
		if !validWeChatIdentifier(openID, false) {
			return Session{}, xerr.New(400, "invalid_code", "mock code contains an invalid openid")
		}
		return Session{OpenID: openID}, nil
	}

	if c.appID == "" || c.secret == "" {
		return Session{}, xerr.New(500, "wechat_not_configured", "wechat appid or secret is not configured")
	}

	query := url.Values{}
	query.Set("appid", c.appID)
	query.Set("secret", c.secret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return Session{}, xerr.Wrap(500, "wechat_request_error", "failed to create wechat request", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Session{}, xerr.Wrap(502, "wechat_request_failed", "failed to request wechat session", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxSessionResponseSize+1))
	if err != nil {
		return Session{}, xerr.Wrap(502, "wechat_response_error", "failed to read wechat response", err)
	}
	if len(payload) > maxSessionResponseSize {
		return Session{}, xerr.New(502, "wechat_response_too_large", "wechat response is too large")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Session{}, xerr.Wrap(502, "wechat_request_failed", "wechat service returned an invalid status", fmt.Errorf("http status %d", resp.StatusCode))
	}

	var body struct {
		OpenID     string `json:"openid"`
		UnionID    string `json:"unionid"`
		SessionKey string `json:"session_key"`
		ErrCode    int    `json:"errcode"`
		ErrMsg     string `json:"errmsg"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return Session{}, xerr.Wrap(502, "wechat_response_error", "failed to decode wechat response", err)
	}
	if body.ErrCode != 0 {
		return Session{}, xerr.Wrap(401, "wechat_login_failed", "wechat login failed", fmt.Errorf("wechat error %d: %s", body.ErrCode, body.ErrMsg))
	}
	if !validWeChatIdentifier(body.OpenID, false) || !validWeChatIdentifier(body.UnionID, true) {
		return Session{}, xerr.New(502, "wechat_response_error", "wechat response contains invalid identity data")
	}

	return Session{
		OpenID:     body.OpenID,
		UnionID:    body.UnionID,
		SessionKey: body.SessionKey,
	}, nil
}

func validWeChatIdentifier(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxWeChatIdentifierRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}
