package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

const (
	idempotencyStaffAdd    = "staff_add"
	idempotencyAdminAdjust = "admin_points_adjust"
	idempotencyAdminGoods  = "admin_goods_create"
	idempotencyExchange    = "exchange"
	idempotencyCancelOrder = "cancel_exchange_order"
	idempotencyRedeem      = "redeem_order"
)

func validateRequestID(requestID string) (string, error) {
	requestID = strings.TrimSpace(requestID)
	if len(requestID) < 8 || len(requestID) > 64 {
		return "", xerr.New(400, "invalid_request_id", "request_id must be between 8 and 64 characters")
	}
	for _, char := range requestID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return "", xerr.New(400, "invalid_request_id", "request_id contains invalid characters")
	}
	return requestID, nil
}

func idempotencyHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func loadIdempotentResult[T any](ctx context.Context, db *gorm.DB, actorID uint64, operation string, requestID string, requestHash string) (T, bool, error) {
	var zero T
	var record model.IdempotencyRecord
	err := db.WithContext(ctx).
		Where("actor_id = ? AND operation = ? AND request_id = ?", actorID, operation, requestID).
		First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return zero, false, nil
		}
		return zero, false, err
	}
	if record.RequestHash != requestHash {
		return zero, true, xerr.New(409, "idempotency_conflict", "request_id was already used for different request data")
	}
	if record.Compacted {
		return zero, true, xerr.New(409, "operation_result_archived", "operation was already completed; check its business history before creating a new operation")
	}
	var result T
	if err := json.Unmarshal([]byte(record.ResponseJSON), &result); err != nil {
		return zero, true, err
	}
	return result, true, nil
}

func createIdempotencyRecord(tx *gorm.DB, actorID uint64, operation string, requestID string, requestHash string) (model.IdempotencyRecord, error) {
	record := model.IdempotencyRecord{
		ActorID:      actorID,
		Operation:    operation,
		RequestID:    requestID,
		RequestHash:  requestHash,
		ResponseJSON: "{}",
	}
	return record, tx.Create(&record).Error
}

func saveIdempotentResult(tx *gorm.DB, record *model.IdempotencyRecord, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	updates := map[string]any{"response_json": string(encoded)}
	if exchange, ok := result.(ExchangeResult); ok && record.Operation == idempotencyExchange {
		updates["resource_id"] = exchange.Order.ID
	}
	return tx.Model(record).Updates(updates).Error
}
