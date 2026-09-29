package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestAdminOperationsFiltersPaginatesAndExportsSafely(t *testing.T) {
	db := newCatalogDBForTest(t)
	now := time.Now()
	agency := model.Agency{Name: "明细事务所", Status: model.MemberStatusNormal}
	admin := model.User{OpenID: "operations_admin", Nickname: "管理员", Status: model.UserStatusNormal}
	staff := model.User{OpenID: "operations_staff", Nickname: "员工", Status: model.UserStatusNormal}
	target := model.User{OpenID: "operations_target", Nickname: "=HYPERLINK(\"bad\")", Status: model.UserStatusNormal}
	outsider := model.User{OpenID: "operations_outsider", Nickname: "无权限", Status: model.UserStatusNormal}
	for _, value := range []any{&agency, &admin, &staff, &target, &outsider} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create operations fixture: %v", err)
		}
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "明细团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&model.AdminMember{AgencyID: agency.ID, UserID: admin.ID, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create admin permission: %v", err)
	}
	if err := db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: group.ID, UserID: staff.ID, DisplayName: "售票员工", Role: model.StaffRoleStaff, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create staff membership: %v", err)
	}
	saleOne := "OPS-SALE-001"
	saleTwo := "OPS-SALE-002"
	grants := []model.PointGrantRequest{
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleOne, TicketUnitPrice: 60, TicketCount: 2, TotalAmount: 120, Points: 120, PaymentMethod: "wechat", Remark: "+SUM(1,1)", RequestStatus: model.ApprovalStatusApproved, ClientRequestID: "operations_grant_01", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeTicket, SaleNo: &saleTwo, TicketUnitPrice: 100, TicketCount: 100, TotalAmount: 10000, Points: 10000, PaymentMethod: "cash", RequestStatus: model.ApprovalStatusPending, ClientRequestID: "operations_grant_02", CreatedAt: now},
		{AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, SubmittedBy: staff.ID, GrantMode: model.PointGrantModeManual, Points: 20, RequestStatus: model.ApprovalStatusRejected, ClientRequestID: "operations_grant_03", CreatedAt: now},
	}
	for index := range grants {
		if err := db.Create(&grants[index]).Error; err != nil {
			t.Fatalf("create grant: %v", err)
		}
	}
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "限定商品", PricePoints: 30, Stock: 2, Status: model.GoodsStatusOnSale}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	redeemedAt := now
	orders := []model.ExchangeOrder{
		{OrderNo: "OPS-ORDER-001", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusRedeemed, RedeemTokenHash: "operations_hash_1", RedeemedBy: staff.ID, RedeemedAt: &redeemedAt, CreatedAt: now},
		{OrderNo: "OPS-ORDER-002", AgencyID: agency.ID, GroupID: group.ID, UserID: target.ID, GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 30, Status: model.ExchangeOrderStatusPending, RedeemTokenHash: "operations_hash_2", CreatedAt: now},
	}
	for index := range orders {
		if err := db.Create(&orders[index]).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
	}

	service := NewAdminOperationsService(db, &AuthService{db: db})
	base := AdminOperationFilter{AgencyID: agency.ID, DateFrom: now.Format(adminReportDateLayout), DateTo: now.Format(adminReportDateLayout), Status: "all", Limit: 1}
	first, err := service.ListSales(context.Background(), admin.ID, base)
	if err != nil {
		t.Fatalf("list first sales page: %v", err)
	}
	if len(first.Items) != 1 || !first.HasMore || first.NextCursor == 0 {
		t.Fatalf("unexpected first sales page: %+v", first)
	}
	base.Cursor = first.NextCursor
	second, err := service.ListSales(context.Background(), admin.ID, base)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID >= first.Items[0].ID {
		t.Fatalf("unexpected second sales page: page=%+v err=%v", second, err)
	}
	pendingFilter := base
	pendingFilter.Cursor = 0
	pendingFilter.Limit = 20
	pendingFilter.Status = model.ApprovalStatusPending
	pendingFilter.Keyword = "OPS-SALE-002"
	pending, err := service.ListSales(context.Background(), admin.ID, pendingFilter)
	if err != nil || len(pending.Items) != 1 || pending.Items[0].RequestStatus != model.ApprovalStatusPending {
		t.Fatalf("sale filters failed: page=%+v err=%v", pending, err)
	}
	orderFilter := AdminOperationFilter{AgencyID: agency.ID, DateFrom: base.DateFrom, DateTo: base.DateTo, Status: model.ExchangeOrderStatusRedeemed, Keyword: "限定商品", Limit: 20}
	orderPage, err := service.ListOrders(context.Background(), admin.ID, orderFilter)
	if err != nil || len(orderPage.Items) != 1 || orderPage.Items[0].OrderNo != orders[0].OrderNo || orderPage.Items[0].RedeemerName != staff.Nickname {
		t.Fatalf("order filters failed: page=%+v err=%v", orderPage, err)
	}

	exported, err := service.ExportCSV(context.Background(), admin.ID, "sales", AdminOperationFilter{AgencyID: agency.ID, DateFrom: base.DateFrom, DateTo: base.DateTo, Status: "all"})
	if err != nil {
		t.Fatalf("export sales csv: %v", err)
	}
	if !bytes.HasPrefix(exported.Content, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV export is missing UTF-8 BOM")
	}
	csvText := string(exported.Content)
	if !strings.Contains(csvText, "'=HYPERLINK") || !strings.Contains(csvText, "'+SUM") {
		t.Fatalf("CSV formula values were not escaped: %s", csvText)
	}

	_, err = service.ListOrders(context.Background(), outsider.ID, orderFilter)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "admin_forbidden" {
		t.Fatalf("expected admin permission error, got %v", err)
	}
}

func TestCSVCellEscapesLeadingFormulaCharacters(t *testing.T) {
	for _, value := range []string{"=1+1", "+SUM(A1)", "-2+3", "@cmd", "  =trimmed"} {
		if escaped := csvCell(value); !strings.HasPrefix(escaped, "'") {
			t.Fatalf("formula-like cell was not escaped: %q", value)
		}
	}
	if escaped := csvCell("normal text"); escaped != "normal text" {
		t.Fatalf("normal CSV cell changed: %q", escaped)
	}
}
