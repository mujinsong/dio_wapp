package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxGoodsPricePoints     = int64(1_000_000_000)
	MaxGoodsStock           = int64(1_000_000_000)
	MaxPurchaseLimitPerUser = int64(1_000_000_000)
	MaxPurchaseLimitHours   = int64(8760)
)

type AdminGoodsService struct {
	db          *gorm.DB
	authService *AuthService
}

type AdminGroupDTO struct {
	ID          uint64 `json:"id"`
	AgencyID    uint64 `json:"agency_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Sort        int    `json:"sort"`
}

type AdminGroupInput struct {
	AgencyID    uint64 `json:"agency_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Sort        int    `json:"sort"`
}

type AdminGoodsInput struct {
	AgencyID             uint64     `json:"agency_id"`
	GroupID              uint64     `json:"group_id"`
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
	ExpectedUpdatedAt    *time.Time `json:"expected_updated_at"`
}

func NewAdminGoodsService(db *gorm.DB, authService *AuthService) *AdminGoodsService {
	return &AdminGoodsService{db: db, authService: authService}
}

func (s *AdminGoodsService) ListGroups(ctx context.Context, operatorID uint64, agencyID uint64) ([]AdminGroupDTO, error) {
	if agencyID == 0 {
		return nil, xerr.New(400, "invalid_agency", "agency_id is required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, agencyID); err != nil {
		return nil, err
	}

	var groups []model.IdolGroup
	if err := s.db.WithContext(ctx).
		Where("agency_id = ?", agencyID).
		Order("sort DESC, id ASC").
		Find(&groups).Error; err != nil {
		return nil, err
	}

	result := make([]AdminGroupDTO, 0, len(groups))
	for _, group := range groups {
		result = append(result, AdminGroupDTO{
			ID:          group.ID,
			AgencyID:    group.AgencyID,
			Name:        group.Name,
			Description: group.Description,
			Status:      group.Status,
			Sort:        group.Sort,
		})
	}
	return result, nil
}

func (s *AdminGoodsService) CreateGroup(ctx context.Context, operatorID uint64, input AdminGroupInput) (AdminGroupDTO, error) {
	if err := s.validateGroupInput(ctx, operatorID, input, 0); err != nil {
		return AdminGroupDTO{}, err
	}

	group := model.IdolGroup{
		AgencyID:    input.AgencyID,
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Status:      normalizeGroupStatus(input.Status),
		Sort:        input.Sort,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, input.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		return tx.Create(&group).Error
	}); err != nil {
		return AdminGroupDTO{}, err
	}
	return toAdminGroupDTO(group), nil
}

func (s *AdminGoodsService) UpdateGroup(ctx context.Context, operatorID uint64, groupID uint64, input AdminGroupInput) (AdminGroupDTO, error) {
	if groupID == 0 {
		return AdminGroupDTO{}, xerr.New(400, "invalid_group", "group_id is required")
	}

	var group model.IdolGroup
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminGroupDTO{}, xerr.New(404, "group_not_found", "group not found")
		}
		return AdminGroupDTO{}, err
	}
	if input.AgencyID == 0 {
		input.AgencyID = group.AgencyID
	}
	if input.AgencyID != group.AgencyID {
		return AdminGroupDTO{}, xerr.New(400, "invalid_agency", "group cannot be moved to another agency")
	}
	if err := s.validateGroupInput(ctx, operatorID, input, group.ID); err != nil {
		return AdminGroupDTO{}, err
	}

	updates := map[string]any{
		"name":        strings.TrimSpace(input.Name),
		"description": strings.TrimSpace(input.Description),
		"status":      normalizeGroupStatus(input.Status),
		"sort":        input.Sort,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, group.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, group.ID).Error; err != nil {
			return err
		}
		return tx.Model(&group).Updates(updates).Error
	}); err != nil {
		return AdminGroupDTO{}, err
	}
	if err := s.db.WithContext(ctx).First(&group, group.ID).Error; err != nil {
		return AdminGroupDTO{}, err
	}
	return toAdminGroupDTO(group), nil
}

