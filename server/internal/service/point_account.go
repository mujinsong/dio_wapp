package service

import (
	"context"
	"errors"

	"dio_wapp/server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgencyPointDTO struct {
	AgencyID   uint64 `json:"agency_id"`
	AgencyName string `json:"agency_name"`
	Balance    int64  `json:"balance"`
}

func lockAgencyPointAccount(tx *gorm.DB, agencyID uint64, userID uint64) (model.AgencyPointAccount, error) {
	seed := model.AgencyPointAccount{AgencyID: agencyID, UserID: userID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return model.AgencyPointAccount{}, err
	}

	var account model.AgencyPointAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agency_id = ? AND user_id = ?", agencyID, userID).
		First(&account).Error; err != nil {
		return model.AgencyPointAccount{}, err
	}
	return account, nil
}

func listAgencyPointAccounts(ctx context.Context, db *gorm.DB, userID uint64) ([]AgencyPointDTO, error) {
	var accounts []AgencyPointDTO
	err := db.WithContext(ctx).
		Table("agency_point_accounts").
		Select("agency_point_accounts.agency_id, agencies.name AS agency_name, agency_point_accounts.balance").
		Joins("JOIN agencies ON agencies.id = agency_point_accounts.agency_id").
		Where("agency_point_accounts.user_id = ?", userID).
		Order("agency_point_accounts.agency_id ASC").
		Scan(&accounts).Error
	return accounts, err
}

// BackfillAgencyPointAccounts creates missing agency accounts from immutable point
// ledgers. Legacy balances without ledgers are assigned only when there is one agency.
func BackfillAgencyPointAccounts(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ledgerBalances []struct {
			AgencyID uint64
			UserID   uint64
			Balance  int64
		}
		if err := tx.Model(&model.PointLedger{}).
			Select("agency_id, user_id, SUM(delta_points) AS balance").
			Where("agency_id > 0").
			Group("agency_id, user_id").
			Scan(&ledgerBalances).Error; err != nil {
			return err
		}
		for _, row := range ledgerBalances {
			account := model.AgencyPointAccount{AgencyID: row.AgencyID, UserID: row.UserID, Balance: row.Balance}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
				return err
			}
		}

		var agencyIDs []uint64
		if err := tx.Model(&model.Agency{}).Pluck("id", &agencyIDs).Error; err != nil {
			return err
		}
		if len(agencyIDs) == 1 {
			var users []model.User
			if err := tx.Where("points_balance <> 0").Find(&users).Error; err != nil {
				return err
			}
			for _, user := range users {
				var existing model.AgencyPointAccount
				err := tx.Where("user_id = ?", user.ID).First(&existing).Error
				if err == nil {
					continue
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				account := model.AgencyPointAccount{AgencyID: agencyIDs[0], UserID: user.ID, Balance: user.PointsBalance}
				if err := tx.Create(&account).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&model.User{}).Where("1 = 1").
			Update("points_balance", gorm.Expr("(SELECT COALESCE(SUM(balance), 0) FROM agency_point_accounts WHERE agency_point_accounts.user_id = users.id)")).Error
	})
}
