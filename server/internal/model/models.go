package model

import "time"

const (
	UserStatusNormal   = "normal"
	UserStatusDisabled = "disabled"

	MemberStatusNormal   = "normal"
	MemberStatusDisabled = "disabled"

	StaffRoleStaff      = "staff"
	StaffRoleTeamLeader = "team_leader"

	GoodsStatusOnSale  = "on_sale"
	GoodsStatusOffSale = "off_sale"
	GoodsStatusDeleted = "deleted"

	ExchangeOrderStatusPending  = "pending"
	ExchangeOrderStatusRedeemed = "redeemed"
	ExchangeOrderStatusCanceled = "canceled"
	ExchangeOrderStatusExpired  = "expired"

	PointGrantModeManual = "manual"
	PointGrantModeTicket = "ticket"

	GoodsChangeActionCreate = "create"
	GoodsChangeActionUpdate = "update"
	GoodsChangeActionDelete = "delete"

	ApprovalStatusPending  = "pending"
	ApprovalStatusApproved = "approved"
	ApprovalStatusRejected = "rejected"

	StockMovementInitial       = "initial"
	StockMovementAdminAdjust   = "admin_adjust"
	StockMovementGoodsApproval = "goods_approval"
	StockMovementExchange      = "exchange"
	StockMovementOrderCancel   = "order_cancel"
	StockMovementOrderExpire   = "order_expire"
)

