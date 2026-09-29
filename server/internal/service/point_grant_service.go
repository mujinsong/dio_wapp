package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PointGrantApprovalThreshold = int64(10_000)
	StaffDailyGrantLimit        = int64(200_000)
)

type StaffGrantInput struct {
	AgencyID        uint64 `json:"agency_id"`
	GroupID         uint64 `json:"group_id"`
	IdentityToken   string `json:"identity_token"`
	Points          int64  `json:"points"`
	GrantMode       string `json:"grant_mode"`
	TicketUnitPrice int64  `json:"ticket_unit_price"`
	TicketCount     int64  `json:"ticket_count"`
	PaymentMethod   string `json:"payment_method"`
	Remark          string `json:"remark"`
	RequestID       string `json:"request_id"`
}

type PointGrantRequestDTO struct {
	ID              uint64     `json:"id"`
	AgencyID        uint64     `json:"agency_id"`
	AgencyName      string     `json:"agency_name"`
	GroupID         uint64     `json:"group_id"`
	GroupName       string     `json:"group_name"`
	UserID          uint64     `json:"user_id"`
	UserNickname    string     `json:"user_nickname"`
	SubmittedBy     uint64     `json:"submitted_by"`
	SubmitterName   string     `json:"submitter_name"`
	GrantMode       string     `json:"grant_mode"`
	SaleNo          string     `json:"sale_no"`
	TicketUnitPrice int64      `json:"ticket_unit_price"`
	TicketCount     int64      `json:"ticket_count"`
	TotalAmount     int64      `json:"total_amount"`
	Points          int64      `json:"points"`
	PaymentMethod   string     `json:"payment_method"`
	Remark          string     `json:"remark"`
	RequestStatus   string     `json:"request_status"`
	LedgerID        uint64     `json:"ledger_id,omitempty"`
	ReviewedBy      uint64     `json:"reviewed_by,omitempty"`
	ReviewRemark    string     `json:"review_remark"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (s *PointsService) SubmitStaffGrant(ctx context.Context, operatorID uint64, input StaffGrantInput) (AddPointsResult, error) {
	if err := normalizeAndValidateStaffGrant(&input); err != nil {
		return AddPointsResult{}, err
	}
	identityToken, err := validateIdentityToken(input.IdentityToken)
	if err != nil {
		return AddPointsResult{}, err
	}
	requestID, err := validateRequestID(input.RequestID)
	if err != nil {
		return AddPointsResult{}, err
	}
	input.IdentityToken = identityToken
	input.RequestID = requestID

	ok, err := s.authService.HasStaffGroup(ctx, operatorID, input.AgencyID, input.GroupID)
	if err != nil {
		return AddPointsResult{}, err
	}
	if !ok {
		return AddPointsResult{}, xerr.New(403, "staff_group_forbidden", "staff has no permission for this group")
	}
	requestHash := staffGrantRequestHash(input)
	if existing, found, err := loadIdempotentResult[AddPointsResult](ctx, s.db, operatorID, idempotencyStaffAdd, requestID, requestHash); found || err != nil {
		return existing, err
	}

	var result AddPointsResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, operatorID, idempotencyStaffAdd, requestID, requestHash)
		if err != nil {
			return err
		}

		member, err := lockActiveStaffMember(tx, operatorID, input.AgencyID, input.GroupID)
		if err != nil {
			return err
		}
		if err := reserveStaffDailyGrant(tx, operatorID, input.Points, time.Now()); err != nil {
			return err
		}

		var qrToken model.IdentityQRToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", tokenHash(identityToken)).First(&qrToken).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "qr_token_not_found", "identity QR code is invalid")
			}
			return err
		}
		if time.Now().After(qrToken.ExpiresAt) {
			return xerr.New(410, "qr_token_expired", "identity QR code has expired")
		}
		if qrToken.UserID == operatorID {
			return xerr.New(400, "cannot_add_self", "staff cannot add points to self")
		}

		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, qrToken.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "user_not_found", "user not found")
			}
			return err
		}
		if user.Status != model.UserStatusNormal {
			return xerr.New(403, "user_disabled", "user is disabled")
		}

		requestStatus := model.ApprovalStatusApproved
		if input.Points >= PointGrantApprovalThreshold && member.Role != model.StaffRoleTeamLeader {
			requestStatus = model.ApprovalStatusPending
		}
		grant := model.PointGrantRequest{
			AgencyID:        input.AgencyID,
			GroupID:         input.GroupID,
			UserID:          user.ID,
			SubmittedBy:     operatorID,
			GrantMode:       input.GrantMode,
			TicketUnitPrice: input.TicketUnitPrice,
			TicketCount:     input.TicketCount,
			TotalAmount:     input.TicketUnitPrice * input.TicketCount,
			Points:          input.Points,
			PaymentMethod:   input.PaymentMethod,
			Remark:          input.Remark,
			RequestStatus:   requestStatus,
			ClientRequestID: requestID,
		}
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
		if grant.GrantMode == model.PointGrantModeTicket {
			saleNo := buildTicketSaleNo(grant.ID, grant.CreatedAt)
			if err := tx.Model(&grant).Update("sale_no", saleNo).Error; err != nil {
				return err
			}
			grant.SaleNo = &saleNo
		}
		if err := tx.Delete(&qrToken).Error; err != nil {
			return err
		}

		result = AddPointsResult{
			AgencyID:        input.AgencyID,
			UserID:          user.ID,
			DeltaPoints:     input.Points,
			GrantRequestID:  grant.ID,
			RequestStatus:   requestStatus,
			PendingApproval: requestStatus == model.ApprovalStatusPending,
			SaleNo:          valueOrEmpty(grant.SaleNo),
		}
		if requestStatus == model.ApprovalStatusApproved {
			applied, err := applyPointGrantTx(tx, &grant, operatorID)
			if err != nil {
				return err
			}
			result.BeforePoints = applied.BeforePoints
			result.AfterPoints = applied.AfterPoints
			result.LedgerID = applied.LedgerID
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[AddPointsResult](ctx, s.db, operatorID, idempotencyStaffAdd, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return AddPointsResult{}, err
	}
	return result, nil
}

type PointGrantRequestPage struct {
	Items      []PointGrantRequestDTO `json:"items"`
	NextCursor uint64                 `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
}

