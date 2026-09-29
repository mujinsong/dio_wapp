package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminCanManageGroupsInsideAgency(t *testing.T) {
	db := newCatalogDBForTest(t)
	authService := &AuthService{db: db}
	service := NewAdminGoodsService(db, authService)

	admin := model.User{OpenID: "group_admin", Status: model.UserStatusNormal}
	agency := model.Agency{Name: "团体测试事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	if err := db.Create(&model.AdminMember{
		AgencyID: agency.ID,
		UserID:   admin.ID,
		Status:   model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create admin member: %v", err)
	}

	created, err := service.CreateGroup(context.Background(), admin.ID, AdminGroupInput{
		AgencyID:    agency.ID,
		Name:        "星屑少女",
		Description: "测试团体",
		Status:      model.MemberStatusNormal,
		Sort:        20,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if created.Name != "星屑少女" || created.Sort != 20 {
		t.Fatalf("unexpected group: %+v", created)
	}

	updated, err := service.UpdateGroup(context.Background(), admin.ID, created.ID, AdminGroupInput{
		AgencyID:    agency.ID,
		Name:        "星屑少女改",
		Description: "新说明",
		Status:      model.MemberStatusNormal,
		Sort:        30,
	})
	if err != nil {
		t.Fatalf("update group: %v", err)
	}
	if updated.Name != "星屑少女改" || updated.Description != "新说明" {
		t.Fatalf("unexpected updated group: %+v", updated)
	}

	startsAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	createdGoods, err := service.CreateGoods(context.Background(), admin.ID, AdminGoodsInput{
		AgencyID: agency.ID, GroupID: created.ID, Name: "定时商品", PricePoints: 20,
		Stock: 10, PurchaseLimitPerUser: 1, PurchaseLimitHours: 2,
		SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale, RequestID: "admin_goods_create_01",
	})
	if err != nil {
		t.Fatalf("create scheduled goods: %v", err)
	}
	if createdGoods.SaleStartsAt == nil || !createdGoods.SaleStartsAt.Equal(startsAt) {
		t.Fatalf("scheduled goods lost start time: %+v", createdGoods)
	}
	var storedGoods model.Goods
	if err := db.First(&storedGoods, createdGoods.ID).Error; err != nil {
		t.Fatalf("load scheduled goods: %v", err)
	}
	if storedGoods.PurchaseLimitStartedAt == nil || !storedGoods.PurchaseLimitStartedAt.Equal(startsAt) {
		t.Fatalf("scheduled limit did not start with sale: %+v", storedGoods)
	}
	repeatedGoods, err := service.CreateGoods(context.Background(), admin.ID, AdminGoodsInput{
		AgencyID: agency.ID, GroupID: created.ID, Name: "定时商品", PricePoints: 20,
		Stock: 10, PurchaseLimitPerUser: 1, PurchaseLimitHours: 2,
		SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale, RequestID: "admin_goods_create_01",
	})
	if err != nil {
		t.Fatalf("repeat admin goods creation: %v", err)
	}
	if repeatedGoods.ID != createdGoods.ID {
		t.Fatalf("admin goods retry created another item: %d != %d", repeatedGoods.ID, createdGoods.ID)
	}
	var createdGoodsCount int64
	if err := db.Model(&model.Goods{}).Where("name = ?", "定时商品").Count(&createdGoodsCount).Error; err != nil {
		t.Fatalf("count idempotent goods: %v", err)
	}
	if createdGoodsCount != 1 {
		t.Fatalf("expected one idempotent goods record, got %d", createdGoodsCount)
	}
	var initialMovements int64
	if err := db.Model(&model.StockMovement{}).Where("goods_id = ? AND type = ?", createdGoods.ID, model.StockMovementInitial).Count(&initialMovements).Error; err != nil {
		t.Fatalf("count initial stock movements: %v", err)
	}
	if initialMovements != 1 {
		t.Fatalf("idempotent create must write one initial stock movement, got %d", initialMovements)
	}
	updatedGoods, err := service.UpdateGoods(context.Background(), admin.ID, createdGoods.ID, AdminGoodsInput{
		AgencyID: agency.ID, GroupID: created.ID, Name: "定时商品", PricePoints: 20,
		Stock: 12, PurchaseLimitPerUser: 1, PurchaseLimitHours: 2,
		SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale, ExpectedUpdatedAt: &createdGoods.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("update goods stock: %v", err)
	}
	if updatedGoods.Stock != 12 {
		t.Fatalf("goods stock not updated: %+v", updatedGoods)
	}
	var adjustment model.StockMovement
	if err := db.Where("goods_id = ? AND type = ?", createdGoods.ID, model.StockMovementAdminAdjust).First(&adjustment).Error; err != nil {
		t.Fatalf("load stock adjustment: %v", err)
	}
	if adjustment.BeforeStock != 10 || adjustment.DeltaStock != 2 || adjustment.AfterStock != 12 || adjustment.OperatorID != admin.ID {
		t.Fatalf("unexpected stock adjustment: %+v", adjustment)
	}
	newUpdatedAt := updatedGoods.UpdatedAt.Add(time.Second)
	if err := db.Model(&model.Goods{}).Where("id = ?", updatedGoods.ID).Updates(map[string]any{
		"stock":      11,
		"updated_at": newUpdatedAt,
	}).Error; err != nil {
		t.Fatalf("simulate concurrent exchange: %v", err)
	}
	_, err = service.UpdateGoods(context.Background(), admin.ID, updatedGoods.ID, AdminGoodsInput{
		AgencyID: agency.ID, GroupID: created.ID, Name: "定时商品", PricePoints: 20,
		Stock: 12, PurchaseLimitPerUser: 1, PurchaseLimitHours: 2,
		SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale, ExpectedUpdatedAt: &updatedGoods.UpdatedAt,
	})
	var staleErr *xerr.Error
	if !errors.As(err, &staleErr) || staleErr.Code != "goods_snapshot_changed" {
		t.Fatalf("expected stale goods snapshot rejection, got %v", err)
	}
	if err := db.First(&storedGoods, updatedGoods.ID).Error; err != nil {
		t.Fatalf("reload concurrently changed goods: %v", err)
	}
	if storedGoods.Stock != 11 {
		t.Fatalf("stale admin update overwrote stock: %d", storedGoods.Stock)
	}
	_, err = service.CreateGoods(context.Background(), admin.ID, AdminGoodsInput{
		AgencyID: agency.ID, GroupID: created.ID, Name: "定时商品", PricePoints: 20,
		Stock: 11, PurchaseLimitPerUser: 1, PurchaseLimitHours: 2,
		SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale, RequestID: "admin_goods_create_01",
	})
	var idempotencyErr *xerr.Error
	if !errors.As(err, &idempotencyErr) || idempotencyErr.Code != "idempotency_conflict" {
		t.Fatalf("expected admin goods idempotency conflict, got %v", err)
	}

	invalidGoods := []struct {
		name  string
		input AdminGoodsInput
		code  string
	}{
		{
			name:  "oversized name",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: strings.Repeat("名", 129), PricePoints: 1, Stock: 1, Status: model.GoodsStatusOnSale},
			code:  "invalid_name",
		},
		{
			name:  "oversized description",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", Description: strings.Repeat("说", 513), PricePoints: 1, Stock: 1, Status: model.GoodsStatusOnSale},
			code:  "invalid_goods_text",
		},
		{
			name:  "invalid image URL",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", ImageURL: "javascript:alert(1)", PricePoints: 1, Stock: 1, Status: model.GoodsStatusOnSale},
			code:  "invalid_image_url",
		},
		{
			name:  "oversized price",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", PricePoints: MaxPointBalance + 1, Stock: 1, Status: model.GoodsStatusOnSale},
			code:  "invalid_price",
		},
		{
			name:  "oversized stock",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", PricePoints: 1, Stock: 1_000_000_001, Status: model.GoodsStatusOnSale},
			code:  "invalid_stock",
		},
		{
			name:  "oversized purchase limit",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", PricePoints: 1, Stock: 1, PurchaseLimitPerUser: 1_000_000_001, PurchaseLimitHours: 1, Status: model.GoodsStatusOnSale},
			code:  "invalid_purchase_limit",
		},
		{
			name:  "oversized purchase hours",
			input: AdminGoodsInput{AgencyID: agency.ID, GroupID: created.ID, Name: "商品", PricePoints: 1, Stock: 1, PurchaseLimitPerUser: 1, PurchaseLimitHours: 8761, Status: model.GoodsStatusOnSale},
			code:  "invalid_purchase_limit",
		},
	}
	for _, testCase := range invalidGoods {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := service.CreateGoods(context.Background(), admin.ID, testCase.input)
			var appErr *xerr.Error
			if !errors.As(err, &appErr) || appErr.Code != testCase.code {
				t.Fatalf("expected %s, got %v", testCase.code, err)
			}
		})
	}

	if err := service.DeleteGroup(context.Background(), admin.ID, created.ID); err != nil {
		t.Fatalf("disable group: %v", err)
	}
	groups, err := service.ListGroups(context.Background(), admin.ID, agency.ID)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 || groups[0].Status != model.MemberStatusDisabled {
		t.Fatalf("expected disabled group in admin list, got %+v", groups)
	}
}

