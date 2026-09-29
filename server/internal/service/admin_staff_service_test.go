package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

func newAdminStaffFixture(t *testing.T) (*AdminStaffService, *gorm.DB, model.Agency, model.IdolGroup, model.IdolGroup, model.User, model.User, model.User) {
	t.Helper()
	db := newCatalogDBForTest(t)
	agency := model.Agency{Name: "人员管理事务所", Status: model.MemberStatusNormal}
	admin := model.User{OpenID: "staff_admin", Status: model.UserStatusNormal}
	userA := model.User{OpenID: "managed_user_a", Nickname: "成员 A", Status: model.UserStatusNormal}
	userB := model.User{OpenID: "managed_user_b", Nickname: "成员 B", Status: model.UserStatusNormal}
	for _, value := range []any{&agency, &admin, &userA, &userB} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create staff fixture: %v", err)
		}
	}
	groupA := model.IdolGroup{AgencyID: agency.ID, Name: "人员团队 A", Status: model.MemberStatusNormal}
	groupB := model.IdolGroup{AgencyID: agency.ID, Name: "人员团队 B", Status: model.MemberStatusNormal}
	if err := db.Create(&groupA).Error; err != nil {
		t.Fatalf("create group A: %v", err)
	}
	if err := db.Create(&groupB).Error; err != nil {
		t.Fatalf("create group B: %v", err)
	}
	if err := db.Create(&model.AdminMember{
		AgencyID: agency.ID, UserID: admin.ID, Status: model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create admin membership: %v", err)
	}
	auth := &AuthService{db: db}
	return NewAdminStaffService(db, auth), db, agency, groupA, groupB, admin, userA, userB
}

func TestAdminCanCreateUpdateDisableAndEnableStaffMembers(t *testing.T) {
	service, db, agency, groupA, _, admin, userA, userB := newAdminStaffFixture(t)
	ctx := context.Background()

	created, err := service.Save(ctx, admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userA.ID,
		Role: model.StaffRoleStaff, DisplayName: "现场成员", Remark: "早班",
	})
	if err != nil {
		t.Fatalf("create staff member: %v", err)
	}
	if created.Role != model.StaffRoleStaff || created.Status != model.MemberStatusNormal || created.OpenIDHint == userA.OpenID {
		t.Fatalf("unexpected created member: %+v", created)
	}

	updated, err := service.Update(ctx, admin.ID, created.ID, AdminStaffUpdateInput{
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
		DisplayName: "团队负责人", Remark: "负责人", ExpectedUpdatedAt: &created.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("promote member: %v", err)
	}
	if updated.Role != model.StaffRoleTeamLeader {
		t.Fatalf("member was not promoted: %+v", updated)
	}
	if _, err := service.Save(ctx, admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userB.ID, Role: model.StaffRoleTeamLeader,
	}); err != nil {
		t.Fatalf("create replacement leader: %v", err)
	}
	members, err := service.List(ctx, admin.ID, agency.ID, groupA.ID)
	if err != nil {
		t.Fatalf("list group members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected two group members, got %+v", members)
	}

	if err := service.Disable(ctx, admin.ID, created.ID); err != nil {
		t.Fatalf("disable member: %v", err)
	}
	ok, err := service.authService.HasStaffGroup(ctx, userA.ID, agency.ID, groupA.ID)
	if err != nil {
		t.Fatalf("check disabled staff permission: %v", err)
	}
	if ok {
		t.Fatal("disabled member retained staff permission")
	}

	var disabled model.StaffMember
	if err := db.First(&disabled, created.ID).Error; err != nil {
		t.Fatalf("load disabled member: %v", err)
	}
	enabled, err := service.Update(ctx, admin.ID, created.ID, AdminStaffUpdateInput{
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
		DisplayName: "团队负责人", Remark: "恢复", ExpectedUpdatedAt: &disabled.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("enable member: %v", err)
	}
	if enabled.Status != model.MemberStatusNormal {
		t.Fatalf("member was not enabled: %+v", enabled)
	}

	var auditCount int64
	if err := db.Model(&model.StaffMemberAuditLog{}).Where("staff_member_id = ?", created.ID).Count(&auditCount).Error; err != nil {
		t.Fatalf("count staff audit logs: %v", err)
	}
	if auditCount != 4 {
		t.Fatalf("expected four audit records, got %d", auditCount)
	}
}

