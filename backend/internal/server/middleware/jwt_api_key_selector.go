package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const CanvasAPIKeyIDHeader = "X-Sub2API-Key-ID"

type apiKeyByIDReader interface {
	GetByID(ctx context.Context, id int64) (*service.APIKey, error)
}

// SelectOwnedAPIKey lets a JWT-authenticated browser select one of its own API
// keys by ID, then delegates all key, group, subscription, IP, and quota checks
// to the existing API key authentication middleware.
func SelectOwnedAPIKey(apiKeys apiKeyByIDReader, apiKeyAuth APIKeyAuthMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok {
			AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "User not authenticated")
			return
		}

		rawID := strings.TrimSpace(c.GetHeader(CanvasAPIKeyIDHeader))
		if rawID == "" {
			AbortWithError(c, http.StatusBadRequest, "API_KEY_ID_REQUIRED", CanvasAPIKeyIDHeader+" header is required")
			return
		}
		keyID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || keyID <= 0 {
			AbortWithError(c, http.StatusBadRequest, "INVALID_API_KEY_ID", CanvasAPIKeyIDHeader+" must be a positive integer")
			return
		}

		apiKey, err := apiKeys.GetByID(c.Request.Context(), keyID)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				AbortWithError(c, http.StatusNotFound, "API_KEY_NOT_FOUND", "API key not found")
				return
			}
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load API key")
			return
		}
		// Return the same response for a missing and a foreign key so IDs cannot be
		// used to discover another user's API keys.
		if apiKey == nil || apiKey.UserID != subject.UserID || strings.TrimSpace(apiKey.Key) == "" {
			AbortWithError(c, http.StatusNotFound, "API_KEY_NOT_FOUND", "API key not found")
			return
		}

		authorization := append([]string(nil), c.Request.Header.Values("Authorization")...)
		selector := append([]string(nil), c.Request.Header.Values(CanvasAPIKeyIDHeader)...)
		defer func() {
			restoreHeaderValues(c.Request.Header, "Authorization", authorization)
			restoreHeaderValues(c.Request.Header, CanvasAPIKeyIDHeader, selector)
		}()

		c.Request.Header.Set("Authorization", "Bearer "+apiKey.Key)
		c.Request.Header.Del(CanvasAPIKeyIDHeader)
		gin.HandlerFunc(apiKeyAuth)(c)
	}
}

func restoreHeaderValues(header http.Header, name string, values []string) {
	header.Del(name)
	for _, value := range values {
		header.Add(name, value)
	}
}