func TestShopHidesAndRejectsDisabledGroup(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	agency := model.Agency{Name: "商店测试事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	groupA := model.IdolGroup{AgencyID: agency.ID, Name: "团体 A", Status: model.MemberStatusNormal, Sort: 20}
	groupB := model.IdolGroup{AgencyID: agency.ID, Name: "团体 B", Status: model.MemberStatusNormal, Sort: 10}
	if err := db.Create(&groupA).Error; err != nil {
		t.Fatalf("create group A: %v", err)
	}
	if err := db.Create(&groupB).Error; err != nil {
		t.Fatalf("create group B: %v", err)
	}
	goodsA := model.Goods{AgencyID: agency.ID, GroupID: groupA.ID, Name: "A 商品", PricePoints: 10, Stock: 2, Status: model.GoodsStatusOnSale}
	goodsB := model.Goods{AgencyID: agency.ID, GroupID: groupB.ID, Name: "B 商品", PricePoints: 20, Stock: 2, Status: model.GoodsStatusOnSale}
	if err := db.Create(&goodsA).Error; err != nil {
		t.Fatalf("create goods A: %v", err)
	}
	if err := db.Create(&goodsB).Error; err != nil {
		t.Fatalf("create goods B: %v", err)
	}

	groups, err := service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("list shop groups: %v", err)
	}
	if len(groups) != 2 || groups[0].ID != groupA.ID {
		t.Fatalf("unexpected shop groups: %+v", groups)
	}
	goods, err := service.ListGoods(context.Background(), 0, groupA.ID)
	if err != nil {
		t.Fatalf("list group goods: %v", err)
	}
	if len(goods) != 1 || goods[0].ID != goodsA.ID {
		t.Fatalf("unexpected filtered goods: %+v", goods)
	}
	detail, err := service.GetGoods(context.Background(), 0, goodsA.ID)
	if err != nil {
		t.Fatalf("get goods detail: %v", err)
	}
	if detail.ID != goodsA.ID || detail.GroupName != groupA.Name {
		t.Fatalf("unexpected goods detail: %+v", detail)
	}

	if err := db.Model(&groupA).Update("status", model.MemberStatusDisabled).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}
	goods, err = service.ListGoods(context.Background(), 0, groupA.ID)
	if err != nil {
		t.Fatalf("list disabled group goods: %v", err)
	}
	if len(goods) != 0 {
		t.Fatalf("expected hidden goods, got %+v", goods)
	}
	_, err = service.GetGoods(context.Background(), 0, goodsA.ID)
	var detailErr *xerr.Error
	if !errors.As(err, &detailErr) || detailErr.Code != "goods_not_found" {
		t.Fatalf("expected hidden goods detail, got %v", err)
	}

	user := model.User{OpenID: "shop_user", Status: model.UserStatusNormal, PointsBalance: 100}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}
	_, err = service.ExchangeGoods(context.Background(), user.ID, goodsA.ID, "disabled_group_01", goodsA.PricePoints)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "goods_group_unavailable" {
		t.Fatalf("expected disabled group rejection, got %v", err)
	}
}

