package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestTicketGrantIsCalculatedAndRecordedByServer(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	result, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: 1, GrantMode: model.PointGrantModeTicket, TicketUnitPrice: 60, TicketCount: 2,
		PaymentMethod: "wechat", Remark: "现场购券", RequestID: "ticket_grant_record_01",
	})
	if err != nil {
		t.Fatalf("submit ticket grant: %v", err)
	}
	if result.DeltaPoints != 120 || result.AfterPoints != 120 || result.PendingApproval || result.SaleNo == "" {
		t.Fatalf("unexpected ticket grant result: %+v", result)
	}
	var grant model.PointGrantRequest
	if err := fixture.service.db.First(&grant, result.GrantRequestID).Error; err != nil {
		t.Fatalf("load grant record: %v", err)
	}
	if grant.TicketUnitPrice != 60 || grant.TicketCount != 2 || grant.TotalAmount != 120 || grant.Points != 120 || grant.SaleNo == nil {
		t.Fatalf("ticket sale was not recorded correctly: %+v", grant)
	}
	var ledger model.PointLedger
	if err := fixture.service.db.First(&ledger, result.LedgerID).Error; err != nil {
		t.Fatalf("load ticket ledger: %v", err)
	}
	if ledger.ReferenceID != grant.ID || ledger.DeltaPoints != 120 {
		t.Fatalf("ledger was not linked to grant: %+v", ledger)
	}
}

func TestLargeStaffGrantRequiresLeaderApproval(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	leader := model.User{OpenID: "point_grant_leader", Status: model.UserStatusNormal}
	if err := fixture.service.db.Create(&leader).Error; err != nil {
		t.Fatalf("create leader: %v", err)
	}
	if err := fixture.service.db.Create(&model.StaffMember{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, UserID: leader.ID,
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create leader membership: %v", err)
	}

	result, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: PointGrantApprovalThreshold, GrantMode: model.PointGrantModeManual,
		Remark: "大额活动积分", RequestID: "large_grant_approval_01",
	})
	if err != nil {
		t.Fatalf("submit large grant: %v", err)
	}
	if !result.PendingApproval || result.RequestStatus != model.ApprovalStatusPending || result.LedgerID != 0 {
		t.Fatalf("large staff grant bypassed approval: %+v", result)
	}
	assertPointBalance(t, fixture.service, fixture.agency.ID, fixture.target.ID, 0)

	approved, err := fixture.service.ReviewPointGrant(context.Background(), leader.ID, result.GrantRequestID, true, "金额已核对")
	if err != nil {
		t.Fatalf("approve large grant: %v", err)
	}
	if approved.RequestStatus != model.ApprovalStatusApproved || approved.LedgerID == 0 || approved.ReviewRemark != "金额已核对" {
		t.Fatalf("unexpected approved grant: %+v", approved)
	}
	assertPointBalance(t, fixture.service, fixture.agency.ID, fixture.target.ID, PointGrantApprovalThreshold)

	if _, err := fixture.service.ReviewPointGrant(context.Background(), leader.ID, result.GrantRequestID, true, "重复请求"); err != nil {
		t.Fatalf("idempotent approval retry failed: %v", err)
	}
	var ledgerCount int64
	fixture.service.db.Model(&model.PointLedger{}).Where("reference_id = ? AND type = ?", result.GrantRequestID, PointLedgerStaffAdd).Count(&ledgerCount)
	if ledgerCount != 1 {
		t.Fatalf("approval retry created %d ledgers", ledgerCount)
	}
}

func TestTeamLeaderLargeGrantAppliesImmediately(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleTeamLeader)
	result, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: PointGrantApprovalThreshold, GrantMode: model.PointGrantModeManual,
		Remark: "负责人现场发放", RequestID: "leader_direct_grant_01",
	})
	if err != nil {
		t.Fatalf("team leader direct grant: %v", err)
	}
	if result.PendingApproval || result.RequestStatus != model.ApprovalStatusApproved || result.LedgerID == 0 {
		t.Fatalf("team leader grant did not apply directly: %+v", result)
	}
	assertPointBalance(t, fixture.service, fixture.agency.ID, fixture.target.ID, PointGrantApprovalThreshold)
}

func TestRejectedGrantReleasesDailyQuota(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	leader := model.User{OpenID: "point_reject_leader", Status: model.UserStatusNormal}
	fixture.service.db.Create(&leader)
	fixture.service.db.Create(&model.StaffMember{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, UserID: leader.ID,
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
	})
	result, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: PointGrantApprovalThreshold, GrantMode: model.PointGrantModeManual,
		RequestID: "reject_grant_quota_01",
	})
	if err != nil {
		t.Fatalf("submit grant for rejection: %v", err)
	}
	_, err = fixture.service.ReviewPointGrant(context.Background(), leader.ID, result.GrantRequestID, false, "")
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "invalid_review_remark" {
		t.Fatalf("expected rejection remark validation, got %v", err)
	}
	if _, err := fixture.service.ReviewPointGrant(context.Background(), leader.ID, result.GrantRequestID, false, "单据不完整"); err != nil {
		t.Fatalf("reject grant: %v", err)
	}
	var usage model.StaffPointDailyUsage
	if err := fixture.service.db.Where("staff_id = ?", fixture.staff.ID).First(&usage).Error; err != nil {
		t.Fatalf("load daily usage: %v", err)
	}
	if usage.ReservedPoints != 0 {
		t.Fatalf("rejected grant retained daily quota: %d", usage.ReservedPoints)
	}
}

