package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestAdminOverviewReportAggregatesOperationsAndStockAudit(t *testing.T) {
	db := newCatalogDBForTest(t)
	now := time.Now()
	agency := model.Agency{Name: "报表事务所", Status: model.MemberStatusNormal}
	admin := model.User{OpenID: "report_admin", Nickname: "管理员", Status: model.UserStatusNormal}
	staff := model.User{OpenID: "report_staff", Nickname: "小林", Status: model.UserStatusNormal}
	target := model.User{OpenID: "report_target", Nickname: "会员", Status: model.UserStatusNormal}
	for _, value := range []any{&agency, &admin, &staff, &target} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create report fixture: %v", err)
		}
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "A 团", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&model.AdminMember{AgencyID: agency.ID, UserID: admin.ID, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: group.ID, UserID: staff.ID, DisplayName: "林店员", Role: model.StaffRoleStaff, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create staff: %v", err)
	}
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "兑换券", PricePoints: 30, Stock: 4, Status: model.GoodsStatusOnSale}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	saleNo := "TS-REPORT-1"
	grants := []model.PointGrantRequest{
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleNo, TicketUnitPrice: 60, TicketCount: 2, TotalAmount: 120, Points: 120, RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "report_ticket_01", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeManual, TotalAmount: 0, Points: 20, RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "report_manual_01", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, TicketUnitPrice: 100, TicketCount: 100, TotalAmount: 10000, Points: 10000, RequestStatus: model.ApprovalStatusPending, ClientRequestID: "report_pending_01", CreatedAt: now},
	}
	for index := range grants {
		if err := db.Create(&grants[index]).Error; err != nil {
			t.Fatalf("create grant: %v", err)
		}
	}
	correctionKey := "report-grant:" + saleNo
	if err := db.Create(&model.PointCorrectionRequest{
		AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID,
		OriginalGrantRequestID: grants[0].ID, OriginalLedgerID: 1, Points: grants[0].Points,
		Reason: "报表冲正", RequestStatus: model.ApprovalStatusApproved, SubmittedBy: staff.ID,
		ClientRequestID: "report_correction_01", RequestHash: "report_correction_hash", ConflictKey: &correctionKey,
	}).Error; err != nil {
		t.Fatalf("create correction: %v", err)
	}
	orders := []model.ExchangeOrder{
		{OrderNo: "REPORT-ORDER-1", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusRedeemed, RedeemTokenHash: "report_hash_1", CreatedAt: now},
		{OrderNo: "REPORT-ORDER-2", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusCanceled, RedeemTokenHash: "report_hash_2", CreatedAt: now},
		{OrderNo: "REPORT-ORDER-3", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusPending, RedeemTokenHash: "report_hash_3", CreatedAt: now},
	}
	for index := range orders {
		if err := db.Create(&orders[index]).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
	}
	if err := db.Create(&model.StockMovement{AgencyID: agency.ID, GroupID: group.ID, GoodsID: goods.ID, BeforeStock: 5, DeltaStock: -1, AfterStock: 4, Type: model.StockMovementExchange, OperatorID: target.ID, ReferenceID: orders[0].ID, Remark: "兑换扣减", CreatedAt: now}).Error; err != nil {
		t.Fatalf("create stock movement: %v", err)
	}

	reportService := NewAdminReportService(db, &AuthService{db: db})
	report, err := reportService.Overview(context.Background(), admin.ID, agency.ID, now.Format(adminReportDateLayout), now.Format(adminReportDateLayout))
	if err != nil {
		t.Fatalf("load overview: %v", err)
	}
	if report.Metrics.TicketSalesCount != 1 || report.Metrics.TicketSalesAmount != 120 || report.Metrics.GrantCount != 1 || report.Metrics.GrantedPoints != 20 || report.Metrics.CorrectedGrantCount != 1 || report.Metrics.CorrectedPoints != 120 {
		t.Fatalf("unexpected grant metrics: %+v", report.Metrics)
	}
	if report.Metrics.PendingGrantCount != 1 || report.Metrics.PendingGrantPoints != 10000 {
		t.Fatalf("unexpected pending metrics: %+v", report.Metrics)
	}
	if report.Metrics.ExchangeCount != 3 || report.Metrics.ExchangePoints != 90 || report.Metrics.RedeemedCount != 1 || report.Metrics.RefundedCount != 1 || report.Metrics.PendingOrderCount != 1 {
		t.Fatalf("unexpected order metrics: %+v", report.Metrics)
	}
	if report.Metrics.LowStockGoodsCount != 1 || len(report.StaffStats) != 1 || report.StaffStats[0].DisplayName != "林店员" || report.StaffStats[0].GrantedPoints != 20 || report.StaffStats[0].CorrectedPoints != 120 {
		t.Fatalf("unexpected operational details: metrics=%+v staff=%+v", report.Metrics, report.StaffStats)
	}
	if len(report.StockMovements) != 1 || report.StockMovements[0].GoodsName != goods.Name || report.StockMovements[0].DeltaStock != -1 {
		t.Fatalf("unexpected stock movements: %+v", report.StockMovements)
	}

	_, err = reportService.Overview(context.Background(), target.ID, agency.ID, "", "")
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "admin_forbidden" {
		t.Fatalf("expected admin permission error, got %v", err)
	}
}

