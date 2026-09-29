package service

import (
	"errors"

	"dio_wapp/server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func activeAdminAgencyTx(tx *gorm.DB, userID uint64, agencyID uint64) (bool, error) {
	var user model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&user, userID).Error; err != nil {
		return false, err
	}
	if user.Status != model.UserStatusNormal {
		return false, nil
	}
	var agency model.Agency
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&agency, agencyID).Error; err != nil {
		return false, err
	}
	if agency.Status != model.MemberStatusNormal {
		return false, nil
	}
	var admin model.AdminMember
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND agency_id = ?", userID, agencyID).First(&admin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return admin.Status == model.MemberStatusNormal, nil
}

func activeReviewerRoleTx(tx *gorm.DB, userID uint64, agencyID uint64, groupID uint64) (bool, bool, error) {
	var user model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&user, userID).Error; err != nil {
		return false, false, err
	}
	if user.Status != model.UserStatusNormal {
		return false, false, nil
	}

	var agency model.Agency
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&agency, agencyID).Error; err != nil {
		return false, false, err
	}
	if agency.Status != model.MemberStatusNormal {
		return false, false, nil
	}
	var group model.IdolGroup
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "agency_id", "status").First(&group, groupID).Error; err != nil {
		return false, false, err
	}
	if group.AgencyID != agencyID || group.Status != model.MemberStatusNormal {
		return false, false, nil
	}

	var admin model.AdminMember
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND agency_id = ?", userID, agencyID).First(&admin).Error
	if err == nil && admin.Status == model.MemberStatusNormal {
		return true, false, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, err
	}

	var member model.StaffMember
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND agency_id = ? AND group_id = ?", userID, agencyID, groupID).First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return false, member.Status == model.MemberStatusNormal && member.Role == model.StaffRoleTeamLeader, nil
}
