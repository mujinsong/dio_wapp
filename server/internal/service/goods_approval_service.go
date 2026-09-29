package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GoodsApprovalService struct {
	db          *gorm.DB
	authService *AuthService
	agencyID    uint64
}

type StaffGoodsRequestInput struct {
	Action               string     `json:"action"`
	GroupID              uint64     `json:"group_id"`
	GoodsID              uint64     `json:"goods_id"`
	Name                 string     `json:"name"`
	Description          string     `json:"description"`
	ImageURL             string     `json:"image_url"`
	PricePoints          int64      `json:"price_points"`
	Stock                int64      `json:"stock"`
	PurchaseLimitPerUser int64      `json:"purchase_limit_per_user"`
	PurchaseLimitHours   int64      `json:"purchase_limit_hours"`
	SaleStartsAt         *time.Time `json:"sale_starts_at"`
	Status               string     `json:"status"`
	Sort                 int        `json:"sort"`
	RequestID            string     `json:"request_id"`
}

type GoodsChangeRequestDTO struct {
	ID                   uint64     `json:"id"`
	GroupID              uint64     `json:"group_id"`
	GroupName            string     `json:"group_name"`
	GoodsID              uint64     `json:"goods_id"`
	Action               string     `json:"action"`
	Name                 string     `json:"name"`
	Description          string     `json:"description"`
	ImageURL             string     `json:"image_url"`
	PricePoints          int64      `json:"price_points"`
	Stock                int64      `json:"stock"`
	PurchaseLimitPerUser int64      `json:"purchase_limit_per_user"`
	PurchaseLimitHours   int64      `json:"purchase_limit_hours"`
	SaleStartsAt         *time.Time `json:"sale_starts_at"`
	TargetStatus         string     `json:"target_status"`
	Sort                 int        `json:"sort"`
	RequestStatus        string     `json:"request_status"`
	SubmittedBy          uint64     `json:"submitted_by"`
	ReviewedBy           uint64     `json:"reviewed_by"`
	ReviewRemark         string     `json:"review_remark"`
	ReviewedAt           *time.Time `json:"reviewed_at"`
	CreatedAt            time.Time  `json:"created_at"`
}

type GoodsChangeRequestCounts struct {
	Pending  int64 `json:"pending"`
	Approved int64 `json:"approved"`
	Rejected int64 `json:"rejected"`
	All      int64 `json:"all"`
}

type GoodsChangeRequestPage struct {
	Items      []GoodsChangeRequestDTO  `json:"items"`
	NextCursor uint64                   `json:"next_cursor"`
	HasMore    bool                     `json:"has_more"`
	Counts     GoodsChangeRequestCounts `json:"counts"`
}

func NewGoodsApprovalService(db *gorm.DB, authService *AuthService, agencyID uint64) *GoodsApprovalService {
	return &GoodsApprovalService{db: db, authService: authService, agencyID: agencyID}
}

func (s *GoodsApprovalService) ListStaffGoods(ctx context.Context, staffID uint64, groupID uint64) ([]GoodsDTO, error) {
	if _, err := s.requireStaffGroup(ctx, staffID, groupID); err != nil {
		return nil, err
	}
	var rows []GoodsDTO
	err := s.db.WithContext(ctx).
		Table("goods").
		Select("goods.id, goods.agency_id, agencies.name AS agency_name, goods.group_id, idol_groups.name AS group_name, goods.name, goods.description, goods.image_url, goods.price_points, goods.stock, goods.purchase_limit_per_user, goods.purchase_limit_hours, goods.purchase_limit_started_at, goods.sale_starts_at, goods.status, goods.sort").
		Joins("JOIN agencies ON agencies.id = goods.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = goods.group_id AND idol_groups.agency_id = goods.agency_id").
		Where("goods.agency_id = ? AND goods.group_id = ? AND goods.status <> ?", s.agencyID, groupID, model.GoodsStatusDeleted).
		Order("goods.sort DESC, goods.id DESC").
		Scan(&rows).Error
	for index := range rows {
		rows[index].SoldOut = rows[index].Stock <= 0
	}
	return rows, err
}