func (s *AdminGoodsService) DeleteGroup(ctx context.Context, operatorID uint64, groupID uint64) error {
	if groupID == 0 {
		return xerr.New(400, "invalid_group", "group_id is required")
	}

	var group model.IdolGroup
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(404, "group_not_found", "group not found")
		}
		return err
	}
	if err := s.requireAdminAgency(ctx, operatorID, group.AgencyID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, group.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, group.ID).Error; err != nil {
			return err
		}
		return tx.Model(&group).Update("status", model.MemberStatusDisabled).Error
	})
}

func (s *AdminGoodsService) ListGoods(ctx context.Context, operatorID uint64, agencyID uint64) ([]GoodsDTO, error) {
	if agencyID == 0 {
		return nil, xerr.New(400, "invalid_agency", "agency_id is required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, agencyID); err != nil {
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
		Status                 string     `gorm:"column:status"`
		Sort                   int        `gorm:"column:sort"`
		UpdatedAt              time.Time  `gorm:"column:updated_at"`
	}

	err := s.db.WithContext(ctx).
		Table("goods").
		Select("goods.id, goods.agency_id, agencies.name AS agency_name, goods.group_id, idol_groups.name AS group_name, goods.name, goods.description, goods.image_url, goods.price_points, goods.stock, goods.purchase_limit_per_user, goods.purchase_limit_hours, goods.purchase_limit_started_at, goods.sale_starts_at, goods.status, goods.sort, goods.updated_at").
		Joins("JOIN agencies ON agencies.id = goods.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = goods.group_id AND idol_groups.agency_id = goods.agency_id").
		Where("goods.agency_id = ? AND goods.status <> ?", agencyID, model.GoodsStatusDeleted).
		Order("goods.sort DESC, goods.id DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make([]GoodsDTO, 0, len(rows))
	for _, row := range rows {
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
			Status:                 row.Status,
			Sort:                   row.Sort,
			SoldOut:                row.Stock <= 0,
			UpdatedAt:              row.UpdatedAt,
		})
	}
	return result, nil
}

func (s *AdminGoodsService) CreateGoods(ctx context.Context, operatorID uint64, input AdminGoodsInput) (GoodsDTO, error) {
	if err := s.validateInput(ctx, operatorID, input); err != nil {
		return GoodsDTO{}, err
	}
	requestID, err := validateRequestID(input.RequestID)
	if err != nil {
		return GoodsDTO{}, err
	}
	hashInput := input
	hashInput.RequestID = ""
	encoded, err := json.Marshal(hashInput)
	if err != nil {
		return GoodsDTO{}, err
	}
	requestHash := idempotencyHash(string(encoded))
	if existing, found, err := loadIdempotentResult[GoodsDTO](ctx, s.db, operatorID, idempotencyAdminGoods, requestID, requestHash); found || err != nil {
		return existing, err
	}

	status := normalizeGoodsStatus(input.Status)
	saleStartsAt := normalizeSaleStartsAt(status, input.SaleStartsAt)
	limitStartedAt := nextPurchaseLimitStart(nil, status, input.PurchaseLimitPerUser, input.PurchaseLimitHours, saleStartsAt, time.Now())
	var result GoodsDTO
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, operatorID, idempotencyAdminGoods, requestID, requestHash)
		if err != nil {
			return err
		}
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, input.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := requireActiveGoodsGroupTx(tx, input.GroupID, input.AgencyID); err != nil {
			return err
		}
		goods := model.Goods{
			AgencyID:               input.AgencyID,
			GroupID:                input.GroupID,
			Name:                   strings.TrimSpace(input.Name),
			Description:            strings.TrimSpace(input.Description),
			ImageURL:               strings.TrimSpace(input.ImageURL),
			PricePoints:            input.PricePoints,
			Stock:                  input.Stock,
			PurchaseLimitPerUser:   input.PurchaseLimitPerUser,
			PurchaseLimitHours:     input.PurchaseLimitHours,
			PurchaseLimitStartedAt: limitStartedAt,
			SaleStartsAt:           saleStartsAt,
			Status:                 status,
			Sort:                   input.Sort,
		}
		if err := tx.Create(&goods).Error; err != nil {
			return err
		}
		if err := recordStockMovement(tx, goods, 0, goods.Stock, model.StockMovementInitial, operatorID, goods.ID, "管理员创建商品初始库存"); err != nil {
			return err
		}
		result, err = loadGoodsDTO(ctx, tx, goods)
		if err != nil {
			return err
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[GoodsDTO](ctx, s.db, operatorID, idempotencyAdminGoods, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return GoodsDTO{}, err
	}
	return result, nil
}

