package response

import (
	"errors"

	"dio_wapp/server/internal/xerr"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func Error(c *app.RequestContext, status int, code string, message string) {
	errorPayload := map[string]any{
		"code":    code,
		"message": message,
	}
	if requestID, ok := c.Get("request_id"); ok {
		if value, valid := requestID.(string); valid && value != "" {
			errorPayload["request_id"] = value
		}
	}
	c.JSON(status, map[string]any{"error": errorPayload})
}

func FromError(c *app.RequestContext, err error) {
	var appErr *xerr.Error
	if errors.As(err, &appErr) {
		Error(c, appErr.Status, appErr.Code, appErr.Message)
		return
	}

	Error(c, consts.StatusInternalServerError, "internal_error", "internal server error")
}
