package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type AdminOperationsHandler struct {
	service *service.AdminOperationsService
}

func NewAdminOperationsHandler(operationsService *service.AdminOperationsService) *AdminOperationsHandler {
	return &AdminOperationsHandler{service: operationsService}
}

func (h *AdminOperationsHandler) ListSales(ctx context.Context, c *app.RequestContext) {
	operatorID, filter, ok := adminOperationRequest(c)
	if !ok {
		return
	}
	page, err := h.service.ListSales(ctx, operatorID, filter)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"page": page})
}

func (h *AdminOperationsHandler) ListOrders(ctx context.Context, c *app.RequestContext) {
	operatorID, filter, ok := adminOperationRequest(c)
	if !ok {
		return
	}
	page, err := h.service.ListOrders(ctx, operatorID, filter)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"page": page})
}

func (h *AdminOperationsHandler) Export(ctx context.Context, c *app.RequestContext) {
	operatorID, filter, ok := adminOperationRequest(c)
	if !ok {
		return
	}
	exported, err := h.service.ExportCSV(ctx, operatorID, strings.TrimSpace(c.Query("type")), filter)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.Response.Header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exported.Filename))
	c.Data(consts.StatusOK, "text/csv; charset=utf-8", exported.Content)
}

func adminOperationRequest(c *app.RequestContext) (uint64, service.AdminOperationFilter, bool) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return 0, service.AdminOperationFilter{}, false
	}
	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil || agencyID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return 0, service.AdminOperationFilter{}, false
	}
	cursor, err := parseUintQuery(c, "cursor")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return 0, service.AdminOperationFilter{}, false
	}
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			response.Error(c, consts.StatusBadRequest, "invalid_pagination", "limit is invalid")
			return 0, service.AdminOperationFilter{}, false
		}
		limit = parsed
	}
	return operatorID, service.AdminOperationFilter{
		AgencyID: agencyID,
		DateFrom: strings.TrimSpace(c.Query("date_from")),
		DateTo:   strings.TrimSpace(c.Query("date_to")),
		Status:   strings.TrimSpace(c.Query("status")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Cursor:   cursor,
		Limit:    limit,
	}, true
}
