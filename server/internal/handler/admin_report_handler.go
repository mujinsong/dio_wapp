package handler

import (
	"context"
	"strings"

	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type AdminReportHandler struct {
	service *service.AdminReportService
}

func NewAdminReportHandler(reportService *service.AdminReportService) *AdminReportHandler {
	return &AdminReportHandler{service: reportService}
}

func (h *AdminReportHandler) Reconcile(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil || agencyID == 0 {
		response.Error(c, 400, "invalid_agency", "agency_id is invalid")
		return
	}
	cursor, limit, err := parseLedgerPage(c)
	if err != nil {
		response.Error(c, 400, "invalid_pagination", "cursor or limit is invalid")
		return
	}
	page, err := h.service.Reconcile(ctx, operatorID, agencyID, c.Query("kind"), cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(200, map[string]any{"page": page})
}

func (h *AdminReportHandler) Overview(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil || agencyID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}
	report, err := h.service.Overview(ctx, operatorID, agencyID, strings.TrimSpace(c.Query("date_from")), strings.TrimSpace(c.Query("date_to")))
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"report": report})
}
