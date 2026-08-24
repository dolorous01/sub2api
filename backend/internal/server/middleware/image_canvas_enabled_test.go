package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageCanvasSettingReaderFunc func(context.Context) bool

func (f imageCanvasSettingReaderFunc) IsImageCanvasEnabled(ctx context.Context) bool {
	return f(ctx)
}

func TestImageCanvasEnabledReturnsStandardDisabledEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/canvas", ImageCanvasEnabled(imageCanvasSettingReaderFunc(func(context.Context) bool {
		return false
	})), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/canvas", nil))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.JSONEq(t, `{"code":404,"message":"Image canvas is not enabled","reason":"image_canvas_disabled"}`, recorder.Body.String())
}

func TestImageCanvasEnabledContinuesWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/canvas", ImageCanvasEnabled(imageCanvasSettingReaderFunc(func(context.Context) bool {
		return true
	})), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/canvas", nil))

	require.Equal(t, http.StatusNoContent, recorder.Code)
}
