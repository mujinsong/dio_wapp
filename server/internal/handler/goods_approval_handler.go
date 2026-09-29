package handler

import (
	"context"
	"strings"

	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type GoodsApprovalHandler struct {
	service *service.GoodsApprovalService
}

type reviewGoodsRequest struct {
	Remark string `json:"remark"`
}

func NewGoodsApprovalHandler(goodsApprovalService *service.GoodsApprovalService) *GoodsApprovalHandler {
	return &GoodsApprovalHandler{service: goodsApprovalService}
}

func (h *GoodsApprovalHandler) ListStaffGoods(ctx context.Context, c *app.RequestContext) {
	staffID, ok := approvalOperator(c)
	if !ok {
		return
	}
	groupID, err := parseUintQuery(c, "group_id")
	if err != nil || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	goods, err := h.service.ListStaffGoods(ctx, staffID, groupID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *GoodsApprovalHandler) ListStaffRequests(ctx context.Context, c *app.RequestContext) {
	staffID, ok := approvalOperator(c)
	if !ok {
		return
	}
	groupID, err := parseUintQuery(c, "group_id")
	if err != nil || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.service.ListStaffRequests(ctx, staffID, groupID, cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *GoodsApprovalHandler) SubmitStaffRequest(ctx context.Context, c *app.RequestContext) {
	staffID, ok := approvalOperator(c)
	if !ok {
		return
	}
	var input service.StaffGoodsRequestInput
	if err := c.BindAndValidate(&input); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	request, err := h.service.Submit(ctx, staffID, input)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"request": request})
}

func (h *GoodsApprovalHandler) ListTeamLeaderRequests(ctx context.Context, c *app.RequestContext) {
	leaderID, ok := approvalOperator(c)
	if !ok {
		return
	}
	groupID, err := parseUintQuery(c, "group_id")
	if err != nil || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.service.ListTeamLeaderRequests(ctx, leaderID, groupID, strings.TrimSpace(c.Query("status")), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *GoodsApprovalHandler) ListAdminRequests(ctx context.Context, c *app.RequestContext) {
	adminID, ok := approvalOperator(c)
	if !ok {
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.service.ListAdminRequests(ctx, adminID, strings.TrimSpace(c.Query("status")), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"requests": page.Items, "page": page})
}

func (h *GoodsApprovalHandler) Approve(ctx context.Context, c *app.RequestContext) {
	h.review(ctx, c, true)
}

func (h *GoodsApprovalHandler) Reject(ctx context.Context, c *app.RequestContext) {
	h.review(ctx, c, false)
}

func (h *GoodsApprovalHandler) review(ctx context.Context, c *app.RequestContext, approve bool) {
	reviewerID, ok := approvalOperator(c)
	if !ok {
		return
	}
	requestID, err := parseUintParam(c, "id")
	if err != nil || requestID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_goods_request", "request id is invalid")
		return
	}
	var input reviewGoodsRequest
	if len(c.Request.Body()) > 0 {
		if err := c.BindAndValidate(&input); err != nil {
			response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
	}
	request, err := h.service.Review(ctx, reviewerID, requestID, approve, input.Remark)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"request": request})
}

func approvalOperator(c *app.RequestContext) (uint64, bool) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return 0, false
	}
	return operatorID, true
}
