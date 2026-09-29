package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/xerr"

	qrcode "github.com/skip2/go-qrcode"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PointLedgerStaffAdd      = "staff_add"
	PointLedgerAdminAdjust   = "admin_adjust"
	PointLedgerGrantReversal = "grant_reversal"
	IdentityQRPrefix         = "dio_identity:"
	MaxPointsPerGrant        = int64(100_000)
	MaxPointsPerAdjustment   = int64(100_000)
	MaxPointBalance          = int64(1_000_000_000)
	MaxPointRemarkRunes      = 512
	identityQRTokenBytes     = 32
)

type PointsService struct {
	db          *gorm.DB
	authService *AuthService
}

type IdentityQRCodeDTO struct {
	Token     string    `json:"token"`
	Payload   string    `json:"payload"`
	QRImage   string    `json:"qr_image"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AddPointsResult struct {
	AgencyID        uint64 `json:"agency_id"`
	UserID          uint64 `json:"user_id"`
	BeforePoints    int64  `json:"before_points"`
	DeltaPoints     int64  `json:"delta_points"`
	AfterPoints     int64  `json:"after_points"`
	LedgerID        uint64 `json:"ledger_id"`
	GrantRequestID  uint64 `json:"grant_request_id,omitempty"`
	RequestStatus   string `json:"request_status,omitempty"`
	PendingApproval bool   `json:"pending_approval,omitempty"`
	SaleNo          string `json:"sale_no,omitempty"`
}

type PointLedgerDTO struct {
	ID           uint64    `json:"id"`
	AgencyID     uint64    `json:"agency_id"`
	AgencyName   string    `json:"agency_name"`
	GroupID      uint64    `json:"group_id"`
	GroupName    string    `json:"group_name"`
	BeforePoints int64     `json:"before_points"`
	DeltaPoints  int64     `json:"delta_points"`
	AfterPoints  int64     `json:"after_points"`
	Type         string    `json:"type"`
	OperatorID   uint64    `json:"operator_id,omitempty"`
	ReferenceID  uint64    `json:"reference_id,omitempty"`
	Remark       string    `json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
}

type PointLedgerPage struct {
	Items      []PointLedgerDTO `json:"items"`
	NextCursor uint64           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}

type AdminPointUserDTO struct {
	ID            uint64 `json:"id"`
	Nickname      string `json:"nickname"`
	Avatar        string `json:"avatar"`
	Status        string `json:"status"`
	AgencyID      uint64 `json:"agency_id"`
	AgencyName    string `json:"agency_name"`
	PointsBalance int64  `json:"points_balance"`
}

func NewPointsService(db *gorm.DB, authService *AuthService) *PointsService {
	return &PointsService{db: db, authService: authService}
}

func (s *PointsService) ListUserLedgers(ctx context.Context, userID uint64, cursor uint64, limit int) (PointLedgerPage, error) {
	if userID == 0 {
		return PointLedgerPage{}, xerr.New(400, "invalid_user", "user_id is required")
	}
	page, err := s.listLedgers(ctx, userID, 0, cursor, limit)
	if err != nil {
		return PointLedgerPage{}, err
	}
	for index := range page.Items {
		page.Items[index].OperatorID = 0
		page.Items[index].ReferenceID = 0
	}
	return page, nil
}

