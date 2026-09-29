package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PointCorrectionRequestDTO struct {
	ID                     uint64     `json:"id"`
	AgencyID               uint64     `json:"agency_id"`
	AgencyName             string     `json:"agency_name"`
	GroupID                uint64     `json:"group_id"`
	GroupName              string     `json:"group_name"`
	UserID                 uint64     `json:"user_id"`
	UserNickname           string     `json:"user_nickname"`
	OriginalGrantRequestID uint64     `json:"original_grant_request_id"`
	OriginalLedgerID       uint64     `json:"original_ledger_id"`
	OriginalSaleNo         string     `json:"original_sale_no"`
	OriginalGrantMode      string     `json:"original_grant_mode"`
	OriginalPaymentMethod  string     `json:"original_payment_method"`
	OriginalCreatedAt      time.Time  `json:"original_created_at"`
	Points                 int64      `json:"points"`
	Reason                 string     `json:"reason"`
	RequestStatus          string     `json:"request_status"`
	SubmittedBy            uint64     `json:"submitted_by"`
	SubmitterName          string     `json:"submitter_name"`
	CorrectionLedgerID     uint64     `json:"correction_ledger_id,omitempty"`
	ReviewedBy             uint64     `json:"reviewed_by,omitempty"`
	ReviewRemark           string     `json:"review_remark"`
	ReviewedAt             *time.Time `json:"reviewed_at"`
	CreatedAt              time.Time  `json:"created_at"`
}

func (s *PointsService) SubmitPointCorrection(ctx context.Context, staffID uint64, originalGrantID uint64, reason string, requestID string) (PointCorrectionRequestDTO, error) {
	if originalGrantID == 0 {
		return PointCorrectionRequestDTO{}, xerr.New(400, "invalid_grant_request", "original grant request id is required")
	}
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 2 || utf8.RuneCountInString(reason) > MaxPointRemarkRunes {
		return PointCorrectionRequestDTO{}, xerr.New(400, "invalid_reason", "correction reason must be between 2 and 512 characters")
	}
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return PointCorrectionRequestDTO{}, err
	}
	requestHash := idempotencyHash(strconv.FormatUint(originalGrantID, 10), reason)
	if existing, found, err := s.loadCorrectionByClient(ctx, staffID, requestID, requestHash); found || err != nil {
		return existing, err
	}

	var correctionID uint64
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grant model.PointGrantRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&grant, originalGrantID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "grant_request_not_found", "original point grant request not found")
			}
			return err
		}
		if grant.SubmittedBy != staffID {
			return xerr.New(403, "correction_grant_forbidden", "staff can only correct their own grant")
		}
		if grant.RequestStatus != model.ApprovalStatusApproved || grant.LedgerID == nil || *grant.LedgerID == 0 {
			return xerr.New(409, "grant_not_correctable", "only approved point grants can be corrected")
		}
		if _, err := lockActiveStaffMember(tx, staffID, grant.AgencyID, grant.GroupID); err != nil {
			return err
		}
		var existingCount int64
		if err := tx.Model(&model.PointCorrectionRequest{}).
			Where("original_grant_request_id = ? AND request_status IN ?", grant.ID, []string{model.ApprovalStatusPending, model.ApprovalStatusApproved}).
			Count(&existingCount).Error; err != nil {
			return err
		}
		if existingCount > 0 {
			return xerr.New(409, "correction_already_exists", "an active or approved correction already exists for this grant")
		}
		conflictKey := "point-grant:" + strconv.FormatUint(grant.ID, 10)
		correction := model.PointCorrectionRequest{
			AgencyID:               grant.AgencyID,
			GroupID:                grant.GroupID,
			UserID:                 grant.UserID,
			OriginalGrantRequestID: grant.ID,
			OriginalLedgerID:       *grant.LedgerID,
			Points:                 grant.Points,
			Reason:                 reason,
			RequestStatus:          model.ApprovalStatusPending,
			SubmittedBy:            staffID,
			ClientRequestID:        requestID,
			RequestHash:            requestHash,
			ConflictKey:            &conflictKey,
		}
		if err := tx.Create(&correction).Error; err != nil {
			return err
		}
		correctionID = correction.ID
		return nil
	})
	if err != nil {
		if existing, found, loadErr := s.loadCorrectionByClient(ctx, staffID, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		var businessErr *xerr.Error
		if !errors.As(err, &businessErr) {
			active, found, loadErr := s.loadActiveCorrectionForGrant(ctx, originalGrantID)
			if loadErr != nil {
				return PointCorrectionRequestDTO{}, loadErr
			}
			if found {
				return active, xerr.New(409, "correction_already_exists", "an active or approved correction already exists for this grant")
			}
		}
		return PointCorrectionRequestDTO{}, err
	}
	return s.loadPointCorrectionDTO(ctx, correctionID)
}