func (s *PointsService) ListPointGrantRequests(ctx context.Context, reviewerID uint64, groupID uint64, status string, cursor uint64, limit int) (PointGrantRequestPage, error) {
	if groupID == 0 {
		return PointGrantRequestPage{}, xerr.New(400, "invalid_group", "group_id is required")
	}
	var group model.IdolGroup
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointGrantRequestPage{}, xerr.New(404, "group_not_found", "group not found")
		}
		return PointGrantRequestPage{}, err
	}
	if err := s.requirePointGrantReviewer(ctx, reviewerID, group.AgencyID, group.ID); err != nil {
		return PointGrantRequestPage{}, err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = model.ApprovalStatusPending
	}
	if status != "all" && status != model.ApprovalStatusPending && status != model.ApprovalStatusApproved && status != model.ApprovalStatusRejected {
		return PointGrantRequestPage{}, xerr.New(400, "invalid_status", "request status is invalid")
	}

	query := s.db.WithContext(ctx).
		Table("point_grant_requests AS grants").
		Select("grants.id, grants.agency_id, agencies.name AS agency_name, grants.group_id, idol_groups.name AS group_name, grants.user_id, target.nickname AS user_nickname, grants.submitted_by, COALESCE(NULLIF(staff_members.display_name, ''), submitter.nickname) AS submitter_name, grants.grant_mode, COALESCE(grants.sale_no, '') AS sale_no, grants.ticket_unit_price, grants.ticket_count, grants.total_amount, grants.points, grants.payment_method, grants.remark, grants.request_status, COALESCE(grants.ledger_id, 0) AS ledger_id, grants.reviewed_by, grants.review_remark, grants.reviewed_at, grants.created_at").
		Joins("JOIN agencies ON agencies.id = grants.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = grants.group_id AND idol_groups.agency_id = grants.agency_id").
		Joins("JOIN users AS target ON target.id = grants.user_id").
		Joins("JOIN users AS submitter ON submitter.id = grants.submitted_by").
		Joins("LEFT JOIN staff_members ON staff_members.user_id = grants.submitted_by AND staff_members.agency_id = grants.agency_id AND staff_members.group_id = grants.group_id").
		Where("grants.group_id = ? AND grants.agency_id = ?", group.ID, group.AgencyID)
	if status != "all" {
		query = query.Where("grants.request_status = ?", status)
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	if cursor > 0 {
		query = query.Where("grants.id < ?", cursor)
	}
	page := PointGrantRequestPage{Items: make([]PointGrantRequestDTO, 0)}
	if err := query.Order("grants.id DESC").Limit(limit + 1).Scan(&page.Items).Error; err != nil {
		return PointGrantRequestPage{}, err
	}
	page.HasMore = len(page.Items) > limit
	if page.HasMore {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *PointsService) ReviewPointGrant(ctx context.Context, reviewerID uint64, requestID uint64, approve bool, remark string) (PointGrantRequestDTO, error) {
	if requestID == 0 {
		return PointGrantRequestDTO{}, xerr.New(400, "invalid_request", "request id is required")
	}
	remark = strings.TrimSpace(remark)
	if !utf8.ValidString(remark) || utf8.RuneCountInString(remark) > MaxPointRemarkRunes {
		return PointGrantRequestDTO{}, xerr.New(400, "invalid_remark", "review remark must not exceed 512 characters")
	}
	if !approve && utf8.RuneCountInString(remark) < 2 {
		return PointGrantRequestDTO{}, xerr.New(400, "invalid_review_remark", "rejection remark must be between 2 and 512 characters")
	}
	var initial model.PointGrantRequest
	if err := s.db.WithContext(ctx).First(&initial, requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PointGrantRequestDTO{}, xerr.New(404, "grant_request_not_found", "point grant request not found")
		}
		return PointGrantRequestDTO{}, err
	}
	if err := s.requirePointGrantReviewer(ctx, reviewerID, initial.AgencyID, initial.GroupID); err != nil {
		return PointGrantRequestDTO{}, err
	}
	if initial.SubmittedBy == reviewerID {
		return PointGrantRequestDTO{}, xerr.New(403, "cannot_review_own_request", "reviewer cannot review own request")
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grant model.PointGrantRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&grant, requestID).Error; err != nil {
			return err
		}
		isAdmin, isLeader, err := activeReviewerRoleTx(tx, reviewerID, grant.AgencyID, grant.GroupID)
		if err != nil {
			return err
		}
		if (!isAdmin && !isLeader) || grant.SubmittedBy == reviewerID {
			return xerr.New(403, "point_grant_review_forbidden", "reviewer no longer has permission for this group")
		}
		if grant.RequestStatus != model.ApprovalStatusPending {
			if (approve && grant.RequestStatus == model.ApprovalStatusApproved) || (!approve && grant.RequestStatus == model.ApprovalStatusRejected) {
				return nil
			}
			return xerr.New(409, "grant_request_already_reviewed", "point grant request was already reviewed")
		}
		now := time.Now()
		if approve {
			if _, err := lockActiveStaffMember(tx, grant.SubmittedBy, grant.AgencyID, grant.GroupID); err != nil {
				return xerr.New(409, "grant_submitter_inactive", "grant submitter no longer has active group permission")
			}
			if _, err := applyPointGrantTx(tx, &grant, reviewerID); err != nil {
				return err
			}
			if remark != "" {
				if err := tx.Model(&grant).Update("review_remark", remark).Error; err != nil {
					return err
				}
			}
			grant.RequestStatus = model.ApprovalStatusApproved
		} else {
			if err := releaseStaffDailyGrant(tx, grant.SubmittedBy, grant.Points, grant.CreatedAt); err != nil {
				return err
			}
			if err := tx.Model(&grant).Updates(map[string]any{
				"request_status": model.ApprovalStatusRejected,
				"reviewed_by":    reviewerID,
				"review_remark":  remark,
				"reviewed_at":    &now,
			}).Error; err != nil {
				return err
			}
			grant.RequestStatus = model.ApprovalStatusRejected
		}
		return nil
	})
	if err != nil {
		return PointGrantRequestDTO{}, err
	}
	return s.loadPointGrantRequestDTO(ctx, requestID)
}