func (s *GoodsApprovalService) ListStaffRequests(ctx context.Context, staffID uint64, groupID uint64, cursor uint64, limit int) (GoodsChangeRequestPage, error) {
	if _, err := s.requireStaffGroup(ctx, staffID, groupID); err != nil {
		return GoodsChangeRequestPage{}, err
	}
	return s.listRequests(ctx, cursor, limit, "requests.group_id = ? AND requests.submitted_by = ?", groupID, staffID)
}

func (s *GoodsApprovalService) ListTeamLeaderRequests(ctx context.Context, leaderID uint64, groupID uint64, status string, cursor uint64, limit int) (GoodsChangeRequestPage, error) {
	if _, err := s.requireTeamLeaderGroup(ctx, leaderID, groupID); err != nil {
		return GoodsChangeRequestPage{}, err
	}
	status, err := normalizeApprovalStatus(status)
	if err != nil {
		return GoodsChangeRequestPage{}, err
	}
	if status == "all" {
		return s.listRequests(ctx, cursor, limit, "requests.agency_id = ? AND requests.group_id = ?", s.agencyID, groupID)
	}
	return s.listRequests(ctx, cursor, limit, "requests.agency_id = ? AND requests.group_id = ? AND requests.request_status = ?", s.agencyID, groupID, status)
}

func (s *GoodsApprovalService) Submit(ctx context.Context, staffID uint64, input StaffGoodsRequestInput) (GoodsChangeRequestDTO, error) {
	requestID, err := validateRequestID(input.RequestID)
	if err != nil {
		return GoodsChangeRequestDTO{}, err
	}
	input.RequestID = requestID
	input.Action = strings.TrimSpace(input.Action)
	if input.Action != model.GoodsChangeActionCreate && input.Action != model.GoodsChangeActionUpdate && input.Action != model.GoodsChangeActionDelete {
		return GoodsChangeRequestDTO{}, xerr.New(400, "invalid_action", "action must be create, update, or delete")
	}
	requestHash := goodsInputRequestHash(input)
	if existing, found, err := s.findClientRequest(ctx, staffID, requestID, requestHash); found || err != nil {
		return existing, err
	}
	group, err := s.requireStaffGroup(ctx, staffID, input.GroupID)
	if err != nil {
		return GoodsChangeRequestDTO{}, err
	}
	isTeamLeader, err := s.authService.HasTeamLeaderGroup(ctx, staffID, s.agencyID, group.ID)
	if err != nil {
		return GoodsChangeRequestDTO{}, err
	}

	request := model.GoodsChangeRequest{
		AgencyID:        s.agencyID,
		GroupID:         group.ID,
		Action:          input.Action,
		RequestStatus:   model.ApprovalStatusPending,
		SubmittedBy:     staffID,
		ClientRequestID: requestID,
		RequestHash:     requestHash,
	}
	if input.Action == model.GoodsChangeActionCreate {
		if input.GoodsID != 0 {
			return GoodsChangeRequestDTO{}, xerr.New(400, "invalid_goods", "goods_id must be empty for create request")
		}
		copyInputToRequest(&request, input)
		if err := validateRequestedGoods(request); err != nil {
			return GoodsChangeRequestDTO{}, err
		}
	} else if input.Action == model.GoodsChangeActionUpdate || input.Action == model.GoodsChangeActionDelete {
		var goods model.Goods
		if err := s.db.WithContext(ctx).First(&goods, input.GoodsID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return GoodsChangeRequestDTO{}, xerr.New(404, "goods_not_found", "goods not found")
			}
			return GoodsChangeRequestDTO{}, err
		}
		if goods.AgencyID != s.agencyID || goods.GroupID != group.ID || goods.Status == model.GoodsStatusDeleted {
			return GoodsChangeRequestDTO{}, xerr.New(403, "staff_goods_forbidden", "staff cannot manage this goods")
		}
		request.GoodsID = goods.ID
		request.BaseGoodsHash = goodsSnapshotHash(goods)
		conflictKey := fmt.Sprintf("goods:%d", goods.ID)
		request.ConflictKey = &conflictKey
		if input.Action == model.GoodsChangeActionUpdate {
			copyInputToRequest(&request, input)
			if err := validateRequestedGoods(request); err != nil {
				return GoodsChangeRequestDTO{}, err
			}
		} else {
			copyGoodsToRequest(&request, goods)
		}
	}

	if request.ConflictKey != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&model.GoodsChangeRequest{}).
			Where("conflict_key = ? AND request_status = ?", *request.ConflictKey, model.ApprovalStatusPending).
			Count(&count).Error; err != nil {
			return GoodsChangeRequestDTO{}, err
		}
		if count > 0 {
			return GoodsChangeRequestDTO{}, xerr.New(409, "goods_request_pending", "goods already has a pending change request")
		}
	}
	if isTeamLeader {
		if err := s.createAndApplyLeaderRequest(ctx, staffID, &request); err != nil {
			if existing, found, loadErr := s.findClientRequest(ctx, staffID, requestID, requestHash); found || loadErr != nil {
				return existing, loadErr
			}
			return GoodsChangeRequestDTO{}, err
		}
		return s.toRequestDTO(ctx, request)
	}
	if err := s.db.WithContext(ctx).Create(&request).Error; err != nil {
		if existing, found, loadErr := s.findClientRequest(ctx, staffID, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		if request.ConflictKey != nil {
			var count int64
			if loadErr := s.db.WithContext(ctx).Model(&model.GoodsChangeRequest{}).
				Where("conflict_key = ? AND request_status = ?", *request.ConflictKey, model.ApprovalStatusPending).
				Count(&count).Error; loadErr == nil && count > 0 {
				return GoodsChangeRequestDTO{}, xerr.New(409, "goods_request_pending", "goods already has a pending change request")
			}
		}
		return GoodsChangeRequestDTO{}, err
	}
	return s.toRequestDTO(ctx, request)
}

