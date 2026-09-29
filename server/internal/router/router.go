package router

import (
	"context"
	"time"

	"dio_wapp/server/internal/config"
	"dio_wapp/server/internal/handler"
	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/middlewares/server/recovery"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"gorm.io/gorm"
)

func New(cfg config.Config, gormDB *gorm.DB, authHandler *handler.AuthHandler, pointsHandler *handler.PointsHandler, shopHandler *handler.ShopHandler, adminGoodsHandler *handler.AdminGoodsHandler, adminStaffHandler *handler.AdminStaffHandler, goodsApprovalHandler *handler.GoodsApprovalHandler, adminReportHandler *handler.AdminReportHandler, staffWorkbenchHandler *handler.StaffWorkbenchHandler, adminOperationsHandler *handler.AdminOperationsHandler, jwtManager *service.JWTManager, authService *service.AuthService, rateStore middleware.DistributedRateStore) *server.Hertz {
	h := server.New(
		server.WithHostPorts(cfg.HTTPAddr),
		server.WithReadTimeout(10*time.Second),
		server.WithWriteTimeout(15*time.Second),
		server.WithIdleTimeout(60*time.Second),
		server.WithMaxRequestBodySize(64<<10),
		server.WithMaxHeaderBytes(16<<10),
	)
	h.Use(middleware.RequestObservability(nil), recovery.Recovery())

	h.GET("/healthz", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(consts.StatusOK, map[string]string{"status": "ok"})
	})
	h.GET("/readyz", func(ctx context.Context, c *app.RequestContext) {
		sqlDB, err := gormDB.DB()
		if err != nil {
			response.Error(c, consts.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
			return
		}
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(pingCtx); err != nil {
			response.Error(c, consts.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
			return
		}
		c.JSON(consts.StatusOK, map[string]string{"status": "ready"})
	})

	loginLimiter := middleware.NewDistributedFixedWindowLimiter(120, time.Minute, 10000, "dio:rate:login", rateStore)
	apiLimiter := middleware.NewDistributedFixedWindowLimiter(300, time.Minute, 10000, "dio:rate:api", rateStore)
	qrLimiter := middleware.NewDistributedFixedWindowLimiter(30, time.Minute, 10000, "dio:rate:qr", rateStore)
	exportLimiter := middleware.NewDistributedFixedWindowLimiter(10, time.Minute, 10000, "dio:rate:export", rateStore)
	v1 := h.Group("/api/v1")
	v1.POST("/auth/wechat-login", loginLimiter.ByIP(), authHandler.WechatLogin)

	authenticated := v1.Group("", middleware.Auth(jwtManager, authService), apiLimiter.ByUser())
	authenticated.GET("/auth/me", authHandler.Me)
	authenticated.POST("/users/me/identity-qrcode", qrLimiter.ByUser(), pointsHandler.CreateIdentityQRCode)
	authenticated.GET("/users/me/point-ledgers", pointsHandler.ListMyLedgers)
	authenticated.POST("/staff/points/add", pointsHandler.StaffAddPoints)
	authenticated.GET("/staff/points/corrections", pointsHandler.ListOwnPointCorrections)
	authenticated.POST("/staff/points/corrections", pointsHandler.SubmitPointCorrection)
	authenticated.GET("/staff/workbench", staffWorkbenchHandler.Get)
	authenticated.GET("/team-leader/points/requests", pointsHandler.ListPointGrantRequests)
	authenticated.POST("/team-leader/points/requests/:id/approve", pointsHandler.ApprovePointGrant)
	authenticated.POST("/team-leader/points/requests/:id/reject", pointsHandler.RejectPointGrant)
	authenticated.GET("/team-leader/points/corrections", pointsHandler.ListReviewPointCorrections)
	authenticated.POST("/team-leader/points/corrections/:id/approve", pointsHandler.ApprovePointCorrection)
	authenticated.POST("/team-leader/points/corrections/:id/reject", pointsHandler.RejectPointCorrection)
	authenticated.GET("/admin/points/users/:id", pointsHandler.GetAdminPointUser)
	authenticated.GET("/admin/points/users/:id/ledgers", pointsHandler.ListAdminUserLedgers)
	authenticated.POST("/admin/points/adjust", pointsHandler.AdminAdjustPoints)
	authenticated.GET("/staff/goods", goodsApprovalHandler.ListStaffGoods)
	authenticated.GET("/staff/goods/requests", goodsApprovalHandler.ListStaffRequests)
	authenticated.POST("/staff/goods/requests", goodsApprovalHandler.SubmitStaffRequest)
	authenticated.GET("/team-leader/goods/requests", goodsApprovalHandler.ListTeamLeaderRequests)
	authenticated.POST("/team-leader/goods/requests/:id/approve", goodsApprovalHandler.Approve)
	authenticated.POST("/team-leader/goods/requests/:id/reject", goodsApprovalHandler.Reject)
	authenticated.GET("/shop/groups", shopHandler.ListGroups)
	authenticated.GET("/goods", shopHandler.ListGoods)
	authenticated.GET("/goods/:id", shopHandler.GetGoods)
	authenticated.POST("/exchange/orders", shopHandler.ExchangeGoods)
	authenticated.GET("/exchange/requests/:request_id", shopHandler.RecoverExchange)
	authenticated.GET("/exchange/orders", shopHandler.ListOrders)
	authenticated.GET("/exchange/orders/:id", shopHandler.GetOrder)
	authenticated.POST("/exchange/orders/:id/cancel", shopHandler.CancelOrder)
	authenticated.POST("/exchange/orders/:id/redeem-qrcode", shopHandler.CreateRedeemQRCode)
	authenticated.POST("/staff/exchange/orders/redeem", shopHandler.StaffRedeemOrder)
	authenticated.GET("/admin/groups", adminGoodsHandler.ListGroups)
	authenticated.POST("/admin/groups", adminGoodsHandler.CreateGroup)
	authenticated.PUT("/admin/groups/:id", adminGoodsHandler.UpdateGroup)
	authenticated.DELETE("/admin/groups/:id", adminGoodsHandler.DeleteGroup)
	authenticated.GET("/admin/goods", adminGoodsHandler.ListGoods)
	authenticated.POST("/admin/goods", adminGoodsHandler.CreateGoods)
	authenticated.PUT("/admin/goods/:id", adminGoodsHandler.UpdateGoods)
	authenticated.DELETE("/admin/goods/:id", adminGoodsHandler.DeleteGoods)
	authenticated.GET("/admin/staff-members", adminStaffHandler.List)
	authenticated.POST("/admin/staff-members", adminStaffHandler.Save)
	authenticated.PUT("/admin/staff-members/:id", adminStaffHandler.Update)
	authenticated.DELETE("/admin/staff-members/:id", adminStaffHandler.Disable)
	authenticated.GET("/admin/goods/requests", goodsApprovalHandler.ListAdminRequests)
	authenticated.POST("/admin/goods/requests/:id/approve", goodsApprovalHandler.Approve)
	authenticated.POST("/admin/goods/requests/:id/reject", goodsApprovalHandler.Reject)
	authenticated.GET("/admin/reports/overview", adminReportHandler.Overview)
	authenticated.GET("/admin/reports/reconciliation", exportLimiter.ByUser(), adminReportHandler.Reconcile)
	authenticated.GET("/admin/operations/sales", adminOperationsHandler.ListSales)
	authenticated.GET("/admin/operations/orders", adminOperationsHandler.ListOrders)
	authenticated.GET("/admin/operations/export", exportLimiter.ByUser(), adminOperationsHandler.Export)

	return h
}