func applyPointGrantTx(tx *gorm.DB, grant *model.PointGrantRequest, reviewerID uint64) (AddPointsResult, error) {
	var user model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, grant.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AddPointsResult{}, xerr.New(404, "user_not_found", "user not found")
		}
		return AddPointsResult{}, err
	}
	if user.Status != model.UserStatusNormal {
		return AddPointsResult{}, xerr.New(403, "user_disabled", "user is disabled")
	}
	account, err := lockAgencyPointAccount(tx, grant.AgencyID, grant.UserID)
	if err != nil {
		return AddPointsResult{}, err
	}
	before := account.Balance
	if before < 0 || before > MaxPointBalance || grant.Points > MaxPointBalance-before {
		return AddPointsResult{}, xerr.New(400, "points_balance_limit", "points balance would exceed the allowed limit")
	}
	after := before + grant.Points
	if err := tx.Model(&account).Update("balance", after).Error; err != nil {
		return AddPointsResult{}, err
	}
	if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
		return AddPointsResult{}, err
	}
	ledger := model.PointLedger{
		AgencyID:     grant.AgencyID,
		GroupID:      grant.GroupID,
		UserID:       grant.UserID,
		BeforePoints: before,
		DeltaPoints:  grant.Points,
		AfterPoints:  after,
		Type:         PointLedgerStaffAdd,
		OperatorID:   grant.SubmittedBy,
		ReferenceID:  grant.ID,
		Remark:       grant.Remark,
	}
	if err := tx.Create(&ledger).Error; err != nil {
		return AddPointsResult{}, err
	}
	now := time.Now()
	updates := map[string]any{
		"request_status": model.ApprovalStatusApproved,
		"ledger_id":      ledger.ID,
	}
	if reviewerID != 0 && reviewerID != grant.SubmittedBy {
		updates["reviewed_by"] = reviewerID
		updates["reviewed_at"] = &now
	}
	if err := tx.Model(grant).Updates(updates).Error; err != nil {
		return AddPointsResult{}, err
	}
	grant.LedgerID = &ledger.ID
	grant.RequestStatus = model.ApprovalStatusApproved
	return AddPointsResult{
		AgencyID:       grant.AgencyID,
		UserID:         grant.UserID,
		BeforePoints:   before,
		DeltaPoints:    grant.Points,
		AfterPoints:    after,
		LedgerID:       ledger.ID,
		GrantRequestID: grant.ID,
		RequestStatus:  model.ApprovalStatusApproved,
		SaleNo:         valueOrEmpty(grant.SaleNo),
	}, nil
}

