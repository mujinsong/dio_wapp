package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

const maxAdminOperationExportRows = 5000

type AdminOperationsService struct {
	db          *gorm.DB
	authService *AuthService
}

type AdminOperationFilter struct {
	AgencyID uint64
	DateFrom string
	DateTo   string
	Status   string
	Keyword  string
	Cursor   uint64
	Limit    int
}

type AdminSaleRecord struct {
	ID              uint64     `json:"id"`
	SaleNo          string     `json:"sale_no"`
	GroupID         uint64     `json:"group_id"`
	GroupName       string     `json:"group_name"`
	UserID          uint64     `json:"user_id"`
	UserNickname    string     `json:"user_nickname"`
	SubmittedBy     uint64     `json:"submitted_by"`
	SubmitterName   string     `json:"submitter_name"`
	GrantMode       string     `json:"grant_mode"`
	TicketUnitPrice int64      `json:"ticket_unit_price"`
	TicketCount     int64      `json:"ticket_count"`
	TotalAmount     int64      `json:"total_amount"`
	Points          int64      `json:"points"`
	PaymentMethod   string     `json:"payment_method"`
	Remark          string     `json:"remark"`
	RequestStatus   string     `json:"request_status"`
	ReviewRemark    string     `json:"review_remark"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type AdminSalePage struct {
	Items      []AdminSaleRecord `json:"items"`
	NextCursor uint64            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

type AdminOrderRecord struct {
	ID           uint64     `json:"id"`
	OrderNo      string     `json:"order_no"`
	GroupID      uint64     `json:"group_id"`
	GroupName    string     `json:"group_name"`
	UserID       uint64     `json:"user_id"`
	UserNickname string     `json:"user_nickname"`
	GoodsID      uint64     `json:"goods_id"`
	GoodsName    string     `json:"goods_name"`
	PointsCost   int64      `json:"points_cost"`
	Status       string     `json:"status"`
	RedeemedBy   uint64     `json:"redeemed_by"`
	RedeemerName string     `json:"redeemer_name"`
	RedeemedAt   *time.Time `json:"redeemed_at"`
	CanceledAt   *time.Time `json:"canceled_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ExpiredAt    *time.Time `json:"expired_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type AdminOrderPage struct {
	Items      []AdminOrderRecord `json:"items"`
	NextCursor uint64             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

type AdminCSVExport struct {
	Filename string
	Content  []byte
}

func NewAdminOperationsService(db *gorm.DB, authService *AuthService) *AdminOperationsService {
	return &AdminOperationsService{db: db, authService: authService}
}

func (s *AdminOperationsService) ListSales(ctx context.Context, operatorID uint64, filter AdminOperationFilter) (AdminSalePage, error) {
	from, to, limit, err := s.validateFilter(ctx, operatorID, &filter, true)
	if err != nil {
		return AdminSalePage{}, err
	}
	items, err := s.querySales(ctx, filter, from, to, limit+1)
	if err != nil {
		return AdminSalePage{}, err
	}
	page := AdminSalePage{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *AdminOperationsService) ListOrders(ctx context.Context, operatorID uint64, filter AdminOperationFilter) (AdminOrderPage, error) {
	from, to, limit, err := s.validateFilter(ctx, operatorID, &filter, false)
	if err != nil {
		return AdminOrderPage{}, err
	}
	items, err := s.queryOrders(ctx, filter, from, to, limit+1)
	if err != nil {
		return AdminOrderPage{}, err
	}
	page := AdminOrderPage{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *AdminOperationsService) ExportCSV(ctx context.Context, operatorID uint64, exportType string, filter AdminOperationFilter) (AdminCSVExport, error) {
	filter.Cursor = 0
	filter.Limit = maxAdminOperationExportRows
	exportType = strings.TrimSpace(exportType)
	if exportType != "sales" && exportType != "orders" {
		return AdminCSVExport{}, xerr.New(400, "invalid_export_type", "export type must be sales or orders")
	}
	from, to, _, err := s.validateFilter(ctx, operatorID, &filter, exportType == "sales")
	if err != nil {
		return AdminCSVExport{}, err
	}
	var buffer bytes.Buffer
	buffer.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buffer)
	if exportType == "sales" {
		items, err := s.querySales(ctx, filter, from, to, maxAdminOperationExportRows+1)
		if err != nil {
			return AdminCSVExport{}, err
		}
		if len(items) > maxAdminOperationExportRows {
			return AdminCSVExport{}, xerr.New(400, "export_too_large", "export exceeds 5000 rows; narrow the date range")
		}
		writeSalesCSV(writer, items)
	} else {
		items, err := s.queryOrders(ctx, filter, from, to, maxAdminOperationExportRows+1)
		if err != nil {
			return AdminCSVExport{}, err
		}
		if len(items) > maxAdminOperationExportRows {
			return AdminCSVExport{}, xerr.New(400, "export_too_large", "export exceeds 5000 rows; narrow the date range")
		}
		writeOrdersCSV(writer, items)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return AdminCSVExport{}, err
	}
	filename := fmt.Sprintf("dio-%s-%s-%s.csv", exportType, from.Format(adminReportDateLayout), to.AddDate(0, 0, -1).Format(adminReportDateLayout))
	return AdminCSVExport{Filename: filename, Content: buffer.Bytes()}, nil
}

func (s *AdminOperationsService) validateFilter(ctx context.Context, operatorID uint64, filter *AdminOperationFilter, sales bool) (time.Time, time.Time, int, error) {
	if filter.AgencyID == 0 {
		return time.Time{}, time.Time{}, 0, xerr.New(400, "invalid_agency", "agency_id is required")
	}
	ok, err := s.authService.HasAdminAgency(ctx, operatorID, filter.AgencyID)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	if !ok {
		return time.Time{}, time.Time{}, 0, xerr.New(403, "admin_forbidden", "admin has no permission for this agency")
	}
	from, to, err := parseAdminReportRange(filter.DateFrom, filter.DateTo, time.Now())
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.Status == "" {
		filter.Status = "all"
	}
	if sales {
		if filter.Status != "all" && filter.Status != model.ApprovalStatusPending && filter.Status != model.ApprovalStatusApproved && filter.Status != model.ApprovalStatusRejected {
			return time.Time{}, time.Time{}, 0, xerr.New(400, "invalid_status", "sale status is invalid")
		}
	} else if filter.Status != "all" && filter.Status != model.ExchangeOrderStatusPending && filter.Status != model.ExchangeOrderStatusRedeemed && filter.Status != model.ExchangeOrderStatusCanceled && filter.Status != model.ExchangeOrderStatusExpired {
		return time.Time{}, time.Time{}, 0, xerr.New(400, "invalid_status", "order status is invalid")
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	if !utf8.ValidString(filter.Keyword) || utf8.RuneCountInString(filter.Keyword) > 64 {
		return time.Time{}, time.Time{}, 0, xerr.New(400, "invalid_keyword", "keyword must not exceed 64 characters")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 && limit != maxAdminOperationExportRows {
		limit = 100
	}
	return from, to, limit, nil
}

func (s *AdminOperationsService) querySales(ctx context.Context, filter AdminOperationFilter, from time.Time, to time.Time, limit int) ([]AdminSaleRecord, error) {
	query := s.db.WithContext(ctx).Table("point_grant_requests AS grants").
		Select("grants.id, COALESCE(grants.sale_no, '') AS sale_no, grants.group_id, idol_groups.name AS group_name, grants.user_id, COALESCE(NULLIF(target.nickname, ''), '微信用户') AS user_nickname, grants.submitted_by, COALESCE(NULLIF(staff_members.display_name, ''), NULLIF(submitter.nickname, ''), '工作人员') AS submitter_name, grants.grant_mode, grants.ticket_unit_price, grants.ticket_count, grants.total_amount, grants.points, grants.payment_method, grants.remark, grants.request_status, grants.review_remark, grants.reviewed_at, grants.created_at").
		Joins("JOIN idol_groups ON idol_groups.id = grants.group_id AND idol_groups.agency_id = grants.agency_id").
		Joins("JOIN users AS target ON target.id = grants.user_id").
		Joins("JOIN users AS submitter ON submitter.id = grants.submitted_by").
		Joins("LEFT JOIN staff_members ON staff_members.user_id = grants.submitted_by AND staff_members.agency_id = grants.agency_id AND staff_members.group_id = grants.group_id").
		Where("grants.agency_id = ? AND grants.created_at >= ? AND grants.created_at < ?", filter.AgencyID, from, to)
	if filter.Status != "all" {
		query = query.Where("grants.request_status = ?", filter.Status)
	}
	if filter.Keyword != "" {
		keyword := "%" + filter.Keyword + "%"
		query = query.Where("COALESCE(grants.sale_no, '') LIKE ? OR target.nickname LIKE ? OR submitter.nickname LIKE ? OR staff_members.display_name LIKE ?", keyword, keyword, keyword, keyword)
	}
	if filter.Cursor > 0 {
		query = query.Where("grants.id < ?", filter.Cursor)
	}
	var items []AdminSaleRecord
	err := query.Order("grants.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func (s *AdminOperationsService) queryOrders(ctx context.Context, filter AdminOperationFilter, from time.Time, to time.Time, limit int) ([]AdminOrderRecord, error) {
	query := s.db.WithContext(ctx).Table("exchange_orders AS orders").
		Select("orders.id, orders.order_no, orders.group_id, idol_groups.name AS group_name, orders.user_id, COALESCE(NULLIF(users.nickname, ''), '微信用户') AS user_nickname, orders.goods_id, orders.goods_name, orders.points_cost, orders.status, orders.redeemed_by, COALESCE(NULLIF(redeemer.nickname, ''), CASE WHEN orders.redeemed_by = 0 THEN '' ELSE '工作人员' END) AS redeemer_name, orders.redeemed_at, orders.canceled_at, orders.expires_at, orders.expired_at, orders.created_at").
		Joins("JOIN idol_groups ON idol_groups.id = orders.group_id AND idol_groups.agency_id = orders.agency_id").
		Joins("JOIN users ON users.id = orders.user_id").
		Joins("LEFT JOIN users AS redeemer ON redeemer.id = orders.redeemed_by").
		Where("orders.agency_id = ? AND orders.created_at >= ? AND orders.created_at < ?", filter.AgencyID, from, to)
	if filter.Status != "all" {
		query = query.Where("orders.status = ?", filter.Status)
	}
	if filter.Keyword != "" {
		keyword := "%" + filter.Keyword + "%"
		query = query.Where("orders.order_no LIKE ? OR orders.goods_name LIKE ? OR users.nickname LIKE ?", keyword, keyword, keyword)
	}
	if filter.Cursor > 0 {
		query = query.Where("orders.id < ?", filter.Cursor)
	}
	var items []AdminOrderRecord
	err := query.Order("orders.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func writeSalesCSV(writer *csv.Writer, items []AdminSaleRecord) {
	_ = writer.Write([]string{"销售单号", "创建时间", "团体", "会员编号", "会员昵称", "工作人员", "类型", "支付方式", "面额", "张数", "金额", "积分", "状态", "审核备注", "备注"})
	for _, item := range items {
		_ = writer.Write([]string{
			csvCell(item.SaleNo), formatCSVTime(&item.CreatedAt), csvCell(item.GroupName), strconv.FormatUint(item.UserID, 10), csvCell(item.UserNickname), csvCell(item.SubmitterName),
			item.GrantMode, item.PaymentMethod, strconv.FormatInt(item.TicketUnitPrice, 10), strconv.FormatInt(item.TicketCount, 10), strconv.FormatInt(item.TotalAmount, 10), strconv.FormatInt(item.Points, 10), item.RequestStatus, csvCell(item.ReviewRemark), csvCell(item.Remark),
		})
	}
}

func writeOrdersCSV(writer *csv.Writer, items []AdminOrderRecord) {
	_ = writer.Write([]string{"订单号", "创建时间", "团体", "会员编号", "会员昵称", "商品", "积分", "状态", "核销人", "核销时间", "取消时间", "过期时间"})
	for _, item := range items {
		_ = writer.Write([]string{
			csvCell(item.OrderNo), formatCSVTime(&item.CreatedAt), csvCell(item.GroupName), strconv.FormatUint(item.UserID, 10), csvCell(item.UserNickname), csvCell(item.GoodsName), strconv.FormatInt(item.PointsCost, 10), item.Status, csvCell(item.RedeemerName), formatCSVTime(item.RedeemedAt), formatCSVTime(item.CanceledAt), formatCSVTime(item.ExpiredAt),
		})
	}
}

func csvCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func formatCSVTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}
