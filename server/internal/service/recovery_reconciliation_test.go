package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
	"gorm.io/gorm"
)

func mustCreate(t *testing.T, db *gorm.DB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatal(err)
	}
}

func auditFixture(t *testing.T, db *gorm.DB) (model.User, model.Goods) {
	t.Helper()
	agency := model.Agency{Name: "audit-agency"}
	mustCreate(t, db, &agency)
	group := model.IdolGroup{AgencyID: agency.ID, Name: "audit-group"}
	mustCreate(t, db, &group)
	user := model.User{OpenID: "audit-user", PointsBalance: 100}
	mustCreate(t, db, &user)
	mustCreate(t, db, &model.AdminMember{AgencyID: agency.ID, UserID: user.ID})
	mustCreate(t, db, &model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100})
	mustCreate(t, db, &model.PointLedger{AgencyID: agency.ID, UserID: user.ID, DeltaPoints: 100, AfterPoints: 100, Type: PointLedgerAdminAdjust, OperatorID: user.ID})
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "audit-goods", PricePoints: 10, Stock: 2}
	mustCreate(t, db, &goods)
	mustCreate(t, db, &model.StockMovement{AgencyID: agency.ID, GroupID: group.ID, GoodsID: goods.ID, DeltaStock: 2, AfterStock: 2, Type: model.StockMovementInitial})
	return user, goods
}

