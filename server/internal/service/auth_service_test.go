package service

import (
	"context"
	"testing"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/wechat"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoginCreatesUser(t *testing.T) {
	svc, db := newAuthServiceForTest(t, nil)

	result, err := svc.Login(context.Background(), "mock:openid_001")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.Token == "" {
		t.Fatal("expected token")
	}
	if result.User.OpenID != "openid_001" {
		t.Fatalf("unexpected openid: %s", result.User.OpenID)
	}
	if !hasRole(result.User.Roles, "user") {
		t.Fatalf("expected user role, got %v", result.User.Roles)
	}

	var count int64
	if err := db.Model(&model.User{}).Where("open_id = ?", "openid_001").Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one user, got %d", count)
	}
}

func TestLoginAutoCreatesAgencyAdminFromConfiguredOpenID(t *testing.T) {
	svc, _ := newAuthServiceForTest(t, map[string]struct{}{"admin_openid": {}})

	result, err := svc.Login(context.Background(), "mock:admin_openid")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !hasRole(result.User.Roles, "admin") {
		t.Fatalf("expected admin role, got %v", result.User.Roles)
	}
	if len(result.User.AdminAgencies) != 1 {
		t.Fatalf("expected one admin agency, got %v", result.User.AdminAgencies)
	}
	if result.User.AdminAgencies[0].AgencyName != "测试事务所" {
		t.Fatalf("unexpected agency name: %s", result.User.AdminAgencies[0].AgencyName)
	}
}

func TestLoginAutoCreatesStaffFromConfiguredOpenID(t *testing.T) {
	svc, _ := newAuthServiceForTestWithStaff(t, map[string]struct{}{"staff_config_openid": {}})

	result, err := svc.Login(context.Background(), "mock:staff_config_openid")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !hasRole(result.User.Roles, "staff") {
		t.Fatalf("expected staff role, got %v", result.User.Roles)
	}
	if len(result.User.StaffGroups) != 1 {
		t.Fatalf("expected one staff group, got %v", result.User.StaffGroups)
	}
	if result.User.StaffGroups[0].AgencyName != "测试事务所" || result.User.StaffGroups[0].GroupName != defaultShopGroupName {
		t.Fatalf("unexpected staff scope: %+v", result.User.StaffGroups[0])
	}
	if result.User.StaffGroups[0].MemberRole != model.StaffRoleStaff {
		t.Fatalf("unexpected staff member role: %+v", result.User.StaffGroups[0])
	}
}

func TestLoginAutoCreatesTeamLeaderWithStaffPermissions(t *testing.T) {
	svc, _ := newAuthServiceForTestWithMembers(t, nil, map[string]struct{}{"leader_openid": {}})

	result, err := svc.Login(context.Background(), "mock:leader_openid")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !hasRole(result.User.Roles, "staff") || !hasRole(result.User.Roles, "team_leader") {
		t.Fatalf("expected staff and team_leader roles, got %v", result.User.Roles)
	}
	if len(result.User.StaffGroups) != 1 || result.User.StaffGroups[0].MemberRole != model.StaffRoleTeamLeader {
		t.Fatalf("unexpected team leader scope: %+v", result.User.StaffGroups)
	}
	ok, err := svc.HasTeamLeaderGroup(
		context.Background(),
		result.User.ID,
		result.User.StaffGroups[0].AgencyID,
		result.User.StaffGroups[0].GroupID,
	)
	if err != nil {
		t.Fatalf("check team leader group: %v", err)
	}
	if !ok {
		t.Fatal("expected team leader permission")
	}
}