func (s *GoodsApprovalService) ListAdminRequests(ctx context.Context, adminID uint64, status string, cursor uint64, limit int) (GoodsChangeRequestPage, error) {
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return GoodsChangeRequestPage{}, err
	}
	var err error
	status, err = normalizeApprovalStatus(status)
	if err != nil {
		return GoodsChangeRequestPage{}, err
	}
	var page GoodsChangeRequestPage
	if status == "all" {
		page, err = s.listRequests(ctx, cursor, limit, "requests.agency_id = ?", s.agencyID)
	} else {
		page, err = s.listRequests(ctx, cursor, limit, "requests.agency_id = ? AND requests.request_status = ?", s.agencyID, status)
	}
	if err != nil {
		return GoodsChangeRequestPage{}, err
	}
	page.Counts, err = s.requestCounts(ctx, "requests.agency_id = ?", s.agencyID)
	return page, err
}

func (s *GoodsApprovalService) Review(ctx context.Context, reviewerID uint64, requestID uint64, approve bool, remark string) (GoodsChangeRequestDTO, error) {
	if requestID == 0 {
		return GoodsChangeRequestDTO{}, xerr.New(400, "invalid_goods_request", "request id is required")
	}
	remark = strings.TrimSpace(remark)
	if !utf8.ValidString(remark) || utf8.RuneCountInString(remark) > 512 {
		return GoodsChangeRequestDTO{}, xerr.New(400, "invalid_review_remark", "review remark must not exceed 512 characters")
	}
	if !approve && utf8.RuneCountInString(remark) < 2 {
		return GoodsChangeRequestDTO{}, xerr.New(400, "invalid_review_remark", "rejection remark must be between 2 and 512 characters")
	}
	var initial model.GoodsChangeRequest
	if err := s.db.WithContext(ctx).First(&initial, requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GoodsChangeRequestDTO{}, xerr.New(404, "goods_request_not_found", "goods change request not found")
		}
		return GoodsChangeRequestDTO{}, err
	}
	if initial.AgencyID != s.agencyID {
		return GoodsChangeRequestDTO{}, xerr.New(404, "goods_request_not_found", "goods change request not found")
	}
	if err := s.requireRequestReviewer(ctx, reviewerID, initial); err != nil {
		return GoodsChangeRequestDTO{}, err
	}
	if initial.RequestStatus != model.ApprovalStatusPending {
		if (approve && initial.RequestStatus == model.ApprovalStatusApproved) || (!approve && initial.RequestStatus == model.ApprovalStatusRejected) {
			return s.toRequestDTO(ctx, initial)
		}
		return GoodsChangeRequestDTO{}, xerr.New(409, "goods_request_reviewed", "goods change request was already reviewed")
	}
	if approve {
		ok, err := s.authService.HasStaffGroup(ctx, initial.SubmittedBy, initial.AgencyID, initial.GroupID)
		if err != nil {
			return GoodsChangeRequestDTO{}, err
		}
		if !ok {
			return GoodsChangeRequestDTO{}, xerr.New(409, "submitter_permission_changed", "submitter no longer has permission for this group")
		}
	}

	var reviewed model.GoodsChangeRequest
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&reviewed, requestID).Error; err != nil {
			return err
		}
		isAdmin, isLeader, err := activeReviewerRoleTx(tx, reviewerID, reviewed.AgencyID, reviewed.GroupID)
		if err != nil {
			return err
		}
		if (!isAdmin && !isLeader) || (!isAdmin && reviewed.SubmittedBy == reviewerID) {
			return xerr.New(403, "goods_review_forbidden", "reviewer no longer has permission for this goods request")
		}
		if reviewed.RequestStatus != model.ApprovalStatusPending {
			if approve && reviewed.RequestStatus == model.ApprovalStatusApproved {
				return nil
			}
			if !approve && reviewed.RequestStatus == model.ApprovalStatusRejected {
				return nil
			}
			return xerr.New(409, "goods_request_reviewed", "goods change request was already reviewed")
		}
		if approve {
			if _, err := lockActiveStaffMember(tx, reviewed.SubmittedBy, reviewed.AgencyID, reviewed.GroupID); err != nil {
				return xerr.New(409, "submitter_permission_changed", "submitter no longer has permission for this group")
			}
			if err := s.applyRequest(tx, &reviewed, reviewerID); err != nil {
				return err
			}
			reviewed.RequestStatus = model.ApprovalStatusApproved
		} else {
			reviewed.RequestStatus = model.ApprovalStatusRejected
		}
		now := time.Now()
		reviewed.ReviewedBy = reviewerID
		reviewed.ReviewedAt = &now
		reviewed.ReviewRemark = remark
		reviewed.ConflictKey = nil
		return tx.Model(&reviewed).Updates(map[string]any{
			"goods_id":       reviewed.GoodsID,
			"request_status": reviewed.RequestStatus,
			"reviewed_by":    reviewed.ReviewedBy,
			"reviewed_at":    reviewed.ReviewedAt,
			"review_remark":  reviewed.ReviewRemark,
			"conflict_key":   nil,
		}).Error
	})
	if err != nil {
		return GoodsChangeRequestDTO{}, err
	}
	return s.toRequestDTO(ctx, reviewed)
}

