package handler

import (
	"context"
	"strconv"
	"strings"

	"dio_wapp/server/internal/middleware"
	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type AdminGoodsHandler struct {
	adminGoodsService *service.AdminGoodsService
}

func NewAdminGoodsHandler(adminGoodsService *service.AdminGoodsService) *AdminGoodsHandler {
	return &AdminGoodsHandler{adminGoodsService: adminGoodsService}
}

func (h *AdminGoodsHandler) ListGroups(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}

	groups, err := h.adminGoodsService.ListGroups(ctx, operatorID, agencyID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"groups": groups})
}

func (h *AdminGoodsHandler) CreateGroup(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	var req service.AdminGroupInput
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	group, err := h.adminGoodsService.CreateGroup(ctx, operatorID, req)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"group": group})
}

func (h *AdminGoodsHandler) UpdateGroup(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	groupID, err := parseUintParam(c, "id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}

	var req service.AdminGroupInput
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	group, err := h.adminGoodsService.UpdateGroup(ctx, operatorID, groupID, req)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"group": group})
}

func (h *AdminGoodsHandler) DeleteGroup(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	groupID, err := parseUintParam(c, "id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	if err := h.adminGoodsService.DeleteGroup(ctx, operatorID, groupID); err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"ok": true})
}

func (h *AdminGoodsHandler) ListGoods(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}

	goods, err := h.adminGoodsService.ListGoods(ctx, operatorID, agencyID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *AdminGoodsHandler) CreateGoods(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	var req service.AdminGoodsInput
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	goods, err := h.adminGoodsService.CreateGoods(ctx, operatorID, req)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *AdminGoodsHandler) UpdateGoods(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	goodsID, err := parseUintParam(c, "id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_goods", "goods_id is invalid")
		return
	}

	var req service.AdminGoodsInput
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	goods, err := h.adminGoodsService.UpdateGoods(ctx, operatorID, goodsID, req)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *AdminGoodsHandler) DeleteGoods(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}

	goodsID, err := parseUintParam(c, "id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_goods", "goods_id is invalid")
		return
	}

	if err := h.adminGoodsService.DeleteGoods(ctx, operatorID, goodsID); err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"ok": true})
}

func currentOperator(c *app.RequestContext) (uint64, bool) {
	operatorID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return 0, false
	}
	return operatorID, true
}

func parseUintQuery(c *app.RequestContext, name string) (uint64, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func parseUintParam(c *app.RequestContext, name string) (uint64, error) {
	value := strings.TrimSpace(c.Param(name))
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}
