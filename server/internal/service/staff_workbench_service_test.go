package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestStaffWorkbenchSummarizesOwnDailyOperations(t *testing.T) {
	db := newCatalogDBForTest(t)
	now := time.Now()
	agency := model.Agency{Name: "工作台事务所", Status: model.MemberStatusNormal}
	staff := model.User{OpenID: "workbench_staff", Nickname: "员工", Status: model.UserStatusNormal}
	otherStaff := model.User{OpenID: "workbench_other", Nickname: "其他员工", Status: model.UserStatusNormal}
	target := model.User{OpenID: "workbench_target", Nickname: "会员甲", Status: model.UserStatusNormal}
	for _, value := range []any{&agency, &staff, &otherStaff, &target} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create workbench fixture: %v", err)
		}
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "工作台团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	for _, userID := range []uint64{staff.ID, otherStaff.ID} {
		if err := db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: group.ID, UserID: userID, Role: model.StaffRoleStaff, Status: model.MemberStatusNormal}).Error; err != nil {
			t.Fatalf("create staff membership: %v", err)
		}
	}
	date := now.Format(staffWorkbenchDateLayout)
	if err := db.Create(&model.StaffPointDailyUsage{StaffID: staff.ID, GrantDate: date, ReservedPoints: 10140}).Error; err != nil {
		t.Fatalf("create quota usage: %v", err)
	}
	saleOne := "WORKBENCH-SALE-1"
	saleTwo := "WORKBENCH-SALE-2"
	saleRejected := "WORKBENCH-SALE-3"
	grants := []model.PointGrantRequest{
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleOne, TicketUnitPrice: 60, TicketCount: 2, TotalAmount: 120, Points: 120, PaymentMethod: "wechat", RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "workbench_grant_01", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleTwo, TicketUnitPrice: 100, TicketCount: 100, TotalAmount: 10000, Points: 10000, PaymentMethod: "cash", RequestStatus: model.ApprovalStatusPending, ClientRequestID: "workbench_grant_02", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleRejected, TicketUnitPrice: 60, TicketCount: 1, TotalAmount: 60, Points: 60, PaymentMethod: "cash", RequestStatus: model.ApprovalStatusRejected, ClientRequestID: "workbench_grant_03", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeManual, Points: 20, RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "workbench_grant_04", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: otherStaff.ID, GrantMode: model.PointGrantModeTicket, TicketUnitPrice: 500, TicketCount: 1, TotalAmount: 500, Points: 500, PaymentMethod: "cash", RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "workbench_other_grant", CreatedAt: now},
	}
	for index := range grants {
		if err := db.Create(&grants[index]).Error; err != nil {
			t.Fatalf("create grant record: %v", err)
		}
	}
	correctionKey := "workbench-grant:" + saleOne
	if err := db.Create(&model.PointCorrectionRequest{
		AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID,
		OriginalGrantRequestID: grants[0].ID, OriginalLedgerID: 1, Points: grants[0].Points,
		Reason: "测试冲正", RequestStatus: model.ApprovalStatusApproved, SubmittedBy: staff.ID,
		ClientRequestID: "workbench_correction_01", RequestHash: "workbench_correction_hash", ConflictKey: &correctionKey,
	}).Error; err != nil {
		t.Fatalf("create correction record: %v", err)
	}
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "核销商品", PricePoints: 30, Stock: 1, Status: model.GoodsStatusOnSale}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	redeemedAt := now
	orders := []model.ExchangeOrder{
		{OrderNo: "WORKBENCH-ORDER-1", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusRedeemed, RedeemTokenHash: "workbench_hash_1", RedeemedBy: staff.ID, RedeemedAt: &redeemedAt, CreatedAt: now},
		{OrderNo: "WORKBENCH-ORDER-2", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusRedeemed, RedeemTokenHash: "workbench_hash_2", RedeemedBy: otherStaff.ID, RedeemedAt: &redeemedAt, CreatedAt: now},
	}
	for index := range orders {
		if err := db.Create(&orders[index]).Error; err != nil {
			t.Fatalf("create redeemed order: %v", err)
		}
	}

	workbenchService := NewStaffWorkbenchService(db, &AuthService{db: db})
	workbench, err := workbenchService.Get(context.Background(), staff.ID, agency.ID, group.ID, date)
	if err != nil {
		t.Fatalf("load staff workbench: %v", err)
	}
	if workbench.Summary.DailyUsed != 10140 || workbench.Summary.DailyRemaining != StaffDailyGrantLimit-10140 {
		t.Fatalf("unexpected quota summary: %+v", workbench.Summary)
	}
	if workbench.Summary.TicketSalesCount != 2 || workbench.Summary.TicketSalesAmount != 10120 {
		t.Fatalf("unexpected ticket summary: %+v", workbench.Summary)
	}
	if workbench.Summary.ApprovedGrantCount != 1 || workbench.Summary.ApprovedPoints != 20 || workbench.Summary.CorrectedGrantCount != 1 || workbench.Summary.CorrectedPoints != 120 || workbench.Summary.PendingGrantCount != 1 || workbench.Summary.PendingPoints != 10000 || workbench.Summary.RejectedGrantCount != 1 {
		t.Fatalf("unexpected grant summary: %+v", workbench.Summary)
	}
	if workbench.Summary.RedeemCount != 1 || workbench.Summary.RedeemedPoints != 30 || len(workbench.RedeemRecords) != 1 || workbench.RedeemRecords[0].OrderNo != orders[0].OrderNo {
		t.Fatalf("unexpected redemption summary: summary=%+v records=%+v", workbench.Summary, workbench.RedeemRecords)
	}
	if len(workbench.GrantRecords) != 4 || len(workbench.PaymentSummary) != 2 {
		t.Fatalf("unexpected workbench records: grants=%d payments=%+v", len(workbench.GrantRecords), workbench.PaymentSummary)
	}
	if workbench.GrantRecords[0].CorrectionStatus == "" && workbench.GrantRecords[3].CorrectionStatus == "" {
		t.Fatalf("approved correction is missing from grant records: %+v", workbench.GrantRecords)
	}
	var paymentTotal int64
	for _, payment := range workbench.PaymentSummary {
		paymentTotal += payment.Amount
	}
	if paymentTotal != 10120 {
		t.Fatalf("rejected or other staff sale leaked into payment total: %d", paymentTotal)
	}

	_, err = workbenchService.Get(context.Background(), target.ID, agency.ID, group.ID, date)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "staff_group_forbidden" {
		t.Fatalf("expected staff permission error, got %v", err)
	}
}

func TestStaffWorkbenchRejectsOutOfRangeDate(t *testing.T) {
	_, _, err := parseStaffWorkbenchDate(time.Now().AddDate(0, 0, 1).Format(staffWorkbenchDateLayout), time.Now())
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "invalid_date" {
		t.Fatalf("expected future date rejection, got %v", err)
	}
}