func (s *GoodsApprovalService) createAndApplyLeaderRequest(ctx context.Context, leaderID uint64, request *model.GoodsChangeRequest) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(request).Error; err != nil {
			return err
		}
		_, isLeader, err := activeReviewerRoleTx(tx, leaderID, request.AgencyID, request.GroupID)
		if err != nil {
			return err
		}
		if !isLeader {
			return xerr.New(403, "team_leader_group_forbidden", "team leader no longer has permission for this group")
		}
		if err := s.applyRequest(tx, request, leaderID); err != nil {
			return err
		}
		now := time.Now()
		request.RequestStatus = model.ApprovalStatusApproved
		request.ReviewedBy = leaderID
		request.ReviewedAt = &now
		request.ReviewRemark = "团队负责人直接生效"
		request.ConflictKey = nil
		return tx.Model(request).Updates(map[string]any{
			"goods_id":       request.GoodsID,
			"request_status": request.RequestStatus,
			"reviewed_by":    request.ReviewedBy,
			"reviewed_at":    request.ReviewedAt,
			"review_remark":  request.ReviewRemark,
			"conflict_key":   nil,
		}).Error
	})
}

func (s *GoodsApprovalService) applyRequest(tx *gorm.DB, request *model.GoodsChangeRequest, operatorID uint64) error {
	if request.Action == model.GoodsChangeActionCreate {
		if err := s.ensureGroupAvailable(tx, request.GroupID); err != nil {
			return err
		}
		saleStartsAt := normalizeSaleStartsAt(request.TargetStatus, request.SaleStartsAt)
		limitStartedAt := nextPurchaseLimitStart(nil, request.TargetStatus, request.PurchaseLimitPerUser, request.PurchaseLimitHours, saleStartsAt, time.Now())
		goods := model.Goods{
			AgencyID:               request.AgencyID,
			GroupID:                request.GroupID,
			Name:                   request.Name,
			Description:            request.Description,
			ImageURL:               request.ImageURL,
			PricePoints:            request.PricePoints,
			Stock:                  request.Stock,
			PurchaseLimitPerUser:   request.PurchaseLimitPerUser,
			PurchaseLimitHours:     request.PurchaseLimitHours,
			PurchaseLimitStartedAt: limitStartedAt,
			SaleStartsAt:           saleStartsAt,
			Status:                 request.TargetStatus,
			Sort:                   request.Sort,
		}
		if err := tx.Create(&goods).Error; err != nil {
			return err
		}
		request.GoodsID = goods.ID
		return recordStockMovement(tx, goods, 0, goods.Stock, model.StockMovementGoodsApproval, operatorID, request.ID, "商品申请创建初始库存")
	}

	var goods model.Goods
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&goods, request.GoodsID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(409, "goods_changed", "goods no longer exists")
		}
		return err
	}
	if goods.AgencyID != request.AgencyID || goods.GroupID != request.GroupID || goods.Status == model.GoodsStatusDeleted {
		return xerr.New(409, "goods_changed", "goods changed after request was submitted")
	}
	if goodsSnapshotHash(goods) != request.BaseGoodsHash {
		return xerr.New(409, "goods_changed", "goods changed after request was submitted")
	}
	if request.Action == model.GoodsChangeActionDelete {
		return tx.Model(&goods).Update("status", model.GoodsStatusDeleted).Error
	}
	if request.Action != model.GoodsChangeActionUpdate {
		return xerr.New(400, "invalid_action", "goods request action is invalid")
	}
	if err := s.ensureGroupAvailable(tx, request.GroupID); err != nil {
		return err
	}
	saleStartsAt := normalizeSaleStartsAt(request.TargetStatus, request.SaleStartsAt)
	limitStartedAt := nextPurchaseLimitStart(&goods, request.TargetStatus, request.PurchaseLimitPerUser, request.PurchaseLimitHours, saleStartsAt, time.Now())
	beforeStock := goods.Stock
	if err := tx.Model(&goods).Updates(map[string]any{
		"name":                      request.Name,
		"description":               request.Description,
		"image_url":                 request.ImageURL,
		"price_points":              request.PricePoints,
		"stock":                     request.Stock,
		"purchase_limit_per_user":   request.PurchaseLimitPerUser,
		"purchase_limit_hours":      request.PurchaseLimitHours,
		"purchase_limit_started_at": limitStartedAt,
		"sale_starts_at":            saleStartsAt,
		"status":                    request.TargetStatus,
		"sort":                      request.Sort,
	}).Error; err != nil {
		return err
	}
	return recordStockMovement(tx, goods, beforeStock, request.Stock, model.StockMovementGoodsApproval, operatorID, request.ID, "商品申请调整库存")
}