func normalizeAndValidateStaffGrant(input *StaffGrantInput) error {
	if input.AgencyID == 0 || input.GroupID == 0 {
		return xerr.New(400, "invalid_group", "agency_id and group_id are required")
	}
	input.GrantMode = strings.TrimSpace(input.GrantMode)
	if input.GrantMode == "" {
		input.GrantMode = model.PointGrantModeManual
	}
	input.PaymentMethod = strings.TrimSpace(input.PaymentMethod)
	input.Remark = strings.TrimSpace(input.Remark)
	if !utf8.ValidString(input.Remark) || utf8.RuneCountInString(input.Remark) > MaxPointRemarkRunes {
		return xerr.New(400, "invalid_remark", "remark must not exceed 512 characters")
	}
	if input.GrantMode == model.PointGrantModeTicket {
		if input.TicketUnitPrice <= 0 || input.TicketCount <= 0 || input.TicketUnitPrice > MaxPointsPerGrant || input.TicketCount > MaxPointsPerGrant {
			return xerr.New(400, "invalid_ticket_sale", "ticket price or count is invalid")
		}
		if input.TicketUnitPrice > MaxPointsPerGrant/input.TicketCount {
			return xerr.New(400, "points_too_large", "ticket sale points is too large")
		}
		input.Points = input.TicketUnitPrice * input.TicketCount
		if !validPaymentMethod(input.PaymentMethod) {
			return xerr.New(400, "invalid_payment_method", "payment method is invalid")
		}
	} else if input.GrantMode == model.PointGrantModeManual {
		input.TicketUnitPrice = 0
		input.TicketCount = 0
		input.PaymentMethod = ""
	} else {
		return xerr.New(400, "invalid_grant_mode", "grant mode is invalid")
	}
	if input.Points <= 0 {
		return xerr.New(400, "invalid_points", "points must be greater than 0")
	}
	if input.Points > MaxPointsPerGrant {
		return xerr.New(400, "points_too_large", "points is too large")
	}
	return nil
}

func validPaymentMethod(value string) bool {
	switch value {
	case "cash", "wechat", "alipay", "card", "other":
		return true
	default:
		return false
	}
}

func lockActiveStaffMember(tx *gorm.DB, userID uint64, agencyID uint64, groupID uint64) (model.StaffMember, error) {
	var member model.StaffMember
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND agency_id = ? AND group_id = ? AND status = ?", userID, agencyID, groupID, model.MemberStatusNormal).
		First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.StaffMember{}, xerr.New(403, "staff_group_forbidden", "staff has no permission for this group")
		}
		return model.StaffMember{}, err
	}
	var count int64
	if err := tx.Model(&model.IdolGroup{}).
		Joins("JOIN agencies ON agencies.id = idol_groups.agency_id").
		Where("idol_groups.id = ? AND idol_groups.agency_id = ?", groupID, agencyID).
		Where("idol_groups.status = ? AND agencies.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Count(&count).Error; err != nil {
		return model.StaffMember{}, err
	}
	if count == 0 {
		return model.StaffMember{}, xerr.New(403, "staff_group_forbidden", "staff group is unavailable")
	}
	return member, nil
}

