package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestEnsureSingleAgencyRejectsSecondAgency(t *testing.T) {
	db := newCatalogDBForTest(t)
	first, err := EnsureSingleAgency(context.Background(), db, "唯一事务所")
	if err != nil {
		t.Fatalf("create single agency: %v", err)
	}
	again, err := EnsureSingleAgency(context.Background(), db, "另一个名字")
	if err != nil {
		t.Fatalf("load single agency: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("created a second agency: %d != %d", again.ID, first.ID)
	}
	if err := db.Create(&model.Agency{Name: "非法第二事务所", Status: model.MemberStatusNormal}).Error; err == nil {
		t.Fatal("expected database singleton constraint to reject second agency")
	}
}

func TestExchangeRequestIsIdempotent(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	user := model.User{OpenID: "idempotent_exchange_user", Status: model.UserStatusNormal, PointsBalance: 100}
	agency := model.Agency{Name: "幂等兑换事务所", Status: model.MemberStatusNormal}
	db.Create(&user)
	db.Create(&agency)
	group := model.IdolGroup{AgencyID: agency.ID, Name: "幂等团", Status: model.MemberStatusNormal}
	db.Create(&group)
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "幂等商品", PricePoints: 10, Stock: 2, Status: model.GoodsStatusOnSale}
	db.Create(&goods)
	db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100})

	first, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "same_exchange_01", goods.PricePoints)
	if err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	second, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "same_exchange_01", goods.PricePoints)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if first.Order.ID != second.Order.ID {
		t.Fatalf("retry created another order: %d != %d", first.Order.ID, second.Order.ID)
	}
	if _, err := NewMaintenanceService(db).CleanupExpiredData(context.Background(), time.Now().Add(8*24*time.Hour), 7*24*time.Hour, 100); err != nil {
		t.Fatal(err)
	}
	_, err = service.ExchangeGoods(context.Background(), user.ID, goods.ID, "same_exchange_01", goods.PricePoints)
	assertCorrectionErrorCode(t, err, "operation_result_archived")
	var orderCount int64
	db.Model(&model.ExchangeOrder{}).Where("user_id = ? AND goods_id = ?", user.ID, goods.ID).Count(&orderCount)
	if orderCount != 1 {
		t.Fatalf("expected one order, got %d", orderCount)
	}
	var account model.AgencyPointAccount
	db.Where("agency_id = ? AND user_id = ?", agency.ID, user.ID).First(&account)
	if account.Balance != 90 {
		t.Fatalf("expected one deduction, got balance %d", account.Balance)
	}
}

func TestExchangeRequiresConfirmedPriceAndPreservesRetry(t *testing.T) {
	db := newCatalogDBForTest(t)
	shop := NewShopService(db)
	user := model.User{OpenID: "price_confirm_user", Status: model.UserStatusNormal, PointsBalance: 100}
	agency := model.Agency{Name: "Price confirmation", Status: model.MemberStatusNormal}
	for _, value := range []any{&user, &agency} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "Price team", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	goods := model.Goods{AgencyID: agency.ID, GroupID: group.ID, Name: "Price item", PricePoints: 60, Stock: 3, Status: model.GoodsStatusOnSale}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, price := range []int64{0, -1, MaxGoodsPricePoints + 1} {
		_, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "confirm_price_01", price)
		assertCorrectionErrorCode(t, err, "invalid_expected_price")
	}
	for _, price := range []int64{30, 80} {
		_, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "confirm_price_01", price)
		assertCorrectionErrorCode(t, err, "goods_price_changed")
	}
	var account model.AgencyPointAccount
	if err := db.Where("user_id = ?", user.ID).First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.Balance != 100 {
		t.Fatalf("price mismatch deducted points: %d", account.Balance)
	}
	var storedGoods model.Goods
	if err := db.First(&storedGoods, goods.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedGoods.Stock != 3 {
		t.Fatalf("price mismatch consumed stock: %d", storedGoods.Stock)
	}
	for _, table := range []any{&model.ExchangeOrder{}, &model.PointLedger{}, &model.StockMovement{}, &model.IdempotencyRecord{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("price mismatch wrote %T records: %d", table, count)
		}
	}
	first, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "confirm_price_01", 60)
	if err != nil {
		t.Fatal(err)
	}
	if first.AfterPoints != 40 || first.Order.PointsCost != 60 {
		t.Fatalf("wrong confirmed price: %+v", first)
	}
	if err := db.Model(&goods).Update("price_points", 40).Error; err != nil {
		t.Fatal(err)
	}
	retry, err := shop.ExchangeGoods(ctx, user.ID, goods.ID, "confirm_price_01", 60)
	if err != nil || retry.Order.ID != first.Order.ID {
		t.Fatalf("retry lost original order after price change: %+v %v", retry, err)
	}
	_, err = shop.ExchangeGoods(ctx, user.ID, goods.ID, "confirm_price_02", 60)
	assertCorrectionErrorCode(t, err, "goods_price_changed")
	var count int64
	if err := db.Model(&model.ExchangeOrder{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unexpected extra order: %d", count)
	}
}

