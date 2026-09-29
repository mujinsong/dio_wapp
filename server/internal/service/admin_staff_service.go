package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxStaffDisplayNameRunes = 128
	maxStaffRemarkRunes      = 512
)

type AdminStaffService struct {
	db          *gorm.DB
	authService *AuthService
}

type AdminStaffSaveInput struct {
	AgencyID    uint64 `json:"agency_id"`
	GroupID     uint64 `json:"group_id"`
	UserID      uint64 `json:"user_id"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	Remark      string `json:"remark"`
}

type AdminStaffUpdateInput struct {
	Role              string     `json:"role"`
	Status            string     `json:"status"`
	DisplayName       string     `json:"display_name"`
	Remark            string     `json:"remark"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

type AdminStaffDTO struct {
	ID          uint64    `json:"id"`
	AgencyID    uint64    `json:"agency_id"`
	GroupID     uint64    `json:"group_id"`
	GroupName   string    `json:"group_name"`
	UserID      uint64    `json:"user_id"`
	Nickname    string    `json:"nickname"`
	OpenIDHint  string    `json:"openid_hint"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	DisplayName string    `json:"display_name"`
	Remark      string    `json:"remark"`
	CreatedBy   uint64    `json:"created_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func NewAdminStaffService(db *gorm.DB, authService *AuthService) *AdminStaffService {
	return &AdminStaffService{db: db, authService: authService}
}

func (s *AdminStaffService) List(ctx context.Context, operatorID uint64, agencyID uint64, groupID uint64) ([]AdminStaffDTO, error) {
	if err := s.requireAdmin(ctx, operatorID, agencyID); err != nil {
		return nil, err
	}

	var rows []struct {
		model.StaffMember
		GroupName string `gorm:"column:group_name"`
		Nickname  string `gorm:"column:nickname"`
		OpenID    string `gorm:"column:open_id"`
	}
	query := s.db.WithContext(ctx).
		Table("staff_members").
		Select("staff_members.*, idol_groups.name AS group_name, users.nickname, users.open_id").
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Joins("JOIN users ON users.id = staff_members.user_id").
		Where("staff_members.agency_id = ?", agencyID)
	if groupID > 0 {
		query = query.Where("staff_members.group_id = ?", groupID)
	}
	if err := query.Order("staff_members.status ASC, staff_members.role DESC, staff_members.id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]AdminStaffDTO, 0, len(rows))
	for _, row := range rows {
		result = append(result, staffMemberDTO(row.StaffMember, row.GroupName, row.Nickname, row.OpenID))
	}
	return result, nil
}

func (s *AdminStaffService) Save(ctx context.Context, operatorID uint64, input AdminStaffSaveInput) (AdminStaffDTO, error) {
	input.Role = strings.TrimSpace(input.Role)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Remark = strings.TrimSpace(input.Remark)
	if input.AgencyID == 0 || input.GroupID == 0 || input.UserID == 0 {
		return AdminStaffDTO{}, xerr.New(400, "invalid_staff_member", "agency_id, group_id, and user_id are required")
	}
	if err := validateStaffFields(input.Role, model.MemberStatusNormal, input.DisplayName, input.Remark); err != nil {
		return AdminStaffDTO{}, err
	}
	if err := s.requireAdmin(ctx, operatorID, input.AgencyID); err != nil {
		return AdminStaffDTO{}, err
	}
	if err := s.requireActiveGroupAndUser(ctx, input.AgencyID, input.GroupID, input.UserID); err != nil {
		return AdminStaffDTO{}, err
	}

	var member model.StaffMember
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, input.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := lockStaffManagementScopeTx(tx, input.AgencyID, input.GroupID, input.UserID, true); err != nil {
			return err
		}
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agency_id = ? AND group_id = ? AND user_id = ?", input.AgencyID, input.GroupID, input.UserID).
			First(&member).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			member = model.StaffMember{
				AgencyID:    input.AgencyID,
				GroupID:     input.GroupID,
				UserID:      input.UserID,
				Role:        input.Role,
				Status:      model.MemberStatusNormal,
				DisplayName: input.DisplayName,
				Remark:      input.Remark,
				CreatedBy:   operatorID,
			}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
			return createStaffAudit(tx, operatorID, member, "create", "", "", member.Role, member.Status, input.Remark)
		}
		if err != nil {
			return err
		}

		beforeRole, beforeStatus := member.Role, member.Status
		if err := s.protectLastTeamLeader(tx, member, input.Role, model.MemberStatusNormal); err != nil {
			return err
		}
		member.Role = input.Role
		member.Status = model.MemberStatusNormal
		member.DisplayName = input.DisplayName
		member.Remark = input.Remark
		if err := tx.Model(&member).Updates(map[string]any{
			"role":         member.Role,
			"status":       member.Status,
			"display_name": member.DisplayName,
			"remark":       member.Remark,
		}).Error; err != nil {
			return err
		}
		action := "update"
		if beforeStatus == model.MemberStatusDisabled {
			action = "enable"
		}
		return createStaffAudit(tx, operatorID, member, action, beforeRole, beforeStatus, member.Role, member.Status, input.Remark)
	})
	if err != nil {
		return AdminStaffDTO{}, err
	}
	return s.get(ctx, operatorID, member.ID)
}

