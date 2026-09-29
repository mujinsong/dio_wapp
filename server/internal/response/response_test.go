package response

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestErrorIncludesRequestID(t *testing.T) {
	ctx := app.NewContext(0)
	ctx.Set("request_id", "request-1234")
	Error(ctx, 400, "invalid_request", "invalid request")

	var payload struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Code != "invalid_request" || payload.Error.RequestID != "request-1234" {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}
