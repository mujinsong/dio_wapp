package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

type goodsApprovalFixture struct {
	db      *gorm.DB
	service *GoodsApprovalService
	agency  model.Agency
	groupA  model.IdolGroup
	groupB  model.IdolGroup
	staff   model.User
	leader  model.User
	admin   model.User
}

func newGoodsApprovalFixture(t *testing.T) goodsApprovalFixture {
	t.Helper()
	db := newCatalogDBForTest(t)
	agency := model.Agency{Name: "审批测试事务所", Status: model.MemberStatusNormal}
	staff := model.User{OpenID: "approval_staff", Status: model.UserStatusNormal}
	leader := model.User{OpenID: "approval_team_leader", Status: model.UserStatusNormal}
	admin := model.User{OpenID: "approval_admin", Status: model.UserStatusNormal}
	for _, value := range []any{&agency, &staff, &leader, &admin} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create approval fixture: %v", err)
		}
	}
	groupA := model.IdolGroup{AgencyID: agency.ID, Name: "审批团体 A", Status: model.MemberStatusNormal}
	groupB := model.IdolGroup{AgencyID: agency.ID, Name: "审批团体 B", Status: model.MemberStatusNormal}
	if err := db.Create(&groupA).Error; err != nil {
		t.Fatalf("create group A: %v", err)
	}
	if err := db.Create(&groupB).Error; err != nil {
		t.Fatalf("create group B: %v", err)
	}
	if err := db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: groupA.ID, UserID: staff.ID, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create staff membership: %v", err)
	}
	if err := db.Create(&model.StaffMember{AgencyID: agency.ID, GroupID: groupA.ID, UserID: leader.ID, Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create team leader membership: %v", err)
	}
	if err := db.Create(&model.AdminMember{AgencyID: agency.ID, UserID: admin.ID, Status: model.MemberStatusNormal}).Error; err != nil {
		t.Fatalf("create admin membership: %v", err)
	}
	authService := &AuthService{db: db}
	return goodsApprovalFixture{
		db: db, service: NewGoodsApprovalService(db, authService, agency.ID),
		agency: agency, groupA: groupA, groupB: groupB, staff: staff, leader: leader, admin: admin,
	}
}

func validStaffGoodsInput(groupID uint64, requestID string) StaffGoodsRequestInput {
	return StaffGoodsRequestInput{
		Action: model.GoodsChangeActionCreate, GroupID: groupID, Name: "工作人员商品",
		Description: "等待管理员审批", PricePoints: 30, Stock: 20,
		PurchaseLimitPerUser: 2, PurchaseLimitHours: 3,
		Status: model.GoodsStatusOnSale, Sort: 10, RequestID: requestID,
	}
}

func TestRequestedGoodsRejectsUnsafeImageURL(t *testing.T) {
	request := model.GoodsChangeRequest{
		Name:         "测试商品",
		ImageURL:     "data:text/html,unsafe",
		PricePoints:  10,
		Stock:        1,
		TargetStatus: model.GoodsStatusOnSale,
	}
	err := validateRequestedGoods(request)
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != "invalid_image_url" {
		t.Fatalf("expected invalid_image_url, got %v", err)
	}
}