func TestStaffAddIsIdempotentAndConsumesQRCode(t *testing.T) {
	db := newCatalogDBForTest(t)
	authService := &AuthService{db: db}
	service := NewPointsService(db, authService)
	staff := model.User{OpenID: "idempotent_staff", Status: model.UserStatusNormal}
	target := model.User{OpenID: "idempotent_target", Status: model.UserStatusNormal}
	agency := model.Agency{Name: "幂等加分事务所", Status: model.MemberStatusNormal}
	db.Create(&staff)
	db.Create(&target)
	db.Create(&agency)
	group := model.IdolGroup{AgencyID: agency.ID, Name: "幂等加分团", Status: model.MemberStatusNormal}
	db.Create(&group)
	db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: group.ID, UserID: staff.ID, Status: model.MemberStatusNormal})
	qrToken, err := randomToken()
	if err != nil {
		t.Fatalf("generate identity token: %v", err)
	}
	db.Create(&model.IdentityQRToken{TokenHash: tokenHash(qrToken), UserID: target.ID, ExpiresAt: time.Now().Add(time.Minute)})

	first, err := service.AddByStaff(context.Background(), staff.ID, agency.ID, group.ID, qrToken, 20, "现场活动", "same_staff_add_01")
	if err != nil {
		t.Fatalf("first staff add: %v", err)
	}
	second, err := service.AddByStaff(context.Background(), staff.ID, agency.ID, group.ID, qrToken, 20, "现场活动", "same_staff_add_01")
	if err != nil {
		t.Fatalf("idempotent staff retry: %v", err)
	}
	if first.LedgerID != second.LedgerID {
		t.Fatalf("retry created another ledger: %d != %d", first.LedgerID, second.LedgerID)
	}
	_, err = service.AddByStaff(context.Background(), staff.ID, agency.ID, group.ID, qrToken, 20, "再次提交", "another_staff_add_01")
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "qr_token_not_found" {
		t.Fatalf("expected consumed QR rejection, got %v", err)
	}
	var account model.AgencyPointAccount
	db.Where("agency_id = ? AND user_id = ?", agency.ID, target.ID).First(&account)
	if account.Balance != 20 {
		t.Fatalf("expected one point addition, got %d", account.Balance)
	}
	if err := db.First(&target, target.ID).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if target.PointsBalance != account.Balance {
		t.Fatalf("point addition did not synchronize user mirror: user=%d account=%d", target.PointsBalance, account.Balance)
	}
}

func TestDatabaseRejectsInvalidMonetaryAndInventoryValues(t *testing.T) {
	db := newCatalogDBForTest(t)
	for modelValue, indexName := range map[any]string{
		&model.PointLedger{}:   "idx_point_user_cursor",
		&model.ExchangeOrder{}: "idx_exchange_user_cursor",
		&model.Goods{}:         "idx_goods_catalog",
	} {
		if !db.Migrator().HasIndex(modelValue, indexName) {
			t.Fatalf("expected database index %s", indexName)
		}
	}
	agency := model.Agency{Name: "数据库约束事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "约束团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	user := model.User{OpenID: "constraint_user", Status: model.UserStatusNormal}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: -1}).Error; err == nil {
		t.Fatal("expected database to reject negative point balance")
	}
	if err := db.Create(&model.Goods{
		AgencyID: agency.ID, GroupID: group.ID, Name: "非法库存商品",
		PricePoints: 1, Stock: MaxGoodsStock + 1, Status: model.GoodsStatusOnSale,
	}).Error; err == nil {
		t.Fatal("expected database to reject oversized stock")
	}
	if err := db.Create(&model.PointLedger{
		AgencyID: agency.ID, GroupID: group.ID, UserID: user.ID,
		BeforePoints: 0, DeltaPoints: -1, AfterPoints: -1,
		Type: PointLedgerExchangeCost, OperatorID: user.ID,
	}).Error; err == nil {
		t.Fatal("expected database to reject negative ledger balance")
	}
}