type PointCorrectionRequestPage struct {
	Items      []PointCorrectionRequestDTO `json:"items"`
	NextCursor uint64                      `json:"next_cursor"`
	HasMore    bool                        `json:"has_more"`
}

func (s *PointsService) ListOwnPointCorrections(ctx context.Context, staffID uint64, status string, cursor uint64, limit int) (PointCorrectionRequestPage, error) {
	status, err := normalizeCorrectionStatus(status)
	if err != nil {
		return PointCorrectionRequestPage{}, err
	}
	return s.listPointCorrections(ctx, "corrections.submitted_by = ?", staffID, status, cursor, limit)
}

func (s *PointsService) ListReviewPointCorrections(ctx context.Context, reviewerID uint64, groupID uint64, status string, cursor uint64, limit int) (PointCorrectionRequestPage, error) {
	if groupID == 0 {
		return PointCorrectionRequestPage{}, xerr.New(400, "invalid_group", "group_id is required")
	}
	var group model.IdolGroup
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointCorrectionRequestPage{}, xerr.New(404, "group_not_found", "group not found")
		}
		return PointCorrectionRequestPage{}, err
	}
	if err := s.requirePointGrantReviewer(ctx, reviewerID, group.AgencyID, group.ID); err != nil {
		return PointCorrectionRequestPage{}, err
	}
	normalizedStatus, err := normalizeCorrectionStatus(status)
	if err != nil {
		return PointCorrectionRequestPage{}, err
	}
	return s.listPointCorrections(ctx, "corrections.group_id = ?", groupID, normalizedStatus, cursor, limit)
}