func TestStaffGoodsChangesOnlyApplyAfterAdminApproval(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	ctx := context.Background()

	createdRequest, err := f.service.Submit(ctx, f.staff.ID, validStaffGoodsInput(f.groupA.ID, "create_goods_01"))
	if err != nil {
		t.Fatalf("submit create request: %v", err)
	}
	var goodsCount int64
	f.db.Model(&model.Goods{}).Count(&goodsCount)
	if goodsCount != 0 {
		t.Fatalf("goods changed before approval: count=%d", goodsCount)
	}

	approved, err := f.service.Review(ctx, f.admin.ID, createdRequest.ID, true, "同意上架")
	if err != nil {
		t.Fatalf("approve create request: %v", err)
	}
	if approved.RequestStatus != model.ApprovalStatusApproved || approved.GoodsID == 0 {
		t.Fatalf("unexpected approved request: %+v", approved)
	}
	var goods model.Goods
	if err := f.db.First(&goods, approved.GoodsID).Error; err != nil {
		t.Fatalf("load approved goods: %v", err)
	}
	if goods.Name != "工作人员商品" || goods.GroupID != f.groupA.ID {
		t.Fatalf("unexpected approved goods: %+v", goods)
	}

	retry, err := f.service.Submit(ctx, f.staff.ID, validStaffGoodsInput(f.groupA.ID, "create_goods_01"))
	if err != nil {
		t.Fatalf("retry approved request: %v", err)
	}
	if retry.ID != createdRequest.ID || retry.RequestStatus != model.ApprovalStatusApproved {
		t.Fatalf("retry did not return original request: %+v", retry)
	}

	update := validStaffGoodsInput(f.groupA.ID, "update_goods_01")
	update.Action = model.GoodsChangeActionUpdate
	update.GoodsID = goods.ID
	update.Name = "审批后的新名称"
	update.Stock = 9
	updateRequest, err := f.service.Submit(ctx, f.staff.ID, update)
	if err != nil {
		t.Fatalf("submit update request: %v", err)
	}
	f.db.First(&goods, goods.ID)
	if goods.Name != "工作人员商品" || goods.Stock != 20 {
		t.Fatalf("update applied before approval: %+v", goods)
	}
	if _, err := f.service.Review(ctx, f.admin.ID, updateRequest.ID, true, "信息无误"); err != nil {
		t.Fatalf("approve update request: %v", err)
	}
	f.db.First(&goods, goods.ID)
	if goods.Name != "审批后的新名称" || goods.Stock != 9 {
		t.Fatalf("approved update was not applied: %+v", goods)
	}

	deleteRequest, err := f.service.Submit(ctx, f.staff.ID, StaffGoodsRequestInput{
		Action: model.GoodsChangeActionDelete, GroupID: f.groupA.ID, GoodsID: goods.ID, RequestID: "delete_goods_01",
	})
	if err != nil {
		t.Fatalf("submit delete request: %v", err)
	}
	_, err = f.service.Review(ctx, f.admin.ID, deleteRequest.ID, false, "")
	assertGoodsApprovalErrorCode(t, err, "invalid_review_remark")
	if _, err := f.service.Review(ctx, f.admin.ID, deleteRequest.ID, false, "暂不删除"); err != nil {
		t.Fatalf("reject delete request: %v", err)
	}
	f.db.First(&goods, goods.ID)
	if goods.Status == model.GoodsStatusDeleted {
		t.Fatal("rejected delete request deleted goods")
	}
	secondDelete, err := f.service.Submit(ctx, f.staff.ID, StaffGoodsRequestInput{
		Action: model.GoodsChangeActionDelete, GroupID: f.groupA.ID, GoodsID: goods.ID, RequestID: "delete_goods_02",
	})
	if err != nil {
		t.Fatalf("submit delete request after rejection: %v", err)
	}
	if _, err := f.service.Review(ctx, f.admin.ID, secondDelete.ID, true, "同意删除"); err != nil {
		t.Fatalf("approve delete request: %v", err)
	}
	f.db.First(&goods, goods.ID)
	if goods.Status != model.GoodsStatusDeleted {
		t.Fatalf("approved delete was not applied: %+v", goods)
	}
}

func TestStaffCannotManageGoodsOutsideAssignedGroup(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	_, err := f.service.Submit(context.Background(), f.staff.ID, validStaffGoodsInput(f.groupB.ID, "wrong_group_01"))
	assertGoodsApprovalErrorCode(t, err, "staff_group_forbidden")
}

