package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

const staffWorkbenchDateLayout = "2006-01-02"

type StaffWorkbenchService struct {
	db          *gorm.DB
	authService *AuthService
}

type StaffWorkbenchSummary struct {
	DailyLimit          int64 `json:"daily_limit"`
	DailyUsed           int64 `json:"daily_used"`
	DailyRemaining      int64 `json:"daily_remaining"`
	TicketSalesCount    int64 `json:"ticket_sales_count"`
	TicketSalesAmount   int64 `json:"ticket_sales_amount"`
	ApprovedGrantCount  int64 `json:"approved_grant_count"`
	ApprovedPoints      int64 `json:"approved_points"`
	CorrectedGrantCount int64 `json:"corrected_grant_count"`
	CorrectedPoints     int64 `json:"corrected_points"`
	PendingGrantCount   int64 `json:"pending_grant_count"`
	PendingPoints       int64 `json:"pending_points"`
	RejectedGrantCount  int64 `json:"rejected_grant_count"`
	RedeemCount         int64 `json:"redeem_count"`
	RedeemedPoints      int64 `json:"redeemed_points"`
}

type StaffPaymentSummary struct {
	PaymentMethod string `json:"payment_method"`
	SaleCount     int64  `json:"sale_count"`
	Amount        int64  `json:"amount"`
}

type StaffGrantRecord struct {
	ID               uint64    `json:"id"`
	UserID           uint64    `json:"user_id"`
	UserNickname     string    `json:"user_nickname"`
	GrantMode        string    `json:"grant_mode"`
	SaleNo           string    `json:"sale_no"`
	TicketUnitPrice  int64     `json:"ticket_unit_price"`
	TicketCount      int64     `json:"ticket_count"`
	TotalAmount      int64     `json:"total_amount"`
	Points           int64     `json:"points"`
	PaymentMethod    string    `json:"payment_method"`
	Remark           string    `json:"remark"`
	RequestStatus    string    `json:"request_status"`
	ReviewRemark     string    `json:"review_remark"`
	CorrectionStatus string    `json:"correction_status"`
	CreatedAt        time.Time `json:"created_at"`
}

type StaffRedeemRecord struct {
	OrderID      uint64    `json:"order_id"`
	OrderNo      string    `json:"order_no"`
	UserID       uint64    `json:"user_id"`
	UserNickname string    `json:"user_nickname"`
	GoodsID      uint64    `json:"goods_id"`
	GoodsName    string    `json:"goods_name"`
	PointsCost   int64     `json:"points_cost"`
	RedeemedAt   time.Time `json:"redeemed_at"`
}

type StaffWorkbench struct {
	AgencyID       uint64                `json:"agency_id"`
	GroupID        uint64                `json:"group_id"`
	Date           string                `json:"date"`
	Summary        StaffWorkbenchSummary `json:"summary"`
	PaymentSummary []StaffPaymentSummary `json:"payment_summary"`
	GrantRecords   []StaffGrantRecord    `json:"grant_records"`
	RedeemRecords  []StaffRedeemRecord   `json:"redeem_records"`
}

func NewStaffWorkbenchService(db *gorm.DB, authService *AuthService) *StaffWorkbenchService {
	return &StaffWorkbenchService{db: db, authService: authService}
}