func (s *AdminGoodsService) UpdateGoods(ctx context.Context, operatorID uint64, goodsID uint64, input AdminGoodsInput) (GoodsDTO, error) {
	if goodsID == 0 {
		return GoodsDTO{}, xerr.New(400, "invalid_goods", "goods_id is required")
	}

	var goods model.Goods
	if err := s.db.WithContext(ctx).First(&goods, goodsID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GoodsDTO{}, xerr.New(404, "goods_not_found", "goods not found")
		}
		return GoodsDTO{}, err
	}
	if goods.Status == model.GoodsStatusDeleted {
		return GoodsDTO{}, xerr.New(404, "goods_not_found", "goods not found")
	}
	if input.ExpectedUpdatedAt == nil {
		return GoodsDTO{}, xerr.New(400, "missing_goods_version", "expected_updated_at is required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, goods.AgencyID); err != nil {
		return GoodsDTO{}, err
	}

	if input.AgencyID == 0 {
		input.AgencyID = goods.AgencyID
	}
	if input.AgencyID != goods.AgencyID {
		return GoodsDTO{}, xerr.New(400, "invalid_agency", "goods cannot be moved to another agency")
	}
	if input.GroupID == 0 {
		input.GroupID = goods.GroupID
	}
	if err := s.validateInput(ctx, operatorID, input); err != nil {
		return GoodsDTO{}, err
	}

	status := normalizeGoodsStatus(input.Status)
	saleStartsAt := normalizeSaleStartsAt(status, input.SaleStartsAt)
	limitStartedAt := nextPurchaseLimitStart(&goods, status, input.PurchaseLimitPerUser, input.PurchaseLimitHours, saleStartsAt, time.Now())

	updates := map[string]any{
		"agency_id":                 input.AgencyID,
		"group_id":                  input.GroupID,
		"name":                      strings.TrimSpace(input.Name),
		"description":               strings.TrimSpace(input.Description),
		"image_url":                 strings.TrimSpace(input.ImageURL),
		"price_points":              input.PricePoints,
		"stock":                     input.Stock,
		"purchase_limit_per_user":   input.PurchaseLimitPerUser,
		"purchase_limit_hours":      input.PurchaseLimitHours,
		"purchase_limit_started_at": limitStartedAt,
		"sale_starts_at":            saleStartsAt,
		"status":                    status,
		"sort":                      input.Sort,
	}
	var result GoodsDTO
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, goods.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := requireActiveGoodsGroupTx(tx, input.GroupID, input.AgencyID); err != nil {
			return err
		}
		var locked model.Goods
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, goods.ID).Error; err != nil {
			return err
		}
		if !locked.UpdatedAt.Equal(*input.ExpectedUpdatedAt) {
			return xerr.New(409, "goods_snapshot_changed", "goods changed while being edited")
		}
		beforeStock := locked.Stock
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		if err := recordStockMovement(tx, locked, beforeStock, input.Stock, model.StockMovementAdminAdjust, operatorID, locked.ID, "管理员调整商品库存"); err != nil {
			return err
		}
		locked.Stock = input.Stock
		loaded, loadErr := loadGoodsDTO(ctx, tx, locked)
		if loadErr != nil {
			return loadErr
		}
		result = loaded
		return nil
	})
	if err != nil {
		return GoodsDTO{}, err
	}
	return result, nil
}

