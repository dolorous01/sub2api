package middleware

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type imageCanvasSettingReader interface {
	IsImageCanvasEnabled(context.Context) bool
}

func ImageCanvasEnabled(settings imageCanvasSettingReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settings == nil || !settings.IsImageCanvasEnabled(c.Request.Context()) {
			response.ErrorWithDetails(c, http.StatusNotFound, "Image canvas is not enabled", "image_canvas_disabled", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