func TestScheduledGoodsAreHiddenAndCannotBeExchangedBeforeStart(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	agency := model.Agency{Name: "定时上架事务所", Status: model.MemberStatusNormal}
	user := model.User{OpenID: "scheduled_goods_user", Status: model.UserStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "定时上架团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	startsAt := time.Now().Add(2 * time.Hour)
	goods := model.Goods{
		AgencyID: agency.ID, GroupID: group.ID, Name: "未来商品", PricePoints: 10,
		Stock: 2, SaleStartsAt: &startsAt, Status: model.GoodsStatusOnSale,
	}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create scheduled goods: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}

	groups, err := service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("scheduled-only group was visible: %+v", groups)
	}
	goodsList, err := service.ListGoods(context.Background(), user.ID, group.ID)
	if err != nil {
		t.Fatalf("list goods: %v", err)
	}
	if len(goodsList) != 0 {
		t.Fatalf("scheduled goods was visible: %+v", goodsList)
	}
	_, err = service.GetGoods(context.Background(), user.ID, goods.ID)
	var detailErr *xerr.Error
	if !errors.As(err, &detailErr) || detailErr.Code != "goods_not_found" {
		t.Fatalf("expected hidden scheduled detail, got %v", err)
	}
	_, err = service.ExchangeGoods(context.Background(), user.ID, goods.ID, "scheduled_before_01", goods.PricePoints)
	var exchangeErr *xerr.Error
	if !errors.As(err, &exchangeErr) || exchangeErr.Code != "goods_not_started" {
		t.Fatalf("expected scheduled exchange rejection, got %v", err)
	}

	started := time.Now().Add(-time.Minute)
	if err := db.Model(&goods).Update("sale_starts_at", &started).Error; err != nil {
		t.Fatalf("start scheduled goods: %v", err)
	}
	goodsList, err = service.ListGoods(context.Background(), user.ID, group.ID)
	if err != nil || len(goodsList) != 1 || goodsList[0].ID != goods.ID {
		t.Fatalf("started goods not visible: goods=%+v err=%v", goodsList, err)
	}
	if _, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "scheduled_after_01", goods.PricePoints); err != nil {
		t.Fatalf("exchange after scheduled start: %v", err)
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatalf("reload scheduled user: %v", err)
	}
	if user.PointsBalance != 90 {
		t.Fatalf("exchange did not repair user balance mirror: %d", user.PointsBalance)
	}
}