func (s *AdminGoodsService) DeleteGoods(ctx context.Context, operatorID uint64, goodsID uint64) error {
	if goodsID == 0 {
		return xerr.New(400, "invalid_goods", "goods_id is required")
	}

	var goods model.Goods
	if err := s.db.WithContext(ctx).First(&goods, goodsID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(404, "goods_not_found", "goods not found")
		}
		return err
	}
	if err := s.requireAdminAgency(ctx, operatorID, goods.AgencyID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, goods.AgencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_agency_forbidden", "admin no longer has permission for this agency")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&goods, goods.ID).Error; err != nil {
			return err
		}
		return tx.Model(&goods).Update("status", model.GoodsStatusDeleted).Error
	})
}

func (s *AdminGoodsService) GetGoods(ctx context.Context, operatorID uint64, goodsID uint64) (GoodsDTO, error) {
	var goods model.Goods
	if err := s.db.WithContext(ctx).First(&goods, goodsID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GoodsDTO{}, xerr.New(404, "goods_not_found", "goods not found")
		}
		return GoodsDTO{}, err
	}
	if goods.Status == model.GoodsStatusDeleted {
		return GoodsDTO{}, xerr.New(404, "goods_not_found", "goods not found")
	}
	if err := s.requireAdminAgency(ctx, operatorID, goods.AgencyID); err != nil {
		return GoodsDTO{}, err
	}
	return loadGoodsDTO(ctx, s.db, goods)
}

func loadGoodsDTO(ctx context.Context, db *gorm.DB, goods model.Goods) (GoodsDTO, error) {
	var agency model.Agency
	if err := db.WithContext(ctx).First(&agency, goods.AgencyID).Error; err != nil {
		return GoodsDTO{}, err
	}
	var group model.IdolGroup
	if err := db.WithContext(ctx).First(&group, goods.GroupID).Error; err != nil {
		return GoodsDTO{}, err
	}

	return GoodsDTO{
		ID:                     goods.ID,
		AgencyID:               goods.AgencyID,
		AgencyName:             agency.Name,
		GroupID:                goods.GroupID,
		GroupName:              group.Name,
		Name:                   goods.Name,
		Description:            goods.Description,
		ImageURL:               goods.ImageURL,
		PricePoints:            goods.PricePoints,
		Stock:                  goods.Stock,
		PurchaseLimitPerUser:   goods.PurchaseLimitPerUser,
		PurchaseLimitHours:     goods.PurchaseLimitHours,
		PurchaseLimitStartedAt: goods.PurchaseLimitStartedAt,
		SaleStartsAt:           goods.SaleStartsAt,
		Status:                 goods.Status,
		Sort:                   goods.Sort,
		SoldOut:                goods.Stock <= 0,
		UpdatedAt:              goods.UpdatedAt,
	}, nil
}

func requireActiveGoodsGroupTx(tx *gorm.DB, groupID uint64, agencyID uint64) error {
	var group model.IdolGroup
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND agency_id = ? AND status = ?", groupID, agencyID, model.MemberStatusNormal).
		First(&group).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return xerr.New(400, "group_not_found", "group not found in agency")
	}
	return err
}