func TestMaintenanceCleanupRetainsIdempotencyKeys(t *testing.T) {
	db := newCatalogDBForTest(t)
	now := time.Now()
	oldToken := model.IdentityQRToken{TokenHash: "old_token", UserID: 1, ExpiresAt: now.Add(-time.Hour)}
	newToken := model.IdentityQRToken{TokenHash: "new_token", UserID: 1, ExpiresAt: now.Add(time.Hour)}
	oldRequest := model.IdempotencyRecord{ActorID: 1, Operation: "test", RequestID: "old_request", RequestHash: "old", ResponseJSON: "{}", CreatedAt: now.Add(-8 * 24 * time.Hour)}
	newRequest := model.IdempotencyRecord{ActorID: 1, Operation: "test", RequestID: "new_request", RequestHash: "new", ResponseJSON: "{}", CreatedAt: now.Add(-time.Hour)}
	for _, value := range []any{&oldToken, &newToken, &oldRequest, &newRequest} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create cleanup fixture: %v", err)
		}
	}

	result, err := NewMaintenanceService(db).CleanupExpiredData(context.Background(), now, 7*24*time.Hour, 100)
	if err != nil {
		t.Fatalf("cleanup temporary data: %v", err)
	}
	if result.IdentityTokensDeleted != 1 || result.IdempotencyCompacted != 1 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	var tokenCount, requestCount int64
	if err := db.Model(&model.IdentityQRToken{}).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if err := db.Model(&model.IdempotencyRecord{}).Count(&requestCount).Error; err != nil {
		t.Fatalf("count idempotency: %v", err)
	}
	if tokenCount != 1 || requestCount != 2 {
		t.Fatalf("cleanup removed active data: tokens=%d requests=%d", tokenCount, requestCount)
	}
	var compacted model.IdempotencyRecord
	if err := db.First(&compacted, oldRequest.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !compacted.Compacted || compacted.ResponseJSON != "" {
		t.Fatalf("response not compacted: %+v", compacted)
	}
	_, found, err := loadIdempotentResult[map[string]any](context.Background(), db, 1, "test", "old_request", "old")
	if !found {
		t.Fatal("late retry lost its idempotency key")
	}
	assertCorrectionErrorCode(t, err, "operation_result_archived")
	_, _, err = loadIdempotentResult[map[string]any](context.Background(), db, 1, "test", "old_request", "changed")
	assertCorrectionErrorCode(t, err, "idempotency_conflict")
	second, err := NewMaintenanceService(db).CleanupExpiredData(context.Background(), now, 7*24*time.Hour, 100)
	if err != nil || second.IdempotencyCompacted != 0 {
		t.Fatalf("compacted records processed again: %+v %v", second, err)
	}
}
