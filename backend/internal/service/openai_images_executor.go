package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/google/uuid"
)

// ImageExecutionInput is the request data needed by a background or HTTP
// image execution. Headers are copied at the ingress boundary and are never
// read from a Gin context inside the executor.
type ImageExecutionInput struct {
	APIKey             *APIKey
	User               *User
	Subscription       *UserSubscription
	Body               []byte
	Parsed             *OpenAIImagesRequest
	ChannelMapping     ChannelMappingResult
	SessionHash        string
	InboundEndpoint    string
	UserAgent          string
	IPAddress          string
	RequestPayloadHash string
	RequestHeaders     http.Header
	InputObjectIDs     []string
	Observer           ImageExecutionObserver
}

// ImageExecutionAttempt reports one selected-account outcome without tying the
// executor to an HTTP framework. HTTP callers use it for request-scoped ops
// diagnostics while background workers can leave it nil.
type ImageExecutionAttempt struct {
	Account   *Account
	Result    *OpenAIForwardResult
	Err       error
	Retrying  bool
	Switching bool
}

type ImageExecutionObserver func(ImageExecutionAttempt)

// ImageExecutor executes one upstream image request and emits artifacts to a
// sink. A single Execute call corresponds to one upstream request, including
// n>1 batches.
type ImageExecutor interface {
	Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error)
}

// ImageExecutionError is the sanitized error contract shared by HTTP and job
// callers. Cause is retained for errors.As checks by the failover policy.
type ImageExecutionError struct {
	Status        int
	Type          string
	Code          string
	Message       string
	Retryable     bool
	Indeterminate bool
	Cause         error
}