func (s *GoodsApprovalService) ensureGroupAvailable(db *gorm.DB, groupID uint64) error {
	var count int64
	if err := db.Model(&model.IdolGroup{}).
		Where("id = ? AND agency_id = ? AND status = ?", groupID, s.agencyID, model.MemberStatusNormal).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return xerr.New(409, "group_unavailable", "group is unavailable")
	}
	return nil
}

func (s *GoodsApprovalService) requireStaffGroup(ctx context.Context, staffID uint64, groupID uint64) (model.IdolGroup, error) {
	if groupID == 0 {
		return model.IdolGroup{}, xerr.New(400, "invalid_group", "group_id is required")
	}
	var group model.IdolGroup
	if err := s.db.WithContext(ctx).
		Where("id = ? AND agency_id = ? AND status = ?", groupID, s.agencyID, model.MemberStatusNormal).
		First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.IdolGroup{}, xerr.New(404, "group_not_found", "group not found")
		}
		return model.IdolGroup{}, err
	}
	ok, err := s.authService.HasStaffGroup(ctx, staffID, s.agencyID, groupID)
	if err != nil {
		return model.IdolGroup{}, err
	}
	if !ok {
		return model.IdolGroup{}, xerr.New(403, "staff_group_forbidden", "staff has no permission for this group")
	}
	return group, nil
}