func (s *PointsService) ReviewPointCorrection(ctx context.Context, reviewerID uint64, correctionID uint64, approve bool, remark string) (PointCorrectionRequestDTO, error) {
	if correctionID == 0 {
		return PointCorrectionRequestDTO{}, xerr.New(400, "invalid_correction", "correction request id is required")
	}
	remark = strings.TrimSpace(remark)
	if !utf8.ValidString(remark) || utf8.RuneCountInString(remark) > MaxPointRemarkRunes {
		return PointCorrectionRequestDTO{}, xerr.New(400, "invalid_remark", "review remark must not exceed 512 characters")
	}
	if !approve && utf8.RuneCountInString(remark) < 2 {
		return PointCorrectionRequestDTO{}, xerr.New(400, "invalid_review_remark", "rejection remark must be between 2 and 512 characters")
	}
	var initial model.PointCorrectionRequest
	if err := s.db.WithContext(ctx).First(&initial, correctionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointCorrectionRequestDTO{}, xerr.New(404, "correction_not_found", "point correction request not found")
		}
		return PointCorrectionRequestDTO{}, err
	}
	if err := s.requirePointGrantReviewer(ctx, reviewerID, initial.AgencyID, initial.GroupID); err != nil {
		return PointCorrectionRequestDTO{}, err
	}
	if initial.SubmittedBy == reviewerID {
		return PointCorrectionRequestDTO{}, xerr.New(403, "cannot_review_own_request", "reviewer cannot review own correction request")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var correction model.PointCorrectionRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&correction, correctionID).Error; err != nil {
			return err
		}
		isAdmin, isLeader, err := activeReviewerRoleTx(tx, reviewerID, correction.AgencyID, correction.GroupID)
		if err != nil {
			return err
		}
		if (!isAdmin && !isLeader) || correction.SubmittedBy == reviewerID {
			return xerr.New(403, "point_grant_review_forbidden", "reviewer no longer has permission for this group")
		}
		if correction.RequestStatus != model.ApprovalStatusPending {
			if (approve && correction.RequestStatus == model.ApprovalStatusApproved) || (!approve && correction.RequestStatus == model.ApprovalStatusRejected) {
				return nil
			}
			return xerr.New(409, "correction_already_reviewed", "point correction request was already reviewed")
		}
		now := time.Now()
		if !approve {
			return tx.Model(&correction).Updates(map[string]any{
				"request_status": model.ApprovalStatusRejected,
				"reviewed_by":    reviewerID,
				"review_remark":  remark,
				"reviewed_at":    &now,
				"conflict_key":   nil,
			}).Error
		}

		var grant model.PointGrantRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&grant, correction.OriginalGrantRequestID).Error; err != nil {
			return err
		}
		if grant.RequestStatus != model.ApprovalStatusApproved || grant.LedgerID == nil || *grant.LedgerID != correction.OriginalLedgerID || grant.UserID != correction.UserID || grant.Points != correction.Points {
			return xerr.New(409, "correction_source_changed", "original point grant is inconsistent")
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, correction.UserID).Error; err != nil {
			return err
		}
		if user.Status != model.UserStatusNormal {
			return xerr.New(409, "correction_user_disabled", "target user is disabled")
		}
		account, err := lockAgencyPointAccount(tx, correction.AgencyID, correction.UserID)
		if err != nil {
			return err
		}
		if account.Balance < correction.Points {
			return xerr.New(409, "correction_insufficient_points", "user balance is insufficient for this correction")
		}
		before := account.Balance
		after := before - correction.Points
		if err := tx.Model(&account).Update("balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
			return err
		}
		ledger := model.PointLedger{
			AgencyID:     correction.AgencyID,
			GroupID:      correction.GroupID,
			UserID:       correction.UserID,
			BeforePoints: before,
			DeltaPoints:  -correction.Points,
			AfterPoints:  after,
			Type:         PointLedgerGrantReversal,
			OperatorID:   reviewerID,
			ReferenceID:  correction.ID,
			Remark:       "积分冲正：" + correction.Reason,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
		return tx.Model(&correction).Updates(map[string]any{
			"request_status":       model.ApprovalStatusApproved,
			"correction_ledger_id": ledger.ID,
			"reviewed_by":          reviewerID,
			"review_remark":        remark,
			"reviewed_at":          &now,
		}).Error
	})
	if err != nil {
		return PointCorrectionRequestDTO{}, err
	}
	return s.loadPointCorrectionDTO(ctx, correctionID)
}

func (s *PointsService) loadCorrectionByClient(ctx context.Context, staffID uint64, requestID string, requestHash string) (PointCorrectionRequestDTO, bool, error) {
	var correction model.PointCorrectionRequest
	err := s.db.WithContext(ctx).Where("submitted_by = ? AND client_request_id = ?", staffID, requestID).First(&correction).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointCorrectionRequestDTO{}, false, nil
		}
		return PointCorrectionRequestDTO{}, false, err
	}
	if correction.RequestHash != requestHash {
		return PointCorrectionRequestDTO{}, true, xerr.New(409, "idempotency_conflict", "request_id was already used for different request data")
	}
	dto, err := s.loadPointCorrectionDTO(ctx, correction.ID)
	return dto, true, err
}