func (e *ImageExecutionError) Error() string {
	if e == nil {
		return "image execution failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "image execution failed"
}

func (e *ImageExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type OpenAIImageExecutor struct {
	gateway            *OpenAIGatewayService
	concurrencyService *ConcurrencyService
	cfg                *config.Config
	maxAccountSwitches int
	imageSemaphore     *imageExecutionSemaphore
	selectAccount      imageExecutorAccountSelector
	forward            imageExecutorAccountForwarder
}

const (
	imageExecutorUserWaitTimeout       = 30 * time.Second
	imageExecutorWaitBackoff           = 100 * time.Millisecond
	imageExecutorMaxBackoff            = 2 * time.Second
	imageExecutorSameAccountRetryDelay = 500 * time.Millisecond
)

type imageExecutorAccountSelector func(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability OpenAIImagesCapability,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error)

type imageExecutorAccountForwarder func(
	ctx context.Context,
	input ImageExecutionInput,
	account *Account,
	sink ImageResultSink,
) (*OpenAIForwardResult, error)

// NewOpenAIImageExecutor constructs the Gin-independent image executor.
func NewOpenAIImageExecutor(gateway *OpenAIGatewayService, concurrencyService *ConcurrencyService, cfg *config.Config) *OpenAIImageExecutor {
	maxSwitches := 3
	if cfg != nil && cfg.Gateway.MaxAccountSwitches > 0 {
		maxSwitches = cfg.Gateway.MaxAccountSwitches
	}
	return &OpenAIImageExecutor{
		gateway:            gateway,
		concurrencyService: concurrencyService,
		cfg:                cfg,
		maxAccountSwitches: maxSwitches,
		imageSemaphore:     &imageExecutionSemaphore{},
	}
}

func (e *OpenAIImageExecutor) Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error) {
	if e == nil || e.gateway == nil {
		return nil, newImageExecutionError(http.StatusServiceUnavailable, "api_error", "executor_unavailable", "image executor is unavailable", false, false, nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if input.Parsed == nil {
		return nil, newImageExecutionError(http.StatusBadRequest, "invalid_request_error", "invalid_image_request", "parsed image request is required", false, false, nil)
	}
	if sink == nil {
		return nil, newImageExecutionError(http.StatusInternalServerError, "api_error", "sink_unavailable", "image result sink is required", false, false, nil)
	}
	if input.APIKey == nil || input.APIKey.GroupID == nil {
		return nil, newImageExecutionError(http.StatusUnauthorized, "authentication_error", "invalid_api_key", "API key is required", false, false, nil)
	}

	globalRelease, err := e.acquireImageSemaphore(ctx)
	if err != nil {
		return nil, err
	}
	if globalRelease != nil {
		defer globalRelease()
	}
	userRelease, err := e.acquireUserSlot(ctx, input.User, input.APIKey)
	if err != nil {
		return nil, err
	}
	if userRelease != nil {
		defer userRelease()
	}

	tracker := &trackingImageResultSink{sink: sink}
	excluded := make(map[int64]struct{})
	sameAccountRetries := make(map[int64]int)
	maxSwitches := e.maxAccountSwitches
	if maxSwitches < 0 {
		maxSwitches = 0
	}
	switches := 0
	var lastExecution *ImageExecutionResult
	var lastForwardErr error
	var routingLatencyMs int64
	bindSession := strings.TrimSpace(input.SessionHash) != ""
	generatedSession := false
	for {
		routingStart := time.Now()
		selection, _, selectErr := e.selectImageAccount(
			ctx,
			input.APIKey.GroupID,
			strings.TrimSpace(input.SessionHash),
			strings.TrimSpace(firstNonEmptyString(input.ChannelMapping.MappedModel, input.Parsed.Model)),
			excluded,
			input.Parsed.RequiredCapability,
		)
		if selectErr != nil || selection == nil || selection.Account == nil {
			if lastForwardErr != nil {
				return lastExecution, sanitizeImageExecutionError(lastForwardErr)
			}
			message := "No available compatible accounts"
			if selectErr != nil {
				message = sanitizeImageExecutionMessage(selectErr.Error())
			}
			return nil, newImageExecutionError(http.StatusServiceUnavailable, "api_error", "no_available_account", message, true, false, selectErr)
		}
		account := selection.Account
		if strings.TrimSpace(input.SessionHash) == "" && account.IsPoolMode() {
			input.SessionHash = "openai-image-pool-retry-" + uuid.NewString()
			generatedSession = true
		}
		accountRelease, acquireErr := e.acquireSelectedAccountSlot(ctx, selection)
		if acquireErr != nil {
			return nil, acquireErr
		}
		// The scheduler binds an explicit session when it acquires a slot during
		// selection. Synthetic pool-mode hashes are generated after selection,
		// so bind those after the executor has acquired the slot as well.
		if generatedSession || (!selection.Acquired && bindSession) {
			// A sticky-session write is an optimization. Keep the request
			// running when Redis is temporarily unavailable.
			_ = e.gateway.BindStickySession(ctx, input.APIKey.GroupID, input.SessionHash, account.ID)
		}
		routingLatencyMs += time.Since(routingStart).Milliseconds()
		forwardStart := time.Now()
		attemptResult, forwardErr := e.forwardImageAccount(ctx, input, account, tracker)
		forwardElapsedMs := time.Since(forwardStart).Milliseconds()
		if accountRelease != nil {
			accountRelease()
		}
		if attemptResult == nil {
			attemptResult = &OpenAIForwardResult{
				Model: input.Parsed.Model, UpstreamModel: input.Parsed.Model,
				UpstreamLatencyMs: forwardElapsedMs,
			}
		}
		if finalCount := tracker.FinalCount(); attemptResult.ImageCount < finalCount {
			attemptResult.ImageCount = finalCount
		}
		execution := &ImageExecutionResult{
			Forward: attemptResult, Account: account, ChannelMapping: input.ChannelMapping,
			RoutingLatencyMs: routingLatencyMs,
		}
		lastExecution = execution
		if forwardErr == nil {
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult})
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, attemptResult.FirstTokenMs)
			return execution, nil
		}
		lastForwardErr = forwardErr
		var sinkErr *imageResultSinkError
		if errors.As(forwardErr, &sinkErr) {
			// Retrying another account cannot repair a client, storage, or result
			// serialization failure and could create duplicate upstream output.
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, attemptResult.FirstTokenMs)
			return execution, sanitizeImageExecutionError(forwardErr)
		}
		if tracker.FinalCount() > 0 {
			// Once a final artifact has escaped, switching accounts would create
			// duplicate/mixed output. Preserve the partial result and stop.
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, attemptResult.FirstTokenMs)
			return execution, sanitizeImageExecutionError(forwardErr)
		}

		var failoverErr *UpstreamFailoverError
		if errors.As(forwardErr, &failoverErr) {
			if failoverErr.RetryableOnSameAccount {
				limit := account.GetPoolModeRetryCount()
				if sameAccountRetries[account.ID] < limit {
					sameAccountRetries[account.ID]++
					notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr, Retrying: true})
					select {
					case <-ctx.Done():
						return execution, imageExecutionContextError(ctx, forwardErr)
					case <-time.After(imageExecutorSameAccountRetryDelay):
					}
					continue
				}
			}
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
			e.gateway.RecordOpenAIAccountSwitch()
			excluded[account.ID] = struct{}{}
			if switches >= maxSwitches {
				notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
				return execution, sanitizeImageExecutionError(forwardErr)
			}
			switches++
			if e.gateway.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switches) {
				notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
				return execution, sanitizeImageExecutionError(forwardErr)
			}
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr, Retrying: true, Switching: true})
			continue
		}
		var upstreamErr *OpenAIImagesUpstreamError
		if errors.As(forwardErr, &upstreamErr) && !IsOpenAIImagesRetryableUpstreamError(upstreamErr) {
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
			return execution, sanitizeImageExecutionError(forwardErr)
		}
		// Network failures before any output are safe to fail over once. The
		// wrapped error remains retryable for callers that want to retry later.
		if isImageExecutionRetryableTransportError(forwardErr) && switches < maxSwitches {
			e.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
			e.gateway.RecordOpenAIAccountSwitch()
			excluded[account.ID] = struct{}{}
			switches++
			notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr, Retrying: true, Switching: true})
			continue
		}
		e.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		notifyImageExecutionObserver(input.Observer, ImageExecutionAttempt{Account: account, Result: attemptResult, Err: forwardErr})
		return execution, sanitizeImageExecutionError(forwardErr)
	}
}