func reserveStaffDailyGrant(tx *gorm.DB, staffID uint64, points int64, now time.Time) error {
	local := now.In(time.Local)
	grantDate := local.Format("2006-01-02")
	seed := model.StaffPointDailyUsage{StaffID: staffID, GrantDate: grantDate}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return err
	}
	var usage model.StaffPointDailyUsage
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("staff_id = ? AND grant_date = ?", staffID, grantDate).First(&usage).Error; err != nil {
		return err
	}
	if usage.ReservedPoints < 0 || points > StaffDailyGrantLimit-usage.ReservedPoints {
		return xerr.New(429, "staff_daily_points_limit", "staff daily point grant limit would be exceeded")
	}
	return tx.Model(&usage).Update("reserved_points", usage.ReservedPoints+points).Error
}

func releaseStaffDailyGrant(tx *gorm.DB, staffID uint64, points int64, createdAt time.Time) error {
	grantDate := createdAt.In(time.Local).Format("2006-01-02")
	var usage model.StaffPointDailyUsage
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("staff_id = ? AND grant_date = ?", staffID, grantDate).First(&usage).Error; err != nil {
		return err
	}
	if points < 0 || usage.ReservedPoints < points {
		return xerr.New(409, "staff_daily_usage_inconsistent", "staff daily point usage is inconsistent")
	}
	return tx.Model(&usage).Update("reserved_points", usage.ReservedPoints-points).Error
}

func staffGrantRequestHash(input StaffGrantInput) string {
	return idempotencyHash(
		strconv.FormatUint(input.AgencyID, 10),
		strconv.FormatUint(input.GroupID, 10),
		tokenHash(input.IdentityToken),
		input.GrantMode,
		strconv.FormatInt(input.Points, 10),
		strconv.FormatInt(input.TicketUnitPrice, 10),
		strconv.FormatInt(input.TicketCount, 10),
		input.PaymentMethod,
		input.Remark,
	)
}

func (s *PointsService) requirePointGrantReviewer(ctx context.Context, reviewerID uint64, agencyID uint64, groupID uint64) error {
	isLeader, err := s.authService.HasTeamLeaderGroup(ctx, reviewerID, agencyID, groupID)
	if err != nil {
		return err
	}
	if isLeader {
		return nil
	}
	isAdmin, err := s.authService.HasAdminAgency(ctx, reviewerID, agencyID)
	if err != nil {
		return err
	}
	if !isAdmin {
		return xerr.New(403, "point_grant_review_forbidden", "reviewer has no permission for this group")
	}
	return nil
}

func (s *PointsService) loadPointGrantRequestDTO(ctx context.Context, requestID uint64) (PointGrantRequestDTO, error) {
	var result PointGrantRequestDTO
	err := s.db.WithContext(ctx).
		Table("point_grant_requests AS grants").
		Select("grants.id, grants.agency_id, agencies.name AS agency_name, grants.group_id, idol_groups.name AS group_name, grants.user_id, target.nickname AS user_nickname, grants.submitted_by, COALESCE(NULLIF(staff_members.display_name, ''), submitter.nickname) AS submitter_name, grants.grant_mode, COALESCE(grants.sale_no, '') AS sale_no, grants.ticket_unit_price, grants.ticket_count, grants.total_amount, grants.points, grants.payment_method, grants.remark, grants.request_status, COALESCE(grants.ledger_id, 0) AS ledger_id, grants.reviewed_by, grants.review_remark, grants.reviewed_at, grants.created_at").
		Joins("JOIN agencies ON agencies.id = grants.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = grants.group_id AND idol_groups.agency_id = grants.agency_id").
		Joins("JOIN users AS target ON target.id = grants.user_id").
		Joins("JOIN users AS submitter ON submitter.id = grants.submitted_by").
		Joins("LEFT JOIN staff_members ON staff_members.user_id = grants.submitted_by AND staff_members.agency_id = grants.agency_id AND staff_members.group_id = grants.group_id").
		Where("grants.id = ?", requestID).Scan(&result).Error
	return result, err
}

func buildTicketSaleNo(id uint64, createdAt time.Time) string {
	return fmt.Sprintf("TS%s%08d", createdAt.Format("20060102"), id)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