func (s *PointsService) GetAdminPointUser(ctx context.Context, operatorID uint64, agencyID uint64, userID uint64) (AdminPointUserDTO, error) {
	if agencyID == 0 || userID == 0 {
		return AdminPointUserDTO{}, xerr.New(400, "invalid_user", "agency_id and user_id are required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, agencyID); err != nil {
		return AdminPointUserDTO{}, err
	}

	var result AdminPointUserDTO
	query := s.db.WithContext(ctx).
		Table("users").
		Select("users.id, users.nickname, users.avatar, users.status, agencies.id AS agency_id, agencies.name AS agency_name, COALESCE(agency_point_accounts.balance, 0) AS points_balance").
		Joins("JOIN agencies ON agencies.id = ?", agencyID).
		Joins("LEFT JOIN agency_point_accounts ON agency_point_accounts.user_id = users.id AND agency_point_accounts.agency_id = agencies.id").
		Where("users.id = ? AND agencies.status = ?", userID, model.MemberStatusNormal).
		Scan(&result)
	if query.Error != nil {
		return AdminPointUserDTO{}, query.Error
	}
	if query.RowsAffected == 0 {
		return AdminPointUserDTO{}, xerr.New(404, "user_not_found", "user not found")
	}
	return result, nil
}

func (s *PointsService) ListAdminUserLedgers(ctx context.Context, operatorID uint64, agencyID uint64, userID uint64, cursor uint64, limit int) (PointLedgerPage, error) {
	if agencyID == 0 || userID == 0 {
		return PointLedgerPage{}, xerr.New(400, "invalid_user", "agency_id and user_id are required")
	}
	if err := s.requireAdminAgency(ctx, operatorID, agencyID); err != nil {
		return PointLedgerPage{}, err
	}
	return s.listLedgers(ctx, userID, agencyID, cursor, limit)
}

func (s *PointsService) AdjustByAdmin(ctx context.Context, operatorID uint64, agencyID uint64, userID uint64, delta int64, remark string, requestID string) (AddPointsResult, error) {
	if agencyID == 0 || userID == 0 {
		return AddPointsResult{}, xerr.New(400, "invalid_user", "agency_id and user_id are required")
	}
	if delta == 0 || delta > MaxPointsPerAdjustment || delta < -MaxPointsPerAdjustment {
		return AddPointsResult{}, xerr.New(400, "invalid_points", "adjustment must be between -100000 and 100000 and cannot be zero")
	}
	remark = strings.TrimSpace(remark)
	if !utf8.ValidString(remark) || utf8.RuneCountInString(remark) < 2 || utf8.RuneCountInString(remark) > MaxPointRemarkRunes {
		return AddPointsResult{}, xerr.New(400, "invalid_remark", "adjustment remark must be between 2 and 512 characters")
	}
	requestID, err := validateRequestID(requestID)
	if err != nil {
		return AddPointsResult{}, err
	}
	if err := s.requireAdminAgency(ctx, operatorID, agencyID); err != nil {
		return AddPointsResult{}, err
	}
	requestHash := idempotencyHash(
		strconv.FormatUint(agencyID, 10),
		strconv.FormatUint(userID, 10),
		strconv.FormatInt(delta, 10),
		remark,
	)
	if existing, found, err := loadIdempotentResult[AddPointsResult](ctx, s.db, operatorID, idempotencyAdminAdjust, requestID, requestHash); found || err != nil {
		return existing, err
	}

	var result AddPointsResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		idempotency, err := createIdempotencyRecord(tx, operatorID, idempotencyAdminAdjust, requestID, requestHash)
		if err != nil {
			return err
		}
		isAdmin, err := activeAdminAgencyTx(tx, operatorID, agencyID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return xerr.New(403, "admin_forbidden", "admin no longer has permission for this agency")
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "user_not_found", "user not found")
			}
			return err
		}
		account, err := lockAgencyPointAccount(tx, agencyID, userID)
		if err != nil {
			return err
		}
		before := account.Balance
		if before < 0 || before > MaxPointBalance || delta > MaxPointBalance-before || delta < -before {
			return xerr.New(400, "points_balance_limit", "adjustment would move points balance outside the allowed range")
		}
		after := before + delta
		if err := tx.Model(&account).Update("balance", after).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("points_balance", after).Error; err != nil {
			return err
		}
		ledger := model.PointLedger{
			AgencyID:     agencyID,
			UserID:       userID,
			BeforePoints: before,
			DeltaPoints:  delta,
			AfterPoints:  after,
			Type:         PointLedgerAdminAdjust,
			OperatorID:   operatorID,
			Remark:       remark,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
		result = AddPointsResult{
			AgencyID:     agencyID,
			UserID:       userID,
			BeforePoints: before,
			DeltaPoints:  delta,
			AfterPoints:  after,
			LedgerID:     ledger.ID,
		}
		return saveIdempotentResult(tx, &idempotency, result)
	})
	if err != nil {
		if existing, found, loadErr := loadIdempotentResult[AddPointsResult](ctx, s.db, operatorID, idempotencyAdminAdjust, requestID, requestHash); found || loadErr != nil {
			return existing, loadErr
		}
		return AddPointsResult{}, err
	}
	return result, nil
}