func TestExchangeRecoveryRetainsOriginalOrder(t *testing.T) {
	db := newCatalogDBForTest(t)
	user, goods := auditFixture(t, db)
	shop := NewShopService(db)
	ctx := context.Background()
	exchange, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "recover-request-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	var record model.IdempotencyRecord
	if err := db.Where("request_id = ?", "recover-request-1").First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.ResourceID != exchange.Order.ID {
		t.Fatal("order reference not saved")
	}
	// Simulate a pre-migration response, then compact it and verify the reference is recovered.
	if err := db.Model(&record).Updates(map[string]any{"resource_id": 0, "created_at": time.Now().Add(-48 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewMaintenanceService(db).CleanupExpiredData(ctx, time.Now(), 24*time.Hour, 10); err != nil {
		t.Fatal(err)
	}
	recovered, err := shop.RecoverExchange(ctx, user.ID, goods.ID, record.RequestID)
	if err != nil || recovered.Order == nil || recovered.Order.ID != exchange.Order.ID {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	for _, input := range []struct {
		user, goods uint64
		code        string
	}{{user.ID + 1, goods.ID, "exchange_request_not_found"}, {user.ID, goods.ID + 1, "idempotency_conflict"}} {
		_, err := shop.RecoverExchange(ctx, input.user, input.goods, record.RequestID)
		var appErr *xerr.Error
		if !errors.As(err, &appErr) || appErr.Code != input.code {
			t.Fatalf("unexpected recovery error: %v", err)
		}
	}
	if err := db.Model(&record).Update("resource_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := shop.RecoverExchange(ctx, user.ID, goods.ID, record.RequestID)
	if err != nil || !legacy.Completed || legacy.Order != nil {
		t.Fatalf("legacy fallback: %+v %v", legacy, err)
	}
	_, err = shop.ExchangeGoods(ctx, user.ID, goods.ID, record.RequestID, 10)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "operation_result_archived" {
		t.Fatalf("archived request must remain blocked: %v", err)
	}
}

func TestReconciliationCleanCorruptAndReadOnly(t *testing.T) {
	db := newCatalogDBForTest(t)
	user, goods := auditFixture(t, db)
	ctx := context.Background()
	shop := NewShopService(db)
	exchange, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "audit-exchange-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shop.CancelOrder(ctx, user.ID, exchange.Order.ID, "audit-cancel-1"); err != nil {
		t.Fatal(err)
	}
	report := NewAdminReportService(db, &AuthService{db: db})
	for _, kind := range []string{"accounts", "orders", "stock"} {
		page, err := report.Reconcile(ctx, user.ID, goods.AgencyID, kind, 0, 20)
		if err != nil || len(page.Issues) != 0 || page.Checked != 1 {
			t.Fatalf("clean %s: %+v %v", kind, page, err)
		}
	}
	if err := db.Model(&user).Update("points_balance", 99).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&goods).Update("stock", 7).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", PointLedgerOrderRefund).Delete(&model.PointLedger{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"accounts", "orders", "stock"} {
		page, err := report.Reconcile(ctx, user.ID, goods.AgencyID, kind, 0, 20)
		if err != nil || len(page.Issues) == 0 {
			t.Fatalf("corruption missed in %s: %+v %v", kind, page, err)
		}
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&goods, goods.ID).Error; err != nil {
		t.Fatal(err)
	}
	if user.PointsBalance != 99 || goods.Stock != 7 {
		t.Fatal("reconciliation modified data")
	}
	for _, agencyID := range []uint64{goods.AgencyID, goods.AgencyID + 1} {
		_, err := report.Reconcile(ctx, user.ID+1, agencyID, "accounts", 0, 20)
		var appErr *xerr.Error
		if !errors.As(err, &appErr) || appErr.Code != "admin_forbidden" {
			t.Fatalf("permission bypass: %v", err)
		}
	}
}

func TestReconciliationPaginationAndLegacyBaseline(t *testing.T) {
	db := newCatalogDBForTest(t)
	user, goods := auditFixture(t, db)
	legacy := model.User{OpenID: "legacy", PointsBalance: 12}
	mustCreate(t, db, &legacy)
	mustCreate(t, db, &model.AgencyPointAccount{AgencyID: goods.AgencyID, UserID: legacy.ID, Balance: 12})
	report := NewAdminReportService(db, &AuthService{db: db})
	page, err := report.Reconcile(context.Background(), user.ID, goods.AgencyID, "accounts", 0, 1)
	if err != nil || !page.HasMore || page.NextCursor != user.ID {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page, err = report.Reconcile(context.Background(), user.ID, goods.AgencyID, "accounts", page.NextCursor, 1)
	if err != nil || page.HasMore || len(page.Issues) != 1 || page.Issues[0].Severity != "warning" {
		t.Fatalf("legacy page: %+v %v", page, err)
	}
	for _, limit := range []int{0, 51, -1} {
		if _, err := report.Reconcile(context.Background(), user.ID, goods.AgencyID, "accounts", 0, limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
}

func TestReconciliationDetectsOffsettingChainGaps(t *testing.T) {
	db := newCatalogDBForTest(t)
	user, goods := auditFixture(t, db)
	// Totals and final values still match; only the intermediate chain is broken.
	mustCreate(t, db, &model.PointLedger{AgencyID: goods.AgencyID, UserID: user.ID, BeforePoints: 105, DeltaPoints: 1, AfterPoints: 106, Type: PointLedgerAdminAdjust})
	mustCreate(t, db, &model.PointLedger{AgencyID: goods.AgencyID, UserID: user.ID, BeforePoints: 101, DeltaPoints: -1, AfterPoints: 100, Type: PointLedgerAdminAdjust})
	mustCreate(t, db, &model.StockMovement{AgencyID: goods.AgencyID, GoodsID: goods.ID, BeforeStock: 7, DeltaStock: 1, AfterStock: 8, Type: model.StockMovementAdminAdjust})
	mustCreate(t, db, &model.StockMovement{AgencyID: goods.AgencyID, GoodsID: goods.ID, BeforeStock: 3, DeltaStock: -1, AfterStock: 2, Type: model.StockMovementAdminAdjust})
	report := NewAdminReportService(db, &AuthService{db: db})
	for kind, code := range map[string]string{"accounts": "points_chain", "stock": "stock_chain"} {
		page, err := report.Reconcile(context.Background(), user.ID, goods.AgencyID, kind, 0, 20)
		if err != nil || len(page.Issues) != 1 || page.Issues[0].Code != code {
			t.Fatalf("chain gap not detected: %+v %v", page, err)
		}
	}
}