func TestStaffDailyGrantLimitRejectsWithoutConsumingQR(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	if err := fixture.service.db.Create(&model.StaffPointDailyUsage{
		StaffID: fixture.staff.ID, GrantDate: time.Now().In(time.Local).Format("2006-01-02"),
		ReservedPoints: StaffDailyGrantLimit - 5,
	}).Error; err != nil {
		t.Fatalf("seed daily usage: %v", err)
	}
	_, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: 10, GrantMode: model.PointGrantModeManual, RequestID: "daily_limit_reject_01",
	})
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "staff_daily_points_limit" {
		t.Fatalf("expected daily limit error, got %v", err)
	}
	var tokenCount int64
	fixture.service.db.Model(&model.IdentityQRToken{}).Where("token_hash = ?", tokenHash(fixture.token)).Count(&tokenCount)
	if tokenCount != 1 {
		t.Fatal("daily limit rejection consumed identity QR")
	}
}

func TestExpiredOrderRefundsPointsAndRestoresStockOnce(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	user := model.User{OpenID: "expired_order_user", Status: model.UserStatusNormal, PointsBalance: 90}
	intruder := model.User{OpenID: "expired_order_intruder", Status: model.UserStatusNormal}
	agency := model.Agency{Name: "过期订单事务所", Status: model.MemberStatusNormal}
	db.Create(&user)
	db.Create(&intruder)
	db.Create(&agency)
	group := model.IdolGroup{AgencyID: agency.ID, Name: "过期订单团体", Status: model.MemberStatusNormal}
	db.Create(&group)
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "待领取商品", PricePoints: 10, Stock: 0, Status: model.GoodsStatusOnSale}
	db.Create(&goods)
	db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 90})
	expiresAt := time.Now().Add(-time.Minute)
	order := model.ExchangeOrder{
		OrderNo: "EXPIRED_ORDER_01", AgencyID: agency.ID, GroupID: group.ID, UserID: user.ID,
		GoodsID: goods.ID, GoodsName: goods.Name, PointsCost: 10, Status: model.ExchangeOrderStatusPending,
		RedeemTokenHash: tokenHash("expired-order-token"), ExpiresAt: &expiresAt,
	}
	db.Create(&order)
	_, err := service.GetOrder(context.Background(), intruder.ID, order.ID)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "order_not_found" {
		t.Fatalf("expected unauthorized order lookup to be hidden, got %v", err)
	}
	db.First(&order, order.ID)
	if order.Status != model.ExchangeOrderStatusPending {
		t.Fatalf("unauthorized lookup changed order status: %s", order.Status)
	}
	assertPointBalance(t, NewPointsService(db, &AuthService{db: db}), agency.ID, user.ID, 90)

	count, err := service.ExpirePendingOrders(context.Background(), time.Now(), 100)
	if err != nil || count != 1 {
		t.Fatalf("expire pending order: count=%d err=%v", count, err)
	}
	count, err = service.ExpirePendingOrders(context.Background(), time.Now(), 100)
	if err != nil || count != 0 {
		t.Fatalf("repeat expiration changed order: count=%d err=%v", count, err)
	}
	assertPointBalance(t, NewPointsService(db, &AuthService{db: db}), agency.ID, user.ID, 100)
	db.First(&goods, goods.ID)
	if goods.Stock != 1 {
		t.Fatalf("expired order stock was not restored: %d", goods.Stock)
	}
	db.First(&order, order.ID)
	if order.Status != model.ExchangeOrderStatusExpired || order.ExpiredAt == nil || order.RefundLedgerID == nil {
		t.Fatalf("order was not marked expired: %+v", order)
	}
	var refundCount int64
	db.Model(&model.PointLedger{}).Where("reference_id = ? AND type = ?", order.ID, PointLedgerOrderRefund).Count(&refundCount)
	if refundCount != 1 {
		t.Fatalf("expected one expiration refund ledger, got %d", refundCount)
	}
	var stockMovements []model.StockMovement
	db.Where("goods_id = ?", goods.ID).Find(&stockMovements)
	if len(stockMovements) != 1 || stockMovements[0].Type != model.StockMovementOrderExpire || stockMovements[0].BeforeStock != 0 || stockMovements[0].AfterStock != 1 {
		t.Fatalf("unexpected expiration stock audit: %+v", stockMovements)
	}
}