func (s *AdminGoodsService) validateInput(ctx context.Context, operatorID uint64, input AdminGoodsInput) error {
	if input.AgencyID == 0 || input.GroupID == 0 {
		return xerr.New(400, "invalid_group", "agency_id and group_id are required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, input.AgencyID); err != nil {
		return err
	}
	if strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(strings.TrimSpace(input.Name)) > 128 {
		return xerr.New(400, "invalid_name", "goods name is required and must not exceed 128 characters")
	}
	if utf8.RuneCountInString(strings.TrimSpace(input.Description)) > 512 || utf8.RuneCountInString(strings.TrimSpace(input.ImageURL)) > 512 {
		return xerr.New(400, "invalid_goods_text", "goods description or image URL is too long")
	}
	if !validGoodsImageURL(input.ImageURL) {
		return xerr.New(400, "invalid_image_url", "image_url must be a valid HTTP or HTTPS URL")
	}
	if input.PricePoints <= 0 || input.PricePoints > MaxGoodsPricePoints {
		return xerr.New(400, "invalid_price", "price_points is invalid")
	}
	if input.Stock < 0 || input.Stock > MaxGoodsStock {
		return xerr.New(400, "invalid_stock", "stock is invalid")
	}
	if input.PurchaseLimitPerUser < 0 || input.PurchaseLimitPerUser > MaxPurchaseLimitPerUser ||
		input.PurchaseLimitHours < 0 || input.PurchaseLimitHours > MaxPurchaseLimitHours {
		return xerr.New(400, "invalid_purchase_limit", "purchase limit is invalid")
	}
	if (input.PurchaseLimitPerUser == 0) != (input.PurchaseLimitHours == 0) {
		return xerr.New(400, "invalid_purchase_limit_window", "purchase limit count and hours must both be zero or greater than zero")
	}
	status := normalizeGoodsStatus(input.Status)
	if status != model.GoodsStatusOnSale && status != model.GoodsStatusOffSale {
		return xerr.New(400, "invalid_status", "status is invalid")
	}

	var count int64
	err := s.db.WithContext(ctx).Model(&model.IdolGroup{}).
		Where("id = ? AND agency_id = ? AND status = ?", input.GroupID, input.AgencyID, model.MemberStatusNormal).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return xerr.New(400, "group_not_found", "group not found in agency")
	}
	return nil
}

func (s *AdminGoodsService) validateGroupInput(ctx context.Context, operatorID uint64, input AdminGroupInput, excludeID uint64) error {
	if input.AgencyID == 0 {
		return xerr.New(400, "invalid_agency", "agency_id is required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, input.AgencyID); err != nil {
		return err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return xerr.New(400, "invalid_group_name", "group name is required")
	}
	status := normalizeGroupStatus(input.Status)
	if status != model.MemberStatusNormal && status != model.MemberStatusDisabled {
		return xerr.New(400, "invalid_group_status", "group status is invalid")
	}

	query := s.db.WithContext(ctx).Model(&model.IdolGroup{}).
		Where("agency_id = ? AND name = ?", input.AgencyID, name)
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return xerr.New(409, "group_name_exists", "group name already exists in agency")
	}
	return nil
}

func (s *AdminGoodsService) requireAdminAgency(ctx context.Context, operatorID uint64, agencyID uint64) error {
	ok, err := s.authService.HasAdminAgency(ctx, operatorID, agencyID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.New(403, "admin_agency_forbidden", "admin has no permission for this agency")
	}
	return nil
}

func normalizeGoodsStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return model.GoodsStatusOnSale
	}
	return status
}

func normalizeSaleStartsAt(status string, startsAt *time.Time) *time.Time {
	if status != model.GoodsStatusOnSale || startsAt == nil {
		return nil
	}
	value := startsAt.In(time.Local)
	return &value
}

func nextPurchaseLimitStart(existing *model.Goods, status string, perUser int64, hours int64, saleStartsAt *time.Time, now time.Time) *time.Time {
	if status != model.GoodsStatusOnSale || perUser <= 0 || hours <= 0 {
		return nil
	}
	if saleStartsAt != nil && saleStartsAt.After(now) {
		value := *saleStartsAt
		return &value
	}
	if existing == nil || existing.Status != model.GoodsStatusOnSale ||
		existing.PurchaseLimitPerUser != perUser || existing.PurchaseLimitHours != hours ||
		!sameOptionalTime(existing.SaleStartsAt, saleStartsAt) || existing.PurchaseLimitStartedAt == nil {
		value := now
		return &value
	}
	value := *existing.PurchaseLimitStartedAt
	return &value
}

func sameOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func normalizeGroupStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return model.MemberStatusNormal
	}
	return status
}

func toAdminGroupDTO(group model.IdolGroup) AdminGroupDTO {
	return AdminGroupDTO{
		ID:          group.ID,
		AgencyID:    group.AgencyID,
		Name:        group.Name,
		Description: group.Description,
		Status:      group.Status,
		Sort:        group.Sort,
	}
}
