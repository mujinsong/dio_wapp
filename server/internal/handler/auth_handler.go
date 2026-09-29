package handler

import (
	"context"
	"strings"

	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type AuthHandler struct {
	authService *service.AuthService
	jwtManager  *service.JWTManager
}

type wechatLoginRequest struct {
	Code string `json:"code"`
}

func NewAuthHandler(authService *service.AuthService, jwtManager *service.JWTManager) *AuthHandler {
	return &AuthHandler{authService: authService, jwtManager: jwtManager}
}

func (h *AuthHandler) WechatLogin(ctx context.Context, c *app.RequestContext) {
	var req wechatLoginRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		response.Error(c, consts.StatusBadRequest, "invalid_code", "code is required")
		return
	}

	result, err := h.authService.Login(ctx, req.Code)
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, result)
}

func (h *AuthHandler) Me(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}

	user, err := h.authService.Me(ctx, userID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{"user": user})
}