func (s *StaffWorkbenchService) Get(ctx context.Context, staffID uint64, agencyID uint64, groupID uint64, dateValue string) (StaffWorkbench, error) {
	if agencyID == 0 || groupID == 0 {
		return StaffWorkbench{}, xerr.New(400, "invalid_group", "agency_id and group_id are required")
	}
	ok, err := s.authService.HasStaffGroup(ctx, staffID, agencyID, groupID)
	if err != nil {
		return StaffWorkbench{}, err
	}
	if !ok {
		return StaffWorkbench{}, xerr.New(403, "staff_group_forbidden", "staff has no permission for this group")
	}
	dayStart, dayEnd, err := parseStaffWorkbenchDate(dateValue, time.Now())
	if err != nil {
		return StaffWorkbench{}, err
	}
	result := StaffWorkbench{
		AgencyID:       agencyID,
		GroupID:        groupID,
		Date:           dayStart.Format(staffWorkbenchDateLayout),
		PaymentSummary: make([]StaffPaymentSummary, 0),
		GrantRecords:   make([]StaffGrantRecord, 0),
		RedeemRecords:  make([]StaffRedeemRecord, 0),
	}
	result.Summary.DailyLimit = StaffDailyGrantLimit
	var usage model.StaffPointDailyUsage
	err = s.db.WithContext(ctx).Where("staff_id = ? AND grant_date = ?", staffID, result.Date).First(&usage).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return StaffWorkbench{}, err
	}
	result.Summary.DailyUsed = usage.ReservedPoints
	result.Summary.DailyRemaining = StaffDailyGrantLimit - usage.ReservedPoints
	if result.Summary.DailyRemaining < 0 {
		result.Summary.DailyRemaining = 0
	}

	grantRange := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&model.PointGrantRequest{}).
			Where("submitted_by = ? AND agency_id = ? AND group_id = ? AND created_at >= ? AND created_at < ?", staffID, agencyID, groupID, dayStart, dayEnd)
	}
	var grantSummary struct {
		TicketSalesCount    int64
		TicketSalesAmount   int64
		ApprovedGrantCount  int64
		ApprovedPoints      int64
		CorrectedGrantCount int64
		CorrectedPoints     int64
		PendingGrantCount   int64
		PendingPoints       int64
		RejectedGrantCount  int64
	}
	if err := grantRange().Select(`
		COALESCE(SUM(CASE WHEN grant_mode = ? AND request_status <> ? THEN 1 ELSE 0 END), 0) AS ticket_sales_count,
		COALESCE(SUM(CASE WHEN grant_mode = ? AND request_status <> ? THEN total_amount ELSE 0 END), 0) AS ticket_sales_amount,
		COALESCE(SUM(CASE WHEN request_status = ? AND NOT EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN 1 ELSE 0 END), 0) AS approved_grant_count,
		COALESCE(SUM(CASE WHEN request_status = ? AND NOT EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN points ELSE 0 END), 0) AS approved_points,
		COALESCE(SUM(CASE WHEN request_status = ? AND EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN 1 ELSE 0 END), 0) AS corrected_grant_count,
		COALESCE(SUM(CASE WHEN request_status = ? AND EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN points ELSE 0 END), 0) AS corrected_points,
		COALESCE(SUM(CASE WHEN request_status = ? THEN 1 ELSE 0 END), 0) AS pending_grant_count,
		COALESCE(SUM(CASE WHEN request_status = ? THEN points ELSE 0 END), 0) AS pending_points,
		COALESCE(SUM(CASE WHEN request_status = ? THEN 1 ELSE 0 END), 0) AS rejected_grant_count`,
		model.PointGrantModeTicket, model.ApprovalStatusRejected,
		model.PointGrantModeTicket, model.ApprovalStatusRejected,
		model.ApprovalStatusApproved, model.ApprovalStatusApproved,
		model.ApprovalStatusApproved, model.ApprovalStatusApproved,
		model.ApprovalStatusApproved, model.ApprovalStatusApproved,
		model.ApprovalStatusApproved, model.ApprovalStatusApproved,
		model.ApprovalStatusPending, model.ApprovalStatusPending, model.ApprovalStatusRejected).
		Scan(&grantSummary).Error; err != nil {
		return StaffWorkbench{}, err
	}
	result.Summary.TicketSalesCount = grantSummary.TicketSalesCount
	result.Summary.TicketSalesAmount = grantSummary.TicketSalesAmount
	result.Summary.ApprovedGrantCount = grantSummary.ApprovedGrantCount
	result.Summary.ApprovedPoints = grantSummary.ApprovedPoints
	result.Summary.CorrectedGrantCount = grantSummary.CorrectedGrantCount
	result.Summary.CorrectedPoints = grantSummary.CorrectedPoints
	result.Summary.PendingGrantCount = grantSummary.PendingGrantCount
	result.Summary.PendingPoints = grantSummary.PendingPoints
	result.Summary.RejectedGrantCount = grantSummary.RejectedGrantCount

	if err := grantRange().Select("payment_method, COUNT(*) AS sale_count, COALESCE(SUM(total_amount), 0) AS amount").
		Where("grant_mode = ? AND request_status <> ?", model.PointGrantModeTicket, model.ApprovalStatusRejected).
		Group("payment_method").Order("amount DESC, payment_method ASC").Scan(&result.PaymentSummary).Error; err != nil {
		return StaffWorkbench{}, err
	}
	if err := s.db.WithContext(ctx).Table("point_grant_requests AS grants").
		Select("grants.id, grants.user_id, COALESCE(NULLIF(users.nickname, ''), '微信用户') AS user_nickname, grants.grant_mode, COALESCE(grants.sale_no, '') AS sale_no, grants.ticket_unit_price, grants.ticket_count, grants.total_amount, grants.points, grants.payment_method, grants.remark, grants.request_status, grants.review_remark, COALESCE((SELECT request_status FROM point_correction_requests WHERE original_grant_request_id = grants.id AND request_status IN (?, ?) ORDER BY id DESC LIMIT 1), '') AS correction_status, grants.created_at", model.ApprovalStatusPending, model.ApprovalStatusApproved).
		Joins("JOIN users ON users.id = grants.user_id").
		Where("grants.submitted_by = ? AND grants.agency_id = ? AND grants.group_id = ? AND grants.created_at >= ? AND grants.created_at < ?", staffID, agencyID, groupID, dayStart, dayEnd).
		Order("grants.id DESC").Limit(100).Scan(&result.GrantRecords).Error; err != nil {
		return StaffWorkbench{}, err
	}

	var redeemSummary struct {
		RedeemCount    int64
		RedeemedPoints int64
	}
	orderRange := s.db.WithContext(ctx).Model(&model.ExchangeOrder{}).
		Where("redeemed_by = ? AND agency_id = ? AND group_id = ? AND status = ? AND redeemed_at >= ? AND redeemed_at < ?", staffID, agencyID, groupID, model.ExchangeOrderStatusRedeemed, dayStart, dayEnd)
	if err := orderRange.Select("COUNT(*) AS redeem_count, COALESCE(SUM(points_cost), 0) AS redeemed_points").Scan(&redeemSummary).Error; err != nil {
		return StaffWorkbench{}, err
	}
	result.Summary.RedeemCount = redeemSummary.RedeemCount
	result.Summary.RedeemedPoints = redeemSummary.RedeemedPoints
	if err := s.db.WithContext(ctx).Table("exchange_orders AS orders").
		Select("orders.id AS order_id, orders.order_no, orders.user_id, COALESCE(NULLIF(users.nickname, ''), '微信用户') AS user_nickname, orders.goods_id, orders.goods_name, orders.points_cost, orders.redeemed_at").
		Joins("JOIN users ON users.id = orders.user_id").
		Where("orders.redeemed_by = ? AND orders.agency_id = ? AND orders.group_id = ? AND orders.status = ? AND orders.redeemed_at >= ? AND orders.redeemed_at < ?", staffID, agencyID, groupID, model.ExchangeOrderStatusRedeemed, dayStart, dayEnd).
		Order("orders.redeemed_at DESC, orders.id DESC").Limit(100).Scan(&result.RedeemRecords).Error; err != nil {
		return StaffWorkbench{}, err
	}
	return result, nil
}

func parseStaffWorkbenchDate(value string, now time.Time) (time.Time, time.Time, error) {
	location := now.Location()
	today, _ := time.ParseInLocation(staffWorkbenchDateLayout, now.In(location).Format(staffWorkbenchDateLayout), location)
	value = strings.TrimSpace(value)
	if value == "" {
		value = today.Format(staffWorkbenchDateLayout)
	}
	day, err := time.ParseInLocation(staffWorkbenchDateLayout, value, location)
	if err != nil {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date", "date must use YYYY-MM-DD")
	}
	if day.After(today) || day.Before(today.AddDate(0, 0, -180)) {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date", "date must be within the last 180 days")
	}
	return day, day.AddDate(0, 0, 1), nil
}
