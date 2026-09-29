package service

import (
	"context"
	"errors"
	"testing"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestPointLedgerPaginationAndAdminAdjustment(t *testing.T) {
	db := newCatalogDBForTest(t)
	authService := &AuthService{db: db}
	service := NewPointsService(db, authService)

	admin := model.User{OpenID: "points_admin", Status: model.UserStatusNormal}
	target := model.User{OpenID: "points_target", Nickname: "目标用户", Status: model.UserStatusNormal, PointsBalance: 100}
	outsider := model.User{OpenID: "points_outsider", Status: model.UserStatusNormal}
	for _, user := range []*model.User{&admin, &target, &outsider} {
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	agency := model.Agency{Name: "积分纠错事务所", Status: model.MemberStatusNormal}
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
	if err := db.Create(&model.AgencyPointAccount{AgencyID: agency.ID, UserID: target.ID, Balance: 100}).Error; err != nil {
		t.Fatalf("create point account: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if err := db.Create(&model.PointLedger{
			AgencyID:     agency.ID,
			UserID:       target.ID,
			BeforePoints: int64(i - 1),
			DeltaPoints:  1,
			AfterPoints:  int64(i),
			Type:         PointLedgerStaffAdd,
			OperatorID:   admin.ID,
			Remark:       "历史流水",
		}).Error; err != nil {
			t.Fatalf("create ledger: %v", err)
		}
	}

	firstPage, err := service.ListUserLedgers(context.Background(), target.ID, 0, 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(firstPage.Items) != 2 || !firstPage.HasMore || firstPage.NextCursor == 0 {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}
	if firstPage.Items[0].ID <= firstPage.Items[1].ID {
		t.Fatalf("ledger page is not newest first: %+v", firstPage.Items)
	}
	if firstPage.Items[0].OperatorID != 0 {
		t.Fatalf("user ledger page leaked operator ID: %+v", firstPage.Items[0])
	}
	secondPage, err := service.ListUserLedgers(context.Background(), target.ID, firstPage.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(secondPage.Items) != 1 || secondPage.HasMore {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}

	pointUser, err := service.GetAdminPointUser(context.Background(), admin.ID, agency.ID, target.ID)
	if err != nil {
		t.Fatalf("get target user: %v", err)
	}
	if pointUser.ID != target.ID || pointUser.Nickname != target.Nickname || pointUser.PointsBalance != 100 {
		t.Fatalf("unexpected target user: %+v", pointUser)
	}
	adminPage, err := service.ListAdminUserLedgers(context.Background(), admin.ID, agency.ID, target.ID, 0, 2)
	if err != nil {
		t.Fatalf("list admin ledger page: %v", err)
	}
	if len(adminPage.Items) != 2 || adminPage.Items[0].OperatorID != admin.ID {
		t.Fatalf("admin ledger page lost audit operator: %+v", adminPage)
	}

	added, err := service.AdjustByAdmin(context.Background(), admin.ID, agency.ID, target.ID, 50, "补录线下购券积分", "admin_adjust_01")
	if err != nil {
		t.Fatalf("add correction: %v", err)
	}
	if added.BeforePoints != 100 || added.AfterPoints != 150 || added.DeltaPoints != 50 {
		t.Fatalf("unexpected add correction: %+v", added)
	}
	same, err := service.AdjustByAdmin(context.Background(), admin.ID, agency.ID, target.ID, 50, "补录线下购券积分", "admin_adjust_01")
	if err != nil {
		t.Fatalf("repeat correction: %v", err)
	}
	if same.LedgerID != added.LedgerID {
		t.Fatalf("idempotent correction returned another ledger: %d != %d", same.LedgerID, added.LedgerID)
	}
	deducted, err := service.AdjustByAdmin(context.Background(), admin.ID, agency.ID, target.ID, -30, "冲正重复授予积分", "admin_adjust_02")
	if err != nil {
		t.Fatalf("deduct correction: %v", err)
	}
	if deducted.BeforePoints != 150 || deducted.AfterPoints != 120 {
		t.Fatalf("unexpected deduction: %+v", deducted)
	}

	var correctionCount int64
	if err := db.Model(&model.PointLedger{}).
		Where("user_id = ? AND type = ?", target.ID, PointLedgerAdminAdjust).
		Count(&correctionCount).Error; err != nil {
		t.Fatalf("count correction ledgers: %v", err)
	}
	if correctionCount != 2 {
		t.Fatalf("expected two correction ledgers, got %d", correctionCount)
	}
	var account model.AgencyPointAccount
	if err := db.Where("agency_id = ? AND user_id = ?", agency.ID, target.ID).First(&account).Error; err != nil {
		t.Fatalf("load account: %v", err)
	}
	if err := db.First(&target, target.ID).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if account.Balance != 120 || target.PointsBalance != 120 {
		t.Fatalf("balance mirrors diverged: account=%d user=%d", account.Balance, target.PointsBalance)
	}

	_, err = service.GetAdminPointUser(context.Background(), outsider.ID, agency.ID, target.ID)
	assertPointServiceErrorCode(t, err, "admin_forbidden")
	_, err = service.AdjustByAdmin(context.Background(), outsider.ID, agency.ID, target.ID, 1, "无权修改", "admin_adjust_03")
	assertPointServiceErrorCode(t, err, "admin_forbidden")
	_, err = service.AdjustByAdmin(context.Background(), admin.ID, agency.ID, target.ID, -121, "余额不足扣减", "admin_adjust_04")
	assertPointServiceErrorCode(t, err, "points_balance_limit")
	_, err = service.AdjustByAdmin(context.Background(), admin.ID, agency.ID, target.ID, 1, "短", "admin_adjust_05")
	assertPointServiceErrorCode(t, err, "invalid_remark")
}

func assertPointServiceErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