func TestApprovalRejectsStaleGoodsSnapshot(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	goods := model.Goods{
		AgencyID: f.agency.ID, GroupID: f.groupA.ID, Name: "原商品", PricePoints: 10,
		Stock: 5, Status: model.GoodsStatusOnSale,
	}
	if err := f.db.Create(&goods).Error; err != nil {
		t.Fatalf("create goods: %v", err)
	}
	input := validStaffGoodsInput(f.groupA.ID, "stale_goods_01")
	input.Action = model.GoodsChangeActionUpdate
	input.GoodsID = goods.ID
	request, err := f.service.Submit(context.Background(), f.staff.ID, input)
	if err != nil {
		t.Fatalf("submit stale request: %v", err)
	}
	if err := f.db.Model(&goods).Update("stock", 4).Error; err != nil {
		t.Fatalf("change goods before approval: %v", err)
	}
	_, err = f.service.Review(context.Background(), f.admin.ID, request.ID, true, "")
	assertGoodsApprovalErrorCode(t, err, "goods_changed")
	var stored model.GoodsChangeRequest
	f.db.First(&stored, request.ID)
	if stored.RequestStatus != model.ApprovalStatusPending {
		t.Fatalf("stale request left pending state: %+v", stored)
	}
}

func TestNonAdminCannotReviewGoodsRequest(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	request, err := f.service.Submit(context.Background(), f.staff.ID, validStaffGoodsInput(f.groupA.ID, "non_admin_01"))
	if err != nil {
		t.Fatalf("submit request: %v", err)
	}
	_, err = f.service.Review(context.Background(), f.staff.ID, request.ID, true, "")
	assertGoodsApprovalErrorCode(t, err, "goods_review_forbidden")
}

func TestTeamLeaderChangesApplyImmediatelyWithAuditRecord(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	ctx := context.Background()

	request, err := f.service.Submit(ctx, f.leader.ID, validStaffGoodsInput(f.groupA.ID, "leader_create_01"))
	if err != nil {
		t.Fatalf("team leader create goods: %v", err)
	}
	if request.RequestStatus != model.ApprovalStatusApproved || request.GoodsID == 0 {
		t.Fatalf("leader request was not auto-approved: %+v", request)
	}
	if request.ReviewedBy != f.leader.ID || request.ReviewRemark != "团队负责人直接生效" || request.ReviewedAt == nil {
		t.Fatalf("leader audit fields are incomplete: %+v", request)
	}

	var goods model.Goods
	if err := f.db.First(&goods, request.GoodsID).Error; err != nil {
		t.Fatalf("load directly created goods: %v", err)
	}
	if goods.GroupID != f.groupA.ID || goods.Name != "工作人员商品" {
		t.Fatalf("unexpected directly created goods: %+v", goods)
	}

	retry, err := f.service.Submit(ctx, f.leader.ID, validStaffGoodsInput(f.groupA.ID, "leader_create_01"))
	if err != nil {
		t.Fatalf("retry leader create: %v", err)
	}
	if retry.ID != request.ID {
		t.Fatalf("leader idempotent retry created another request: first=%d retry=%d", request.ID, retry.ID)
	}
}