func TestStaffAddRejectsOversizedAndMalformedInputs(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewPointsService(db, &AuthService{db: db})
	validToken, err := randomToken()
	if err != nil {
		t.Fatalf("generate identity token: %v", err)
	}

	tests := []struct {
		name      string
		token     string
		points    int64
		remark    string
		wantError string
	}{
		{name: "points above one-time limit", token: validToken, points: MaxPointsPerGrant + 1, wantError: "points_too_large"},
		{name: "malformed token", token: "' OR 1=1 --", points: 1, wantError: "invalid_qr_token"},
		{name: "oversized remark", token: validToken, points: 1, remark: strings.Repeat("积", MaxPointRemarkRunes+1), wantError: "invalid_remark"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.AddByStaff(context.Background(), 1, 1, 1, tt.token, tt.points, tt.remark, "security_input_01")
			assertSecurityErrorCode(t, err, tt.wantError)
		})
	}
}

func TestStaffAddRejectsBalanceAboveLimitWithoutMutation(t *testing.T) {
	fixture := newStaffPointSecurityFixture(t, MaxPointBalance-10)

	_, err := fixture.service.AddByStaff(
		context.Background(),
		fixture.staff.ID,
		fixture.agency.ID,
		fixture.group.ID,
		fixture.token,
		20,
		"大数保护",
		"balance_limit_01",
	)
	assertSecurityErrorCode(t, err, "points_balance_limit")

	var account model.AgencyPointAccount
	if err := fixture.service.db.Where("agency_id = ? AND user_id = ?", fixture.agency.ID, fixture.target.ID).First(&account).Error; err != nil {
		t.Fatalf("load account: %v", err)
	}
	if account.Balance != MaxPointBalance-10 {
		t.Fatalf("balance changed after rejection: %d", account.Balance)
	}
	var tokenCount int64
	fixture.service.db.Model(&model.IdentityQRToken{}).Where("token_hash = ?", tokenHash(fixture.token)).Count(&tokenCount)
	if tokenCount != 1 {
		t.Fatal("rejected request consumed the identity QR code")
	}
}

func TestStaffAddTreatsSQLPayloadAsPlainRemark(t *testing.T) {
	fixture := newStaffPointSecurityFixture(t, 0)
	injectionText := "'); DROP TABLE users; --"

	result, err := fixture.service.AddByStaff(
		context.Background(),
		fixture.staff.ID,
		fixture.agency.ID,
		fixture.group.ID,
		fixture.token,
		60,
		injectionText,
		"sql_payload_01",
	)
	if err != nil {
		t.Fatalf("add points with plain-text remark: %v", err)
	}
	var ledger model.PointLedger
	if err := fixture.service.db.First(&ledger, result.LedgerID).Error; err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if ledger.Remark != injectionText {
		t.Fatalf("remark was altered: %q", ledger.Remark)
	}
	var userCount int64
	if err := fixture.service.db.Model(&model.User{}).Count(&userCount).Error; err != nil {
		t.Fatalf("users table should still exist: %v", err)
	}
	if userCount != 2 {
		t.Fatalf("unexpected user count after SQL-like input: %d", userCount)
	}
}

type staffPointSecurityFixture struct {
	service *PointsService
	staff   model.User
	target  model.User
	agency  model.Agency
	group   model.IdolGroup
	token   string
}

