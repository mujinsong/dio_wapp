package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	qrcode "github.com/skip2/go-qrcode"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PointLedgerExchangeCost = "exchange_cost"
	PointLedgerOrderRefund  = "order_refund"
	RedeemQRPrefix          = "dio_redeem:"
	defaultShopGroupName    = "默认团体"
)

type ShopService struct {
	db             *gorm.DB
	agencyID       uint64
	locker         ExchangeLocker
	lockTTL        time.Duration
	lockWait       time.Duration
	orderRedeemTTL time.Duration
}

type ExchangeLocker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration, wait time.Duration) (release func(context.Context) error, acquired bool, err error)
}

type GoodsDTO struct {
	ID                     uint64     `json:"id"`
	AgencyID               uint64     `json:"agency_id"`
	AgencyName             string     `json:"agency_name"`
	GroupID                uint64     `json:"group_id"`
	GroupName              string     `json:"group_name"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	ImageURL               string     `json:"image_url"`
	PricePoints            int64      `json:"price_points"`
	Stock                  int64      `json:"stock"`
	PurchaseLimitPerUser   int64      `json:"purchase_limit_per_user"`
	PurchaseLimitHours     int64      `json:"purchase_limit_hours"`
	PurchaseLimitStartedAt *time.Time `json:"purchase_limit_started_at"`
	SaleStartsAt           *time.Time `json:"sale_starts_at"`
	PurchaseLimitEndsAt    *time.Time `json:"purchase_limit_ends_at"`
	PurchaseLimitActive    bool       `json:"purchase_limit_active"`
	UserExchangeCount      int64      `json:"user_exchange_count"`
	LimitReached           bool       `json:"limit_reached"`
	Status                 string     `json:"status"`
	Sort                   int        `json:"sort"`
	SoldOut                bool       `json:"sold_out"`
	PointsBalance          int64      `json:"points_balance"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type ShopGroupDTO struct {
	ID          uint64 `json:"id"`
	AgencyID    uint64 `json:"agency_id"`
	AgencyName  string `json:"agency_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Sort        int    `json:"sort"`
}

type ExchangeOrderDTO struct {
	ID         uint64     `json:"id"`
	OrderNo    string     `json:"order_no"`
	AgencyID   uint64     `json:"agency_id"`
	AgencyName string     `json:"agency_name"`
	GroupID    uint64     `json:"group_id"`
	GroupName  string     `json:"group_name"`
	GoodsID    uint64     `json:"goods_id"`
	GoodsName  string     `json:"goods_name"`
	PointsCost int64      `json:"points_cost"`
	Status     string     `json:"status"`
	RedeemedBy uint64     `json:"redeemed_by"`
	RedeemedAt *time.Time `json:"redeemed_at"`
	CanceledAt *time.Time `json:"canceled_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	ExpiredAt  *time.Time `json:"expired_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type ExchangeResult struct {
	AgencyID          uint64           `json:"agency_id"`
	Order             ExchangeOrderDTO `json:"order"`
	BeforePoints      int64            `json:"before_points"`
	AfterPoints       int64            `json:"after_points"`
	RemainingStock    int64            `json:"remaining_stock"`
	UserExchangeCount int64            `json:"user_exchange_count"`
}

type ExchangeOrderPage struct {
	Items      []ExchangeOrderDTO `json:"items"`
	NextCursor uint64             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

type RedeemQRCodeDTO struct {
	Token     string    `json:"token"`
	Payload   string    `json:"payload"`
	QRImage   string    `json:"qr_image"`
	OrderID   uint64    `json:"order_id"`
	OrderNo   string    `json:"order_no"`
	GoodsName string    `json:"goods_name"`
	CreatedAt time.Time `json:"created_at"`
}

type RedeemOrderResult struct {
	Order      ExchangeOrderDTO `json:"order"`
	RedeemedAt time.Time        `json:"redeemed_at"`
}

type CancelOrderResult struct {
	Order          ExchangeOrderDTO `json:"order"`
	BeforePoints   int64            `json:"before_points"`
	AfterPoints    int64            `json:"after_points"`
	RestoredStock  int64            `json:"restored_stock"`
	RefundLedgerID uint64           `json:"refund_ledger_id"`
}

func NewShopService(db *gorm.DB, agencyIDs ...uint64) *ShopService {
	var agencyID uint64
	if len(agencyIDs) > 0 {
		agencyID = agencyIDs[0]
	}
	return &ShopService{db: db, agencyID: agencyID, orderRedeemTTL: 24 * time.Hour}
}

func NewShopServiceWithLocker(db *gorm.DB, locker ExchangeLocker, ttl time.Duration, wait time.Duration) *ShopService {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	if wait < 0 {
		wait = 0
	}
	return &ShopService{db: db, locker: locker, lockTTL: ttl, lockWait: wait, orderRedeemTTL: 24 * time.Hour}
}

func (s *ShopService) SetOrderRedeemTTL(ttl time.Duration) {
	if ttl > 0 {
		s.orderRedeemTTL = ttl
	}
}

func NewShopServiceWithAgencyLocker(db *gorm.DB, agencyID uint64, locker ExchangeLocker, ttl time.Duration, wait time.Duration) *ShopService {
	service := NewShopServiceWithLocker(db, locker, ttl, wait)
	service.agencyID = agencyID
	return service
}

func (s *ShopService) singleAgencyID(ctx context.Context) (uint64, error) {
	if s.agencyID > 0 {
		return s.agencyID, nil
	}
	var agencies []model.Agency
	if err := s.db.WithContext(ctx).Order("id ASC").Limit(2).Find(&agencies).Error; err != nil {
		return 0, err
	}
	if len(agencies) != 1 {
		return 0, fmt.Errorf("single-agency invariant violated: expected one agency, found %d", len(agencies))
	}
	return agencies[0].ID, nil
}

func (s *ShopService) ListGroups(ctx context.Context) ([]ShopGroupDTO, error) {
	agencyID, err := s.singleAgencyID(ctx)
	if err != nil {
		return nil, err
	}
	var rows []ShopGroupDTO
	now := time.Now()
	err = s.db.WithContext(ctx).
		Table("idol_groups").
		Select("idol_groups.id, idol_groups.agency_id, agencies.name AS agency_name, idol_groups.name, idol_groups.description, idol_groups.sort").
		Joins("JOIN agencies ON agencies.id = idol_groups.agency_id").
		Where("idol_groups.status = ? AND agencies.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Where("idol_groups.agency_id = ?", agencyID).
		Where("EXISTS (?)", s.db.Model(&model.Goods{}).
			Select("1").
			Where("goods.agency_id = idol_groups.agency_id").
			Where("goods.group_id = idol_groups.id").
			Where("goods.status = ?", model.GoodsStatusOnSale).
			Where("goods.sale_starts_at IS NULL OR goods.sale_starts_at <= ?", now)).
		Order("idol_groups.sort DESC, idol_groups.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ShopService) ListGoods(ctx context.Context, userID uint64, groupID uint64) ([]GoodsDTO, error) {
	agencyID, err := s.singleAgencyID(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID                     uint64     `gorm:"column:id"`
		AgencyID               uint64     `gorm:"column:agency_id"`
		AgencyName             string     `gorm:"column:agency_name"`
		GroupID                uint64     `gorm:"column:group_id"`
		GroupName              string     `gorm:"column:group_name"`
		Name                   string     `gorm:"column:name"`
		Description            string     `gorm:"column:description"`
		ImageURL               string     `gorm:"column:image_url"`
		PricePoints            int64      `gorm:"column:price_points"`
		Stock                  int64      `gorm:"column:stock"`
		PurchaseLimitPerUser   int64      `gorm:"column:purchase_limit_per_user"`
		PurchaseLimitHours     int64      `gorm:"column:purchase_limit_hours"`
		PurchaseLimitStartedAt *time.Time `gorm:"column:purchase_limit_started_at"`
		SaleStartsAt           *time.Time `gorm:"column:sale_starts_at"`
		UserExchangeCount      int64      `gorm:"column:user_exchange_count"`
		PointsBalance          int64      `gorm:"column:points_balance"`
		Status                 string     `gorm:"column:status"`
		Sort                   int        `gorm:"column:sort"`
		UpdatedAt              time.Time  `gorm:"column:updated_at"`
	}

	query := s.db.WithContext(ctx).
		Table("goods").
		Select("goods.id, goods.agency_id, agencies.name AS agency_name, goods.group_id, idol_groups.name AS group_name, goods.name, goods.description, goods.image_url, goods.price_points, goods.stock, goods.purchase_limit_per_user, goods.purchase_limit_hours, goods.purchase_limit_started_at, goods.sale_starts_at, goods.status, goods.sort, goods.updated_at, COALESCE((SELECT balance FROM agency_point_accounts WHERE agency_point_accounts.user_id = ? AND agency_point_accounts.agency_id = goods.agency_id), 0) AS points_balance, (SELECT COUNT(*) FROM exchange_orders AS user_orders WHERE user_orders.user_id = ? AND user_orders.goods_id = goods.id AND user_orders.status IN (?, ?) AND (goods.purchase_limit_started_at IS NULL OR user_orders.created_at >= goods.purchase_limit_started_at)) AS user_exchange_count", userID, userID, model.ExchangeOrderStatusPending, model.ExchangeOrderStatusRedeemed).
		Joins("JOIN agencies ON agencies.id = goods.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = goods.group_id AND idol_groups.agency_id = goods.agency_id").
		Where("goods.status = ?", model.GoodsStatusOnSale).
		Where("goods.sale_starts_at IS NULL OR goods.sale_starts_at <= ?", time.Now()).
		Where("goods.agency_id = ?", agencyID).
		Where("agencies.status = ? AND idol_groups.status = ?", model.MemberStatusNormal, model.MemberStatusNormal)
	if groupID > 0 {
		query = query.Where("goods.group_id = ?", groupID)
	}

	err = query.
		Order("goods.sort DESC, goods.id DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make([]GoodsDTO, 0, len(rows))
	for _, row := range rows {
		limitActive, limitEndsAt := purchaseLimitWindow(row.PurchaseLimitPerUser, row.PurchaseLimitHours, row.PurchaseLimitStartedAt, time.Now())
		result = append(result, GoodsDTO{
			ID:                     row.ID,
			AgencyID:               row.AgencyID,
			AgencyName:             row.AgencyName,
			GroupID:                row.GroupID,
			GroupName:              row.GroupName,
			Name:                   row.Name,
			Description:            row.Description,
			ImageURL:               row.ImageURL,
			PricePoints:            row.PricePoints,
			Stock:                  row.Stock,
			PurchaseLimitPerUser:   row.PurchaseLimitPerUser,
			PurchaseLimitHours:     row.PurchaseLimitHours,
			PurchaseLimitStartedAt: row.PurchaseLimitStartedAt,
			SaleStartsAt:           row.SaleStartsAt,
			PurchaseLimitEndsAt:    limitEndsAt,
			PurchaseLimitActive:    limitActive,
			UserExchangeCount:      row.UserExchangeCount,
			LimitReached:           limitActive && row.UserExchangeCount >= row.PurchaseLimitPerUser,
			Status:                 row.Status,
			Sort:                   row.Sort,
			SoldOut:                row.Stock <= 0,
			PointsBalance:          row.PointsBalance,
			UpdatedAt:              row.UpdatedAt,
		})
	}
	return result, nil
}

func (s *ShopService) GetGoods(ctx context.Context, userID uint64, goodsID uint64) (GoodsDTO, error) {
	if goodsID == 0 {
		return GoodsDTO{}, xerr.New(400, "invalid_goods", "goods_id is required")
	}

	agencyID, err := s.singleAgencyID(ctx)
	if err != nil {
		return GoodsDTO{}, err
	}
	var goods GoodsDTO
	result := s.db.WithContext(ctx).
		Table("goods").
		Select("goods.id, goods.agency_id, agencies.name AS agency_name, goods.group_id, idol_groups.name AS group_name, goods.name, goods.description, goods.image_url, goods.price_points, goods.stock, goods.purchase_limit_per_user, goods.purchase_limit_hours, goods.purchase_limit_started_at, goods.sale_starts_at, goods.status, goods.sort, goods.updated_at, COALESCE((SELECT balance FROM agency_point_accounts WHERE agency_point_accounts.user_id = ? AND agency_point_accounts.agency_id = goods.agency_id), 0) AS points_balance, (SELECT COUNT(*) FROM exchange_orders AS user_orders WHERE user_orders.user_id = ? AND user_orders.goods_id = goods.id AND user_orders.status IN (?, ?) AND (goods.purchase_limit_started_at IS NULL OR user_orders.created_at >= goods.purchase_limit_started_at)) AS user_exchange_count", userID, userID, model.ExchangeOrderStatusPending, model.ExchangeOrderStatusRedeemed).
		Joins("JOIN agencies ON agencies.id = goods.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = goods.group_id AND idol_groups.agency_id = goods.agency_id").
		Where("goods.id = ? AND goods.agency_id = ? AND goods.status = ?", goodsID, agencyID, model.GoodsStatusOnSale).
		Where("goods.sale_starts_at IS NULL OR goods.sale_starts_at <= ?", time.Now()).
		Where("agencies.status = ? AND idol_groups.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Scan(&goods)
	if result.Error != nil {
		return GoodsDTO{}, result.Error
	}
	if result.RowsAffected == 0 {
		return GoodsDTO{}, xerr.New(404, "goods_not_found", "goods not found")
	}
	goods.SoldOut = goods.Stock <= 0
	goods.PurchaseLimitActive, goods.PurchaseLimitEndsAt = purchaseLimitWindow(goods.PurchaseLimitPerUser, goods.PurchaseLimitHours, goods.PurchaseLimitStartedAt, time.Now())
	goods.LimitReached = goods.PurchaseLimitActive && goods.UserExchangeCount >= goods.PurchaseLimitPerUser
	return goods, nil
}

func (s *ShopService) ListOrders(ctx context.Context, userID uint64) ([]ExchangeOrderDTO, error) {
	var orders []ExchangeOrderDTO
	var cursor uint64
	for {
		page, err := s.ListOrdersPage(ctx, userID, cursor, 50)
		if err != nil {
			return nil, err
		}
		orders = append(orders, page.Items...)
		if !page.HasMore {
			return orders, nil
		}
		cursor = page.NextCursor
	}
}

func (s *ShopService) ListOrdersPage(ctx context.Context, userID uint64, cursor uint64, limit int) (ExchangeOrderPage, error) {
	if _, err := s.expirePendingOrdersForUser(ctx, userID, time.Now(), 100); err != nil && ctx.Err() != nil {
		return ExchangeOrderPage{}, ctx.Err()
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	var orders []ExchangeOrderDTO
	query := s.db.WithContext(ctx).
		Table("exchange_orders").
		Select("exchange_orders.id, exchange_orders.order_no, exchange_orders.agency_id, agencies.name AS agency_name, exchange_orders.group_id, idol_groups.name AS group_name, exchange_orders.goods_id, exchange_orders.goods_name, exchange_orders.points_cost, exchange_orders.status, exchange_orders.redeemed_by, exchange_orders.redeemed_at, exchange_orders.canceled_at, exchange_orders.expires_at, exchange_orders.expired_at, exchange_orders.created_at").
		Joins("LEFT JOIN agencies ON agencies.id = exchange_orders.agency_id").
		Joins("LEFT JOIN idol_groups ON idol_groups.id = exchange_orders.group_id AND idol_groups.agency_id = exchange_orders.agency_id").
		Where("exchange_orders.user_id = ?", userID)
	if cursor > 0 {
		query = query.Where("exchange_orders.id < ?", cursor)
	}
	if err := query.Order("exchange_orders.id DESC").Limit(limit + 1).Scan(&orders).Error; err != nil {
		return ExchangeOrderPage{}, err
	}
	page := ExchangeOrderPage{Items: orders}
	if len(orders) > limit {
		page.HasMore = true
		page.Items = orders[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *ShopService) GetOrder(ctx context.Context, userID uint64, orderID uint64) (ExchangeOrderDTO, error) {
	if orderID == 0 {
		return ExchangeOrderDTO{}, xerr.New(400, "invalid_order", "order_id is required")
	}
	order, err := s.loadOrderDTO(ctx, s.db, userID, orderID)
	if err != nil {
		return ExchangeOrderDTO{}, err
	}
	if order.Status == model.ExchangeOrderStatusPending && order.ExpiresAt != nil && !order.ExpiresAt.After(time.Now()) {
		if _, err := s.expirePendingOrder(ctx, orderID, time.Now()); err != nil {
			return ExchangeOrderDTO{}, err
		}
		return s.loadOrderDTO(ctx, s.db, userID, orderID)
	}
	return order, nil
}

func (s *ShopService) CancelOrder(ctx context.Context, userID uint64, orderID uint64, requestID string) (CancelOrderResult, error) {
	if orderID == 0 {
		return CancelOrderResult{}, xerr.New(400, "invalid_order", "order_id is required")
	}
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return CancelOrderResult{}, err
	}
	requestHash := idempotencyHash(strconv.FormatUint(orderID, 10))
	if existing, found, err := loadIdempotentResult[CancelOrderResult](ctx, s.db, userID, idempotencyCancelOrder, requestID, requestHash); found || err != nil {
		return existing, err
	}

	var result CancelOrderResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, userID, idempotencyCancelOrder, requestID, requestHash)
		if err != nil {
			return err
		}

		var order model.ExchangeOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "order_not_found", "exchange order not found")
			}
			return err
		}
		if order.Status == model.ExchangeOrderStatusRedeemed {
			return xerr.New(409, "order_already_redeemed", "redeemed orders cannot be canceled")
		}
		if order.Status == model.ExchangeOrderStatusCanceled {
			if order.RefundLedgerID == nil {
				return xerr.New(409, "order_refund_inconsistent", "canceled order has no refund record")
			}
			var ledger model.PointLedger
			if err := tx.First(&ledger, *order.RefundLedgerID).Error; err != nil {
				return err
			}
			orderDTO, err := s.loadOrderDTO(ctx, tx, userID, order.ID)
			if err != nil {
				return err
			}
			result = CancelOrderResult{
				Order:          orderDTO,
				BeforePoints:   ledger.BeforePoints,
				AfterPoints:    ledger.AfterPoints,
				RestoredStock:  1,
				RefundLedgerID: ledger.ID,
			}
			return saveIdempotentResult(tx, &idempotency, result)
		}
		if order.Status != model.ExchangeOrderStatusPending {
			return xerr.New(409, "order_not_cancelable", "only pending orders can be canceled")
		}
		if order.PointsCost <= 0 {
			return xerr.New(409, "invalid_order_points", "order points cost is invalid")
		}

		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "user_not_found", "user not found")
			}
			return err
		}
		account, err := lockAgencyPointAccount(tx, order.AgencyID, userID)
		if err != nil {
			return err
		}
		if order.PointsCost > MaxPointBalance || account.Balance > MaxPointBalance-order.PointsCost {
			return xerr.New(409, "points_balance_limit", "points balance cannot accept this refund")
		}

		var goods model.Goods
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&goods, order.GoodsID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(409, "order_goods_missing", "order goods no longer exists")
			}
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
		beforeStock := goods.Stock
		restoredStock := beforeStock + 1
		if err := tx.Model(&account).Update("balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&goods).Update("stock", restoredStock).Error; err != nil {
			return err
		}
		if err := recordStockMovement(tx, goods, beforeStock, restoredStock, model.StockMovementOrderCancel, userID, order.ID, "用户取消兑换返还库存"); err != nil {
			return err
		}

		ledger := model.PointLedger{
			AgencyID:     order.AgencyID,
			GroupID:      order.GroupID,
			UserID:       userID,
			BeforePoints: before,
			DeltaPoints:  order.PointsCost,
			AfterPoints:  after,
			Type:         PointLedgerOrderRefund,
			OperatorID:   userID,
			ReferenceID:  order.ID,
			Remark:       "取消兑换退款：" + order.GoodsName,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}

		canceledAt := time.Now()
		if err := tx.Model(&order).Updates(map[string]any{
			"status":           model.ExchangeOrderStatusCanceled,
			"canceled_at":      &canceledAt,
			"refund_ledger_id": ledger.ID,
		}).Error; err != nil {
			return err
		}
		orderDTO, err := s.loadOrderDTO(ctx, tx, userID, order.ID)
		if err != nil {
			return err
		}
		result = CancelOrderResult{
			Order:          orderDTO,
			BeforePoints:   before,
			AfterPoints:    after,
			RestoredStock:  1,
			RefundLedgerID: ledger.ID,
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[CancelOrderResult](ctx, s.db, userID, idempotencyCancelOrder, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return CancelOrderResult{}, err
	}
	return result, nil
}

func (s *ShopService) CreateRedeemQRCode(ctx context.Context, userID uint64, orderID uint64) (RedeemQRCodeDTO, error) {
	if orderID == 0 {
		return RedeemQRCodeDTO{}, xerr.New(400, "invalid_order", "order_id is required")
	}

	token, err := randomToken()
	if err != nil {
		return RedeemQRCodeDTO{}, err
	}

	var order model.ExchangeOrder
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "order_not_found", "exchange order not found")
			}
			return err
		}
		if order.Status != model.ExchangeOrderStatusPending {
			return xerr.New(400, "order_not_pending", "only pending orders can show redeem QR code")
		}
		if orderPastRedeemDeadline(order, time.Now()) {
			return xerr.New(410, "order_expired", "exchange order has expired")
		}
		return tx.Model(&order).Update("redeem_token_hash", tokenHash(token)).Error
	})
	if err != nil {
		return RedeemQRCodeDTO{}, err
	}

	payload := RedeemQRPrefix + token
	png, err := qrcode.Encode(payload, qrcode.Medium, 512)
	if err != nil {
		return RedeemQRCodeDTO{}, err
	}

	return RedeemQRCodeDTO{
		Token:     token,
		Payload:   payload,
		QRImage:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		OrderID:   order.ID,
		OrderNo:   order.OrderNo,
		GoodsName: order.GoodsName,
		CreatedAt: order.CreatedAt,
	}, nil
}

