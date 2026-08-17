package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	imageSequenceEndpoint   = "/v1/images/sequences"
	imageGenerationEndpoint = "/v1/images/generations"
	imageEditsEndpoint      = "/v1/images/edits"
)

type imageJobService interface {
	Create(context.Context, service.CreateImageJobInput) (*service.ImageJob, bool, error)
	GetOwned(context.Context, string, int64) (*service.ImageJob, error)
	CancelOwned(context.Context, string, int64) (*service.ImageJob, error)
	GetOwnedResult(context.Context, string, int64, int) (*service.ImageJobObject, error)
}

func prefersRespondAsync(value string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), "respond-async") {
			return true
		}
	}
	return false
}

func (h *OpenAIGatewayHandler) createImageJob(
	c *gin.Context,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	parsed *service.OpenAIImagesRequest,
	scenes []string,
	mode string,
	channelMapping service.ChannelMappingResult,
) {
	if h == nil || h.imageJobs == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image job service is unavailable")
		return
	}
	job, _, err := h.imageJobs.Create(c.Request.Context(), service.CreateImageJobInput{
		APIKey:             apiKey,
		Subscription:       subscription,
		Parsed:             parsed,
		Scenes:             scenes,
		Mode:               mode,
		IdempotencyKey:     c.GetHeader("Idempotency-Key"),
		MappedModel:        channelMapping.MappedModel,
		ChannelMapping:     channelMapping,
		ChannelUsageFields: channelMapping.ToUsageFields(parsed.Model, ""),
	})
	if err != nil {
		h.writeImageJobError(c, err)
		return
	}
	if job == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image job service returned no job")
		return
	}
	c.Header("Preference-Applied", "respond-async")
	c.Header("Location", "/v1/images/jobs/"+job.PublicID)
	c.JSON(http.StatusAccepted, job.ToResponse())
}

func (h *OpenAIGatewayHandler) ImageSequence(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.image_sequence", zap.Int64("api_key_id", apiKey.ID))
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	parsed, scenes, err := parseImageSequenceRequest(body, c.GetHeader("Content-Type"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	maxInputs := 4
	if h.cfg != nil && h.cfg.Gateway.ImageJobs.MaxInputImages > 0 {
		maxInputs = h.cfg.Gateway.ImageJobs.MaxInputImages
	}
	referenceCount := len(parsed.Uploads) + len(parsed.InputImageURLs)
	if referenceCount > 0 && referenceCount+1 > maxInputs {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("sequence references exceed the maximum of %d inputs", maxInputs))
		return
	}
	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, parsed.Model, imageSequenceModerationBody(parsed, scenes)); decision != nil && decision.Blocked {
		h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, parsed.Model)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	h.createImageJob(c, apiKey, subscription, parsed, scenes, "sequence", channelMapping)
}