func (s *AdminStaffService) Update(ctx context.Context, operatorID uint64, memberID uint64, input AdminStaffUpdateInput) (AdminStaffDTO, error) {
	if memberID == 0 {
		return AdminStaffDTO{}, xerr.New(400, "invalid_staff_member", "staff member id is required")
	}
	if input.ExpectedUpdatedAt == nil {
		return AdminStaffDTO{}, xerr.New(400, "missing_staff_version", "expected_updated_at is required")
	}
	input.Role = strings.TrimSpace(input.Role)
	input.Status = strings.TrimSpace(input.Status)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Remark = strings.TrimSpace(input.Remark)
	if err := validateStaffFields(input.Role, input.Status, input.DisplayName, input.Remark); err != nil {
		return AdminStaffDTO{}, err
	}

	var initial model.StaffMember
	if err := s.db.WithContext(ctx).First(&initial, memberID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminStaffDTO{}, xerr.New(404, "staff_member_not_found", "staff member not found")
		}
		return AdminStaffDTO{}, err
	}

	var member model.StaffMember
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, initial.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := lockStaffManagementScopeTx(tx, initial.AgencyID, initial.GroupID, initial.UserID, input.Status == model.MemberStatusNormal); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&member, memberID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "staff_member_not_found", "staff member not found")
			}
			return err
		}
		if member.AgencyID != initial.AgencyID || member.GroupID != initial.GroupID || member.UserID != initial.UserID {
			return xerr.New(409, "staff_member_changed", "staff member changed while being edited")
		}
		if !member.UpdatedAt.Equal(*input.ExpectedUpdatedAt) {
			return xerr.New(409, "staff_member_changed", "staff member changed while being edited")
		}
		if err := s.protectLastTeamLeader(tx, member, input.Role, input.Status); err != nil {
			return err
		}

		beforeRole, beforeStatus := member.Role, member.Status
		member.Role = input.Role
		member.Status = input.Status
		member.DisplayName = input.DisplayName
		member.Remark = input.Remark
		if err := tx.Model(&member).Updates(map[string]any{
			"role":         member.Role,
			"status":       member.Status,
			"display_name": member.DisplayName,
			"remark":       member.Remark,
		}).Error; err != nil {
			return err
		}
		action := "update"
		if beforeStatus != member.Status {
			if member.Status == model.MemberStatusNormal {
				action = "enable"
			} else {
				action = "disable"
			}
		}
		return createStaffAudit(tx, operatorID, member, action, beforeRole, beforeStatus, member.Role, member.Status, input.Remark)
	})
	if err != nil {
		return AdminStaffDTO{}, err
	}
	return s.get(ctx, operatorID, member.ID)
}