func TestTeamLeaderCanReviewOnlyStaffRequestsInOwnGroup(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	ctx := context.Background()

	ownGroupRequest, err := f.service.Submit(ctx, f.staff.ID, validStaffGoodsInput(f.groupA.ID, "leader_review_own_group"))
	if err != nil {
		t.Fatalf("submit own-group staff request: %v", err)
	}
	approved, err := f.service.Review(ctx, f.leader.ID, ownGroupRequest.ID, true, "负责人批准")
	if err != nil {
		t.Fatalf("leader approve own-group request: %v", err)
	}
	if approved.RequestStatus != model.ApprovalStatusApproved || approved.ReviewedBy != f.leader.ID {
		t.Fatalf("unexpected leader review result: %+v", approved)
	}

	otherStaff := model.User{OpenID: "approval_other_staff", Status: model.UserStatusNormal}
	if err := f.db.Create(&otherStaff).Error; err != nil {
		t.Fatalf("create other staff: %v", err)
	}
	if err := f.db.Create(&model.StaffMember{
		AgencyID: f.agency.ID, GroupID: f.groupB.ID, UserID: otherStaff.ID,
		Role: model.StaffRoleStaff, Status: model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create other staff membership: %v", err)
	}
	otherGroupRequest, err := f.service.Submit(ctx, otherStaff.ID, validStaffGoodsInput(f.groupB.ID, "leader_review_other_group"))
	if err != nil {
		t.Fatalf("submit other-group request: %v", err)
	}
	_, err = f.service.Review(ctx, f.leader.ID, otherGroupRequest.ID, true, "")
	assertGoodsApprovalErrorCode(t, err, "goods_review_forbidden")

	_, err = f.service.ListTeamLeaderRequests(ctx, f.leader.ID, f.groupB.ID, "all", 0, 20)
	assertGoodsApprovalErrorCode(t, err, "staff_group_forbidden")
}

func TestGoodsChangeRequestPaginationAndAdminCounts(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		input := validStaffGoodsInput(f.groupA.ID, fmt.Sprintf("goods_page_%02d", index))
		input.Name = fmt.Sprintf("分页商品 %d", index)
		if _, err := f.service.Submit(ctx, f.staff.ID, input); err != nil {
			t.Fatalf("submit request %d: %v", index, err)
		}
	}

	first, err := f.service.ListStaffRequests(ctx, f.staff.ID, f.groupA.ID, 0, 2)
	if err != nil {
		t.Fatalf("list first request page: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == 0 || first.Items[0].ID <= first.Items[1].ID {
		t.Fatalf("unexpected first request page: %+v", first)
	}
	second, err := f.service.ListStaffRequests(ctx, f.staff.ID, f.groupA.ID, first.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second request page: %v", err)
	}
	if len(second.Items) != 1 || second.HasMore || second.Items[0].ID >= first.NextCursor {
		t.Fatalf("unexpected second request page: %+v", second)
	}

	adminPage, err := f.service.ListAdminRequests(ctx, f.admin.ID, model.ApprovalStatusPending, 0, 2)
	if err != nil {
		t.Fatalf("list admin request page: %v", err)
	}
	if adminPage.Counts.Pending != 3 || adminPage.Counts.All != 3 {
		t.Fatalf("unexpected admin request counts: %+v", adminPage.Counts)
	}
}

func TestTeamLeaderCannotReviewOwnPendingRequest(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	ctx := context.Background()

	request, err := f.service.Submit(ctx, f.staff.ID, validStaffGoodsInput(f.groupA.ID, "leader_self_review"))
	if err != nil {
		t.Fatalf("submit request before promotion: %v", err)
	}
	if err := f.db.Model(&model.StaffMember{}).
		Where("user_id = ? AND agency_id = ? AND group_id = ?", f.staff.ID, f.agency.ID, f.groupA.ID).
		Update("role", model.StaffRoleTeamLeader).Error; err != nil {
		t.Fatalf("promote staff to team leader: %v", err)
	}

	_, err = f.service.Review(ctx, f.staff.ID, request.ID, true, "")
	assertGoodsApprovalErrorCode(t, err, "reviewer_cannot_review_self")
}

func TestApprovedScheduledGoodsUseSaleTimeForPurchaseLimit(t *testing.T) {
	f := newGoodsApprovalFixture(t)
	startsAt := time.Now().Add(4 * time.Hour).UTC().Truncate(time.Second)
	input := validStaffGoodsInput(f.groupA.ID, "scheduled_approval_01")
	input.SaleStartsAt = &startsAt
	request, err := f.service.Submit(context.Background(), f.staff.ID, input)
	if err != nil {
		t.Fatalf("submit scheduled request: %v", err)
	}
	if request.SaleStartsAt == nil || !request.SaleStartsAt.Equal(startsAt) {
		t.Fatalf("request lost sale start: %+v", request)
	}
	approved, err := f.service.Review(context.Background(), f.admin.ID, request.ID, true, "同意定时上架")
	if err != nil {
		t.Fatalf("approve scheduled request: %v", err)
	}
	var goods model.Goods
	if err := f.db.First(&goods, approved.GoodsID).Error; err != nil {
		t.Fatalf("load scheduled goods: %v", err)
	}
	if goods.SaleStartsAt == nil || !goods.SaleStartsAt.Equal(startsAt) {
		t.Fatalf("approved goods lost sale start: %+v", goods)
	}
	if goods.PurchaseLimitStartedAt == nil || !goods.PurchaseLimitStartedAt.Equal(startsAt) {
		t.Fatalf("purchase limit did not start with scheduled sale: %+v", goods)
	}
}

func assertGoodsApprovalErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected error %s, got %v", code, err)
	}
}
