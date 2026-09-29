package main

import (
	"context"
	"log"
	"time"

	"dio_wapp/server/internal/config"
	"dio_wapp/server/internal/db"
	"dio_wapp/server/internal/handler"
	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/migration"
	"dio_wapp/server/internal/redislock"
	"dio_wapp/server/internal/router"
	"dio_wapp/server/internal/service"
	"dio_wapp/server/internal/wechat"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	gormDB, err := db.Open(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	if err := migration.Run(context.Background(), gormDB); err != nil {
		log.Fatalf("database migration: %v", err)
	}
	agency, err := service.EnsureSingleAgency(context.Background(), gormDB, cfg.DefaultAgencyName)
	if err != nil {
		log.Fatalf("ensure single agency: %v", err)
	}
	if cfg.AppEnv == "local" {
		if err := service.EnsureLocalDemoGoods(context.Background(), gormDB, cfg.DefaultAgencyName); err != nil {
			log.Fatalf("seed demo goods: %v", err)
		}
	}

	jwtManager := service.NewJWTManager(cfg.JWTSecret, cfg.JWTTTL)
	wechatClient := wechat.NewClient(cfg.AppEnv, cfg.WeChatAppID, cfg.WeChatSecret, cfg.MockLoginEnabled)
	authService := service.NewAuthService(gormDB, wechatClient, jwtManager, cfg.AdminOpenIDs, cfg.DefaultAgencyName, cfg.StaffOpenIDs, cfg.TeamLeaderOpenIDs)
	authHandler := handler.NewAuthHandler(authService, jwtManager)
	pointsService := service.NewPointsService(gormDB, authService)
	pointsHandler := handler.NewPointsHandler(pointsService)
	shopService := service.NewShopService(gormDB, agency.ID)
	shopService.SetOrderRedeemTTL(cfg.OrderRedeemTTL)
	var rateStore middleware.DistributedRateStore
	if cfg.RedisEnabled {
		locker := redislock.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pingErr := locker.Ping(pingCtx)
		cancel()
		if pingErr != nil {
			log.Printf("redis unavailable, exchange will use MySQL locks only: %v", pingErr)
			_ = locker.Close()
		} else {
			defer locker.Close()
			rateStore = locker
			shopService = service.NewShopServiceWithAgencyLocker(gormDB, agency.ID, locker, cfg.ExchangeLockTTL, cfg.ExchangeLockWait)
			shopService.SetOrderRedeemTTL(cfg.OrderRedeemTTL)
			log.Printf("redis exchange lock enabled: %s", cfg.RedisAddr)
		}
	}
	if err := shopService.BackfillOrderExpirations(context.Background()); err != nil {
		log.Fatalf("backfill order expirations: %v", err)
	}
	if _, err := shopService.ExpirePendingOrders(context.Background(), time.Now(), 500); err != nil {
		log.Printf("initial pending order expiration: %v", err)
	}
	maintenanceService := service.NewMaintenanceService(gormDB)
	if cleaned, err := maintenanceService.CleanupExpiredData(context.Background(), time.Now(), cfg.IdempotencyRetention, 1000); err != nil {
		log.Printf("initial maintenance cleanup: %v", err)
	} else if cleaned.IdentityTokensDeleted > 0 || cleaned.IdempotencyCompacted > 0 {
		log.Printf("maintenance cleanup: identity_tokens=%d idempotency_compacted=%d", cleaned.IdentityTokensDeleted, cleaned.IdempotencyCompacted)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go func() {
		ticker := time.NewTicker(cfg.OrderExpiryScan)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case now := <-ticker.C:
				scanCtx, cancel := context.WithTimeout(workerCtx, 30*time.Second)
				_, err := shopService.ExpirePendingOrders(scanCtx, now, 200)
				cancel()
				if err != nil {
					log.Printf("expire pending orders: %v", err)
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(cfg.MaintenanceScan)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case now := <-ticker.C:
				scanCtx, cancel := context.WithTimeout(workerCtx, 30*time.Second)
				cleaned, err := maintenanceService.CleanupExpiredData(scanCtx, now, cfg.IdempotencyRetention, 1000)
				cancel()
				if err != nil {
					log.Printf("maintenance cleanup: %v", err)
				} else if cleaned.IdentityTokensDeleted > 0 || cleaned.IdempotencyCompacted > 0 {
					log.Printf("maintenance cleanup: identity_tokens=%d idempotency_compacted=%d", cleaned.IdentityTokensDeleted, cleaned.IdempotencyCompacted)
				}
			}
		}
	}()
	shopHandler := handler.NewShopHandler(shopService)
	adminGoodsService := service.NewAdminGoodsService(gormDB, authService)
	adminGoodsHandler := handler.NewAdminGoodsHandler(adminGoodsService)
	adminStaffService := service.NewAdminStaffService(gormDB, authService)
	adminStaffHandler := handler.NewAdminStaffHandler(adminStaffService)
	goodsApprovalService := service.NewGoodsApprovalService(gormDB, authService, agency.ID)
	goodsApprovalHandler := handler.NewGoodsApprovalHandler(goodsApprovalService)
	adminReportService := service.NewAdminReportService(gormDB, authService)
	adminReportHandler := handler.NewAdminReportHandler(adminReportService)
	staffWorkbenchService := service.NewStaffWorkbenchService(gormDB, authService)
	staffWorkbenchHandler := handler.NewStaffWorkbenchHandler(staffWorkbenchService)
	adminOperationsService := service.NewAdminOperationsService(gormDB, authService)
	adminOperationsHandler := handler.NewAdminOperationsHandler(adminOperationsService)

	app := router.New(cfg, gormDB, authHandler, pointsHandler, shopHandler, adminGoodsHandler, adminStaffHandler, goodsApprovalHandler, adminReportHandler, staffWorkbenchHandler, adminOperationsHandler, jwtManager, authService, rateStore)
	app.Spin()
}