func newStaffPointSecurityFixture(t *testing.T, balance int64) staffPointSecurityFixture {
	t.Helper()
	db := newCatalogDBForTest(t)
	fixture := staffPointSecurityFixture{
		service: NewPointsService(db, &AuthService{db: db}),
		staff:   model.User{OpenID: "security_staff", Status: model.UserStatusNormal},
		target:  model.User{OpenID: "security_target", Status: model.UserStatusNormal, PointsBalance: balance},
		agency:  model.Agency{Name: "安全测试事务所", Status: model.MemberStatusNormal},
	}
	for name, value := range map[string]any{
		"staff":  &fixture.staff,
		"target": &fixture.target,
		"agency": &fixture.agency,
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	fixture.group = model.IdolGroup{AgencyID: fixture.agency.ID, Name: "安全测试团", Status: model.MemberStatusNormal}
	if err := db.Create(&fixture.group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&model.StaffMember{AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, UserID: fixture.staff.ID, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create staff membership: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: fixture.agency.ID, UserID: fixture.target.ID, Balance: balance}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}
	var err error
	fixture.token, err = randomToken()
	if err != nil {
		t.Fatalf("generate identity token: %v", err)
	}
	if err := db.Create(&model.IdentityQRToken{TokenHash: tokenHash(fixture.token), UserID: fixture.target.ID, ExpiresAt: time.Now().Add(time.Minute)}).Error; err != nil {
		t.Fatalf("create identity token: %v", err)
	}
	return fixture
}

func assertSecurityErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestCreatingIdentityQRCodeRevokesOlderCode(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewPointsService(db, &AuthService{db: db})
	user := model.User{OpenID: "single_qr_user", Status: model.UserStatusNormal}
	db.Create(&user)

	first, err := service.CreateIdentityQRCode(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create first QR: %v", err)
	}
	second, err := service.CreateIdentityQRCode(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create second QR: %v", err)
	}
	var count int64
	db.Model(&model.IdentityQRToken{}).Where("user_id = ?", user.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected one active QR, got %d", count)
	}
	var stored model.IdentityQRToken
	db.Where("user_id = ?", user.ID).First(&stored)
	if stored.TokenHash != tokenHash(second.Token) || stored.TokenHash == tokenHash(first.Token) {
		t.Fatal("older QR token remained active")
	}
}

func TestDisabledUserHasNoAdminPermission(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := &AuthService{db: db}
	user := model.User{OpenID: "disabled_admin", Status: model.UserStatusDisabled}
	agency := model.Agency{Name: "禁用管理员事务所", Status: model.MemberStatusNormal}
	db.Create(&user)
	db.Create(&agency)
	db.Create(&model.AdminMember{AgencyID: agency.ID, UserID: user.ID, Status: model.MemberStatusNormal})

	ok, err := service.HasAdminAgency(context.Background(), user.ID, agency.ID)
	if err != nil {
		t.Fatalf("check admin permission: %v", err)
	}
	if ok {
		t.Fatal("disabled user retained admin permission")
	}
}

func TestBackfillCreatesAgencyScopedBalances(t *testing.T) {
	db := newCatalogDBForTest(t)
	user := model.User{OpenID: "backfill_user", Status: model.UserStatusNormal, PointsBalance: 30}
	agency := model.Agency{Name: "迁移事务所", Status: model.MemberStatusNormal}
	db.Create(&user)
	db.Create(&agency)
	db.Create(&model.PointLedger{AgencyID: agency.ID, GroupID: 1, UserID: user.ID, DeltaPoints: 50, AfterPoints: 50, Type: PointLedgerStaffAdd, OperatorID: 1})
	db.Create(&model.PointLedger{AgencyID: agency.ID, GroupID: 1, UserID: user.ID, BeforePoints: 50, DeltaPoints: -20, AfterPoints: 30, Type: PointLedgerExchangeCost, OperatorID: user.ID})

	if err := BackfillAgencyPointAccounts(context.Background(), db); err != nil {
		t.Fatalf("backfill point accounts: %v", err)
	}
	var account model.AgencyPointAccount
	if err := db.Where("agency_id = ? AND user_id = ?", agency.ID, user.ID).First(&account).Error; err != nil {
		t.Fatalf("load point account: %v", err)
	}
	if account.Balance != 30 {
		t.Fatalf("expected ledger-derived balance 30, got %d", account.Balance)
	}
}
