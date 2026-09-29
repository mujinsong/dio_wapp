package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"
	"gorm.io/gorm"
)

type ExchangeRecovery struct {
	Completed bool              `json:"completed"`
	RequestID string            `json:"request_id"`
	GoodsID   uint64            `json:"goods_id"`
	Order     *ExchangeOrderDTO `json:"order"`
}

// RecoverExchange is read-only: it never retries a purchase or settles an order.
func (s *ShopService) RecoverExchange(ctx context.Context, userID, goodsID uint64, requestID string) (ExchangeRecovery, error) {
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return ExchangeRecovery{}, err
	}
	if goodsID == 0 {
		return ExchangeRecovery{}, xerr.New(400, "invalid_goods", "goods_id is required")
	}
	var record model.IdempotencyRecord
	err = s.db.WithContext(ctx).Where("actor_id = ? AND operation = ? AND request_id = ?", userID, idempotencyExchange, requestID).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ExchangeRecovery{}, xerr.New(404, "exchange_request_not_found", "completed exchange request not found")
	}
	if err != nil {
		return ExchangeRecovery{}, err
	}
	if record.RequestHash != idempotencyHash(strconv.FormatUint(goodsID, 10)) {
		return ExchangeRecovery{}, xerr.New(409, "idempotency_conflict", "request belongs to different goods")
	}
	orderID := record.ResourceID
	if orderID == 0 && !record.Compacted {
		var result ExchangeResult
		if err := json.Unmarshal([]byte(record.ResponseJSON), &result); err != nil {
			return ExchangeRecovery{}, err
		}
		orderID = result.Order.ID
		if orderID == 0 {
			return ExchangeRecovery{}, xerr.New(409, "exchange_result_incomplete", "exchange result cannot be verified")
		}
	}
	result := ExchangeRecovery{Completed: true, RequestID: requestID, GoodsID: goodsID}
	// Historical compacted responses cannot be linked reliably; never guess an order.
	if orderID == 0 {
		return result, nil
	}
	order, err := s.loadOrderDTO(ctx, s.db, userID, orderID)
	if err != nil {
		return ExchangeRecovery{}, err
	}
	if order.GoodsID != goodsID {
		return ExchangeRecovery{}, xerr.New(409, "idempotency_conflict", "linked order belongs to different goods")
	}
	result.Order = &order
	return result, nil
}
