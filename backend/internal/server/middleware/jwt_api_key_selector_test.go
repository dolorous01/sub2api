//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type selectorAPIKeyReader struct {
	key *service.APIKey
	err error
}

func (r selectorAPIKeyReader) GetByID(_ context.Context, _ int64) (*service.APIKey, error) {
	return r.key, r.err
}

func TestSelectOwnedAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		header     string
		reader     selectorAPIKeyReader
		wantStatus int
		wantCode   string
	}{
		{name: "missing selector", wantStatus: http.StatusBadRequest, wantCode: "API_KEY_ID_REQUIRED"},
		{name: "invalid selector", header: "nope", wantStatus: http.StatusBadRequest, wantCode: "INVALID_API_KEY_ID"},
		{name: "missing key", header: "7", reader: selectorAPIKeyReader{err: service.ErrAPIKeyNotFound}, wantStatus: http.StatusNotFound, wantCode: "API_KEY_NOT_FOUND"},
		{name: "foreign key", header: "7", reader: selectorAPIKeyReader{key: &service.APIKey{ID: 7, UserID: 99, Key: "foreign-key"}}, wantStatus: http.StatusNotFound, wantCode: "API_KEY_NOT_FOUND"},
		{name: "repository error", header: "7", reader: selectorAPIKeyReader{err: errors.New("db unavailable")}, wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := selectorTestRouter(tt.reader, nil)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/canvas", strings.NewReader("request-body"))
			request.Header.Set("Authorization", "Bearer jwt-token")
			if tt.header != "" {
				request.Header.Set(CanvasAPIKeyIDHeader, tt.header)
			}

			router.ServeHTTP(recorder, request)

			require.Equal(t, tt.wantStatus, recorder.Code)
			require.Contains(t, recorder.Body.String(), tt.wantCode)
			require.Equal(t, "Bearer jwt-token", request.Header.Get("Authorization"))
			require.Equal(t, tt.header, request.Header.Get(CanvasAPIKeyIDHeader))
		})
	}
}

func TestSelectOwnedAPIKeyDelegatesWithoutChangingBody(t *testing.T) {
	reader := selectorAPIKeyReader{key: &service.APIKey{ID: 7, UserID: 42, Key: "selected-key"}}
	var delegatedAuthorization string
	var delegatedSelector string
	apiKeyAuth := APIKeyAuthMiddleware(func(c *gin.Context) {
		delegatedAuthorization = c.GetHeader("Authorization")
		delegatedSelector = c.GetHeader(CanvasAPIKeyIDHeader)
		c.Next()
	})
	router := selectorTestRouter(reader, apiKeyAuth)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/canvas", strings.NewReader("request-body"))
	request.Header.Set("Authorization", "Bearer jwt-token")
	request.Header.Set(CanvasAPIKeyIDHeader, "7")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "request-body", recorder.Body.String())
	require.Equal(t, "Bearer selected-key", delegatedAuthorization)
	require.Empty(t, delegatedSelector)
	require.Equal(t, "Bearer jwt-token", request.Header.Get("Authorization"))
	require.Equal(t, "7", request.Header.Get(CanvasAPIKeyIDHeader))
}

func selectorTestRouter(reader apiKeyByIDReader, auth APIKeyAuthMiddleware) *gin.Engine {
	if auth == nil {
		auth = APIKeyAuthMiddleware(func(c *gin.Context) { c.Next() })
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
		c.Next()
	})
	router.Use(SelectOwnedAPIKey(reader, auth))
	router.POST("/canvas", func(c *gin.Context) {
		body := make([]byte, c.Request.ContentLength)
		_, _ = c.Request.Body.Read(body)
		c.String(http.StatusOK, string(body))
	})
	return router
}