func (s *AdminStaffService) Disable(ctx context.Context, operatorID uint64, memberID uint64) error {
	if memberID == 0 {
		return xerr.New(400, "invalid_staff_member", "staff member id is required")
	}
	var initial model.StaffMember
	if err := s.db.WithContext(ctx).First(&initial, memberID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(404, "staff_member_not_found", "staff member not found")
		}
		return err
	}
	var member model.StaffMember
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, initial.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := lockStaffManagementScopeTx(tx, initial.AgencyID, initial.GroupID, initial.UserID, false); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&member, memberID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "staff_member_not_found", "staff member not found")
			}
			return err
		}
		if member.AgencyID != initial.AgencyID || member.GroupID != initial.GroupID || member.UserID != initial.UserID {
			return xerr.New(409, "staff_member_changed", "staff member changed while being edited")
		}
		if member.Status == model.MemberStatusDisabled {
			return nil
		}
		if err := s.protectLastTeamLeader(tx, member, member.Role, model.MemberStatusDisabled); err != nil {
			return err
		}
		beforeStatus := member.Status
		member.Status = model.MemberStatusDisabled
		if err := tx.Model(&member).Update("status", member.Status).Error; err != nil {
			return err
		}
		return createStaffAudit(tx, operatorID, member, "disable", member.Role, beforeStatus, member.Role, member.Status, member.Remark)
	})
}

func (s *AdminStaffService) get(ctx context.Context, operatorID uint64, memberID uint64) (AdminStaffDTO, error) {
	var row struct {
		model.StaffMember
		GroupName string `gorm:"column:group_name"`
		Nickname  string `gorm:"column:nickname"`
		OpenID    string `gorm:"column:open_id"`
	}
	if err := s.db.WithContext(ctx).
		Table("staff_members").
		Select("staff_members.*, idol_groups.name AS group_name, users.nickname, users.open_id").
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Joins("JOIN users ON users.id = staff_members.user_id").
		Where("staff_members.id = ?", memberID).
		Scan(&row).Error; err != nil {
		return AdminStaffDTO{}, err
	}
	if row.ID == 0 {
		return AdminStaffDTO{}, xerr.New(404, "staff_member_not_found", "staff member not found")
	}
	if err := s.requireAdmin(ctx, operatorID, row.AgencyID); err != nil {
		return AdminStaffDTO{}, err
	}
	return staffMemberDTO(row.StaffMember, row.GroupName, row.Nickname, row.OpenID), nil
}

func (s *AdminStaffService) requireAdmin(ctx context.Context, operatorID uint64, agencyID uint64) error {
	if agencyID == 0 {
		return xerr.New(400, "invalid_agency", "agency_id is required")
	}
	ok, err := s.authService.HasAdminAgency(ctx, operatorID, agencyID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.New(403, "admin_agency_forbidden", "admin has no permission for this agency")
	}
	return nil
}

func (s *AdminStaffService) requireActiveGroupAndUser(ctx context.Context, agencyID uint64, groupID uint64, userID uint64) error {
	var groupCount int64
	if err := s.db.WithContext(ctx).Model(&model.IdolGroup{}).
		Where("id = ? AND agency_id = ? AND status = ?", groupID, agencyID, model.MemberStatusNormal).
		Count(&groupCount).Error; err != nil {
		return err
	}
	if groupCount == 0 {
		return xerr.New(404, "group_not_found", "active group not found")
	}
	var userCount int64
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ? AND status = ?", userID, model.UserStatusNormal).
		Count(&userCount).Error; err != nil {
		return err
	}
	if userCount == 0 {
		return xerr.New(404, "user_not_found", "active user not found")
	}
	return nil
}