func (s *ShopService) RedeemOrderByStaff(ctx context.Context, staffID uint64, redeemToken string, requestID string) (RedeemOrderResult, error) {
	redeemToken = strings.TrimSpace(strings.TrimPrefix(redeemToken, RedeemQRPrefix))
	if redeemToken == "" {
		return RedeemOrderResult{}, xerr.New(400, "invalid_redeem_token", "redeem token is required")
	}
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return RedeemOrderResult{}, err
	}
	requestHash := idempotencyHash(tokenHash(redeemToken))
	if existing, found, err := loadIdempotentResult[RedeemOrderResult](ctx, s.db, staffID, idempotencyRedeem, requestID, requestHash); found || err != nil {
		return existing, err
	}

	var result RedeemOrderResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, staffID, idempotencyRedeem, requestID, requestHash)
		if err != nil {
			return err
		}

		var order model.ExchangeOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("redeem_token_hash = ?", tokenHash(redeemToken)).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "redeem_token_not_found", "redeem QR code is invalid")
			}
			return err
		}
		if order.Status == model.ExchangeOrderStatusRedeemed {
			return xerr.New(400, "order_already_redeemed", "exchange order was already redeemed")
		}
		if order.Status != model.ExchangeOrderStatusPending {
			return xerr.New(400, "order_not_pending", "only pending orders can be redeemed")
		}
		if orderPastRedeemDeadline(order, time.Now()) {
			return xerr.New(410, "order_expired", "exchange order has expired")
		}

		ok, err := s.hasStaffGroupTx(tx, staffID, order.AgencyID, order.GroupID)
		if err != nil {
			return err
		}
		if !ok {
			return xerr.New(403, "staff_group_forbidden", "staff has no permission for this order group")
		}

		redeemedAt := time.Now()
		if err := tx.Model(&order).Updates(map[string]any{
			"status":      model.ExchangeOrderStatusRedeemed,
			"redeemed_by": staffID,
			"redeemed_at": &redeemedAt,
		}).Error; err != nil {
			return err
		}

		orderDTO, err := s.loadOrderDTO(ctx, tx, order.UserID, order.ID)
		if err != nil {
			return err
		}
		result = RedeemOrderResult{
			Order:      orderDTO,
			RedeemedAt: redeemedAt,
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[RedeemOrderResult](ctx, s.db, staffID, idempotencyRedeem, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return RedeemOrderResult{}, err
	}
	return result, nil
}