func TestStaffMembershipIsScopedToGroup(t *testing.T) {
	svc, db := newAuthServiceForTest(t, nil)

	result, err := svc.Login(context.Background(), "mock:staff_openid")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	agency, groupA, groupB := createAgencyAndGroups(t, db)

	if err := db.Create(&model.StaffMember{
		AgencyID: agency.ID,
		GroupID:  groupA.ID,
		UserID:   result.User.ID,
		Status:   model.MemberStatusNormal,
	}).Error; err != nil {
		t.Fatalf("create staff: %v", err)
	}

	user, err := svc.Me(context.Background(), result.User.ID)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if !hasRole(user.Roles, "staff") {
		t.Fatalf("expected staff role, got %v", user.Roles)
	}
	if len(user.StaffGroups) != 1 {
		t.Fatalf("expected one staff group, got %v", user.StaffGroups)
	}
	if user.StaffGroups[0].GroupID != groupA.ID {
		t.Fatalf("expected group A membership, got %v", user.StaffGroups[0])
	}

	ok, err := svc.HasStaffGroup(context.Background(), result.User.ID, agency.ID, groupA.ID)
	if err != nil {
		t.Fatalf("has staff group A: %v", err)
	}
	if !ok {
		t.Fatal("expected staff permission in group A")
	}

	ok, err = svc.HasStaffGroup(context.Background(), result.User.ID, agency.ID, groupB.ID)
	if err != nil {
		t.Fatalf("has staff group B: %v", err)
	}
	if ok {
		t.Fatal("did not expect staff permission in group B")
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	svc, db := newAuthServiceForTest(t, nil)
	now := time.Now()
	if err := db.Create(&model.User{
		OpenID:      "disabled_openid",
		Status:      model.UserStatusDisabled,
		LastLoginAt: &now,
	}).Error; err != nil {
		t.Fatalf("create disabled user: %v", err)
	}

	if _, err := svc.Login(context.Background(), "mock:disabled_openid"); err == nil {
		t.Fatal("expected disabled user login to fail")
	}
}

func TestConfiguredStaffLoginDoesNotRestoreRevokedMembership(t *testing.T) {
	staffOpenIDs := map[string]struct{}{"revoked_staff": {}}
	svc, db := newAuthServiceForTestWithStaff(t, staffOpenIDs)
	first, err := svc.Login(context.Background(), "mock:revoked_staff")
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	if !hasRole(first.User.Roles, "staff") {
		t.Fatalf("expected initial staff role, got %v", first.User.Roles)
	}
	if err := db.Model(&model.StaffMember{}).
		Where("user_id = ?", first.User.ID).
		Update("status", model.MemberStatusDisabled).Error; err != nil {
		t.Fatalf("disable configured staff: %v", err)
	}

	second, err := svc.Login(context.Background(), "mock:revoked_staff")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if hasRole(second.User.Roles, "staff") {
		t.Fatalf("revoked membership was restored: %v", second.User.Roles)
	}
}

func TestTeamLeaderConfigurationWinsWhenOpenIDIsInBothSets(t *testing.T) {
	openIDs := map[string]struct{}{"configured_leader": {}}
	svc, _ := newAuthServiceForTestWithMembers(t, openIDs, openIDs)
	result, err := svc.Login(context.Background(), "mock:configured_leader")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !hasRole(result.User.Roles, "team_leader") || result.User.StaffGroups[0].MemberRole != model.StaffRoleTeamLeader {
		t.Fatalf("team leader configuration did not win: %+v", result.User)
	}
}

func newAuthServiceForTest(t *testing.T, adminOpenIDs map[string]struct{}) (*AuthService, *gorm.DB) {
	return newAuthServiceForTestWithStaffAndAdmin(t, adminOpenIDs, nil)
}

func newAuthServiceForTestWithStaff(t *testing.T, staffOpenIDs map[string]struct{}) (*AuthService, *gorm.DB) {
	return newAuthServiceForTestWithStaffAndAdmin(t, nil, staffOpenIDs)
}

func newAuthServiceForTestWithStaffAndAdmin(t *testing.T, adminOpenIDs map[string]struct{}, staffOpenIDs map[string]struct{}) (*AuthService, *gorm.DB) {
	return newAuthServiceForTestWithMembersAndAdmin(t, adminOpenIDs, staffOpenIDs, nil)
}

func newAuthServiceForTestWithMembers(t *testing.T, staffOpenIDs map[string]struct{}, teamLeaderOpenIDs map[string]struct{}) (*AuthService, *gorm.DB) {
	return newAuthServiceForTestWithMembersAndAdmin(t, nil, staffOpenIDs, teamLeaderOpenIDs)
}

func newAuthServiceForTestWithMembersAndAdmin(t *testing.T, adminOpenIDs map[string]struct{}, staffOpenIDs map[string]struct{}, teamLeaderOpenIDs map[string]struct{}) (*AuthService, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.AgencyPointAccount{},
		&model.Agency{},
		&model.IdolGroup{},
		&model.StaffMember{},
		&model.AdminMember{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if adminOpenIDs == nil {
		adminOpenIDs = map[string]struct{}{}
	}
	if staffOpenIDs == nil {
		staffOpenIDs = map[string]struct{}{}
	}
	if teamLeaderOpenIDs == nil {
		teamLeaderOpenIDs = map[string]struct{}{}
	}

	jwtManager := NewJWTManager("test-secret", time.Hour)
	wechatClient := wechat.NewClient("local", "", "", true)
	return NewAuthService(db, wechatClient, jwtManager, adminOpenIDs, "测试事务所", staffOpenIDs, teamLeaderOpenIDs), db
}

func createAgencyAndGroups(t *testing.T, db *gorm.DB) (model.Agency, model.IdolGroup, model.IdolGroup) {
	t.Helper()

	agency := model.Agency{Name: "事务所 A", Status: model.MemberStatusNormal}
	if err := db.Create(&agency).Error; err != nil {
		t.Fatalf("create agency: %v", err)
	}

	groupA := model.IdolGroup{AgencyID: agency.ID, Name: "团体 A", Status: model.MemberStatusNormal}
	if err := db.Create(&groupA).Error; err != nil {
		t.Fatalf("create group A: %v", err)
	}

	groupB := model.IdolGroup{AgencyID: agency.ID, Name: "团体 B", Status: model.MemberStatusNormal}
	if err := db.Create(&groupB).Error; err != nil {
		t.Fatalf("create group B: %v", err)
	}

	return agency, groupA, groupB
}

func hasRole(roles []string, role string) bool {
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}