func (s *GoodsApprovalService) requireTeamLeaderGroup(ctx context.Context, leaderID uint64, groupID uint64) (model.IdolGroup, error) {
	group, err := s.requireStaffGroup(ctx, leaderID, groupID)
	if err != nil {
		return model.IdolGroup{}, err
	}
	ok, err := s.authService.HasTeamLeaderGroup(ctx, leaderID, s.agencyID, groupID)
	if err != nil {
		return model.IdolGroup{}, err
	}
	if !ok {
		return model.IdolGroup{}, xerr.New(403, "team_leader_group_forbidden", "team leader has no permission for this group")
	}
	return group, nil
}

func (s *GoodsApprovalService) requireAdmin(ctx context.Context, adminID uint64) error {
	ok, err := s.authService.HasAdminAgency(ctx, adminID, s.agencyID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.New(403, "admin_agency_forbidden", "admin has no permission for this agency")
	}
	return nil
}

func (s *GoodsApprovalService) requireRequestReviewer(ctx context.Context, reviewerID uint64, request model.GoodsChangeRequest) error {
	isAdmin, err := s.authService.HasAdminAgency(ctx, reviewerID, request.AgencyID)
	if err != nil {
		return err
	}
	if isAdmin {
		return nil
	}
	isTeamLeader, err := s.authService.HasTeamLeaderGroup(ctx, reviewerID, request.AgencyID, request.GroupID)
	if err != nil {
		return err
	}
	if !isTeamLeader {
		return xerr.New(403, "goods_review_forbidden", "reviewer has no permission for this goods request")
	}
	if request.SubmittedBy == reviewerID {
		return xerr.New(403, "reviewer_cannot_review_self", "team leader cannot review their own request")
	}
	return nil
}

func normalizeApprovalStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = model.ApprovalStatusPending
	}
	if status != "all" && status != model.ApprovalStatusPending && status != model.ApprovalStatusApproved && status != model.ApprovalStatusRejected {
		return "", xerr.New(400, "invalid_approval_status", "approval status is invalid")
	}
	return status, nil
}

