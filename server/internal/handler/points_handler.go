package handler

import (
	"context"
	"strconv"
	"strings"

	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"
	"dio_wapp/server/internal/xerr"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type PointsHandler struct {
	pointsService *service.PointsService
}

type addPointsRequest struct {
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

type adjustPointsRequest struct {
	AgencyID    uint64 `json:"agency_id"`
	UserID      uint64 `json:"user_id"`
	DeltaPoints int64  `json:"delta_points"`
	Remark      string `json:"remark"`
	RequestID   string `json:"request_id"`
}

type reviewPointGrantRequest struct {
	Remark string `json:"remark"`
}

type submitPointCorrectionRequest struct {
	OriginalGrantRequestID uint64 `json:"original_grant_request_id"`
	Reason                 string `json:"reason"`
	RequestID              string `json:"request_id"`
}

func NewPointsHandler(pointsService *service.PointsService) *PointsHandler {
	return &PointsHandler{pointsService: pointsService}
}

func (h *PointsHandler) CreateIdentityQRCode(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}

	qr, err := h.pointsService.CreateIdentityQRCode(ctx, userID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{"identity_qr": qr})
}

func (h *PointsHandler) ListMyLedgers(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.pointsService.ListUserLedgers(ctx, userID, cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"page": page})
}

func (h *PointsHandler) GetAdminPointUser(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	agencyID, err := parseOptionalUint(c.Query("agency_id"))
	if err != nil || agencyID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}
	userID, err := parseOptionalUint(c.Param("id"))
	if err != nil || userID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_user", "user_id is invalid")
		return
	}
	user, err := h.pointsService.GetAdminPointUser(ctx, operatorID, agencyID, userID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"user": user})
}

func (h *PointsHandler) ListAdminUserLedgers(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	agencyID, err := parseOptionalUint(c.Query("agency_id"))
	if err != nil || agencyID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}
	userID, err := parseOptionalUint(c.Param("id"))
	if err != nil || userID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_user", "user_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.pointsService.ListAdminUserLedgers(ctx, operatorID, agencyID, userID, cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"page": page})
}

func (h *PointsHandler) AdminAdjustPoints(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	var req adjustPointsRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	result, err := h.pointsService.AdjustByAdmin(ctx, operatorID, req.AgencyID, req.UserID, req.DeltaPoints, req.Remark, req.RequestID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"result": result})
}

func (h *PointsHandler) StaffAddPoints(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}

	var req addPointsRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	req.IdentityToken = strings.TrimSpace(req.IdentityToken)
	req.Remark = strings.TrimSpace(req.Remark)

	result, err := h.pointsService.SubmitStaffGrant(ctx, operatorID, service.StaffGrantInput{
		AgencyID:        req.AgencyID,
		GroupID:         req.GroupID,
		IdentityToken:   req.IdentityToken,
		Points:          req.Points,
		GrantMode:       req.GrantMode,
		TicketUnitPrice: req.TicketUnitPrice,
		TicketCount:     req.TicketCount,
		PaymentMethod:   req.PaymentMethod,
		Remark:          req.Remark,
		RequestID:       req.RequestID,
	})
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{"result": result})
}

func (h *PointsHandler) ListPointGrantRequests(ctx context.Context, c *app.RequestContext) {
	reviewerID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	groupID, err := parseOptionalUint(c.Query("group_id"))
	if err != nil || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.pointsService.ListPointGrantRequests(ctx, reviewerID, groupID, c.Query("status"), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *PointsHandler) ApprovePointGrant(ctx context.Context, c *app.RequestContext) {
	h.reviewPointGrant(ctx, c, true)
}

func (h *PointsHandler) RejectPointGrant(ctx context.Context, c *app.RequestContext) {
	h.reviewPointGrant(ctx, c, false)
}

func (h *PointsHandler) reviewPointGrant(ctx context.Context, c *app.RequestContext, approve bool) {
	reviewerID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	requestID, err := parseOptionalUint(c.Param("id"))
	if err != nil || requestID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "request id is invalid")
		return
	}
	var req reviewPointGrantRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	result, err := h.pointsService.ReviewPointGrant(ctx, reviewerID, requestID, approve, req.Remark)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"request": result})
}

func (h *PointsHandler) SubmitPointCorrection(ctx context.Context, c *app.RequestContext) {
	staffID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	var req submitPointCorrectionRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	result, err := h.pointsService.SubmitPointCorrection(ctx, staffID, req.OriginalGrantRequestID, req.Reason, req.RequestID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"request": result})
}

func (h *PointsHandler) ListOwnPointCorrections(ctx context.Context, c *app.RequestContext) {
	staffID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.pointsService.ListOwnPointCorrections(ctx, staffID, c.Query("status"), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *PointsHandler) ListReviewPointCorrections(ctx context.Context, c *app.RequestContext) {
	reviewerID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	groupID, err := parseOptionalUint(c.Query("group_id"))
	if err != nil || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.pointsService.ListReviewPointCorrections(ctx, reviewerID, groupID, c.Query("status"), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *PointsHandler) ApprovePointCorrection(ctx context.Context, c *app.RequestContext) {
	h.reviewPointCorrection(ctx, c, true)
}

func (h *PointsHandler) RejectPointCorrection(ctx context.Context, c *app.RequestContext) {
	h.reviewPointCorrection(ctx, c, false)
}

func (h *PointsHandler) reviewPointCorrection(ctx context.Context, c *app.RequestContext, approve bool) {
	reviewerID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	requestID, err := parseOptionalUint(c.Param("id"))
	if err != nil || requestID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_correction", "correction request id is invalid")
		return
	}
	var req reviewPointGrantRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	result, err := h.pointsService.ReviewPointCorrection(ctx, reviewerID, requestID, approve, req.Remark)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"request": result})
}

func parseLedgerPage(c *app.RequestContext) (uint64, int, error) {
	cursor, err := parseOptionalUint(c.Query("cursor"))
	if err != nil {
		return 0, 0, err
	}
	limit := 20
	rawLimit := strings.TrimSpace(c.Query("limit"))
	if rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > 50 {
			return 0, 0, xerr.New(400, "invalid_pagination", "limit is invalid")
		}
		limit = parsed
	}
	return cursor, limit, nil
}
