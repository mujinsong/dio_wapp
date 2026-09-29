package handler

import (
	"context"
	"strings"

	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type StaffWorkbenchHandler struct {
	service *service.StaffWorkbenchService
}

func NewStaffWorkbenchHandler(workbenchService *service.StaffWorkbenchService) *StaffWorkbenchHandler {
	return &StaffWorkbenchHandler{service: workbenchService}
}

func (h *StaffWorkbenchHandler) Get(ctx context.Context, c *app.RequestContext) {
	staffID, ok := currentOperator(c)
	if !ok {
		return
	}
	agencyID, agencyErr := parseUintQuery(c, "agency_id")
	groupID, groupErr := parseUintQuery(c, "group_id")
	if agencyErr != nil || groupErr != nil || agencyID == 0 || groupID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "agency_id or group_id is invalid")
		return
	}
	workbench, err := h.service.Get(ctx, staffID, agencyID, groupID, strings.TrimSpace(c.Query("date")))
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"workbench": workbench})
}
