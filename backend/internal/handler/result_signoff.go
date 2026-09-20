package handler

import (
	"net/http"
	"strconv"

	"github.com/blueship581/veterinary-lab-result-review/backend/internal/dto"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/middleware"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/service"
	"github.com/blueship581/veterinary-lab-result-review/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type ResultSignoffHandler struct{ service service.ResultSignoffService }

func NewResultSignoffHandler(s service.ResultSignoffService) *ResultSignoffHandler {
	return &ResultSignoffHandler{service: s}
}

func (h *ResultSignoffHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/signoff")
	resource.GET("", h.list)
	resource.GET("/:id", h.get)
	resource.POST("", middleware.RequireMinimumRole("operator"), h.create)
	resource.PUT("/:id", middleware.RequireMinimumRole("operator"), h.update)
	resource.POST("/:id/transition", middleware.RequireMinimumRole("operator"), h.transition)
	resource.POST("/:id/corrections", middleware.RequireMinimumRole("reviewer"), h.openCorrection)
	resource.POST("/:id/corrections/:correctionId/decision", middleware.RequireMinimumRole("reviewer"), h.decideCorrection)
	resource.DELETE("/:id", middleware.RequireRoles("admin"), h.remove)
}

func (h *ResultSignoffHandler) list(c *gin.Context) {
	query := bindPage(c)
	result, err := h.service.List(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *ResultSignoffHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ResultSignoffHandler) create(c *gin.Context) {
	var input dto.CreateResultSignoff
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, item)
}

func (h *ResultSignoffHandler) update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.UpdateResultSignoff
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Update(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ResultSignoffHandler) transition(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.TransitionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Transition(c.Request.Context(), id, input, actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *ResultSignoffHandler) remove(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id, actorFromContext(c), requestIDFromContext(c)); err != nil {
		handleError(c, err)
		return
	}
	util.NoContent(c)
}

func (h *ResultSignoffHandler) openCorrection(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.OpenCorrectionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	correction, err := h.service.OpenCorrection(c.Request.Context(), id, input,
		actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, correction)
}

func (h *ResultSignoffHandler) decideCorrection(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	correctionID, err := strconv.ParseUint(c.Param("correctionId"), 10, 64)
	if err != nil || correctionID == 0 {
		util.Fail(c, http.StatusBadRequest, "invalid_id", "correctionId must be a positive integer")
		return
	}
	var input dto.CorrectionDecisionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	correction, err := h.service.DecideCorrection(c.Request.Context(), id, uint(correctionID), input,
		actorFromContext(c), roleFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, correction)
}