func (s *AdminStaffService) protectLastTeamLeader(tx *gorm.DB, member model.StaffMember, nextRole string, nextStatus string) error {
	wasActiveLeader := member.Role == model.StaffRoleTeamLeader && member.Status == model.MemberStatusNormal
	willRemainActiveLeader := nextRole == model.StaffRoleTeamLeader && nextStatus == model.MemberStatusNormal
	if !wasActiveLeader || willRemainActiveLeader {
		return nil
	}
	var group model.IdolGroup
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").
		First(&group, member.GroupID).Error; err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.StaffMember{}).
		Where("agency_id = ? AND group_id = ? AND role = ? AND status = ? AND id <> ?",
			member.AgencyID, member.GroupID, model.StaffRoleTeamLeader, model.MemberStatusNormal, member.ID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return xerr.New(409, "last_team_leader", "assign another active team leader before changing this member")
	}
	return nil
}

func lockStaffManagementScopeTx(tx *gorm.DB, agencyID uint64, groupID uint64, userID uint64, requireActive bool) error {
	var group model.IdolGroup
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "agency_id", "status").First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(404, "group_not_found", "group not found")
		}
		return err
	}
	if group.AgencyID != agencyID || (requireActive && group.Status != model.MemberStatusNormal) {
		return xerr.New(404, "group_not_found", "active group not found")
	}

	var user model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(404, "user_not_found", "user not found")
		}
		return err
	}
	if requireActive && user.Status != model.UserStatusNormal {
		return xerr.New(404, "user_not_found", "active user not found")
	}
	return nil
}

func validateStaffFields(role string, status string, displayName string, remark string) error {
	if role != model.StaffRoleStaff && role != model.StaffRoleTeamLeader {
		return xerr.New(400, "invalid_staff_role", "role must be staff or team_leader")
	}
	if status != model.MemberStatusNormal && status != model.MemberStatusDisabled {
		return xerr.New(400, "invalid_member_status", "status must be normal or disabled")
	}
	if utf8.RuneCountInString(displayName) > maxStaffDisplayNameRunes {
		return xerr.New(400, "invalid_display_name", "display name must not exceed 128 characters")
	}
	if utf8.RuneCountInString(remark) > maxStaffRemarkRunes {
		return xerr.New(400, "invalid_remark", "remark must not exceed 512 characters")
	}
	return nil
}

func createStaffAudit(tx *gorm.DB, operatorID uint64, member model.StaffMember, action string, beforeRole string, beforeStatus string, afterRole string, afterStatus string, remark string) error {
	return tx.Create(&model.StaffMemberAuditLog{
		AgencyID:      member.AgencyID,
		GroupID:       member.GroupID,
		StaffMemberID: member.ID,
		TargetUserID:  member.UserID,
		OperatorID:    operatorID,
		Action:        action,
		BeforeRole:    beforeRole,
		AfterRole:     afterRole,
		BeforeStatus:  beforeStatus,
		AfterStatus:   afterStatus,
		Remark:        remark,
	}).Error
}

func staffMemberDTO(member model.StaffMember, groupName string, nickname string, openID string) AdminStaffDTO {
	return AdminStaffDTO{
		ID:          member.ID,
		AgencyID:    member.AgencyID,
		GroupID:     member.GroupID,
		GroupName:   groupName,
		UserID:      member.UserID,
		Nickname:    nickname,
		OpenIDHint:  maskOpenID(openID),
		Role:        normalizedStaffRole(member.Role),
		Status:      member.Status,
		DisplayName: member.DisplayName,
		Remark:      member.Remark,
		CreatedBy:   member.CreatedBy,
		UpdatedAt:   member.UpdatedAt,
	}
}

func maskOpenID(openID string) string {
	openID = strings.TrimSpace(openID)
	if len(openID) <= 8 {
		return openID
	}
	return fmt.Sprintf("%s...%s", openID[:4], openID[len(openID)-4:])
}
