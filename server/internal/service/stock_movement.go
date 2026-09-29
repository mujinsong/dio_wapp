package service

import (
	"fmt"

	"dio_wapp/server/internal/model"

	"gorm.io/gorm"
)

func recordStockMovement(tx *gorm.DB, goods model.Goods, before int64, after int64, movementType string, operatorID uint64, referenceID uint64, remark string) error {
	if before == after {
		return nil
	}
	if before < 0 || before > MaxGoodsStock || after < 0 || after > MaxGoodsStock {
		return fmt.Errorf("stock movement is outside allowed range: %d -> %d", before, after)
	}
	movement := model.StockMovement{
		AgencyID:    goods.AgencyID,
		GroupID:     goods.GroupID,
		GoodsID:     goods.ID,
		BeforeStock: before,
		DeltaStock:  after - before,
		AfterStock:  after,
		Type:        movementType,
		OperatorID:  operatorID,
		ReferenceID: referenceID,
		Remark:      remark,
	}
	return tx.Create(&movement).Error
}
