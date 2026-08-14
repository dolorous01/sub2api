package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageJobHandlerRejectsNonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ImageJobHandler{jobs: &fakeAdminImageJobService{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/image-jobs/imgjob_test", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}}
	setImageJobAdminAuth(c, service.RoleUser)

	h.Get(c)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestImageJobHandlerRejectsMissingAuthSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ImageJobHandler{jobs: &fakeAdminImageJobService{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/image-jobs/imgjob_test", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}}
	c.Set(string(middleware2.ContextKeyUserRole), service.RoleAdmin)

	h.Get(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestImageJobHandlerGetsAndDownloadsAdminJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeAdminImageJobService{
		job:    &service.ImageJob{PublicID: "imgjob_test", Status: service.ImageJobStatusCompleted},
		result: &service.ImageJobObject{Data: []byte("png"), ContentType: "image/png", Size: 3},
	}
	h := &ImageJobHandler{jobs: jobs}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/image-jobs/imgjob_test", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}}
	setImageJobAdminAuth(c, service.RoleAdmin)
	h.Get(c)
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/image-jobs/imgjob_test/results/0", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}, {Key: "index", Value: "0"}}
	setImageJobAdminAuth(c, service.RoleAdmin)
	h.GetResult(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, "png", w.Body.String())
}

func setImageJobAdminAuth(c *gin.Context, role string) {
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1, Concurrency: 1})
	c.Set(string(middleware2.ContextKeyUserRole), role)
}

type fakeAdminImageJobService struct {
	job    *service.ImageJob
	result *service.ImageJobObject
}

func (f *fakeAdminImageJobService) GetAdmin(context.Context, string) (*service.ImageJob, error) {
	return f.job, nil
}

func (f *fakeAdminImageJobService) CancelAdmin(context.Context, string) (*service.ImageJob, error) {
	return f.job, nil
}

func (f *fakeAdminImageJobService) GetAdminResult(context.Context, string, int) (*service.ImageJobObject, error) {
	return f.result, nil
}
