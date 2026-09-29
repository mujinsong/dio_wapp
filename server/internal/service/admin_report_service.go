package service

import (
	"context"
	"strings"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

const adminReportDateLayout = "2006-01-02"

type AdminReportService struct {
	db          *gorm.DB
	authService *AuthService
}

type AdminReportMetrics struct {
	TicketSalesCount    int64 `json:"ticket_sales_count"`
	TicketSalesAmount   int64 `json:"ticket_sales_amount"`
	GrantCount          int64 `json:"grant_count"`
	GrantedPoints       int64 `json:"granted_points"`
	CorrectedGrantCount int64 `json:"corrected_grant_count"`
	CorrectedPoints     int64 `json:"corrected_points"`
	PendingGrantCount   int64 `json:"pending_grant_count"`
	PendingGrantPoints  int64 `json:"pending_grant_points"`
	ExchangeCount       int64 `json:"exchange_count"`
	ExchangePoints      int64 `json:"exchange_points"`
	RedeemedCount       int64 `json:"redeemed_count"`
	RefundedCount       int64 `json:"refunded_count"`
	RefundedPoints      int64 `json:"refunded_points"`
	PendingOrderCount   int64 `json:"pending_order_count"`
	LowStockGoodsCount  int64 `json:"low_stock_goods_count"`
}

type AdminStaffReport struct {
	UserID          uint64 `json:"user_id"`
	DisplayName     string `json:"display_name"`
	GrantCount      int64  `json:"grant_count"`
	GrantedPoints   int64  `json:"granted_points"`
	CorrectedPoints int64  `json:"corrected_points"`
	SaleAmount      int64  `json:"sale_amount"`
}

type StockMovementDTO struct {
	ID           uint64    `json:"id"`
	GoodsID      uint64    `json:"goods_id"`
	GoodsName    string    `json:"goods_name"`
	GroupName    string    `json:"group_name"`
	BeforeStock  int64     `json:"before_stock"`
	DeltaStock   int64     `json:"delta_stock"`
	AfterStock   int64     `json:"after_stock"`
	Type         string    `json:"type"`
	OperatorID   uint64    `json:"operator_id"`
	OperatorName string    `json:"operator_name"`
	ReferenceID  uint64    `json:"reference_id"`
	Remark       string    `json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
}

type AdminOverviewReport struct {
	AgencyID       uint64             `json:"agency_id"`
	DateFrom       string             `json:"date_from"`
	DateTo         string             `json:"date_to"`
	Metrics        AdminReportMetrics `json:"metrics"`
	StaffStats     []AdminStaffReport `json:"staff_stats"`
	StockMovements []StockMovementDTO `json:"stock_movements"`
}

func NewAdminReportService(db *gorm.DB, authService *AuthService) *AdminReportService {
	return &AdminReportService{db: db, authService: authService}
}

func (s *AdminReportService) Overview(ctx context.Context, operatorID uint64, agencyID uint64, dateFrom string, dateTo string) (AdminOverviewReport, error) {
	if agencyID == 0 {
		return AdminOverviewReport{}, xerr.New(400, "invalid_agency", "agency_id is required")
	}
	ok, err := s.authService.HasAdminAgency(ctx, operatorID, agencyID)
	if err != nil {
		return AdminOverviewReport{}, err
	}
	if !ok {
		return AdminOverviewReport{}, xerr.New(403, "admin_forbidden", "admin has no permission for this agency")
	}
	from, toExclusive, err := parseAdminReportRange(dateFrom, dateTo, time.Now())
	if err != nil {
		return AdminOverviewReport{}, err
	}

	report := AdminOverviewReport{
		AgencyID:       agencyID,
		DateFrom:       from.Format(adminReportDateLayout),
		DateTo:         toExclusive.AddDate(0, 0, -1).Format(adminReportDateLayout),
		StaffStats:     make([]AdminStaffReport, 0),
		StockMovements: make([]StockMovementDTO, 0),
	}
	grantRange := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&model.PointGrantRequest{}).
			Where("agency_id = ? AND created_at >= ? AND created_at < ?", agencyID, from, toExclusive)
	}
	var approvedGrants struct {
		GrantCount    int64
		GrantedPoints int64
	}
	if err := grantRange().Select(`
		COALESCE(SUM(CASE WHEN NOT EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN 1 ELSE 0 END), 0) AS grant_count,
		COALESCE(SUM(CASE WHEN NOT EXISTS (SELECT 1 FROM point_correction_requests AS corrections WHERE corrections.original_grant_request_id = point_grant_requests.id AND corrections.request_status = ?) THEN points ELSE 0 END), 0) AS granted_points`, model.ApprovalStatusApproved, model.ApprovalStatusApproved).
		Where("request_status = ?", model.ApprovalStatusApproved).Scan(&approvedGrants).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.GrantCount = approvedGrants.GrantCount
	report.Metrics.GrantedPoints = approvedGrants.GrantedPoints
	var corrections struct {
		CorrectedGrantCount int64
		CorrectedPoints     int64
	}
	if err := s.db.WithContext(ctx).Table("point_correction_requests AS corrections").
		Select("COUNT(*) AS corrected_grant_count, COALESCE(SUM(corrections.points), 0) AS corrected_points").
		Joins("JOIN point_grant_requests AS grants ON grants.id = corrections.original_grant_request_id").
		Where("corrections.agency_id = ? AND corrections.request_status = ? AND grants.created_at >= ? AND grants.created_at < ?", agencyID, model.ApprovalStatusApproved, from, toExclusive).
		Scan(&corrections).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.CorrectedGrantCount = corrections.CorrectedGrantCount
	report.Metrics.CorrectedPoints = corrections.CorrectedPoints
	var ticketSales struct {
		TicketSalesCount  int64
		TicketSalesAmount int64
	}
	if err := grantRange().Select("COUNT(*) AS ticket_sales_count, COALESCE(SUM(total_amount), 0) AS ticket_sales_amount").
		Where("request_status = ? AND grant_mode = ?", model.ApprovalStatusApproved, model.PointGrantModeTicket).
		Scan(&ticketSales).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.TicketSalesCount = ticketSales.TicketSalesCount
	report.Metrics.TicketSalesAmount = ticketSales.TicketSalesAmount
	var pendingGrants struct {
		PendingGrantCount  int64
		PendingGrantPoints int64
	}
	if err := grantRange().Select("COUNT(*) AS pending_grant_count, COALESCE(SUM(points), 0) AS pending_grant_points").
		Where("request_status = ?", model.ApprovalStatusPending).Scan(&pendingGrants).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.PendingGrantCount = pendingGrants.PendingGrantCount
	report.Metrics.PendingGrantPoints = pendingGrants.PendingGrantPoints

	orderRange := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&model.ExchangeOrder{}).
			Where("agency_id = ? AND created_at >= ? AND created_at < ?", agencyID, from, toExclusive)
	}
	var exchanges struct {
		ExchangeCount  int64
		ExchangePoints int64
	}
	if err := orderRange().Select("COUNT(*) AS exchange_count, COALESCE(SUM(points_cost), 0) AS exchange_points").Scan(&exchanges).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.ExchangeCount = exchanges.ExchangeCount
	report.Metrics.ExchangePoints = exchanges.ExchangePoints
	if err := orderRange().Where("status = ?", model.ExchangeOrderStatusRedeemed).Count(&report.Metrics.RedeemedCount).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	var refunds struct {
		RefundedCount  int64
		RefundedPoints int64
	}
	if err := orderRange().Select("COUNT(*) AS refunded_count, COALESCE(SUM(points_cost), 0) AS refunded_points").
		Where("status IN ?", []string{model.ExchangeOrderStatusCanceled, model.ExchangeOrderStatusExpired}).Scan(&refunds).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	report.Metrics.RefundedCount = refunds.RefundedCount
	report.Metrics.RefundedPoints = refunds.RefundedPoints
	if err := orderRange().Where("status = ?", model.ExchangeOrderStatusPending).Count(&report.Metrics.PendingOrderCount).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	if err := s.db.WithContext(ctx).Model(&model.Goods{}).
		Where("agency_id = ? AND status = ? AND stock <= ?", agencyID, model.GoodsStatusOnSale, 5).
		Count(&report.Metrics.LowStockGoodsCount).Error; err != nil {
		return AdminOverviewReport{}, err
	}

	if err := s.db.WithContext(ctx).Table("point_grant_requests AS grants").
		Select(`grants.submitted_by AS user_id,
			COALESCE(NULLIF(MAX(staff_members.display_name), ''), NULLIF(MAX(users.nickname), ''), '未命名工作人员') AS display_name,
			COALESCE(SUM(CASE WHEN corrections.id IS NULL THEN 1 ELSE 0 END), 0) AS grant_count,
			COALESCE(SUM(CASE WHEN corrections.id IS NULL THEN grants.points ELSE 0 END), 0) AS granted_points,
			COALESCE(SUM(CASE WHEN corrections.id IS NOT NULL THEN corrections.points ELSE 0 END), 0) AS corrected_points,
			COALESCE(SUM(CASE WHEN grants.grant_mode = ? THEN grants.total_amount ELSE 0 END), 0) AS sale_amount`, model.PointGrantModeTicket).
		Joins("JOIN users ON users.id = grants.submitted_by").
		Joins("LEFT JOIN staff_members ON staff_members.user_id = grants.submitted_by AND staff_members.agency_id = grants.agency_id AND staff_members.group_id = grants.group_id").
		Joins("LEFT JOIN point_correction_requests AS corrections ON corrections.original_grant_request_id = grants.id AND corrections.request_status = ?", model.ApprovalStatusApproved).
		Where("grants.agency_id = ? AND grants.request_status = ? AND grants.created_at >= ? AND grants.created_at < ?", agencyID, model.ApprovalStatusApproved, from, toExclusive).
		Group("grants.submitted_by").Order("granted_points DESC, grants.submitted_by ASC").Limit(50).
		Scan(&report.StaffStats).Error; err != nil {
		return AdminOverviewReport{}, err
	}

	if err := s.db.WithContext(ctx).Table("stock_movements AS movements").
		Select("movements.id, movements.goods_id, goods.name AS goods_name, idol_groups.name AS group_name, movements.before_stock, movements.delta_stock, movements.after_stock, movements.type, movements.operator_id, COALESCE(NULLIF(users.nickname, ''), CASE WHEN movements.operator_id = 0 THEN '系统' ELSE '工作人员' END) AS operator_name, movements.reference_id, movements.remark, movements.created_at").
		Joins("JOIN goods ON goods.id = movements.goods_id").
		Joins("JOIN idol_groups ON idol_groups.id = movements.group_id").
		Joins("LEFT JOIN users ON users.id = movements.operator_id").
		Where("movements.agency_id = ? AND movements.created_at >= ? AND movements.created_at < ?", agencyID, from, toExclusive).
		Order("movements.id DESC").Limit(30).Scan(&report.StockMovements).Error; err != nil {
		return AdminOverviewReport{}, err
	}
	return report, nil
}

func parseAdminReportRange(dateFrom string, dateTo string, now time.Time) (time.Time, time.Time, error) {
	location := now.Location()
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateTo == "" {
		dateTo = now.In(location).Format(adminReportDateLayout)
	}
	to, err := time.ParseInLocation(adminReportDateLayout, dateTo, location)
	if err != nil {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date_range", "date_to must use YYYY-MM-DD")
	}
	if dateFrom == "" {
		dateFrom = to.AddDate(0, 0, -6).Format(adminReportDateLayout)
	}
	from, err := time.ParseInLocation(adminReportDateLayout, dateFrom, location)
	if err != nil {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date_range", "date_from must use YYYY-MM-DD")
	}
	if from.After(to) || to.Sub(from) > 89*24*time.Hour {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date_range", "report range must be between 1 and 90 days")
	}
	today, _ := time.ParseInLocation(adminReportDateLayout, now.In(location).Format(adminReportDateLayout), location)
	if to.After(today) {
		return time.Time{}, time.Time{}, xerr.New(400, "invalid_date_range", "date_to cannot be in the future")
	}
	return from, to.AddDate(0, 0, 1), nil
}