func TestExchangeEnforcesPerUserPurchaseLimit(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	agency := model.Agency{Name: "限购测试事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "限购团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	goods := model.Goods{
		AgencyID:             agency.ID,
		GroupID:              group.ID,
		Name:                 "限购商品",
		PricePoints:          10,
		Stock:                2,
		PurchaseLimitPerUser: 1,
		PurchaseLimitHours:   2,
		Status:               model.GoodsStatusOnSale,
	}
	limitStartedAt := time.Now()
	goods.PurchaseLimitStartedAt = &limitStartedAt
	user := model.User{OpenID: "limited_user", Status: model.UserStatusNormal, PointsBalance: 100}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}

	first, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "limit_first_01", goods.PricePoints)
	if err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if first.UserExchangeCount != 1 {
		t.Fatalf("expected one exchange, got %+v", first)
	}

	_, err = service.ExchangeGoods(context.Background(), user.ID, goods.ID, "limit_second_01", goods.PricePoints)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "purchase_limit_reached" {
		t.Fatalf("expected purchase limit rejection, got %v", err)
	}
	detail, err := service.GetGoods(context.Background(), user.ID, goods.ID)
	if err != nil {
		t.Fatalf("get limited goods: %v", err)
	}
	if !detail.PurchaseLimitActive || !detail.LimitReached || detail.UserExchangeCount != 1 {
		t.Fatalf("expected reached limit in detail, got %+v", detail)
	}
}