func TestAdminStaffProtectsLastActiveTeamLeader(t *testing.T) {
	service, _, agency, groupA, _, admin, userA, userB := newAdminStaffFixture(t)
	ctx := context.Background()

	first, err := service.Save(ctx, admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userA.ID, Role: model.StaffRoleTeamLeader,
	})
	if err != nil {
		t.Fatalf("create first leader: %v", err)
	}
	err = service.Disable(ctx, admin.ID, first.ID)
	assertAdminStaffErrorCode(t, err, "last_team_leader")

	if _, err := service.Save(ctx, admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userB.ID, Role: model.StaffRoleTeamLeader,
	}); err != nil {
		t.Fatalf("create second leader: %v", err)
	}
	if _, err := service.Update(ctx, admin.ID, first.ID, AdminStaffUpdateInput{
		Role: model.StaffRoleStaff, Status: model.MemberStatusNormal, ExpectedUpdatedAt: &first.UpdatedAt,
	}); err != nil {
		t.Fatalf("demote leader after replacement: %v", err)
	}
}

func TestAdminStaffRejectsStaleUpdate(t *testing.T) {
	service, db, agency, group, _, admin, user, _ := newAdminStaffFixture(t)
	created, err := service.Save(context.Background(), admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: group.ID, UserID: user.ID, Role: model.StaffRoleStaff,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	changedAt := created.UpdatedAt.Add(time.Second)
	if err := db.Model(&model.StaffMember{}).Where("id = ?", created.ID).Updates(map[string]any{
		"display_name": "另一位管理员已修改",
		"updated_at":   changedAt,
	}).Error; err != nil {
		t.Fatalf("simulate concurrent member update: %v", err)
	}
	_, err = service.Update(context.Background(), admin.ID, created.ID, AdminStaffUpdateInput{
		Role: model.StaffRoleTeamLeader, Status: model.MemberStatusNormal,
		DisplayName: "旧页面修改", ExpectedUpdatedAt: &created.UpdatedAt,
	})
	assertAdminStaffErrorCode(t, err, "staff_member_changed")

	var stored model.StaffMember
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("reload member: %v", err)
	}
	if stored.DisplayName != "另一位管理员已修改" || stored.Role != model.StaffRoleStaff {
		t.Fatalf("stale update overwrote member: %+v", stored)
	}
}

func TestNonAdminCannotManageStaffMembers(t *testing.T) {
	service, _, agency, groupA, _, _, userA, userB := newAdminStaffFixture(t)
	_, err := service.Save(context.Background(), userA.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userB.ID, Role: model.StaffRoleStaff,
	})
	assertAdminStaffErrorCode(t, err, "admin_agency_forbidden")
}

func TestAdminStaffRejectsDisabledTargetAndOversizedFields(t *testing.T) {
	service, db, agency, groupA, _, admin, userA, _ := newAdminStaffFixture(t)
	if err := db.Model(&userA).Update("status", model.UserStatusDisabled).Error; err != nil {
		t.Fatalf("disable target user: %v", err)
	}
	_, err := service.Save(context.Background(), admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userA.ID, Role: model.StaffRoleStaff,
	})
	assertAdminStaffErrorCode(t, err, "user_not_found")

	_, err = service.Save(context.Background(), admin.ID, AdminStaffSaveInput{
		AgencyID: agency.ID, GroupID: groupA.ID, UserID: userA.ID, Role: model.StaffRoleStaff,
		DisplayName: strings.Repeat("名", maxStaffDisplayNameRunes+1),
	})
	assertAdminStaffErrorCode(t, err, "invalid_display_name")
}

func assertAdminStaffErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *xerr.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected error %s, got %v", code, err)
	}
}