func notifyImageExecutionObserver(observer ImageExecutionObserver, attempt ImageExecutionAttempt) {
	if observer != nil {
		observer(attempt)
	}
}

func (e *OpenAIImageExecutor) selectImageAccount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability OpenAIImagesCapability,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if e.selectAccount != nil {
		return e.selectAccount(ctx, groupID, sessionHash, requestedModel, excludedIDs, requiredCapability)
	}
	return e.gateway.SelectAccountWithSchedulerForImages(ctx, groupID, sessionHash, requestedModel, excludedIDs, requiredCapability)
}

func (e *OpenAIImageExecutor) forwardImageAccount(ctx context.Context, input ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
	if e.forward != nil {
		return e.forward(ctx, input, account, sink)
	}
	return e.forwardAccount(ctx, input, account, sink)
}

func (e *OpenAIImageExecutor) forwardAccount(ctx context.Context, input ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
	body := input.Body
	if len(body) == 0 && input.Parsed != nil {
		body = input.Parsed.Body
	}
	if account == nil || input.Parsed == nil {
		return nil, fmt.Errorf("image account and request are required")
	}
	if account.Type == AccountTypeOAuth {
		return e.forwardOAuthAccount(ctx, input, account, sink)
	}
	return e.forwardAPIKeyAccount(ctx, input, account, body, sink)
}

func (e *OpenAIImageExecutor) forwardAPIKeyAccount(ctx context.Context, input ImageExecutionInput, account *Account, body []byte, sink ImageResultSink) (*OpenAIForwardResult, error) {
	parsed := input.Parsed
	requestModel := strings.TrimSpace(firstNonEmptyString(input.ChannelMapping.MappedModel, parsed.Model))
	upstreamModel := account.GetMappedModel(requestModel)
	forwardBody, contentType, err := rewriteOpenAIImagesModel(body, parsed.ContentType, upstreamModel)
	if err != nil {
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := imageExecutorUpstreamContext(ctx, parsed, account, sink)
	defer releaseUpstreamCtx()
	token, _, err := e.gateway.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	req, err := e.buildImageAPIKeyRequest(upstreamCtx, input.RequestHeaders, account, forwardBody, contentType, token, parsed.Endpoint)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := e.gateway.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	upstreamLatencyMs := time.Since(upstreamStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %s", sanitizeImageExecutionMessage(err.Error()))
	}
	if resp == nil {
		return nil, fmt.Errorf("upstream returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, openAIUpstreamErrorBodyReadLimitForConfig(e.cfg)))
		if e.gateway.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)), body) {
			e.gateway.handleFailoverSideEffects(upstreamCtx, resp, account, body, upstreamModel)
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone(), RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)}
		}
		if e.gateway.handleOpenAIAccountUpstreamError(upstreamCtx, account, resp.StatusCode, resp.Header, body, upstreamModel) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone(), RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)}
		}
		if !account.ShouldHandleErrorCode(resp.StatusCode) {
			return nil, newImageExecutionError(http.StatusInternalServerError, "upstream_error", "upstream_gateway_error", "Upstream gateway error", false, false, nil)
		}
		return nil, openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, body)
	}
	applyImageResponseHeaders(sink, resp.Header, e.gateway.responseHeaderFilter)
	result, err := e.gateway.consumeOpenAIImagesResponse(upstreamCtx, resp, parsed, sink)
	if result != nil {
		result.Model = requestModel
		result.UpstreamModel = upstreamModel
		result.UpstreamLatencyMs = upstreamLatencyMs
	}
	return result, err
}