func (s *PointsService) loadActiveCorrectionForGrant(ctx context.Context, originalGrantID uint64) (PointCorrectionRequestDTO, bool, error) {
	var correction model.PointCorrectionRequest
	err := s.db.WithContext(ctx).
		Where("original_grant_request_id = ? AND request_status IN ?", originalGrantID, []string{model.ApprovalStatusPending, model.ApprovalStatusApproved}).
		Order("id DESC").First(&correction).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointCorrectionRequestDTO{}, false, nil
		}
		return PointCorrectionRequestDTO{}, false, err
	}
	dto, err := s.loadPointCorrectionDTO(ctx, correction.ID)
	return dto, true, err
}

func (s *PointsService) loadPointCorrectionDTO(ctx context.Context, correctionID uint64) (PointCorrectionRequestDTO, error) {
	items, err := s.queryPointCorrections(ctx, "corrections.id = ?", correctionID, "all", 0, 1)
	if err != nil {
		return PointCorrectionRequestDTO{}, err
	}
	if len(items) == 0 {
		return PointCorrectionRequestDTO{}, xerr.New(404, "correction_not_found", "point correction request not found")
	}
	return items[0], nil
}

func (s *PointsService) listPointCorrections(ctx context.Context, condition string, value any, status string, cursor uint64, limit int) (PointCorrectionRequestPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	items, err := s.queryPointCorrections(ctx, condition, value, status, cursor, limit+1)
	if err != nil {
		return PointCorrectionRequestPage{}, err
	}
	page := PointCorrectionRequestPage{Items: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *PointsService) queryPointCorrections(ctx context.Context, condition string, value any, status string, cursor uint64, limit int) ([]PointCorrectionRequestDTO, error) {
	query := s.db.WithContext(ctx).Table("point_correction_requests AS corrections").
		Select("corrections.id, corrections.agency_id, agencies.name AS agency_name, corrections.group_id, idol_groups.name AS group_name, corrections.user_id, COALESCE(NULLIF(target.nickname, ''), '微信用户') AS user_nickname, corrections.original_grant_request_id, corrections.original_ledger_id, COALESCE(grants.sale_no, '') AS original_sale_no, grants.grant_mode AS original_grant_mode, grants.payment_method AS original_payment_method, grants.created_at AS original_created_at, corrections.points, corrections.reason, corrections.request_status, corrections.submitted_by, COALESCE(NULLIF(staff_members.display_name, ''), NULLIF(submitter.nickname, ''), '工作人员') AS submitter_name, COALESCE(corrections.correction_ledger_id, 0) AS correction_ledger_id, corrections.reviewed_by, corrections.review_remark, corrections.reviewed_at, corrections.created_at").
		Joins("JOIN agencies ON agencies.id = corrections.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = corrections.group_id AND idol_groups.agency_id = corrections.agency_id").
		Joins("JOIN point_grant_requests AS grants ON grants.id = corrections.original_grant_request_id").
		Joins("JOIN users AS target ON target.id = corrections.user_id").
		Joins("JOIN users AS submitter ON submitter.id = corrections.submitted_by").
		Joins("LEFT JOIN staff_members ON staff_members.user_id = corrections.submitted_by AND staff_members.agency_id = corrections.agency_id AND staff_members.group_id = corrections.group_id").
		Where(condition, value)
	if status != "all" {
		query = query.Where("corrections.request_status = ?", status)
	}
	if cursor > 0 {
		query = query.Where("corrections.id < ?", cursor)
	}
	items := make([]PointCorrectionRequestDTO, 0)
	err := query.Order("corrections.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func normalizeCorrectionStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = model.ApprovalStatusPending
	}
	if status != "all" && status != model.ApprovalStatusPending && status != model.ApprovalStatusApproved && status != model.ApprovalStatusRejected {
		return "", xerr.New(400, "invalid_status", "correction status is invalid")
	}
	return status, nil
}