func parseImageSequenceRequest(body []byte, contentType string) (*service.OpenAIImagesRequest, []string, error) {
	mediaType, _, _ := mime.ParseMediaType(strings.TrimSpace(contentType))
	endpoint := imageGenerationEndpoint
	var scenes []string
	if strings.EqualFold(mediaType, "multipart/form-data") {
		parsedScenes, err := parseMultipartImageSequenceScenes(body, contentType)
		if err != nil {
			return nil, nil, err
		}
		scenes = parsedScenes
	} else {
		var envelope struct {
			Scenes []string        `json:"scenes"`
			Images json.RawMessage `json:"images"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, nil, fmt.Errorf("failed to parse request body")
		}
		scenes = envelope.Scenes
		if len(bytes.TrimSpace(envelope.Images)) > 0 && !bytes.Equal(bytes.TrimSpace(envelope.Images), []byte("null")) {
			endpoint = imageEditsEndpoint
		}
	}
	scenes, err := validateImageSequenceScenes(scenes)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := service.ParseOpenAIImagesRequestBody(endpoint, contentType, body)
	if err != nil {
		return nil, nil, err
	}
	if parsed.MaskUpload != nil || strings.TrimSpace(parsed.MaskImageURL) != "" {
		return nil, nil, fmt.Errorf("sequence requests do not support masks")
	}
	if len(parsed.Uploads)+len(parsed.InputImageURLs) > 0 {
		parsed.Endpoint = imageEditsEndpoint
		parsed.RequiredCapability = service.OpenAIImagesCapabilityNative
	}
	parsed.N = 1
	parsed.Stream = false
	return parsed, scenes, nil
}

func parseMultipartImageSequenceScenes(body []byte, contentType string) ([]string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || strings.TrimSpace(params["boundary"]) == "" {
		return nil, fmt.Errorf("invalid multipart content-type")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var scenes []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read multipart body: %w", err)
		}
		if part.FileName() != "" {
			_ = part.Close()
			continue
		}
		name := strings.TrimSpace(part.FormName())
		data, readErr := io.ReadAll(io.LimitReader(part, 64<<10))
		_ = part.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read multipart field %s: %w", name, readErr)
		}
		value := strings.TrimSpace(string(data))
		switch name {
		case "scene", "scenes[]":
			scenes = append(scenes, value)
		case "scenes":
			if strings.HasPrefix(value, "[") {
				var values []string
				if err := json.Unmarshal(data, &values); err != nil {
					return nil, fmt.Errorf("scenes must be a JSON string array")
				}
				scenes = append(scenes, values...)
			} else {
				scenes = append(scenes, value)
			}
		}
	}
	return scenes, nil
}

func validateImageSequenceScenes(scenes []string) ([]string, error) {
	if len(scenes) < 2 || len(scenes) > 4 {
		return nil, fmt.Errorf("scenes must contain between 2 and 4 entries")
	}
	normalized := make([]string, len(scenes))
	for index, scene := range scenes {
		normalized[index] = strings.TrimSpace(scene)
		if normalized[index] == "" {
			return nil, fmt.Errorf("scenes[%d] must not be empty", index)
		}
	}
	return normalized, nil
}

func imageSequenceModerationBody(parsed *service.OpenAIImagesRequest, scenes []string) []byte {
	payload := map[string]any{"scenes": scenes}
	if parsed != nil {
		_ = json.Unmarshal(parsed.ModerationBody(), &payload)
		payload["scenes"] = scenes
	}
	body, _ := json.Marshal(payload)
	return body
}

func (h *OpenAIGatewayHandler) GetImageJob(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if h == nil || h.imageJobs == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image job service is unavailable")
		return
	}
	job, err := h.imageJobs.GetOwned(c.Request.Context(), strings.TrimSpace(c.Param("job_id")), apiKey.ID)
	if err != nil {
		h.writeImageJobError(c, err)
		return
	}
	c.JSON(http.StatusOK, job.ToResponse())
}

func (h *OpenAIGatewayHandler) GetImageJobResult(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Result index must be a non-negative integer")
		return
	}
	if h == nil || h.imageJobs == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image job service is unavailable")
		return
	}
	object, err := h.imageJobs.GetOwnedResult(c.Request.Context(), strings.TrimSpace(c.Param("job_id")), apiKey.ID, index)
	if err != nil {
		h.writeImageJobError(c, err)
		return
	}
	writeImageJobObject(c, object)
}

func (h *OpenAIGatewayHandler) CancelImageJob(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if h == nil || h.imageJobs == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image job service is unavailable")
		return
	}
	job, err := h.imageJobs.CancelOwned(c.Request.Context(), strings.TrimSpace(c.Param("job_id")), apiKey.ID)
	if err != nil {
		h.writeImageJobError(c, err)
		return
	}
	c.JSON(http.StatusOK, job.ToResponse())
}

func writeImageJobObject(c *gin.Context, object *service.ImageJobObject) {
	if object == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Image result not found"}})
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

func (h *OpenAIGatewayHandler) writeImageJobError(c *gin.Context, err error) {
	status, errorType, message := imageJobErrorDetails(err)
	h.errorResponse(c, status, errorType, message)
}

func imageJobErrorDetails(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrImageJobNotFound):
		return http.StatusNotFound, "not_found_error", "Image job not found"
	case errors.Is(err, service.ErrImageJobExpired):
		return http.StatusGone, "image_job_expired", "Image job results have expired"
	case errors.Is(err, service.ErrImageJobCancelConflict), errors.Is(err, service.ErrImageJobConflict), errors.Is(err, service.ErrImageJobIdempotencyConflict):
		return http.StatusConflict, "conflict_error", err.Error()
	case errors.Is(err, service.ErrImageJobPermissionDenied):
		return http.StatusForbidden, "permission_error", err.Error()
	case errors.Is(err, service.ErrImageJobInvalidRequest):
		return http.StatusBadRequest, "invalid_request_error", err.Error()
	case errors.Is(err, service.ErrImageJobReservationInsufficient):
		return http.StatusForbidden, "billing_error", "Insufficient quota for image job reservation"
	case errors.Is(err, service.ErrImageJobDisabled), errors.Is(err, service.ErrImageJobUnavailable), errors.Is(err, service.ErrImageJobReservationUnavailable):
		return http.StatusServiceUnavailable, "api_error", "Image job service is unavailable"
	default:
		return http.StatusInternalServerError, "api_error", "Failed to process image job"
	}
}