func (e *OpenAIImageExecutor) forwardOAuthAccount(ctx context.Context, input ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
	parsed := input.Parsed
	requestModel := strings.TrimSpace(firstNonEmptyString(input.ChannelMapping.MappedModel, parsed.Model))
	if requestModel == "" {
		requestModel = openAIImagesResponsesMainModel
	}
	body, err := buildOpenAIImagesResponsesRequest(parsed, requestModel)
	if err != nil {
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := imageExecutorUpstreamContext(ctx, parsed, account, sink)
	defer releaseUpstreamCtx()
	token, _, err := e.gateway.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	req, err := e.buildOAuthImageRequest(upstreamCtx, input.RequestHeaders, account, body, token, input.SessionHash)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := e.gateway.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	upstreamLatencyMs := time.Since(upstreamStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %s", sanitizeImageExecutionMessage(err.Error()))
	}
	if resp == nil {
		return nil, fmt.Errorf("upstream returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, openAIUpstreamErrorBodyReadLimitForConfig(e.cfg)))
		if e.gateway.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(responseBody)), responseBody) {
			e.gateway.handleFailoverSideEffects(upstreamCtx, resp, account, responseBody, requestModel)
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: responseBody, ResponseHeaders: resp.Header.Clone(), RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)}
		}
		if e.gateway.handleOpenAIAccountUpstreamError(upstreamCtx, account, resp.StatusCode, resp.Header, responseBody, requestModel) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: responseBody, ResponseHeaders: resp.Header.Clone(), RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode)}
		}
		if !account.ShouldHandleErrorCode(resp.StatusCode) {
			return nil, newImageExecutionError(http.StatusInternalServerError, "upstream_error", "upstream_gateway_error", "Upstream gateway error", false, false, nil)
		}
		return nil, openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, responseBody)
	}
	applyImageResponseHeaders(sink, resp.Header, e.gateway.responseHeaderFilter)
	result, err := e.gateway.consumeOpenAIImagesResponsesSSE(upstreamCtx, resp, parsed, sink)
	if result != nil {
		result.Model = requestModel
		result.UpstreamModel = requestModel
		result.UpstreamLatencyMs = upstreamLatencyMs
	}
	return result, err
}

func applyImageResponseHeaders(sink ImageResultSink, headers http.Header, filter *responseheaders.CompiledHeaderFilter) {
	if sink == nil || headers == nil {
		return
	}
	switch target := sink.(type) {
	case *trackingImageResultSink:
		applyImageResponseHeaders(target.sink, headers, filter)
	case *HTTPImageResultSink:
		if target != nil && target.w != nil {
			responseheaders.WriteFilteredHeaders(target.w.Header(), headers, filter)
		}
	}
}

// imageExecutorUpstreamContext preserves the historical disconnect behavior
// for synchronous HTTP requests while keeping worker cancellation effective.
// Background sinks never expose a client response writer, so their context is
// left untouched and task timeouts/cancellation propagate to the upstream.
func imageExecutorUpstreamContext(ctx context.Context, parsed *OpenAIImagesRequest, account *Account, sink ImageResultSink) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !imageSinkUsesHTTPWriter(sink) || parsed == nil || account == nil {
		return ctx, func() {}
	}
	if account.Type == AccountTypeOAuth {
		return detachUpstreamContext(ctx)
	}
	return detachStreamUpstreamContext(ctx, parsed.Stream)
}

func imageSinkUsesHTTPWriter(sink ImageResultSink) bool {
	switch target := sink.(type) {
	case *HTTPImageResultSink:
		return target != nil
	case *trackingImageResultSink:
		return target != nil && imageSinkUsesHTTPWriter(target.sink)
	case *imageSequenceFrameSink:
		return target != nil && imageSinkUsesHTTPWriter(target.target)
	default:
		return false
	}
}