func (s *GoodsApprovalService) listRequests(ctx context.Context, cursor uint64, limit int, condition string, values ...any) (GoodsChangeRequestPage, error) {
	limit = normalizeGoodsRequestLimit(limit)
	var rows []GoodsChangeRequestDTO
	query := s.db.WithContext(ctx).
		Table("goods_change_requests AS requests").
		Select("requests.id, requests.group_id, idol_groups.name AS group_name, requests.goods_id, requests.action, requests.name, requests.description, requests.image_url, requests.price_points, requests.stock, requests.purchase_limit_per_user, requests.purchase_limit_hours, requests.sale_starts_at, requests.target_status, requests.sort, requests.request_status, requests.submitted_by, requests.reviewed_by, requests.review_remark, requests.reviewed_at, requests.created_at").
		Joins("JOIN idol_groups ON idol_groups.id = requests.group_id AND idol_groups.agency_id = requests.agency_id").
		Where(condition, values...)
	if cursor > 0 {
		query = query.Where("requests.id < ?", cursor)
	}
	err := query.
		Order("requests.id DESC").
		Limit(limit + 1).
		Scan(&rows).Error
	if err != nil {
		return GoodsChangeRequestPage{}, err
	}
	page := GoodsChangeRequestPage{Items: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Items = rows[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func normalizeGoodsRequestLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func (s *GoodsApprovalService) requestCounts(ctx context.Context, condition string, values ...any) (GoodsChangeRequestCounts, error) {
	var rows []struct {
		Status string
		Total  int64
	}
	err := s.db.WithContext(ctx).
		Table("goods_change_requests AS requests").
		Select("requests.request_status AS status, COUNT(*) AS total").
		Where(condition, values...).
		Group("requests.request_status").
		Scan(&rows).Error
	if err != nil {
		return GoodsChangeRequestCounts{}, err
	}
	var counts GoodsChangeRequestCounts
	for _, row := range rows {
		counts.All += row.Total
		switch row.Status {
		case model.ApprovalStatusPending:
			counts.Pending = row.Total
		case model.ApprovalStatusApproved:
			counts.Approved = row.Total
		case model.ApprovalStatusRejected:
			counts.Rejected = row.Total
		}
	}
	return counts, nil
}

func (s *GoodsApprovalService) toRequestDTO(ctx context.Context, request model.GoodsChangeRequest) (GoodsChangeRequestDTO, error) {
	var group model.IdolGroup
	if err := s.db.WithContext(ctx).Select("id", "name").First(&group, request.GroupID).Error; err != nil {
		return GoodsChangeRequestDTO{}, err
	}
	return GoodsChangeRequestDTO{
		ID:                   request.ID,
		GroupID:              request.GroupID,
		GroupName:            group.Name,
		GoodsID:              request.GoodsID,
		Action:               request.Action,
		Name:                 request.Name,
		Description:          request.Description,
		ImageURL:             request.ImageURL,
		PricePoints:          request.PricePoints,
		Stock:                request.Stock,
		PurchaseLimitPerUser: request.PurchaseLimitPerUser,
		PurchaseLimitHours:   request.PurchaseLimitHours,
		SaleStartsAt:         request.SaleStartsAt,
		TargetStatus:         request.TargetStatus,
		Sort:                 request.Sort,
		RequestStatus:        request.RequestStatus,
		SubmittedBy:          request.SubmittedBy,
		ReviewedBy:           request.ReviewedBy,
		ReviewRemark:         request.ReviewRemark,
		ReviewedAt:           request.ReviewedAt,
		CreatedAt:            request.CreatedAt,
	}, nil
}

func (s *GoodsApprovalService) findClientRequest(ctx context.Context, staffID uint64, clientRequestID string, requestHash string) (GoodsChangeRequestDTO, bool, error) {
	var request model.GoodsChangeRequest
	err := s.db.WithContext(ctx).
		Where("submitted_by = ? AND client_request_id = ?", staffID, clientRequestID).
		First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return GoodsChangeRequestDTO{}, false, nil
	}
	if err != nil {
		return GoodsChangeRequestDTO{}, false, err
	}
	if request.RequestHash != requestHash {
		return GoodsChangeRequestDTO{}, true, xerr.New(409, "idempotency_conflict", "request_id was already used for different request data")
	}
	dto, err := s.toRequestDTO(ctx, request)
	return dto, true, err
}

func copyInputToRequest(request *model.GoodsChangeRequest, input StaffGoodsRequestInput) {
	request.Name = strings.TrimSpace(input.Name)
	request.Description = strings.TrimSpace(input.Description)
	request.ImageURL = strings.TrimSpace(input.ImageURL)
	request.PricePoints = input.PricePoints
	request.Stock = input.Stock
	request.PurchaseLimitPerUser = input.PurchaseLimitPerUser
	request.PurchaseLimitHours = input.PurchaseLimitHours
	request.TargetStatus = normalizeGoodsStatus(input.Status)
	request.SaleStartsAt = normalizeSaleStartsAt(request.TargetStatus, input.SaleStartsAt)
	request.Sort = input.Sort
}

func copyGoodsToRequest(request *model.GoodsChangeRequest, goods model.Goods) {
	request.Name = goods.Name
	request.Description = goods.Description
	request.ImageURL = goods.ImageURL
	request.PricePoints = goods.PricePoints
	request.Stock = goods.Stock
	request.PurchaseLimitPerUser = goods.PurchaseLimitPerUser
	request.PurchaseLimitHours = goods.PurchaseLimitHours
	request.SaleStartsAt = goods.SaleStartsAt
	request.TargetStatus = goods.Status
	request.Sort = goods.Sort
}

func validateRequestedGoods(request model.GoodsChangeRequest) error {
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 128 {
		return xerr.New(400, "invalid_name", "goods name is required and must not exceed 128 characters")
	}
	if utf8.RuneCountInString(request.Description) > 512 || utf8.RuneCountInString(request.ImageURL) > 512 {
		return xerr.New(400, "invalid_goods_text", "goods description or image URL is too long")
	}
	if !validGoodsImageURL(request.ImageURL) {
		return xerr.New(400, "invalid_image_url", "image_url must be a valid HTTP or HTTPS URL")
	}
	if request.PricePoints <= 0 || request.PricePoints > MaxGoodsPricePoints {
		return xerr.New(400, "invalid_price", "price_points is invalid")
	}
	if request.Stock < 0 || request.Stock > MaxGoodsStock {
		return xerr.New(400, "invalid_stock", "stock is invalid")
	}
	if request.PurchaseLimitPerUser < 0 || request.PurchaseLimitPerUser > MaxPurchaseLimitPerUser ||
		request.PurchaseLimitHours < 0 || request.PurchaseLimitHours > MaxPurchaseLimitHours {
		return xerr.New(400, "invalid_purchase_limit", "purchase limit is invalid")
	}
	if (request.PurchaseLimitPerUser == 0) != (request.PurchaseLimitHours == 0) {
		return xerr.New(400, "invalid_purchase_limit_window", "purchase limit count and hours must both be zero or greater than zero")
	}
	if request.TargetStatus != model.GoodsStatusOnSale && request.TargetStatus != model.GoodsStatusOffSale {
		return xerr.New(400, "invalid_status", "status is invalid")
	}
	return nil
}

func validGoodsImageURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func goodsSnapshotHash(goods model.Goods) string {
	startedAt := ""
	if goods.PurchaseLimitStartedAt != nil {
		startedAt = goods.PurchaseLimitStartedAt.UTC().Format(time.RFC3339Nano)
	}
	saleStartsAt := ""
	if goods.SaleStartsAt != nil {
		saleStartsAt = goods.SaleStartsAt.UTC().Format(time.RFC3339Nano)
	}
	return idempotencyHash(
		strconv.FormatUint(goods.AgencyID, 10), strconv.FormatUint(goods.GroupID, 10),
		goods.Name, goods.Description, goods.ImageURL,
		strconv.FormatInt(goods.PricePoints, 10), strconv.FormatInt(goods.Stock, 10),
		strconv.FormatInt(goods.PurchaseLimitPerUser, 10), strconv.FormatInt(goods.PurchaseLimitHours, 10),
		startedAt, saleStartsAt, goods.Status, strconv.Itoa(goods.Sort),
	)
}

func goodsInputRequestHash(input StaffGoodsRequestInput) string {
	parts := []string{
		strings.TrimSpace(input.Action),
		strconv.FormatUint(input.GroupID, 10),
		strconv.FormatUint(input.GoodsID, 10),
	}
	if input.Action == model.GoodsChangeActionCreate || input.Action == model.GoodsChangeActionUpdate {
		saleStartsAt := ""
		if normalized := normalizeSaleStartsAt(normalizeGoodsStatus(input.Status), input.SaleStartsAt); normalized != nil {
			saleStartsAt = normalized.UTC().Format(time.RFC3339Nano)
		}
		parts = append(parts,
			strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.TrimSpace(input.ImageURL),
			strconv.FormatInt(input.PricePoints, 10), strconv.FormatInt(input.Stock, 10),
			strconv.FormatInt(input.PurchaseLimitPerUser, 10), strconv.FormatInt(input.PurchaseLimitHours, 10),
			normalizeGoodsStatus(input.Status), saleStartsAt, strconv.Itoa(input.Sort),
		)
	}
	return idempotencyHash(parts...)
}