func (s *ShopService) ExchangeGoods(ctx context.Context, userID uint64, goodsID uint64, requestID string, expectedPricePoints int64) (ExchangeResult, error) {
	if goodsID == 0 {
		return ExchangeResult{}, xerr.New(400, "invalid_goods", "goods_id is required")
	}
	if expectedPricePoints <= 0 || expectedPricePoints > MaxGoodsPricePoints {
		return ExchangeResult{}, xerr.New(400, "invalid_expected_price", "confirmed price is required and must be within the allowed range")
	}
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return ExchangeResult{}, err
	}
	// Price is a precondition for a new exchange; retries must still recover an already completed order.
	requestHash := idempotencyHash(strconv.FormatUint(goodsID, 10))
	if existing, found, err := loadIdempotentResult[ExchangeResult](ctx, s.db, userID, idempotencyExchange, requestID, requestHash); found || err != nil {
		return existing, err
	}
	if s.locker == nil {
		return s.exchangeGoodsTransaction(ctx, userID, goodsID, requestID, requestHash, expectedPricePoints)
	}

	key := fmt.Sprintf("dio:exchange:goods:%d", goodsID)
	release, acquired, err := s.locker.Acquire(ctx, key, s.lockTTL, s.lockWait)
	if err != nil {
		if ctx.Err() != nil {
			return ExchangeResult{}, ctx.Err()
		}
		return s.exchangeGoodsTransaction(ctx, userID, goodsID, requestID, requestHash, expectedPricePoints)
	}
	if !acquired {
		return ExchangeResult{}, xerr.New(429, "exchange_busy", "goods exchange is busy, please retry")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = release(releaseCtx)
	}()
	return s.exchangeGoodsTransaction(ctx, userID, goodsID, requestID, requestHash, expectedPricePoints)
}