func (e *OpenAIImageExecutor) buildImageAPIKeyRequest(ctx context.Context, headers http.Header, account *Account, body []byte, contentType, token, endpoint string) (*http.Request, error) {
	targetURL := openAIImagesGenerationsURL
	if endpoint == openAIImagesEditsEndpoint {
		targetURL = openAIImagesEditsURL
	}
	if baseURL := account.GetOpenAIBaseURL(); baseURL != "" {
		validated, err := e.gateway.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = buildOpenAIImagesURL(validated, endpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Authorization", "Bearer "+token)
	copyOpenAIImageHeaders(req.Header, headers, openaiPassthroughAllowedHeaders)
	if ua := account.GetOpenAIUserAgent(); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func (e *OpenAIImageExecutor) buildOAuthImageRequest(ctx context.Context, headers http.Header, account *Account, body []byte, token, sessionHash string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, e.gateway.accountRepo, req.Header, account); err != nil {
		return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
	}
	copyOpenAIImageHeaders(req.Header, headers, openaiAllowedHeaders)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "opencode")
	req.Header.Set("Accept", "text/event-stream")
	if strings.TrimSpace(sessionHash) != "" {
		req.Header.Set("session_id", sessionHash)
		req.Header.Set("conversation_id", sessionHash)
	}
	if ua := account.GetOpenAIUserAgent(); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func copyOpenAIImageHeaders(dst, src http.Header, allowed map[string]bool) {
	for key, values := range src {
		if !allowed[strings.ToLower(key)] {
			continue
		}
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func newImageExecutionError(status int, typ, code, message string, retryable, indeterminate bool, cause error) *ImageExecutionError {
	return &ImageExecutionError{Status: status, Type: typ, Code: code, Message: sanitizeImageExecutionMessage(message), Retryable: retryable, Indeterminate: indeterminate, Cause: cause}
}

func sanitizeImageExecutionMessage(message string) string {
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func sanitizeImageExecutionError(err error) error {
	if err == nil {
		return nil
	}
	var existing *ImageExecutionError
	if errors.As(err, &existing) {
		existing.Message = sanitizeImageExecutionMessage(existing.Message)
		return existing
	}
	var sinkErr *imageResultSinkError
	if errors.As(err, &sinkErr) {
		return newImageExecutionError(http.StatusInternalServerError, "api_error", "result_sink_error", "Image result sink failed", true, false, err)
	}
	var upstreamErr *OpenAIImagesUpstreamError
	if errors.As(err, &upstreamErr) {
		return newImageExecutionError(upstreamErr.clientStatusCode(), upstreamErr.clientErrorType(), upstreamErr.Code, upstreamErr.clientMessage(), IsOpenAIImagesRetryableUpstreamError(upstreamErr), false, err)
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return newImageExecutionError(failoverErr.StatusCode, "upstream_error", "upstream_failover", sanitizeImageExecutionMessage(err.Error()), true, false, err)
	}
	return newImageExecutionError(http.StatusBadGateway, "upstream_error", "upstream_error", sanitizeImageExecutionMessage(err.Error()), true, false, err)
}

func imageExecutionContextError(ctx context.Context, cause error) error {
	if ctx == nil || ctx.Err() == nil {
		return cause
	}
	return newImageExecutionError(http.StatusGatewayTimeout, "upstream_error", "execution_timeout", ctx.Err().Error(), true, true, cause)
}

func isImageExecutionRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	var imageErr *ImageExecutionError
	if errors.As(err, &imageErr) {
		return imageErr.Retryable
	}
	var upstreamErr *OpenAIImagesUpstreamError
	if errors.As(err, &upstreamErr) {
		return IsOpenAIImagesRetryableUpstreamError(upstreamErr)
	}
	return true
}

type trackingImageResultSink struct {
	mu     sync.Mutex
	sink   ImageResultSink
	finals int
}

type imageResultSinkError struct {
	err error
}

func (e *imageResultSinkError) Error() string {
	if e == nil || e.err == nil {
		return "image result sink failed"
	}
	return e.err.Error()
}

func (e *imageResultSinkError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func wrapImageResultSinkError(err error) error {
	if err == nil {
		return nil
	}
	var existing *imageResultSinkError
	if errors.As(err, &existing) {
		return err
	}
	return &imageResultSinkError{err: err}
}

func (s *trackingImageResultSink) Partial(ctx context.Context, index int, data []byte, mimeType string) error {
	if s == nil || s.sink == nil {
		return &imageResultSinkError{err: fmt.Errorf("image result sink is unavailable")}
	}
	return wrapImageResultSinkError(s.sink.Partial(ctx, index, data, mimeType))
}

func (s *trackingImageResultSink) Final(ctx context.Context, image ImageArtifact) error {
	if s == nil || s.sink == nil {
		return &imageResultSinkError{err: fmt.Errorf("image result sink is unavailable")}
	}
	if err := s.sink.Final(ctx, image); err != nil {
		return wrapImageResultSinkError(err)
	}
	s.mu.Lock()
	s.finals++
	s.mu.Unlock()
	return nil
}

func (s *trackingImageResultSink) Complete(ctx context.Context, summary ImageExecutionSummary) error {
	if s == nil || s.sink == nil {
		return &imageResultSinkError{err: fmt.Errorf("image result sink is unavailable")}
	}
	return wrapImageResultSinkError(s.sink.Complete(ctx, summary))
}

func (s *trackingImageResultSink) FinalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finals
}

func (e *OpenAIImageExecutor) acquireUserSlot(ctx context.Context, user *User, apiKey *APIKey) (func(), error) {
	if e.concurrencyService == nil {
		return nil, nil
	}
	userID := int64(0)
	max := 0
	if user != nil {
		userID, max = user.ID, user.Concurrency
	}
	if userID <= 0 && apiKey != nil {
		userID = apiKey.UserID
		if apiKey.User != nil {
			max = apiKey.User.Concurrency
		}
	}
	if userID <= 0 || max <= 0 {
		return nil, nil
	}
	result, err := e.concurrencyService.AcquireUserSlot(ctx, userID, max)
	if err != nil {
		return nil, newImageExecutionError(http.StatusServiceUnavailable, "rate_limit_error", "user_slot_error", err.Error(), true, false, err)
	}
	if result != nil && result.Acquired {
		return result.ReleaseFunc, nil
	}

	queueLimit := CalculateMaxWait(max) - max
	if queueLimit < 1 {
		queueLimit = 1
	}
	canWait, waitErr := e.concurrencyService.IncrementWaitCount(ctx, userID, queueLimit)
	if waitErr != nil {
		return nil, newImageExecutionError(http.StatusServiceUnavailable, "rate_limit_error", "user_wait_queue_error", waitErr.Error(), true, false, waitErr)
	}
	if !canWait {
		return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "user_wait_queue_full", "Too many pending requests, please retry later", true, false, nil)
	}
	defer e.concurrencyService.DecrementWaitCount(ctx, userID)

	waitCtx, cancel := context.WithTimeout(ctx, imageExecutorUserWaitTimeout)
	defer cancel()
	backoff := imageExecutorWaitBackoff
	for {
		timer := time.NewTimer(backoff)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "user_concurrency_timeout", "Timed out waiting for user concurrency", true, false, waitCtx.Err())
			}
			return nil, newImageExecutionError(http.StatusRequestTimeout, "rate_limit_error", "user_concurrency_canceled", "Image execution canceled while waiting for user concurrency", true, false, waitCtx.Err())
		case <-timer.C:
		}
		result, err = e.concurrencyService.AcquireUserSlot(waitCtx, userID, max)
		if err != nil {
			return nil, newImageExecutionError(http.StatusServiceUnavailable, "rate_limit_error", "user_slot_error", err.Error(), true, false, err)
		}
		if result != nil && result.Acquired {
			return result.ReleaseFunc, nil
		}
		backoff = time.Duration(float64(backoff) * 1.5)
		if backoff > imageExecutorMaxBackoff {
			backoff = imageExecutorMaxBackoff
		}
	}
}

