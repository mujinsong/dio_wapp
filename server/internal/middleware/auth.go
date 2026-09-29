package middleware

import (
	"context"
	"strings"

	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const UserIDKey = "user_id"

func Auth(jwtManager *service.JWTManager, authService *service.AuthService) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		header := string(c.Request.Header.Peek("Authorization"))
		if header == "" {
			response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
			c.Abort()
			return
		}

		tokenText := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if tokenText == "" || tokenText == header {
			response.Error(c, consts.StatusUnauthorized, "invalid_token", "invalid authorization token")
			c.Abort()
			return
		}

		claims, err := jwtManager.Parse(tokenText)
		if err != nil {
			response.FromError(c, err)
			c.Abort()
			return
		}
		if err := authService.EnsureActiveUser(ctx, claims.UserID); err != nil {
			response.FromError(c, err)
			c.Abort()
			return
		}

		c.Set(UserIDKey, claims.UserID)
		c.Next(ctx)
	}
}

func CurrentUserID(c *app.RequestContext) (uint64, bool) {
	value, ok := c.Get(UserIDKey)
	if !ok {
		return 0, false
	}
	userID, ok := value.(uint64)
	return userID, ok
}
