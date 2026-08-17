package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Images handles OpenAI Images API requests.
// POST /v1/images/generations
// POST /v1/images/edits
func (h *OpenAIGatewayHandler) Images(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

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
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.images",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
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
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if isMultipartImagesContentType(c.GetHeader("Content-Type")) {
		setOpsRequestContext(c, "", false)
	} else {
		setOpsRequestContext(c, "", false)
	}

	parsed, err := h.gatewayService.ParseOpenAIImagesRequest(c, body)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	requestModel := parsed.Model

	reqLog = reqLog.With(
		zap.String("model", requestModel),
		zap.Bool("stream", parsed.Stream),
		zap.Bool("multipart", parsed.Multipart),
		zap.String("capability", string(parsed.RequiredCapability)),
	)

	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, requestModel, parsed.ModerationBody()); decision != nil && decision.Blocked {
		h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	if parsed.Multipart {
		setOpsRequestContext(c, requestModel, parsed.Stream)
	} else {
		setOpsRequestContext(c, requestModel, parsed.Stream)
	}
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(parsed.Stream, false)))

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, requestModel)

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai.images.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}
	if prefersRespondAsync(c.GetHeader("Prefer")) {
		h.createImageJob(c, apiKey, subscription, parsed, nil, "batch", channelMapping)
		return
	}

	routingStart := time.Now()
	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
	requestCtx := service.WithOpenAIImageGenerationIntent(c.Request.Context())
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if parsed.Multipart {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(parsed.StickySessionSeed()))
	}
	eventPrefix := "image_generation"
	if parsed.IsEdits() {
		eventPrefix = "image_edit"
	}
	sink := service.NewHTTPImageResultSink(c.Writer, service.ImageSinkOptions{
		Stream: parsed.Stream, ResponseFormat: parsed.ResponseFormat,
		OutputFormat: parsed.OutputFormat, EventPrefix: eventPrefix,
	})
	if h.imageExecutor == nil {
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "Image executor is unavailable", false)
		return
	}
	executionStart := time.Now()
	execution, executeErr := h.imageExecutor.Execute(requestCtx, service.ImageExecutionInput{
		APIKey: apiKey, User: apiKey.User, Subscription: subscription,
		Body: body, Parsed: parsed, ChannelMapping: channelMapping,
		SessionHash: sessionHash, InboundEndpoint: GetInboundEndpoint(c),
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		RequestPayloadHash: requestPayloadHash, RequestHeaders: c.Request.Header.Clone(),
		Observer: h.imageExecutionObserver(c, reqLog),
	}, sink)
	executionElapsedMs := time.Since(executionStart).Milliseconds()
	streamStarted = streamStarted || c.Writer.Written()

	var result *service.OpenAIForwardResult
	var account *service.Account
	if execution != nil {
		result = execution.Forward
		account = execution.Account
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, execution.RoutingLatencyMs)
	}
	if execution == nil {
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
	}
	if result != nil && result.UpstreamLatencyMs > 0 {
		service.SetOpsLatencyMs(c, service.OpsUpstreamLatencyMsKey, result.UpstreamLatencyMs)
	}
	routingLatencyMs, _ := getContextInt64(c, service.OpsRoutingLatencyMsKey)
	upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
	responseLatencyMs := executionElapsedMs - routingLatencyMs - upstreamLatencyMs
	if responseLatencyMs < 0 {
		responseLatencyMs = 0
	}
	service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
	if result != nil && result.FirstTokenMs != nil {
		service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
	}
	if account != nil {
		setOpsSelectedAccount(c, account.ID, account.Platform)
	}
	if executeErr != nil {
		imageCount := 0
		if result != nil {
			imageCount = result.ImageCount
		}
		if imageCount > 0 && account != nil {
			reqLog.Warn("openai.images.forward_partial_error_with_image_result",
				zap.Int64("account_id", account.ID), zap.Int("image_count", imageCount), zap.Error(executeErr))
		} else {
			reqLog.Warn("openai.images.forward_failed", zap.Error(executeErr))
		}
		h.handleImageExecutionError(c, apiKey, requestModel, executeErr, streamStarted)
		if imageCount == 0 || account == nil {
			return
		}
	}
	if account == nil || result == nil {
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
		return
	}

	if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
		h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
	}
	upstreamModel := result.UpstreamModel
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result: result, APIKey: apiKey, User: apiKey.User, Account: account,
			Subscription: subscription, InboundEndpoint: inboundEndpoint,
			UpstreamEndpoint: upstreamEndpoint, UserAgent: c.GetHeader("User-Agent"),
			IPAddress: ip.GetClientIP(c), RequestPayloadHash: requestPayloadHash,
			APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform,
			ChannelUsageFields: channelMapping.ToUsageFields(requestModel, upstreamModel),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.images"),
				zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID), zap.String("model", requestModel),
				zap.Int64("account_id", account.ID),
			).Error("openai.images.record_usage_failed", zap.Error(err))
		}
	})
	reqLog.Debug("openai.images.request_completed", zap.Int64("account_id", account.ID))
}