func TestExpiredOrderFailureDoesNotBlockLaterOrders(t *testing.T) {
	db := newCatalogDBForTest(t)
	shopService := NewShopService(db)
	user := model.User{OpenID: "expiry_batch_user", Status: model.UserStatusNormal, PointsBalance: 80}
	agency := model.Agency{Name: "到期批处理事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "到期批处理团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	blockedGoods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "满库存商品", PricePoints: 10, Stock: MaxGoodsStock, Status: model.GoodsStatusOnSale}
	validGoods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "正常商品", PricePoints: 10, Stock: 0, Status: model.GoodsStatusOnSale}
	if err := db.Create(&blockedGoods).Error; err != nil {
		t.Fatalf("create blocked goods: %v", err)
	}
	if err := db.Create(&validGoods).Error; err != nil {
		t.Fatalf("create valid goods: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 80}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}
	expiresAt := time.Now().Add(-time.Minute)
	orders := []model.ExchangeOrder{
		{OrderNo: "EXPIRY_BATCH_BLOCKED", AgencyID: agency.ID, GroupID: group.ID, UserID: user.ID, GoodsID: blockedGoods.ID, GoodsName: blockedGoods.Name, PointsCost: 10, Status: model.ExchangeOrderStatusPending, RedeemTokenHash: tokenHash("expiry-batch-blocked"), ExpiresAt: &expiresAt},
		{OrderNo: "EXPIRY_BATCH_VALID", AgencyID: agency.ID, GroupID: group.ID, UserID: user.ID, GoodsID: validGoods.ID, GoodsName: validGoods.Name, PointsCost: 10, Status: model.ExchangeOrderStatusPending, RedeemTokenHash: tokenHash("expiry-batch-valid"), ExpiresAt: &expiresAt},
	}
	for index := range orders {
		if err := db.Create(&orders[index]).Error; err != nil {
			t.Fatalf("create order %d: %v", index, err)
		}
	}

	count, err := shopService.ExpirePendingOrders(context.Background(), time.Now(), 100)
	if err == nil || count != 1 {
		t.Fatalf("expected one success plus aggregated error: count=%d err=%v", count, err)
	}
	if err := db.First(&orders[0], orders[0].ID).Error; err != nil {
		t.Fatalf("reload blocked order: %v", err)
	}
	if err := db.First(&orders[1], orders[1].ID).Error; err != nil {
		t.Fatalf("reload valid order: %v", err)
	}
	if orders[0].Status != model.ExchangeOrderStatusPending || orders[1].Status != model.ExchangeOrderStatusExpired {
		t.Fatalf("unexpected batch statuses: blocked=%s valid=%s", orders[0].Status, orders[1].Status)
	}
	assertPointBalance(t, NewPointsService(db, &AuthService{db: db}), agency.ID, user.ID, 90)
	page, err := shopService.ListOrdersPage(context.Background(), user.ID, 0, 20)
	if err != nil {
		t.Fatalf("poison expiration must not block order list: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected both orders to remain visible, got %+v", page.Items)
	}
}

type pointGrantFixture struct {
	service *PointsService
	staff   model.User
	target  model.User
	agency  model.Agency
	group   model.IdolGroup
	token   string
}

func newPointGrantFixture(t *testing.T, role string) pointGrantFixture {
	t.Helper()
	db := newCatalogDBForTest(t)
	fixture := pointGrantFixture{
		service: NewPointsService(db, &AuthService{db: db}),
		staff:   model.User{OpenID: "point_grant_staff_" + role, Status: model.UserStatusNormal},
		target:  model.User{OpenID: "point_grant_target_" + role, Status: model.UserStatusNormal},
		agency:  model.Agency{Name: "积分发放事务所 " + role, Status: model.MemberStatusNormal},
	}
	db.Create(&fixture.staff)
	db.Create(&fixture.target)
	db.Create(&fixture.agency)
	fixture.group = model.IdolGroup{AgencyID: fixture.agency.ID, Name: "积分发放团体", Status: model.MemberStatusNormal}
	db.Create(&fixture.group)
	db.Create(&model.StaffMember{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, UserID: fixture.staff.ID,
		Role: role, Status: model.MemberStatusNormal,
	})
	db.Create(&model.AgencyPointAccount{AgencyID: fixture.agency.ID, UserID: fixture.target.ID, Balance: 0})
	var err error
	fixture.token, err = randomToken()
	if err != nil {
		t.Fatalf("create identity token: %v", err)
	}
	db.Create(&model.IdentityQRToken{TokenHash: tokenHash(fixture.token), UserID: fixture.target.ID, ExpiresAt: time.Now().Add(time.Minute)})
	return fixture
}

func assertPointBalance(t *testing.T, service *PointsService, agencyID uint64, userID uint64, want int64) {
	t.Helper()
	var account model.AgencyPointAccount
	if err := service.db.Where("agency_id = ? AND user_id = ?", agencyID, userID).First(&account).Error; err != nil {
		t.Fatalf("load point balance: %v", err)
	}
	if account.Balance != want {
		t.Fatalf("point balance=%d, want %d", account.Balance, want)
	}
	var user model.User
	if err := service.db.First(&user, userID).Error; err != nil {
		t.Fatalf("load user balance mirror: %v", err)
	}
	if user.PointsBalance != want {
		t.Fatalf("user balance mirror=%d, want %d", user.PointsBalance, want)
	}
}
