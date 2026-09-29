package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *ShopService) BackfillOrderExpirations(ctx context.Context) error {
	var orders []model.ExchangeOrder
	if err := s.db.WithContext(ctx).
		Where("status = ? AND expires_at IS NULL", model.ExchangeOrderStatusPending).
		Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		expiresAt := order.CreatedAt.Add(s.orderRedeemTTL)
		if err := s.db.WithContext(ctx).Model(&order).Update("expires_at", &expiresAt).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *ShopService) ExpirePendingOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	limit = normalizeExpiryLimit(limit)
	var ids []uint64
	if err := s.db.WithContext(ctx).Model(&model.ExchangeOrder{}).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", model.ExchangeOrderStatusPending, now).
		Order("expires_at ASC, id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	return s.expirePendingOrderIDs(ctx, ids, now)
}

func (s *ShopService) expirePendingOrdersForUser(ctx context.Context, userID uint64, now time.Time, limit int) (int, error) {
	limit = normalizeExpiryLimit(limit)
	var ids []uint64
	if err := s.db.WithContext(ctx).Model(&model.ExchangeOrder{}).
		Where("user_id = ? AND status = ? AND expires_at IS NOT NULL AND expires_at <= ?", userID, model.ExchangeOrderStatusPending, now).
		Order("expires_at ASC, id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	return s.expirePendingOrderIDs(ctx, ids, now)
}

func (s *ShopService) expirePendingOrderIDs(ctx context.Context, ids []uint64, now time.Time) (int, error) {
	expired := 0
	var failures []error
	for _, id := range ids {
		changed, err := s.expirePendingOrder(ctx, id, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("expire order %d: %w", id, err))
			if ctx.Err() != nil {
				return expired, errors.Join(failures...)
			}
			continue
		}
		if changed {
			expired++
		}
	}
	return expired, errors.Join(failures...)
}

func normalizeExpiryLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func (s *ShopService) expirePendingOrder(ctx context.Context, orderID uint64, now time.Time) (bool, error) {
	changed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order model.ExchangeOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if order.Status != model.ExchangeOrderStatusPending || order.ExpiresAt == nil || order.ExpiresAt.After(now) {
			return nil
		}
		if order.PointsCost <= 0 {
			return xerr.New(409, "invalid_order_points", "order points cost is invalid")
		}

		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, order.UserID).Error; err != nil {
			return err
		}
		account, err := lockAgencyPointAccount(tx, order.AgencyID, order.UserID)
		if err != nil {
			return err
		}
		if order.PointsCost > MaxPointBalance || account.Balance > MaxPointBalance-order.PointsCost {
			return xerr.New(409, "points_balance_limit", "points balance cannot accept this refund")
		}

		var goods model.Goods
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&goods, order.GoodsID).Error; err != nil {
			return err
		}
		if goods.AgencyID != order.AgencyID || goods.GroupID != order.GroupID {
			return xerr.New(409, "order_goods_mismatch", "order goods ownership is inconsistent")
		}
		if goods.Stock >= MaxGoodsStock {
			return xerr.New(409, "goods_stock_limit", "goods stock cannot be restored")
		}

		before := account.Balance
		after := before + order.PointsCost
		if err := tx.Model(&account).Update("balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
			return err
		}
		beforeStock := goods.Stock
		afterStock := beforeStock + 1
		if err := tx.Model(&goods).Update("stock", afterStock).Error; err != nil {
			return err
		}
		if err := recordStockMovement(tx, goods, beforeStock, afterStock, model.StockMovementOrderExpire, 0, order.ID, "兑换超时自动返还库存"); err != nil {
			return err
		}

		ledger := model.PointLedger{
			AgencyID:     order.AgencyID,
			GroupID:      order.GroupID,
			UserID:       order.UserID,
			BeforePoints: before,
			DeltaPoints:  order.PointsCost,
			AfterPoints:  after,
			Type:         PointLedgerOrderRefund,
			OperatorID:   order.UserID,
			ReferenceID:  order.ID,
			Remark:       "兑换超时自动退款：" + order.GoodsName,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Updates(map[string]any{
			"status":           model.ExchangeOrderStatusExpired,
			"expired_at":       &now,
			"refund_ledger_id": ledger.ID,
		}).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

func orderPastRedeemDeadline(order model.ExchangeOrder, now time.Time) bool {
	return order.Status == model.ExchangeOrderStatusPending && order.ExpiresAt != nil && !order.ExpiresAt.After(now)
}
