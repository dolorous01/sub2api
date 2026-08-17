package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type adminImageJobService interface {
	GetAdmin(context.Context, string) (*service.ImageJob, error)
	CancelAdmin(context.Context, string) (*service.ImageJob, error)
	GetAdminResult(context.Context, string, int) (*service.ImageJobObject, error)
}

type ImageJobHandler struct {
	jobs adminImageJobService
}

func NewImageJobHandler(jobs *service.ImageJobService) *ImageJobHandler {
	return &ImageJobHandler{jobs: jobs}
}

func (h *ImageJobHandler) Get(c *gin.Context) {
	if !requireImageJobAdmin(c) {
		return
	}
	if h == nil || h.jobs == nil {
		writeAdminImageJobError(c, service.ErrImageJobUnavailable)
		return
	}
	job, err := h.jobs.GetAdmin(c.Request.Context(), strings.TrimSpace(c.Param("job_id")))
	if err != nil {
		writeAdminImageJobError(c, err)
		return
	}
	c.JSON(http.StatusOK, job.ToResponse())
}

func (h *ImageJobHandler) GetResult(c *gin.Context) {
	if !requireImageJobAdmin(c) {
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "Result index must be a non-negative integer"}})
		return
	}
	if h == nil || h.jobs == nil {
		writeAdminImageJobError(c, service.ErrImageJobUnavailable)
		return
	}
	object, err := h.jobs.GetAdminResult(c.Request.Context(), strings.TrimSpace(c.Param("job_id")), index)
	if err != nil {
		writeAdminImageJobError(c, err)
		return
	}
	contentType := strings.TrimSpace(object.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(object.Data)
	}
	c.Header("Content-Length", strconv.Itoa(len(object.Data)))
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Content-Disposition", "inline")
	c.Data(http.StatusOK, contentType, object.Data)
}

func (h *ImageJobHandler) Cancel(c *gin.Context) {
	if !requireImageJobAdmin(c) {
		return
	}
	if h == nil || h.jobs == nil {
		writeAdminImageJobError(c, service.ErrImageJobUnavailable)
		return
	}
	job, err := h.jobs.CancelAdmin(c.Request.Context(), strings.TrimSpace(c.Param("job_id")))
	if err != nil {
		writeAdminImageJobError(c, err)
		return
	}
	c.JSON(http.StatusOK, job.ToResponse())
}

func requireImageJobAdmin(c *gin.Context) bool {
	if _, ok := middleware2.GetAuthSubjectFromContext(c); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "Admin authentication required"}})
		return false
	}
	role, ok := middleware2.GetUserRoleFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "Admin authentication required"}})
		return false
	}
	if role != service.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "Admin access required"}})
		return false
	}
	return true
}

func writeAdminImageJobError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	typeName := "api_error"
	message := "Failed to process image job"
	switch {
	case errors.Is(err, service.ErrImageJobNotFound):
		status, typeName, message = http.StatusNotFound, "not_found_error", "Image job not found"
	case errors.Is(err, service.ErrImageJobExpired):
		status, typeName, message = http.StatusGone, "image_job_expired", "Image job results have expired"
	case errors.Is(err, service.ErrImageJobCancelConflict), errors.Is(err, service.ErrImageJobConflict):
		status, typeName, message = http.StatusConflict, "conflict_error", err.Error()
	case errors.Is(err, service.ErrImageJobUnavailable):
		status, typeName, message = http.StatusServiceUnavailable, "api_error", "Image job service is unavailable"
	}
	c.JSON(status, gin.H{"error": gin.H{"type": typeName, "message": message}})
}