type User struct {
	ID            uint64     `gorm:"primaryKey" json:"id"`
	OpenID        string     `gorm:"size:128;uniqueIndex;not null" json:"openid"`
	UnionID       string     `gorm:"size:128;index" json:"unionid"`
	Nickname      string     `gorm:"size:128" json:"nickname"`
	Avatar        string     `gorm:"size:512" json:"avatar"`
	Phone         string     `gorm:"size:32" json:"phone"`
	PointsBalance int64      `gorm:"not null;default:0;check:chk_users_points_balance,points_balance >= 0 AND points_balance <= 1000000000" json:"points_balance"`
	Status        string     `gorm:"size:32;not null;default:normal;index" json:"status"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AgencyPointAccount is the authoritative point balance for one user inside one agency.
// Relationships are validated by services; the database intentionally has no foreign keys.
type AgencyPointAccount struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	AgencyID  uint64    `gorm:"not null;uniqueIndex:idx_agency_point_account,priority:1" json:"agency_id"`
	UserID    uint64    `gorm:"not null;uniqueIndex:idx_agency_point_account,priority:2;index" json:"user_id"`
	Balance   int64     `gorm:"not null;default:0;check:chk_agency_accounts_balance,balance >= 0 AND balance <= 1000000000" json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Agency struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	SingletonKey uint8     `gorm:"not null;default:1;uniqueIndex" json:"-"`
	Name         string    `gorm:"size:128;uniqueIndex;not null" json:"name"`
	Status       string    `gorm:"size:32;not null;default:normal;index" json:"status"`
	Remark       string    `gorm:"size:512" json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type IdolGroup struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	AgencyID    uint64    `gorm:"not null;index;uniqueIndex:idx_agency_group_name" json:"agency_id"`
	Name        string    `gorm:"size:128;not null;uniqueIndex:idx_agency_group_name" json:"name"`
	Description string    `gorm:"size:512" json:"description"`
	Status      string    `gorm:"size:32;not null;default:normal;index" json:"status"`
	Sort        int       `gorm:"not null;default:0;index" json:"sort"`
	Remark      string    `gorm:"size:512" json:"remark"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type StaffMember struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	AgencyID    uint64    `gorm:"not null;index;uniqueIndex:idx_staff_user_group" json:"agency_id"`
	GroupID     uint64    `gorm:"not null;index;uniqueIndex:idx_staff_user_group" json:"group_id"`
	UserID      uint64    `gorm:"not null;index;uniqueIndex:idx_staff_user_group" json:"user_id"`
	Role        string    `gorm:"size:32;not null;default:staff;index" json:"role"`
	Status      string    `gorm:"size:32;not null;default:normal;index" json:"status"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Remark      string    `gorm:"size:512" json:"remark"`
	CreatedBy   uint64    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type StaffMemberAuditLog struct {
	ID            uint64    `gorm:"primaryKey" json:"id"`
	AgencyID      uint64    `gorm:"not null;index" json:"agency_id"`
	GroupID       uint64    `gorm:"not null;index" json:"group_id"`
	StaffMemberID uint64    `gorm:"not null;index" json:"staff_member_id"`
	TargetUserID  uint64    `gorm:"not null;index" json:"target_user_id"`
	OperatorID    uint64    `gorm:"not null;index" json:"operator_id"`
	Action        string    `gorm:"size:32;not null;index" json:"action"`
	BeforeRole    string    `gorm:"size:32" json:"before_role"`
	AfterRole     string    `gorm:"size:32" json:"after_role"`
	BeforeStatus  string    `gorm:"size:32" json:"before_status"`
	AfterStatus   string    `gorm:"size:32" json:"after_status"`
	Remark        string    `gorm:"size:512" json:"remark"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

type AdminMember struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	AgencyID    uint64    `gorm:"not null;index;uniqueIndex:idx_admin_user_agency" json:"agency_id"`
	UserID      uint64    `gorm:"not null;index;uniqueIndex:idx_admin_user_agency" json:"user_id"`
	Status      string    `gorm:"size:32;not null;default:normal;index" json:"status"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Remark      string    `gorm:"size:512" json:"remark"`
	CreatedBy   uint64    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type IdentityQRToken struct {
	ID        uint64    `gorm:"primaryKey;index:idx_identity_qr_expiry,priority:2" json:"id"`
	TokenHash string    `gorm:"size:64;uniqueIndex;not null" json:"token_hash"`
	UserID    uint64    `gorm:"not null;index" json:"user_id"`
	ExpiresAt time.Time `gorm:"not null;index;index:idx_identity_qr_expiry,priority:1" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type PointLedger struct {
	ID           uint64    `gorm:"primaryKey;index:idx_point_user_cursor,priority:2" json:"id"`
	AgencyID     uint64    `gorm:"not null;index" json:"agency_id"`
	GroupID      uint64    `gorm:"not null;index" json:"group_id"`
	UserID       uint64    `gorm:"not null;index;index:idx_point_user_cursor,priority:1" json:"user_id"`
	BeforePoints int64     `gorm:"not null;check:chk_point_ledgers_before,before_points >= 0 AND before_points <= 1000000000" json:"before_points"`
	DeltaPoints  int64     `gorm:"not null;check:chk_point_ledgers_delta,delta_points >= -1000000000 AND delta_points <= 1000000000" json:"delta_points"`
	AfterPoints  int64     `gorm:"not null;check:chk_point_ledgers_after,after_points >= 0 AND after_points <= 1000000000" json:"after_points"`
	Type         string    `gorm:"size:32;not null;index" json:"type"`
	OperatorID   uint64    `gorm:"not null;index" json:"operator_id"`
	ReferenceID  uint64    `gorm:"not null;default:0;index" json:"reference_id"`
	Remark       string    `gorm:"size:512" json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
}

// PointGrantRequest is the auditable source record for every staff point grant.
// Ticket-mode records also serve as the structured offline sale record.
type PointGrantRequest struct {
	ID              uint64     `gorm:"primaryKey;index:idx_point_grant_review,priority:4;index:idx_point_grant_admin_list,priority:4" json:"id"`
	AgencyID        uint64     `gorm:"not null;index;index:idx_point_grant_review,priority:1;index:idx_point_grant_admin_list,priority:1" json:"agency_id"`
	GroupID         uint64     `gorm:"not null;index;index:idx_point_grant_review,priority:2" json:"group_id"`
	UserID          uint64     `gorm:"not null;index" json:"user_id"`
	SubmittedBy     uint64     `gorm:"not null;index;index:idx_point_grant_submitter_day,priority:1;uniqueIndex:idx_point_grant_client,priority:1" json:"submitted_by"`
	GrantMode       string     `gorm:"size:16;not null;index" json:"grant_mode"`
	SaleNo          *string    `gorm:"size:64;uniqueIndex" json:"sale_no,omitempty"`
	TicketUnitPrice int64      `gorm:"not null;default:0;check:chk_point_grant_ticket_price,ticket_unit_price >= 0 AND ticket_unit_price <= 100000" json:"ticket_unit_price"`
	TicketCount     int64      `gorm:"not null;default:0;check:chk_point_grant_ticket_count,ticket_count >= 0 AND ticket_count <= 100000" json:"ticket_count"`
	TotalAmount     int64      `gorm:"not null;default:0;check:chk_point_grant_total_amount,total_amount >= 0 AND total_amount <= 100000" json:"total_amount"`
	Points          int64      `gorm:"not null;check:chk_point_grant_points,points > 0 AND points <= 100000" json:"points"`
	PaymentMethod   string     `gorm:"size:32" json:"payment_method"`
	Remark          string     `gorm:"size:512" json:"remark"`
	RequestStatus   string     `gorm:"size:16;not null;index;index:idx_point_grant_review,priority:3;index:idx_point_grant_admin_list,priority:2" json:"request_status"`
	LedgerID        *uint64    `gorm:"uniqueIndex" json:"ledger_id,omitempty"`
	ReviewedBy      uint64     `gorm:"not null;default:0;index" json:"reviewed_by"`
	ReviewRemark    string     `gorm:"size:512" json:"review_remark"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	ClientRequestID string     `gorm:"size:64;not null;uniqueIndex:idx_point_grant_client,priority:2" json:"client_request_id"`
	CreatedAt       time.Time  `gorm:"index;index:idx_point_grant_submitter_day,priority:2;index:idx_point_grant_admin_list,priority:3" json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type StaffPointDailyUsage struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	StaffID        uint64    `gorm:"not null;uniqueIndex:idx_staff_point_usage_day,priority:1" json:"staff_id"`
	GrantDate      string    `gorm:"size:10;not null;uniqueIndex:idx_staff_point_usage_day,priority:2" json:"grant_date"`
	ReservedPoints int64     `gorm:"not null;default:0;check:chk_staff_daily_reserved,reserved_points >= 0 AND reserved_points <= 200000" json:"reserved_points"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PointCorrectionRequest struct {
	ID                     uint64     `gorm:"primaryKey;index:idx_point_correction_group_review,priority:3;index:idx_point_correction_submitter_list,priority:3" json:"id"`
	AgencyID               uint64     `gorm:"not null;index;index:idx_point_correction_review,priority:1" json:"agency_id"`
	GroupID                uint64     `gorm:"not null;index;index:idx_point_correction_review,priority:2;index:idx_point_correction_group_review,priority:1" json:"group_id"`
	UserID                 uint64     `gorm:"not null;index" json:"user_id"`
	OriginalGrantRequestID uint64     `gorm:"not null;index" json:"original_grant_request_id"`
	OriginalLedgerID       uint64     `gorm:"not null;index" json:"original_ledger_id"`
	Points                 int64      `gorm:"not null;check:chk_point_correction_points,points > 0 AND points <= 100000" json:"points"`
	Reason                 string     `gorm:"size:512;not null" json:"reason"`
	RequestStatus          string     `gorm:"size:16;not null;index;index:idx_point_correction_review,priority:3;index:idx_point_correction_group_review,priority:2;index:idx_point_correction_submitter_list,priority:2" json:"request_status"`
	SubmittedBy            uint64     `gorm:"not null;index;index:idx_point_correction_submitter_list,priority:1;uniqueIndex:idx_point_correction_client,priority:1" json:"submitted_by"`
	ClientRequestID        string     `gorm:"size:64;not null;uniqueIndex:idx_point_correction_client,priority:2" json:"client_request_id"`
	RequestHash            string     `gorm:"size:64;not null" json:"-"`
	ConflictKey            *string    `gorm:"size:64;uniqueIndex" json:"-"`
	CorrectionLedgerID     *uint64    `gorm:"uniqueIndex" json:"correction_ledger_id,omitempty"`
	ReviewedBy             uint64     `gorm:"not null;default:0;index" json:"reviewed_by"`
	ReviewRemark           string     `gorm:"size:512" json:"review_remark"`
	ReviewedAt             *time.Time `json:"reviewed_at"`
	CreatedAt              time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type Goods struct {
	ID                     uint64     `gorm:"primaryKey" json:"id"`
	AgencyID               uint64     `gorm:"not null;index;index:idx_goods_catalog,priority:1" json:"agency_id"`
	GroupID                uint64     `gorm:"not null;index;index:idx_goods_catalog,priority:2" json:"group_id"`
	Name                   string     `gorm:"size:128;not null" json:"name"`
	Description            string     `gorm:"size:512" json:"description"`
	ImageURL               string     `gorm:"size:512" json:"image_url"`
	PricePoints            int64      `gorm:"not null;index;check:chk_goods_price,price_points > 0 AND price_points <= 1000000000" json:"price_points"`
	Stock                  int64      `gorm:"not null;default:0;check:chk_goods_stock,stock >= 0 AND stock <= 1000000000" json:"stock"`
	PurchaseLimitPerUser   int64      `gorm:"not null;default:0;check:chk_goods_purchase_limit,purchase_limit_per_user >= 0 AND purchase_limit_per_user <= 1000000000" json:"purchase_limit_per_user"`
	PurchaseLimitHours     int64      `gorm:"not null;default:0;check:chk_goods_purchase_hours,purchase_limit_hours >= 0 AND purchase_limit_hours <= 8760" json:"purchase_limit_hours"`
	PurchaseLimitStartedAt *time.Time `gorm:"index" json:"purchase_limit_started_at"`
	SaleStartsAt           *time.Time `gorm:"index" json:"sale_starts_at"`
	Status                 string     `gorm:"size:32;not null;default:on_sale;index;index:idx_goods_catalog,priority:3" json:"status"`
	Sort                   int        `gorm:"not null;default:0;index;index:idx_goods_catalog,priority:4" json:"sort"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// StockMovement is the immutable audit trail for every stock quantity change.
type StockMovement struct {
	ID          uint64    `gorm:"primaryKey;index:idx_stock_goods_cursor,priority:2" json:"id"`
	AgencyID    uint64    `gorm:"not null;index;index:idx_stock_agency_created,priority:1" json:"agency_id"`
	GroupID     uint64    `gorm:"not null;index" json:"group_id"`
	GoodsID     uint64    `gorm:"not null;index;index:idx_stock_goods_cursor,priority:1" json:"goods_id"`
	BeforeStock int64     `gorm:"not null;check:chk_stock_movement_before,before_stock >= 0 AND before_stock <= 1000000000" json:"before_stock"`
	DeltaStock  int64     `gorm:"not null;check:chk_stock_movement_delta,delta_stock >= -1000000000 AND delta_stock <= 1000000000" json:"delta_stock"`
	AfterStock  int64     `gorm:"not null;check:chk_stock_movement_after,after_stock >= 0 AND after_stock <= 1000000000" json:"after_stock"`
	Type        string    `gorm:"size:32;not null;index" json:"type"`
	OperatorID  uint64    `gorm:"not null;default:0;index" json:"operator_id"`
	ReferenceID uint64    `gorm:"not null;default:0;index" json:"reference_id"`
	Remark      string    `gorm:"size:512" json:"remark"`
	CreatedAt   time.Time `gorm:"index;index:idx_stock_agency_created,priority:2" json:"created_at"`
}

type GoodsChangeRequest struct {
	ID                   uint64     `gorm:"primaryKey;index:idx_goods_request_staff_list,priority:3;index:idx_goods_request_group_review,priority:4;index:idx_goods_request_admin_review,priority:3" json:"id"`
	AgencyID             uint64     `gorm:"not null;index;index:idx_goods_request_group_review,priority:1;index:idx_goods_request_admin_review,priority:1" json:"agency_id"`
	GroupID              uint64     `gorm:"not null;index;index:idx_goods_request_staff_list,priority:1;index:idx_goods_request_group_review,priority:2" json:"group_id"`
	GoodsID              uint64     `gorm:"not null;default:0;index" json:"goods_id"`
	Action               string     `gorm:"size:16;not null;index" json:"action"`
	Name                 string     `gorm:"size:128;not null" json:"name"`
	Description          string     `gorm:"size:512" json:"description"`
	ImageURL             string     `gorm:"size:512" json:"image_url"`
	PricePoints          int64      `gorm:"not null;default:0" json:"price_points"`
	Stock                int64      `gorm:"not null;default:0" json:"stock"`
	PurchaseLimitPerUser int64      `gorm:"not null;default:0" json:"purchase_limit_per_user"`
	PurchaseLimitHours   int64      `gorm:"not null;default:0" json:"purchase_limit_hours"`
	SaleStartsAt         *time.Time `gorm:"index" json:"sale_starts_at"`
	TargetStatus         string     `gorm:"size:32;not null" json:"target_status"`
	Sort                 int        `gorm:"not null;default:0" json:"sort"`
	BaseGoodsHash        string     `gorm:"size:64" json:"base_goods_hash"`
	ConflictKey          *string    `gorm:"size:64;uniqueIndex" json:"-"`
	RequestStatus        string     `gorm:"size:16;not null;index;index:idx_goods_request_group_review,priority:3;index:idx_goods_request_admin_review,priority:2" json:"request_status"`
	SubmittedBy          uint64     `gorm:"not null;index;index:idx_goods_request_staff_list,priority:2;uniqueIndex:idx_goods_request_submitter_client,priority:1" json:"submitted_by"`
	ClientRequestID      string     `gorm:"size:64;not null;uniqueIndex:idx_goods_request_submitter_client,priority:2" json:"client_request_id"`
	RequestHash          string     `gorm:"size:64;not null" json:"-"`
	ReviewedBy           uint64     `gorm:"not null;default:0;index" json:"reviewed_by"`
	ReviewRemark         string     `gorm:"size:512" json:"review_remark"`
	ReviewedAt           *time.Time `json:"reviewed_at"`
	CreatedAt            time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type ExchangeOrder struct {
	ID              uint64     `gorm:"primaryKey;index:idx_exchange_user_cursor,priority:2;index:idx_exchange_admin_list,priority:4;index:idx_exchange_expiry_scan,priority:3;index:idx_exchange_user_expiry,priority:4" json:"id"`
	OrderNo         string     `gorm:"size:64;uniqueIndex;not null" json:"order_no"`
	AgencyID        uint64     `gorm:"not null;index;index:idx_exchange_admin_list,priority:1" json:"agency_id"`
	GroupID         uint64     `gorm:"not null;index" json:"group_id"`
	UserID          uint64     `gorm:"not null;index;index:idx_exchange_user_cursor,priority:1;index:idx_exchange_user_goods_status,priority:1;index:idx_exchange_user_expiry,priority:1" json:"user_id"`
	GoodsID         uint64     `gorm:"not null;index;index:idx_exchange_user_goods_status,priority:2" json:"goods_id"`
	GoodsName       string     `gorm:"size:128;not null" json:"goods_name"`
	PointsCost      int64      `gorm:"not null;check:chk_exchange_points_cost,points_cost > 0 AND points_cost <= 1000000000" json:"points_cost"`
	Status          string     `gorm:"size:32;not null;default:pending;index;index:idx_exchange_user_goods_status,priority:3;index:idx_exchange_admin_list,priority:2;index:idx_exchange_expiry_scan,priority:1;index:idx_exchange_user_expiry,priority:2" json:"status"`
	RedeemTokenHash string     `gorm:"size:64;uniqueIndex;not null" json:"redeem_token_hash"`
	RedeemedBy      uint64     `gorm:"not null;default:0;index;index:idx_exchange_redeemer_time,priority:1" json:"redeemed_by"`
	RedeemedAt      *time.Time `gorm:"index:idx_exchange_redeemer_time,priority:2" json:"redeemed_at"`
	CanceledAt      *time.Time `json:"canceled_at"`
	ExpiresAt       *time.Time `gorm:"index;index:idx_exchange_expiry_scan,priority:2;index:idx_exchange_user_expiry,priority:3" json:"expires_at"`
	ExpiredAt       *time.Time `json:"expired_at"`
	RefundLedgerID  *uint64    `gorm:"uniqueIndex" json:"refund_ledger_id,omitempty"`
	CreatedAt       time.Time  `gorm:"index:idx_exchange_user_goods_status,priority:4;index:idx_exchange_admin_list,priority:3" json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type IdempotencyRecord struct {
	ID           uint64    `gorm:"primaryKey;index:idx_idempotency_cleanup,priority:2;index:idx_idempotency_compaction,priority:3" json:"id"`
	ActorID      uint64    `gorm:"not null;uniqueIndex:idx_idempotency_scope,priority:1" json:"actor_id"`
	Operation    string    `gorm:"size:32;not null;uniqueIndex:idx_idempotency_scope,priority:2" json:"operation"`
	RequestID    string    `gorm:"size:64;not null;uniqueIndex:idx_idempotency_scope,priority:3" json:"request_id"`
	RequestHash  string    `gorm:"size:64;not null" json:"request_hash"`
	ResponseJSON string    `gorm:"type:text;not null" json:"response_json"`
	ResourceID   uint64    `gorm:"not null;default:0" json:"-"`
	Compacted    bool      `gorm:"not null;default:false;index:idx_idempotency_compaction,priority:1" json:"-"`
	CreatedAt    time.Time `gorm:"index;index:idx_idempotency_cleanup,priority:1;index:idx_idempotency_compaction,priority:2" json:"created_at"`
}
