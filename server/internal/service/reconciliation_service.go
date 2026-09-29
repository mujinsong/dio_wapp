package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
	"gorm.io/gorm"
)

type ReconciliationIssue struct {
	Kind     string `json:"kind"`
	ID       uint64 `json:"id"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ReconciliationPage struct {
	Kind       string                `json:"kind"`
	Checked    int                   `json:"checked"`
	NextCursor uint64                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
	CheckedAt  time.Time             `json:"checked_at"`
	Issues     []ReconciliationIssue `json:"issues"`
}

// Each bounded page uses one database snapshot. Different pages are not a global snapshot.
func (s *AdminReportService) Reconcile(ctx context.Context, operatorID, agencyID uint64, kind string, cursor uint64, limit int) (ReconciliationPage, error) {
	result := ReconciliationPage{Kind: kind, CheckedAt: time.Now(), Issues: []ReconciliationIssue{}}
	if agencyID == 0 || (kind != "accounts" && kind != "orders" && kind != "stock") || limit < 1 || limit > 50 {
		return result, xerr.New(400, "invalid_reconciliation", "agency, kind or limit is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	opts := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	if s.db.Dialector.Name() != "mysql" {
		opts = &sql.TxOptions{ReadOnly: true}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := (&AuthService{db: tx}).HasAdminAgency(ctx, operatorID, agencyID)
		if err != nil {
			return err
		}
		if !allowed {
			return xerr.New(403, "admin_forbidden", "admin has no permission for this agency")
		}
		var entity any = &model.User{}
		if kind == "orders" {
			entity = &model.ExchangeOrder{}
		}
		if kind == "stock" {
			entity = &model.Goods{}
		}
		query := tx.Model(entity).Where("id > ?", cursor)
		if kind != "accounts" {
			query = query.Where("agency_id = ?", agencyID)
		}
		var ids []uint64
		if err := query.Order("id ASC").Limit(limit+1).Pluck("id", &ids).Error; err != nil {
			return err
		}
		result.HasMore = len(ids) > limit
		if result.HasMore {
			ids = ids[:limit]
		}
		for _, id := range ids {
			add := func(code, severity, message string) {
				result.Issues = append(result.Issues, ReconciliationIssue{kind, id, code, severity, message})
			}
			switch kind {
			case "accounts":
				err = reconcileAccount(tx, agencyID, id, add)
			case "orders":
				err = reconcileOrder(tx, agencyID, id, add)
			case "stock":
				err = reconcileStock(tx, agencyID, id, add)
			}
			if err != nil {
				return err
			}
			result.Checked++
			result.NextCursor = id
		}
		return nil
	}, opts)
	if err != nil {
		return ReconciliationPage{}, err
	}
	return result, nil
}

type addReconciliationIssue func(string, string, string)

type trailStats struct{ Count, Delta, Invalid int64 }

func reconcileAccount(tx *gorm.DB, agencyID, userID uint64, add addReconciliationIssue) error {
	var user model.User
	if err := tx.First(&user, userID).Error; err != nil {
		return err
	}
	var account model.AgencyPointAccount
	err := tx.Where("agency_id = ? AND user_id = ?", agencyID, userID).First(&account).Error
	missing := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !missing {
		return err
	}
	query := tx.Model(&model.PointLedger{}).Where("agency_id = ? AND user_id = ?", agencyID, userID)
	var stats trailStats
	if err := query.Select("COUNT(*) AS count, COALESCE(SUM(delta_points),0) AS delta, COALESCE(SUM(CASE WHEN before_points + delta_points <> after_points THEN 1 ELSE 0 END),0) AS invalid").Scan(&stats).Error; err != nil {
		return err
	}
	if missing {
		if stats.Count > 0 {
			add("account_missing", "error", "存在积分流水，但事务所积分账户缺失")
		} else if user.PointsBalance != 0 {
			add("baseline_missing", "warning", "历史余额尚无账户及流水，需要人工核对期初值")
		}
		return nil
	}
	if account.Balance != user.PointsBalance {
		add("balance_mirror", "error", "账户余额与用户显示余额不一致")
	}
	if stats.Count == 0 {
		if account.Balance != 0 {
			add("baseline_missing", "warning", "非零积分余额没有历史流水，需要人工核对期初值")
		}
		return nil
	}
	var first, last model.PointLedger
	if err := tx.Where("agency_id = ? AND user_id = ?", agencyID, userID).Order("id ASC").First(&first).Error; err != nil {
		return err
	}
	if err := tx.Where("agency_id = ? AND user_id = ?", agencyID, userID).Order("id DESC").First(&last).Error; err != nil {
		return err
	}
	if stats.Invalid != 0 || first.BeforePoints+stats.Delta != account.Balance || last.AfterPoints != account.Balance {
		add("points_trail", "error", "积分流水计算、累计值或最后余额与账户不一致")
	}
	var gaps int64
	if err := tx.Raw(`SELECT COUNT(*) FROM (
		SELECT before_points, LAG(after_points) OVER (ORDER BY id) AS previous_after
		FROM point_ledgers WHERE agency_id = ? AND user_id = ?
	) AS trail WHERE previous_after IS NOT NULL AND before_points <> previous_after`, agencyID, userID).Scan(&gaps).Error; err != nil {
		return err
	}
	if gaps != 0 {
		add("points_chain", "error", "相邻积分流水的前后余额不连续")
	}
	return nil
}

func reconcileStock(tx *gorm.DB, agencyID, goodsID uint64, add addReconciliationIssue) error {
	var goods model.Goods
	if err := tx.First(&goods, goodsID).Error; err != nil {
		return err
	}
	query := tx.Model(&model.StockMovement{}).Where("agency_id = ? AND goods_id = ?", agencyID, goodsID)
	var stats trailStats
	if err := query.Select("COUNT(*) AS count, COALESCE(SUM(delta_stock),0) AS delta, COALESCE(SUM(CASE WHEN before_stock + delta_stock <> after_stock THEN 1 ELSE 0 END),0) AS invalid").Scan(&stats).Error; err != nil {
		return err
	}
	if stats.Count == 0 {
		if goods.Stock != 0 {
			add("baseline_missing", "warning", "非零库存没有历史流水，需要人工核对期初库存")
		}
		return nil
	}
	var first, last model.StockMovement
	if err := tx.Where("agency_id = ? AND goods_id = ?", agencyID, goodsID).Order("id ASC").First(&first).Error; err != nil {
		return err
	}
	if err := tx.Where("agency_id = ? AND goods_id = ?", agencyID, goodsID).Order("id DESC").First(&last).Error; err != nil {
		return err
	}
	if stats.Invalid != 0 || first.BeforeStock+stats.Delta != goods.Stock || last.AfterStock != goods.Stock {
		add("stock_trail", "error", "库存流水计算、累计值或最后库存与商品不一致")
	}
	var gaps int64
	if err := tx.Raw(`SELECT COUNT(*) FROM (
		SELECT before_stock, LAG(after_stock) OVER (ORDER BY id) AS previous_after
		FROM stock_movements WHERE agency_id = ? AND goods_id = ?
	) AS trail WHERE previous_after IS NOT NULL AND before_stock <> previous_after`, agencyID, goodsID).Scan(&gaps).Error; err != nil {
		return err
	}
	if gaps != 0 {
		add("stock_chain", "error", "相邻库存流水的前后数量不连续")
	}
	return nil
}

func reconcileOrder(tx *gorm.DB, agencyID, orderID uint64, add addReconciliationIssue) error {
	var order model.ExchangeOrder
	if err := tx.First(&order, orderID).Error; err != nil {
		return err
	}
	// At most three rows suffice to prove duplication without loading an unbounded corrupt history.
	var costs, refunds []model.PointLedger
	for _, item := range []struct {
		kind string
		rows *[]model.PointLedger
	}{{PointLedgerExchangeCost, &costs}, {PointLedgerOrderRefund, &refunds}} {
		if err := tx.Where("reference_id = ? AND type = ?", orderID, item.kind).Limit(3).Find(item.rows).Error; err != nil {
			return err
		}
	}
	validLedger := func(row model.PointLedger, delta int64) bool {
		return row.UserID == order.UserID && row.AgencyID == agencyID && row.GroupID == order.GroupID && row.DeltaPoints == delta && row.BeforePoints+row.DeltaPoints == row.AfterPoints
	}
	if len(costs) != 1 || !validLedger(costs[0], -order.PointsCost) {
		add("order_cost", "error", "订单扣分流水缺失、重复或金额及归属不一致")
	}
	refunded := order.Status == model.ExchangeOrderStatusCanceled || order.Status == model.ExchangeOrderStatusExpired
	if refunded {
		if len(refunds) != 1 || order.RefundLedgerID == nil || refunds[0].ID != *order.RefundLedgerID || !validLedger(refunds[0], order.PointsCost) {
			add("order_refund", "error", "已取消或过期订单的退款流水不一致")
		}
	} else if len(refunds) != 0 || order.RefundLedgerID != nil {
		add("unexpected_refund", "error", "未退款状态的订单存在退款记录")
	}
	if !refunded && order.Status != model.ExchangeOrderStatusPending && order.Status != model.ExchangeOrderStatusRedeemed {
		add("order_status", "error", "订单状态无效")
	}
	var movements []model.StockMovement
	if err := tx.Where("reference_id = ? AND type IN ?", orderID, []string{model.StockMovementExchange, model.StockMovementOrderCancel, model.StockMovementOrderExpire}).Limit(4).Find(&movements).Error; err != nil {
		return err
	}
	costCount, refundCount := 0, 0
	valid := true
	for _, row := range movements {
		if row.GoodsID != order.GoodsID || row.AgencyID != agencyID || row.GroupID != order.GroupID || row.BeforeStock+row.DeltaStock != row.AfterStock {
			valid = false
		}
		if row.Type == model.StockMovementExchange {
			costCount++
			valid = valid && row.DeltaStock == -1
		} else {
			refundCount++
			valid = valid && row.DeltaStock == 1 && ((order.Status == model.ExchangeOrderStatusCanceled && row.Type == model.StockMovementOrderCancel) || (order.Status == model.ExchangeOrderStatusExpired && row.Type == model.StockMovementOrderExpire))
		}
	}
	expectedRefunds := 0
	if refunded {
		expectedRefunds = 1
	}
	if !valid || costCount != 1 || refundCount != expectedRefunds {
		add("order_stock", "error", "订单扣库存或退库存记录不一致")
	}
	return nil
}