func (e *OpenAIImageExecutor) acquireSelectedAccountSlot(ctx context.Context, selection *AccountSelectionResult) (func(), error) {
	if selection == nil || selection.Account == nil {
		return nil, newImageExecutionError(http.StatusServiceUnavailable, "api_error", "no_available_account", "No available account", true, false, nil)
	}
	if selection.Acquired {
		if selection.ReleaseFunc != nil {
			return selection.ReleaseFunc, nil
		}
		return func() {}, nil
	}
	if selection.WaitPlan == nil || e.concurrencyService == nil {
		return nil, newImageExecutionError(http.StatusServiceUnavailable, "api_error", "account_slot_unavailable", "No account concurrency slot available", true, false, nil)
	}
	if selection.WaitPlan.MaxWaiting > 0 {
		canWait, err := e.concurrencyService.IncrementAccountWaitCount(ctx, selection.Account.ID, selection.WaitPlan.MaxWaiting)
		if err != nil {
			return nil, newImageExecutionError(http.StatusServiceUnavailable, "rate_limit_error", "account_wait_queue_error", err.Error(), true, false, err)
		}
		if !canWait {
			return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "account_wait_queue_full", "Too many pending requests, please retry later", true, false, nil)
		}
		defer e.concurrencyService.DecrementAccountWaitCount(ctx, selection.Account.ID)
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if selection.WaitPlan.Timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, selection.WaitPlan.Timeout)
		defer cancel()
	}
	for {
		result, err := e.concurrencyService.AcquireAccountSlot(waitCtx, selection.Account.ID, selection.WaitPlan.MaxConcurrency)
		if err != nil {
			return nil, newImageExecutionError(http.StatusServiceUnavailable, "rate_limit_error", "account_slot_error", err.Error(), true, false, err)
		}
		if result != nil && result.Acquired {
			return result.ReleaseFunc, nil
		}
		if selection.WaitPlan.Timeout <= 0 {
			return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "account_concurrency_limit", "Account concurrency limit exceeded", true, false, nil)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "account_concurrency_timeout", "Timed out waiting for account concurrency", true, false, waitCtx.Err())
			}
			return nil, newImageExecutionError(http.StatusRequestTimeout, "rate_limit_error", "account_concurrency_canceled", "Image execution canceled while waiting for account concurrency", true, false, waitCtx.Err())
		case <-timer.C:
		}
	}
}

