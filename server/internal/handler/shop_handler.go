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

type ShopHandler struct {
	shopService *service.ShopService
}

type exchangeGoodsRequest struct {
	GoodsID             uint64 `json:"goods_id"`
	RequestID           string `json:"request_id"`
	ExpectedPricePoints int64  `json:"expected_price_points"`
}

type redeemOrderRequest struct {
	RedeemToken string `json:"redeem_token"`
	RequestID   string `json:"request_id"`
}

type cancelOrderRequest struct {
	RequestID string `json:"request_id"`
}

func NewShopHandler(shopService *service.ShopService) *ShopHandler {
	return &ShopHandler{shopService: shopService}
}

func (h *ShopHandler) RecoverExchange(ctx context.Context, c *app.RequestContext) {
	userID, ok := currentOperator(c)
	if !ok {
		return
	}
	goodsID, err := parseUintQuery(c, "goods_id")
	if err != nil || goodsID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_goods", "goods_id is invalid")
		return
	}
	result, err := h.shopService.RecoverExchange(ctx, userID, goodsID, c.Param("request_id"))
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"recovery": result})
}

func (h *ShopHandler) ListGoods(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	groupID, err := parseOptionalUint(c.Query("group_id"))
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	goods, err := h.shopService.ListGoods(ctx, userID, groupID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *ShopHandler) ListGroups(ctx context.Context, c *app.RequestContext) {
	groups, err := h.shopService.ListGroups(ctx)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"groups": groups})
}

func (h *ShopHandler) GetGoods(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	goodsID, err := parseOptionalUint(c.Param("id"))
	if err != nil || goodsID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_goods", "goods_id is invalid")
		return
	}
	goods, err := h.shopService.GetGoods(ctx, userID, goodsID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"goods": goods})
}

func (h *ShopHandler) ExchangeGoods(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}

	var req exchangeGoodsRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	result, err := h.shopService.ExchangeGoods(ctx, userID, req.GoodsID, req.RequestID, req.ExpectedPricePoints)
	if err != nil {
		response.FromError(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{"result": result})
}

func (h *ShopHandler) ListOrders(ctx context.Context, c *app.RequestContext) {
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
	page, err := h.shopService.ListOrdersPage(ctx, userID, cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"orders": page.Items, "page": page})
}

func (h *ShopHandler) GetOrder(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	orderID, err := parseOptionalUint(c.Param("id"))
	if err != nil || orderID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_order", "order_id is invalid")
		return
	}
	order, err := h.shopService.GetOrder(ctx, userID, orderID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"order": order})
}

func (h *ShopHandler) CancelOrder(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	orderID, err := parseOptionalUint(c.Param("id"))
	if err != nil || orderID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_order", "order_id is invalid")
		return
	}
	var req cancelOrderRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	result, err := h.shopService.CancelOrder(ctx, userID, orderID, req.RequestID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"result": result})
}

func (h *ShopHandler) CreateRedeemQRCode(ctx context.Context, c *app.RequestContext) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}
	orderID, err := parseOptionalUint(c.Param("id"))
	if err != nil || orderID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_order", "order_id is invalid")
		return
	}
	qr, err := h.shopService.CreateRedeemQRCode(ctx, userID, orderID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"redeem_qr": qr})
}

func (h *ShopHandler) StaffRedeemOrder(ctx context.Context, c *app.RequestContext) {
	staffID, ok := middleware.CurrentUserID(c)
	if !ok {
		response.Error(c, consts.StatusUnauthorized, "missing_token", "missing authorization token")
		return
	}

	var req redeemOrderRequest
	if err := c.BindAndValidate(&req); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	req.RedeemToken = strings.TrimSpace(req.RedeemToken)
	result, err := h.shopService.RedeemOrderByStaff(ctx, staffID, req.RedeemToken, req.RequestID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"result": result})
}

func parseOptionalUint(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}