func (s *PointsService) listLedgers(ctx context.Context, userID uint64, agencyID uint64, cursor uint64, limit int) (PointLedgerPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	query := s.db.WithContext(ctx).
		Table("point_ledgers").
		Select("point_ledgers.id, point_ledgers.agency_id, agencies.name AS agency_name, point_ledgers.group_id, idol_groups.name AS group_name, point_ledgers.before_points, point_ledgers.delta_points, point_ledgers.after_points, point_ledgers.type, point_ledgers.operator_id, point_ledgers.reference_id, point_ledgers.remark, point_ledgers.created_at").
		Joins("LEFT JOIN agencies ON agencies.id = point_ledgers.agency_id").
		Joins("LEFT JOIN idol_groups ON idol_groups.id = point_ledgers.group_id AND idol_groups.agency_id = point_ledgers.agency_id").
		Where("point_ledgers.user_id = ?", userID)
	if agencyID > 0 {
		query = query.Where("point_ledgers.agency_id = ?", agencyID)
	}
	if cursor > 0 {
		query = query.Where("point_ledgers.id < ?", cursor)
	}
	var items []PointLedgerDTO
	if err := query.Order("point_ledgers.id DESC").Limit(limit + 1).Scan(&items).Error; err != nil {
		return PointLedgerPage{}, err
	}
	page := PointLedgerPage{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *PointsService) requireAdminAgency(ctx context.Context, operatorID uint64, agencyID uint64) error {
	ok, err := s.authService.HasAdminAgency(ctx, operatorID, agencyID)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.New(403, "admin_forbidden", "admin has no permission for this agency")
	}
	return nil
}

func (s *PointsService) CreateIdentityQRCode(ctx context.Context, userID uint64) (IdentityQRCodeDTO, error) {
	token, err := randomToken()
	if err != nil {
		return IdentityQRCodeDTO{}, err
	}

	expiresAt := time.Now().Add(5 * time.Minute)
	record := model.IdentityQRToken{
		TokenHash: tokenHash(token),
		UserID:    userID,
		ExpiresAt: expiresAt,
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return xerr.New(404, "user_not_found", "user not found")
			}
			return err
		}
		if user.Status != model.UserStatusNormal {
			return xerr.New(403, "user_disabled", "user is disabled")
		}
		if err := tx.Where("user_id = ?", userID).Delete(&model.IdentityQRToken{}).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		return IdentityQRCodeDTO{}, err
	}

	payload := IdentityQRPrefix + token
	png, err := qrcode.Encode(payload, qrcode.Medium, 512)
	if err != nil {
		return IdentityQRCodeDTO{}, err
	}

	return IdentityQRCodeDTO{
		Token:     token,
		Payload:   payload,
		QRImage:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		ExpiresAt: expiresAt,
	}, nil
}

func (s *PointsService) AddByStaff(ctx context.Context, operatorID uint64, agencyID uint64, groupID uint64, identityToken string, points int64, remark string, requestID string) (AddPointsResult, error) {
	return s.SubmitStaffGrant(ctx, operatorID, StaffGrantInput{
		AgencyID:      agencyID,
		GroupID:       groupID,
		IdentityToken: identityToken,
		Points:        points,
		GrantMode:     model.PointGrantModeManual,
		Remark:        remark,
		RequestID:     requestID,
	})
}

func validateIdentityToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(token, IdentityQRPrefix) {
		token = strings.TrimPrefix(token, IdentityQRPrefix)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != identityQRTokenBytes {
		return "", xerr.New(400, "invalid_qr_token", "identity token is invalid")
	}
	return token, nil
}

func randomToken() (string, error) {
	var b [identityQRTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
