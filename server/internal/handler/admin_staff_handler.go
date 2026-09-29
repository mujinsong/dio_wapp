package handler

import (
	"context"

	"dio_wapp/server/internal/response"
	"dio_wapp/server/internal/service"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type AdminStaffHandler struct {
	service *service.AdminStaffService
}

func NewAdminStaffHandler(adminStaffService *service.AdminStaffService) *AdminStaffHandler {
	return &AdminStaffHandler{service: adminStaffService}
}

func (h *AdminStaffHandler) List(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	agencyID, err := parseUintQuery(c, "agency_id")
	if err != nil || agencyID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_agency", "agency_id is invalid")
		return
	}
	groupID, err := parseUintQuery(c, "group_id")
	if err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_group", "group_id is invalid")
		return
	}
	members, err := h.service.List(ctx, operatorID, agencyID, groupID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"members": members})
}

func (h *AdminStaffHandler) Save(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	var input service.AdminStaffSaveInput
	if err := c.BindAndValidate(&input); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	member, err := h.service.Save(ctx, operatorID, input)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"member": member})
}

func (h *AdminStaffHandler) Update(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	memberID, err := parseUintParam(c, "id")
	if err != nil || memberID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_staff_member", "staff member id is invalid")
		return
	}
	var input service.AdminStaffUpdateInput
	if err := c.BindAndValidate(&input); err != nil {
		response.Error(c, consts.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	member, err := h.service.Update(ctx, operatorID, memberID, input)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"member": member})
}

func (h *AdminStaffHandler) Disable(ctx context.Context, c *app.RequestContext) {
	operatorID, ok := currentOperator(c)
	if !ok {
		return
	}
	memberID, err := parseUintParam(c, "id")
	if err != nil || memberID == 0 {
		response.Error(c, consts.StatusBadRequest, "invalid_staff_member", "staff member id is invalid")
		return
	}
	if err := h.service.Disable(ctx, operatorID, memberID); err != nil {
		response.FromError(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"ok": true})
}
