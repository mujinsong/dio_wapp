package service

import (
	"context"
	"encoding/json"
	"time"

	"dio_wapp/server/internal/model"

	"gorm.io/gorm"
)

type MaintenanceService struct {
	db *gorm.DB
}

type CleanupResult struct {
	IdentityTokensDeleted int64 `json:"identity_tokens_deleted"`
	IdempotencyCompacted  int64 `json:"idempotency_compacted"`
}

func NewMaintenanceService(db *gorm.DB) *MaintenanceService {
	return &MaintenanceService{db: db}
}

func (s *MaintenanceService) CleanupExpiredData(ctx context.Context, now time.Time, idempotencyRetention time.Duration, batchSize int) (CleanupResult, error) {
	if idempotencyRetention < 24*time.Hour {
		idempotencyRetention = 24 * time.Hour
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	if batchSize > 5000 {
		batchSize = 5000
	}
	var result CleanupResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		identityIDs, err := expiredRecordIDs(tx.Model(&model.IdentityQRToken{}), "expires_at < ?", "expires_at ASC, id ASC", now, batchSize)
		if err != nil {
			return err
		}
		if len(identityIDs) > 0 {
			deleted := tx.Where("id IN ?", identityIDs).Delete(&model.IdentityQRToken{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.IdentityTokensDeleted = deleted.RowsAffected
		}

		idempotencyIDs, err := expiredRecordIDs(tx.Model(&model.IdempotencyRecord{}).Where("compacted = ?", false), "created_at < ?", "created_at ASC, id ASC", now.Add(-idempotencyRetention), batchSize)
		if err != nil {
			return err
		}
		if len(idempotencyIDs) > 0 {
			var exchanges []model.IdempotencyRecord
			if err := tx.Where("id IN ? AND operation = ? AND resource_id = 0", idempotencyIDs, idempotencyExchange).Find(&exchanges).Error; err != nil {
				return err
			}
			for _, record := range exchanges {
				var exchange ExchangeResult
				if err := json.Unmarshal([]byte(record.ResponseJSON), &exchange); err != nil || exchange.Order.ID == 0 {
					continue
				}
				if err := tx.Model(&record).Update("resource_id", exchange.Order.ID).Error; err != nil {
					return err
				}
			}
			// Keep the unique request key after discarding the response so late retries cannot execute again.
			compacted := tx.Model(&model.IdempotencyRecord{}).Where("id IN ? AND compacted = ?", idempotencyIDs, false).
				Updates(map[string]any{"response_json": "", "compacted": true})
			if compacted.Error != nil {
				return compacted.Error
			}
			result.IdempotencyCompacted = compacted.RowsAffected
		}
		return nil
	})
	return result, err
}

func expiredRecordIDs(query *gorm.DB, condition string, order string, cutoff time.Time, limit int) ([]uint64, error) {
	var ids []uint64
	err := query.Where(condition, cutoff).Order(order).Limit(limit).Pluck("id", &ids).Error
	return ids, err
}