type imageExecutionSemaphore struct {
	mu      sync.Mutex
	notify  chan struct{}
	limit   int
	active  int
	waiting int
	enabled bool
}

func (s *imageExecutionSemaphore) acquire(
	ctx context.Context,
	enabled bool,
	limit int,
	wait bool,
	timeout time.Duration,
	maxWaiting int,
) (func(), error) {
	if s == nil || !enabled || limit <= 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if wait {
		if timeout <= 0 {
			return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "image_concurrency_limit", "Image generation concurrency limit exceeded, please retry later", true, false, nil)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		s.mu.Lock()
		if s.notify == nil {
			s.notify = make(chan struct{})
		}
		s.enabled = enabled
		s.limit = limit
		if s.active < s.limit {
			s.active++
			s.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					s.mu.Lock()
					if s.active > 0 {
						s.active--
					}
					close(s.notify)
					s.notify = make(chan struct{})
					s.mu.Unlock()
				})
			}, nil
		}
		if !wait || (maxWaiting > 0 && s.waiting >= maxWaiting) {
			s.mu.Unlock()
			return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "image_concurrency_limit", "Image generation concurrency limit exceeded, please retry later", true, false, nil)
		}
		s.waiting++
		notify := s.notify
		s.mu.Unlock()
		select {
		case <-notify:
			s.mu.Lock()
			s.waiting--
			s.mu.Unlock()
		case <-ctx.Done():
			s.mu.Lock()
			s.waiting--
			s.mu.Unlock()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, newImageExecutionError(http.StatusTooManyRequests, "rate_limit_error", "image_concurrency_timeout", "Timed out waiting for image generation concurrency", true, false, ctx.Err())
			}
			return nil, imageExecutionContextError(ctx, ctx.Err())
		}
	}
}

func (e *OpenAIImageExecutor) acquireImageSemaphore(ctx context.Context) (func(), error) {
	if e == nil || e.cfg == nil {
		return nil, nil
	}
	settings := e.cfg.Gateway.ImageConcurrency
	wait := strings.TrimSpace(settings.OverflowMode) == config.ImageConcurrencyOverflowModeWait
	return e.imageSemaphore.acquire(
		ctx,
		settings.Enabled,
		settings.MaxConcurrentRequests,
		wait,
		time.Duration(settings.WaitTimeoutSeconds)*time.Second,
		settings.MaxWaitingRequests,
	)
}

// AcquireImageSlot lets other image-producing OpenAI endpoints share the same
// process-wide limiter as synchronous image requests and background jobs.
func (e *OpenAIImageExecutor) AcquireImageSlot(ctx context.Context) (func(), error) {
	if e == nil {
		return nil, nil
	}
	return e.acquireImageSemaphore(ctx)
}

var _ ImageExecutor = (*OpenAIImageExecutor)(nil)