func TestExpiredPurchaseLimitAllowsExchange(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	agency := model.Agency{Name: "过期限购事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "过期限购团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	startedAt := time.Now().Add(-3 * time.Hour)
	goods := model.Goods{
		AgencyID:               agency.ID,
		GroupID:                group.ID,
		Name:                   "限购已结束商品",
		PricePoints:            10,
		Stock:                  2,
		PurchaseLimitPerUser:   1,
		PurchaseLimitHours:     2,
		PurchaseLimitStartedAt: &startedAt,
		Status:                 model.GoodsStatusOnSale,
	}
	user := model.User{OpenID: "expired_limit_user", Status: model.UserStatusNormal, PointsBalance: 100}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}
	if err := db.Create(&model.ExchangeOrder{
		OrderNo:         "expired-limit-order",
		AgencyID:        agency.ID,
		GroupID:         group.ID,
		UserID:          user.ID,
		GoodsID:         goods.ID,
		GoodsName:       goods.Name,
		PointsCost:      goods.PricePoints,
		Status:          model.ExchangeOrderStatusPending,
		RedeemTokenHash: "expired-limit-token",
	}).Error; err != nil {
		t.Fatalf("create previous order: %v", err)
	}

	detail, err := service.GetGoods(context.Background(), user.ID, goods.ID)
	if err != nil {
		t.Fatalf("get goods: %v", err)
	}
	if detail.PurchaseLimitActive || detail.LimitReached {
		t.Fatalf("expected expired limit, got %+v", detail)
	}
	if _, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "expired_limit_01", goods.PricePoints); err != nil {
		t.Fatalf("exchange after limit expiration: %v", err)
	}
}