func (s *ShopService) exchangeGoodsTransaction(ctx context.Context, userID uint64, goodsID uint64, requestID string, requestHash string, expectedPricePoints int64) (ExchangeResult, error) {
	agencyID, err := s.singleAgencyID(ctx)
	if err != nil {
		return ExchangeResult{}, err
	}
	var result ExchangeResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, userID, idempotencyExchange, requestID, requestHash)
		if err != nil {
			return err
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "user_not_found", "user not found")
			}
			return err
		}
		if user.Status == model.UserStatusDisabled {
			return xerr.New(403, "user_disabled", "user is disabled")
		}

		var goods model.Goods
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&goods, goodsID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "goods_not_found", "goods not found")
			}
			return err
		}
		if goods.Status != model.GoodsStatusOnSale {
			return xerr.New(400, "goods_off_sale", "goods is off sale")
		}
		if goods.AgencyID != agencyID {
			return xerr.New(404, "goods_not_found", "goods not found")
		}
		if goods.PricePoints != expectedPricePoints {
			return xerr.New(409, "goods_price_changed", "goods price has changed; refresh and confirm the current price")
		}
		if goods.SaleStartsAt != nil && goods.SaleStartsAt.After(time.Now()) {
			return xerr.New(400, "goods_not_started", "goods sale has not started")
		}
		if goods.Stock <= 0 {
			return xerr.New(400, "goods_sold_out", "goods is sold out")
		}

		var groupAvailable int64
		if err := tx.Model(&model.IdolGroup{}).
			Joins("JOIN agencies ON agencies.id = idol_groups.agency_id").
			Where("idol_groups.id = ? AND idol_groups.agency_id = ?", goods.GroupID, goods.AgencyID).
			Where("idol_groups.status = ? AND agencies.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
			Count(&groupAvailable).Error; err != nil {
			return err
		}
		if groupAvailable == 0 {
			return xerr.New(400, "goods_group_unavailable", "goods group is unavailable")
		}
		account, err := lockAgencyPointAccount(tx, goods.AgencyID, user.ID)
		if err != nil {
			return err
		}
		var userExchangeCount int64
		limitActive, _ := purchaseLimitWindow(goods.PurchaseLimitPerUser, goods.PurchaseLimitHours, goods.PurchaseLimitStartedAt, time.Now())
		if limitActive {
			if err := tx.Model(&model.ExchangeOrder{}).
				Where("user_id = ? AND goods_id = ?", user.ID, goods.ID).
				Where("status IN ?", []string{model.ExchangeOrderStatusPending, model.ExchangeOrderStatusRedeemed}).
				Where("created_at >= ?", *goods.PurchaseLimitStartedAt).
				Count(&userExchangeCount).Error; err != nil {
				return err
			}
			if userExchangeCount >= goods.PurchaseLimitPerUser {
				return xerr.New(400, "purchase_limit_reached", "purchase limit reached")
			}
		}
		if account.Balance < goods.PricePoints {
			return xerr.New(400, "insufficient_points", "points balance is insufficient")
		}

		before := account.Balance
		after := before - goods.PricePoints
		if err := tx.Model(&account).Update("balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
			return err
		}
		beforeStock := goods.Stock
		afterStock := beforeStock - 1
		if err := tx.Model(&goods).Update("stock", afterStock).Error; err != nil {
			return err
		}

		redeemToken, err := randomToken()
		if err != nil {
			return err
		}
		order := model.ExchangeOrder{
			OrderNo:         buildOrderNo(),
			AgencyID:        goods.AgencyID,
			GroupID:         goods.GroupID,
			UserID:          user.ID,
			GoodsID:         goods.ID,
			GoodsName:       goods.Name,
			PointsCost:      goods.PricePoints,
			Status:          model.ExchangeOrderStatusPending,
			RedeemTokenHash: tokenHash(redeemToken),
		}
		expiresAt := time.Now().Add(s.orderRedeemTTL)
		order.ExpiresAt = &expiresAt
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		if err := recordStockMovement(tx, goods, beforeStock, afterStock, model.StockMovementExchange, user.ID, order.ID, "用户兑换商品扣减库存"); err != nil {
			return err
		}

		ledger := model.PointLedger{
			AgencyID:     goods.AgencyID,
			GroupID:      goods.GroupID,
			UserID:       user.ID,
			BeforePoints: before,
			DeltaPoints:  -goods.PricePoints,
			AfterPoints:  after,
			Type:         PointLedgerExchangeCost,
			OperatorID:   user.ID,
			ReferenceID:  order.ID,
			Remark:       "兑换商品：" + goods.Name,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}

		result = ExchangeResult{
			AgencyID: goods.AgencyID,
			Order: ExchangeOrderDTO{
				ID:         order.ID,
				OrderNo:    order.OrderNo,
				GoodsID:    order.GoodsID,
				GoodsName:  order.GoodsName,
				PointsCost: order.PointsCost,
				Status:     order.Status,
				ExpiresAt:  order.ExpiresAt,
				CreatedAt:  order.CreatedAt,
			},
			BeforePoints:      before,
			AfterPoints:       after,
			RemainingStock:    afterStock,
			UserExchangeCount: userExchangeCount + 1,
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[ExchangeResult](ctx, s.db, userID, idempotencyExchange, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return ExchangeResult{}, err
	}
	return result, nil
}

func purchaseLimitWindow(limit int64, hours int64, startedAt *time.Time, now time.Time) (bool, *time.Time) {
	if limit <= 0 || hours <= 0 || startedAt == nil {
		return false, nil
	}
	endsAt := startedAt.Add(time.Duration(hours) * time.Hour)
	return now.Before(endsAt), &endsAt
}

func (s *ShopService) loadOrderDTO(ctx context.Context, db *gorm.DB, userID uint64, orderID uint64) (ExchangeOrderDTO, error) {
	var order ExchangeOrderDTO
	query := db.WithContext(ctx).
		Table("exchange_orders").
		Select("exchange_orders.id, exchange_orders.order_no, exchange_orders.agency_id, agencies.name AS agency_name, exchange_orders.group_id, idol_groups.name AS group_name, exchange_orders.goods_id, exchange_orders.goods_name, exchange_orders.points_cost, exchange_orders.status, exchange_orders.redeemed_by, exchange_orders.redeemed_at, exchange_orders.canceled_at, exchange_orders.expires_at, exchange_orders.expired_at, exchange_orders.created_at").
		Joins("LEFT JOIN agencies ON agencies.id = exchange_orders.agency_id").
		Joins("LEFT JOIN idol_groups ON idol_groups.id = exchange_orders.group_id AND idol_groups.agency_id = exchange_orders.agency_id").
		Where("exchange_orders.id = ?", orderID)
	if userID > 0 {
		query = query.Where("exchange_orders.user_id = ?", userID)
	}
	result := query.Scan(&order)
	if result.Error != nil {
		return ExchangeOrderDTO{}, result.Error
	}
	if result.RowsAffected == 0 {
		return ExchangeOrderDTO{}, xerr.New(404, "order_not_found", "exchange order not found")
	}
	return order, nil
}

func (s *ShopService) hasStaffGroupTx(tx *gorm.DB, staffID uint64, agencyID uint64, groupID uint64) (bool, error) {
	var count int64
	err := tx.Model(&model.StaffMember{}).
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Joins("JOIN agencies ON agencies.id = staff_members.agency_id").
		Where("staff_members.user_id = ? AND staff_members.agency_id = ? AND staff_members.group_id = ?", staffID, agencyID, groupID).
		Where("staff_members.status = ? AND idol_groups.status = ? AND agencies.status = ?", model.MemberStatusNormal, model.MemberStatusNormal, model.MemberStatusNormal).
		Count(&count).Error
	return count > 0, err
}

func EnsureLocalDemoGoods(ctx context.Context, db *gorm.DB, agencyName string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		agency, err := ensureAgency(tx, agencyName)
		if err != nil {
			return err
		}

		group, err := ensureIdolGroup(tx, agency.ID, defaultShopGroupName)
		if err != nil {
			return err
		}

		var count int64
		if err := tx.Model(&model.Goods{}).Where("agency_id = ? AND group_id = ?", agency.ID, group.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}

		items := []model.Goods{
			{AgencyID: agency.ID, GroupID: group.ID, Name: "现场拍立得券", Description: "兑换后到物贩台核销", PricePoints: 30, Stock: 20, Status: model.GoodsStatusOnSale, Sort: 30},
			{AgencyID: agency.ID, GroupID: group.ID, Name: "限定贴纸包", Description: "随机款式，数量有限", PricePoints: 12, Stock: 50, Status: model.GoodsStatusOnSale, Sort: 20},
			{AgencyID: agency.ID, GroupID: group.ID, Name: "优先入场券", Description: "下次活动使用，现场确认", PricePoints: 80, Stock: 8, Status: model.GoodsStatusOnSale, Sort: 10},
		}
		if err := tx.Create(&items).Error; err != nil {
			return err
		}
		for _, goods := range items {
			if err := recordStockMovement(tx, goods, 0, goods.Stock, model.StockMovementInitial, 0, goods.ID, "本地演示商品初始库存"); err != nil {
				return err
			}
		}
		return nil
	})
}

func ensureIdolGroup(tx *gorm.DB, agencyID uint64, name string) (model.IdolGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultShopGroupName
	}

	var group model.IdolGroup
	err := tx.Where("agency_id = ? AND name = ?", agencyID, name).First(&group).Error
	if err == nil {
		if group.Status != model.MemberStatusNormal {
			if err := tx.Model(&group).Update("status", model.MemberStatusNormal).Error; err != nil {
				return model.IdolGroup{}, err
			}
			group.Status = model.MemberStatusNormal
		}
		return group, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.IdolGroup{}, err
	}

	group = model.IdolGroup{
		AgencyID: agencyID,
		Name:     name,
		Status:   model.MemberStatusNormal,
		Remark:   "created for local demo shop",
	}
	if err := tx.Create(&group).Error; err != nil {
		return model.IdolGroup{}, err
	}
	return group, nil
}

func buildOrderNo() string {
	token, err := randomToken()
	if err != nil {
		return fmt.Sprintf("EX%d", time.Now().UnixNano())
	}
	if len(token) > 12 {
		token = token[:12]
	}
	return fmt.Sprintf("EX%s%s", time.Now().Format("20060102150405"), strings.ToUpper(token))
}
