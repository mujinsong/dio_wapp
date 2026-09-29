package service

import (
	"context"
	"errors"
	"testing"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
)

func TestPointCorrectionApprovalIsAuditedAndIdempotent(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	grant, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: 60, GrantMode: model.PointGrantModeTicket, TicketUnitPrice: 60, TicketCount: 1,
		PaymentMethod: "cash", Remark: "线下购券", RequestID: "correction_source_01",
	})
	if err != nil {
		t.Fatalf("create source grant: %v", err)
	}
	leader := createCorrectionLeader(t, fixture)

	request, err := fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "购券积分录入错误", "correction_submit_01")
	if err != nil {
		t.Fatalf("submit correction: %v", err)
	}
	retry, err := fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "购券积分录入错误", "correction_submit_01")
	if err != nil || retry.ID != request.ID {
		t.Fatalf("idempotent correction retry failed: request=%+v err=%v", retry, err)
	}
	_, err = fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "不同的冲正原因", "correction_submit_01")
	assertCorrectionErrorCode(t, err, "idempotency_conflict")

	pending, err := fixture.service.ListReviewPointCorrections(context.Background(), leader.ID, fixture.group.ID, model.ApprovalStatusPending, 0, 20)
	if err != nil || len(pending.Items) != 1 || pending.Items[0].ID != request.ID {
		t.Fatalf("list pending corrections: items=%+v err=%v", pending, err)
	}
	approved, err := fixture.service.ReviewPointCorrection(context.Background(), leader.ID, request.ID, true, "已核对原销售单")
	if err != nil {
		t.Fatalf("approve correction: %v", err)
	}
	if approved.RequestStatus != model.ApprovalStatusApproved || approved.CorrectionLedgerID == 0 || approved.ReviewedBy != leader.ID {
		t.Fatalf("unexpected approved correction: %+v", approved)
	}
	assertPointBalance(t, fixture.service, fixture.agency.ID, fixture.target.ID, 0)
	var ledger model.PointLedger
	if err := fixture.service.db.First(&ledger, approved.CorrectionLedgerID).Error; err != nil {
		t.Fatalf("load reversal ledger: %v", err)
	}
	if ledger.Type != PointLedgerGrantReversal || ledger.DeltaPoints != -60 || ledger.ReferenceID != request.ID || ledger.OperatorID != leader.ID {
		t.Fatalf("unexpected reversal ledger: %+v", ledger)
	}
	if _, err := fixture.service.ReviewPointCorrection(context.Background(), leader.ID, request.ID, true, "重复审批"); err != nil {
		t.Fatalf("repeat approval should be idempotent: %v", err)
	}
	var reversalCount int64
	fixture.service.db.Model(&model.PointLedger{}).Where("type = ? AND reference_id = ?", PointLedgerGrantReversal, request.ID).Count(&reversalCount)
	if reversalCount != 1 {
		t.Fatalf("repeat approval created %d reversal ledgers", reversalCount)
	}
	_, err = fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "再次冲正同一销售单", "correction_submit_02")
	assertCorrectionErrorCode(t, err, "correction_already_exists")
}

func TestPointCorrectionReviewPermissionAndInsufficientBalance(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleStaff)
	grant, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: 80, GrantMode: model.PointGrantModeManual, Remark: "人工补录", RequestID: "correction_source_02",
	})
	if err != nil {
		t.Fatalf("create source grant: %v", err)
	}
	leader := createCorrectionLeader(t, fixture)
	request, err := fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "会员积分录入错误", "correction_submit_03")
	if err != nil {
		t.Fatalf("submit correction: %v", err)
	}
	_, err = fixture.service.ReviewPointCorrection(context.Background(), fixture.staff.ID, request.ID, true, "自己审批")
	assertCorrectionErrorCode(t, err, "point_grant_review_forbidden")

	fixture.service.db.Model(&model.AgencyPointAccount{}).
		Where("agency_id = ? AND user_id = ?", fixture.agency.ID, fixture.target.ID).Update("balance", 10)
	fixture.service.db.Model(&model.User{}).Where("id = ?", fixture.target.ID).Update("points_balance", 10)
	_, err = fixture.service.ReviewPointCorrection(context.Background(), leader.ID, request.ID, true, "余额不足时审批")
	assertCorrectionErrorCode(t, err, "correction_insufficient_points")
	assertPointBalance(t, fixture.service, fixture.agency.ID, fixture.target.ID, 10)
	var stored model.PointCorrectionRequest
	fixture.service.db.First(&stored, request.ID)
	if stored.RequestStatus != model.ApprovalStatusPending || stored.CorrectionLedgerID != nil {
		t.Fatalf("failed approval changed correction: %+v", stored)
	}

	_, err = fixture.service.ReviewPointCorrection(context.Background(), leader.ID, request.ID, false, "")
	assertCorrectionErrorCode(t, err, "invalid_review_remark")
	rejected, err := fixture.service.ReviewPointCorrection(context.Background(), leader.ID, request.ID, false, "确认不执行冲正")
	if err != nil || rejected.RequestStatus != model.ApprovalStatusRejected {
		t.Fatalf("reject correction: request=%+v err=%v", rejected, err)
	}
	resubmitted, err := fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "补充凭证后重新申请", "correction_submit_04")
	if err != nil || resubmitted.RequestStatus != model.ApprovalStatusPending || resubmitted.ID == request.ID {
		t.Fatalf("resubmit rejected correction: request=%+v err=%v", resubmitted, err)
	}
}

func TestTeamLeaderCannotApproveOwnCorrection(t *testing.T) {
	fixture := newPointGrantFixture(t, model.StaffRoleTeamLeader)
	grant, err := fixture.service.SubmitStaffGrant(context.Background(), fixture.staff.ID, StaffGrantInput{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, IdentityToken: fixture.token,
		Points: 50, GrantMode: model.PointGrantModeManual, Remark: "负责人录入", RequestID: "correction_source_03",
	})
	if err != nil {
		t.Fatalf("create leader source grant: %v", err)
	}
	request, err := fixture.service.SubmitPointCorrection(context.Background(), fixture.staff.ID, grant.GrantRequestID, "负责人自己的录入错误", "correction_submit_05")
	if err != nil {
		t.Fatalf("submit leader correction: %v", err)
	}
	_, err = fixture.service.ReviewPointCorrection(context.Background(), fixture.staff.ID, request.ID, true, "负责人自审")
	assertCorrectionErrorCode(t, err, "cannot_review_own_request")
}

func createCorrectionLeader(t *testing.T, fixture pointGrantFixture) model.User {
	t.Helper()
	leader := model.User{OpenID: "correction_leader_" + fixture.staff.OpenID, Status: model.UserStatusNormal}
	if err := fixture.service.db.Create(&leader).Error; err != nil {
		t.Fatalf("create correction leader: %v", err)
	}
	if err := fixture.service.db.Create(&model.StaffMember{
		AgencyID: fixture.agency.ID, GroupID: fixture.group.ID, UserID: leader.ID,
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create leader membership: %v", err)
	}
	return leader
}

func assertCorrectionErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != want {
		t.Fatalf("error=%v, want code %s", err, want)
	}
}