func TestExchangeRedisLockBusyAndFallback(t *testing.T) {
	ctx := context.Background()
	busyLocker := &fakeExchangeLocker{}
	db := newCatalogDBForTest(t)
	if err := db.Create(&model.Agency{Name: "锁测试事务所", Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create lock test agency: %v", err)
	}
	busyService := NewShopServiceWithLocker(db, busyLocker, 5*time.Second, 800*time.Millisecond)
	_, err := busyService.ExchangeGoods(ctx, 1, 9, "busy_lock_01", 10)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "exchange_busy" {
		t.Fatalf("expected exchange busy, got %v", err)
	}
	if busyLocker.key != "dio:exchange:goods:9" {
		t.Fatalf("unexpected lock key: %s", busyLocker.key)
	}

	fallbackLocker := &fakeExchangeLocker{err: errors.New("redis unavailable")}
	fallbackService := NewShopServiceWithLocker(db, fallbackLocker, 5*time.Second, 800*time.Millisecond)
	_, err = fallbackService.ExchangeGoods(ctx, 999, 9, "fallback_lock_01", 10)
	if !errors.As(err, &appErr) || appErr.Code != "user_not_found" {
		t.Fatalf("expected MySQL fallback result, got %v", err)
	}

	acquiredLocker := &fakeExchangeLocker{acquired: true}
	lockedService := NewShopServiceWithLocker(db, acquiredLocker, 5*time.Second, 800*time.Millisecond)
	_, _ = lockedService.ExchangeGoods(ctx, 999, 9, "acquired_lock_01", 10)
	if !acquiredLocker.released {
		t.Fatal("expected distributed lock release after transaction error")
	}
}

func TestStaffCanRedeemExchangeOrderWithQRCode(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	agency := model.Agency{Name: "核销测试事务所", Status: model.MemberStatusNormal}
	user := model.User{OpenID: "redeem_user", Status: model.UserStatusNormal, PointsBalance: 100}
	staff := model.User{OpenID: "redeem_staff", Status: model.UserStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&staff).Error; err != nil {
		t.Fatalf("create staff: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "核销团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&model.StaffMember{
		AgencyID: agency.ID,
		GroupID:  group.ID,
		UserID:   staff.ID,
		Status:   model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create staff member: %v", err)
	}
	goods := model.Goods{
		AgencyID: agency.ID, GroupID: group.ID, Name: "核销商品",
		PricePoints: 20, Stock: 2, Status: model.GoodsStatusOnSale,
	}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	exchanged, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "exchange_redeem_01", goods.PricePoints)
	if err != nil {
		t.Fatalf("exchange goods: %v", err)
	}
	qr, err := service.CreateRedeemQRCode(context.Background(), user.ID, exchanged.Order.ID)
	if err != nil {
		t.Fatalf("create redeem qr: %v", err)
	}
	if !strings.HasPrefix(qr.Payload, RedeemQRPrefix) || qr.QRImage == "" {
		t.Fatalf("unexpected redeem qr: %+v", qr)
	}

	redeemed, err := service.RedeemOrderByStaff(context.Background(), staff.ID, qr.Payload, "redeem_order_01")
	if err != nil {
		t.Fatalf("redeem order: %v", err)
	}
	if redeemed.Order.Status != model.ExchangeOrderStatusRedeemed || redeemed.Order.RedeemedBy != staff.ID || redeemed.Order.RedeemedAt == nil {
		t.Fatalf("unexpected redeemed order: %+v", redeemed.Order)
	}

	sameRequest, err := service.RedeemOrderByStaff(context.Background(), staff.ID, qr.Payload, "redeem_order_01")
	if err != nil {
		t.Fatalf("repeat same idempotent redeem: %v", err)
	}
	if sameRequest.Order.ID != redeemed.Order.ID {
		t.Fatalf("unexpected idempotent result: %+v", sameRequest)
	}

	_, err = service.RedeemOrderByStaff(context.Background(), staff.ID, qr.Payload, "redeem_order_02")
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "order_already_redeemed" {
		t.Fatalf("expected already redeemed error, got %v", err)
	}

	_, err = service.CancelOrder(context.Background(), user.ID, exchanged.Order.ID, "cancel_redeemed_01")
	if !errors.As(err, &appErr) || appErr.Code != "order_already_redeemed" {
		t.Fatalf("expected redeemed order cancellation error, got %v", err)
	}
}

func TestCancelOrderRefundsPointsRestoresStockAndIsIdempotent(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	user := model.User{OpenID: "cancel_order_user", Status: model.UserStatusNormal, PointsBalance: 100}
	otherUser := model.User{OpenID: "cancel_order_other", Status: model.UserStatusNormal}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&otherUser).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	agency := model.Agency{Name: "取消订单事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "退款团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	goods := model.Goods{
		AgencyID: agency.ID, GroupID: group.ID, Name: "可取消商品",
		PricePoints: 20, Stock: 2, Status: model.GoodsStatusOnSale,
	}
	if err := db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: user.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}

	exchanged, err := service.ExchangeGoods(context.Background(), user.ID, goods.ID, "exchange_cancel_01", goods.PricePoints)
	if err != nil {
		t.Fatalf("exchange goods: %v", err)
	}
	_, err = service.CancelOrder(context.Background(), otherUser.ID, exchanged.Order.ID, "cancel_wrong_user_01")
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "order_not_found" {
		t.Fatalf("expected user-scoped order not found, got %v", err)
	}

	canceled, err := service.CancelOrder(context.Background(), user.ID, exchanged.Order.ID, "cancel_order_01")
	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}
	if canceled.Order.Status != model.ExchangeOrderStatusCanceled || canceled.Order.CanceledAt == nil {
		t.Fatalf("unexpected canceled order: %+v", canceled.Order)
	}
	if canceled.BeforePoints != 80 || canceled.AfterPoints != 100 || canceled.RestoredStock != 1 || canceled.RefundLedgerID == 0 {
		t.Fatalf("unexpected cancellation result: %+v", canceled)
	}

	sameRequest, err := service.CancelOrder(context.Background(), user.ID, exchanged.Order.ID, "cancel_order_01")
	if err != nil {
		t.Fatalf("repeat same cancel request: %v", err)
	}
	differentRequest, err := service.CancelOrder(context.Background(), user.ID, exchanged.Order.ID, "cancel_order_02")
	if err != nil {
		t.Fatalf("repeat cancel with different request: %v", err)
	}
	if sameRequest.RefundLedgerID != canceled.RefundLedgerID || differentRequest.RefundLedgerID != canceled.RefundLedgerID {
		t.Fatalf("repeat cancellation created a different refund: first=%d same=%d different=%d", canceled.RefundLedgerID, sameRequest.RefundLedgerID, differentRequest.RefundLedgerID)
	}

	var account model.AgencyPointAccount
	if err := db.Where("agency_id = ? AND user_id = ?", agency.ID, user.ID).First(&account).Error; err != nil {
		t.Fatalf("load account: %v", err)
	}
	if err := db.First(&user, user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if err := db.First(&goods, goods.ID).Error; err != nil {
		t.Fatalf("reload goods: %v", err)
	}
	if account.Balance != 100 || user.PointsBalance != 100 || goods.Stock != 2 {
		t.Fatalf("unexpected restored state: account=%d user=%d stock=%d", account.Balance, user.PointsBalance, goods.Stock)
	}
	var refundCount int64
	if err := db.Model(&model.PointLedger{}).
		Where("type = ? AND reference_id = ?", PointLedgerOrderRefund, exchanged.Order.ID).
		Count(&refundCount).Error; err != nil {
		t.Fatalf("count refund ledgers: %v", err)
	}
	if refundCount != 1 {
		t.Fatalf("expected one refund ledger, got %d", refundCount)
	}
	var stockMovements []model.StockMovement
	if err := db.Where("goods_id = ?", goods.ID).Order("id ASC").Find(&stockMovements).Error; err != nil {
		t.Fatalf("load cancellation stock movements: %v", err)
	}
	if len(stockMovements) != 2 || stockMovements[0].Type != model.StockMovementExchange || stockMovements[0].DeltaStock != -1 || stockMovements[1].Type != model.StockMovementOrderCancel || stockMovements[1].DeltaStock != 1 {
		t.Fatalf("unexpected cancellation stock audit: %+v", stockMovements)
	}

	_, err = service.CreateRedeemQRCode(context.Background(), user.ID, exchanged.Order.ID)
	if !errors.As(err, &appErr) || appErr.Code != "order_not_pending" {
		t.Fatalf("expected canceled QR rejection, got %v", err)
	}

	fullGoods := model.Goods{
		AgencyID: agency.ID, GroupID: group.ID, Name: "满库存商品",
		PricePoints: 20, Stock: MaxGoodsStock, Status: model.GoodsStatusOnSale,
	}
	if err := db.Create(&fullGoods).Error; err != nil {
		t.Fatalf("create full stock goods: %v", err)
	}
	fullOrder := model.ExchangeOrder{
		OrderNo:         "full-stock-cancel-order",
		AgencyID:        agency.ID,
		GroupID:         group.ID,
		UserID:          user.ID,
		GoodsID:         fullGoods.ID,
		GoodsName:       fullGoods.Name,
		PointsCost:      20,
		Status:          model.ExchangeOrderStatusPending,
		RedeemTokenHash: strings.Repeat("a", 64),
	}
	if err := db.Create(&fullOrder).Error; err != nil {
		t.Fatalf("create full stock order: %v", err)
	}
	_, err = service.CancelOrder(context.Background(), user.ID, fullOrder.ID, "cancel_full_stock_01")
	if !errors.As(err, &appErr) || appErr.Code != "goods_stock_limit" {
		t.Fatalf("expected stock limit cancellation error, got %v", err)
	}
	if err := db.First(&fullOrder, fullOrder.ID).Error; err != nil {
		t.Fatalf("reload full stock order: %v", err)
	}
	if fullOrder.Status != model.ExchangeOrderStatusPending {
		t.Fatalf("failed cancellation changed order status: %s", fullOrder.Status)
	}
}

func TestExchangeOrderPaginationIsUserScoped(t *testing.T) {
	db := newCatalogDBForTest(t)
	service := NewShopService(db)
	user := model.User{OpenID: "order_page_user", Status: model.UserStatusNormal}
	other := model.User{OpenID: "order_page_other", Status: model.UserStatusNormal}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("create other user: %v", err)
	}
	agency := model.Agency{Name: "订单分页事务所", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}
	group := model.IdolGroup{AgencyID: agency.ID, Name: "分页团体", Status: model.MemberStatusNormal}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	for i := 1; i <= 3; i++ {
		order := model.ExchangeOrder{
			OrderNo:         fmt.Sprintf("page-order-%d", i),
			AgencyID:        agency.ID,
			GroupID:         group.ID,
			UserID:          user.ID,
			GoodsID:         uint64(i),
			GoodsName:       "分页商品",
			PointsCost:      10,
			Status:          model.ExchangeOrderStatusPending,
			RedeemTokenHash: fmt.Sprintf("%064d", i),
		}
		if err := db.Create(&order).Error; err != nil {
			t.Fatalf("create order: %v", err)
		}
	}
	if err := db.Create(&model.ExchangeOrder{
		OrderNo:         "other-order",
		AgencyID:        agency.ID,
		GroupID:         group.ID,
		UserID:          other.ID,
		GoodsID:         99,
		GoodsName:       "其他用户商品",
		PointsCost:      10,
		Status:          model.ExchangeOrderStatusPending,
		RedeemTokenHash: strings.Repeat("f", 64),
	}).Error; err != nil {
		t.Fatalf("create other order: %v", err)
	}

	first, err := service.ListOrdersPage(context.Background(), user.ID, 0, 2)
	if err != nil {
		t.Fatalf("list first order page: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == 0 {
		t.Fatalf("unexpected first order page: %+v", first)
	}
	second, err := service.ListOrdersPage(context.Background(), user.ID, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second order page: %v", err)
	}
	if len(second.Items) != 1 || second.HasMore {
		t.Fatalf("unexpected second order page: %+v", second)
	}
	for _, order := range append(first.Items, second.Items...) {
		if order.GoodsName == "其他用户商品" {
			t.Fatalf("order pagination leaked another user's order: %+v", order)
		}
	}
}

type fakeExchangeLocker struct {
	key      string
	acquired bool
	err      error
	released bool
}

func (l *fakeExchangeLocker) Acquire(_ context.Context, key string, _ time.Duration, _ time.Duration) (func(context.Context) error, bool, error) {
	l.key = key
	if l.err != nil {
		return nil, false, l.err
	}
	if !l.acquired {
		return nil, false, nil
	}
	return func(context.Context) error {
		l.released = true
		return nil
	}, true, nil
}

func newCatalogDBForTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	migrateCatalogForTest(t, db)
	return db
}

func migrateCatalogForTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&model.User{},
		&model.AgencyPointAccount{},
		&model.Agency{},
		&model.IdolGroup{},
		&model.StaffMember{},
		&model.StaffMemberAuditLog{},
		&model.AdminMember{},
		&model.IdentityQRToken{},
		&model.PointLedger{},
		&model.PointGrantRequest{},
		&model.StaffPointDailyUsage{},
		&model.PointCorrectionRequest{},
		&model.Goods{},
		&model.StockMovement{},
		&model.GoodsChangeRequest{},
		&model.ExchangeOrder{},
		&model.IdempotencyRecord{},
	); err != nil {
		t.Fatalf("migrate catalog: %v", err)
	}
}
