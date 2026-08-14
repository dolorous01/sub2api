package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminImageJobRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerImageJobRoutes(router.Group("/api/v1/admin"), &handler.Handlers{
		Admin: &handler.AdminHandlers{ImageJob: &admin.ImageJobHandler{}},
	})

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		http.MethodGet + " /api/v1/admin/image-jobs/:job_id",
		http.MethodGet + " /api/v1/admin/image-jobs/:job_id/results/:index",
		http.MethodDelete + " /api/v1/admin/image-jobs/:job_id",
	} {
		require.True(t, registered[route], "missing route %s", route)
	}
}