func (h *OpenAIGatewayHandler) imageExecutionObserver(c *gin.Context, reqLog *zap.Logger) service.ImageExecutionObserver {
	return func(attempt service.ImageExecutionAttempt) {
		account := attempt.Account
		if account != nil {
			setOpsSelectedAccount(c, account.ID, account.Platform)
		}
		if attempt.Err == nil {
			return
		}

		status := 0
		message := strings.TrimSpace(attempt.Err.Error())
		requestID := ""
		kind := "http_error"
		if attempt.Retrying {
			kind = "failover"
		}
		var failoverErr *service.UpstreamFailoverError
		if errors.As(attempt.Err, &failoverErr) {
			status = failoverErr.StatusCode
			message = service.ExtractUpstreamErrorMessage(failoverErr.ResponseBody)
			requestID = strings.TrimSpace(failoverErr.ResponseHeaders.Get("x-request-id"))
		}
		var upstreamErr *service.OpenAIImagesUpstreamError
		if errors.As(attempt.Err, &upstreamErr) {
			status = upstreamErr.StatusCode
			message = upstreamErr.Message
			requestID = upstreamErr.UpstreamRequestID
		}
		var executionErr *service.ImageExecutionError
		if status == 0 && errors.As(attempt.Err, &executionErr) {
			status = executionErr.Status
			message = executionErr.Message
		}
		if strings.TrimSpace(message) == "" {
			message = "Upstream request failed"
		}
		service.SetOpsUpstreamError(c, status, message, "")
		event := service.OpsUpstreamErrorEvent{
			UpstreamStatusCode: status, UpstreamRequestID: requestID,
			Kind: kind, Message: message,
		}
		if account != nil {
			event.Platform = account.Platform
			event.AccountID = account.ID
			event.AccountName = account.Name
		}
		service.AppendOpsUpstreamError(c, event)
		if reqLog != nil {
			reqLog.Warn("openai.images.execution_attempt_failed",
				zap.Int64("account_id", event.AccountID), zap.Int("upstream_status", status),
				zap.Bool("retrying", attempt.Retrying), zap.Error(attempt.Err))
		}
	}
}

func (h *OpenAIGatewayHandler) handleImageExecutionError(
	c *gin.Context,
	apiKey *service.APIKey,
	requestModel string,
	err error,
	streamStarted bool,
) {
	var failoverErr *service.UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		h.handleFailoverExhausted(c, failoverErr, streamStarted)
		return
	}

	var executionErr *service.ImageExecutionError
	if errors.As(err, &executionErr) && executionErr.Code == "no_available_account" {
		cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, requestModel, requestModel, service.PlatformOpenAI)
		if !cls.ModelNotFound {
			markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
		}
		message := cls.Message
		if !cls.ModelNotFound {
			message = "No available compatible accounts"
		}
		h.handleStreamingAwareError(c, cls.Status, cls.ErrType, message, streamStarted)
		return
	}

	var upstreamErr *service.OpenAIImagesUpstreamError
	if errors.As(err, &upstreamErr) {
		if service.IsOpenAIImagesRetryableUpstreamError(upstreamErr) {
			status, errType, message := h.mapUpstreamError(upstreamErr.StatusCode)
			h.handleStreamingAwareError(c, status, errType, message, streamStarted)
			return
		}
		h.handleStreamingAwareError(c, upstreamErr.StatusCode, upstreamErr.ErrorType, upstreamErr.Message, streamStarted)
		return
	}
	if executionErr != nil {
		if executionErr.Retryable && executionErr.Status >= http.StatusInternalServerError {
			status, errType, message := h.mapUpstreamError(executionErr.Status)
			h.handleStreamingAwareError(c, status, errType, message, streamStarted)
			return
		}
		status := executionErr.Status
		if status <= 0 {
			status = http.StatusBadGateway
		}
		errType := strings.TrimSpace(executionErr.Type)
		if errType == "" {
			errType = "upstream_error"
		}
		message := strings.TrimSpace(executionErr.Message)
		if message == "" {
			message = "Upstream request failed"
		}
		h.handleStreamingAwareError(c, status, errType, message, streamStarted)
		return
	}
	h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
}

func isMultipartImagesContentType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/form-data")
}
