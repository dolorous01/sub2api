package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const maxCanvasMediaUpstreamJSONBytes int64 = 4 << 20

type CanvasGrokMediaResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Result     *OpenAIForwardResult
}

// ExecuteCanvasGrokMedia is the service-layer form of the Grok media
// transport. It deliberately returns bytes instead of writing a Gin response,
// allowing durable workers to reuse account credentials and proxy policy.
func (s *OpenAIGatewayService) ExecuteCanvasGrokMedia(
	ctx context.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*CanvasGrokMediaResponse, error) {
	started := time.Now()
	if s == nil || s.httpUpstream == nil || account == nil {
		return nil, fmt.Errorf("grok media execution dependencies are unavailable")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok media", account.Platform)
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	targetURL, err := endpoint.upstreamURL(account.GetGrokBaseURL(), requestID)
	if err != nil {
		return nil, err
	}
	body, contentType, err = prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	body, contentType, err = normalizeGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}
	var requestBody io.Reader
	if endpoint.RequiresRequestBody() {
		requestBody = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, endpoint.httpMethod(), targetURL, requestBody)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sub2api-grok/1.0")
	if endpoint.RequiresRequestBody() {
		if strings.TrimSpace(contentType) == "" {
			contentType = "application/json"
		}
		request.Header.Set("Content-Type", contentType)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	response, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("grok media upstream returned an empty response")
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxCanvasMediaUpstreamJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(responseBody)) > maxCanvasMediaUpstreamJSONBytes {
		return nil, fmt.Errorf("grok media upstream response is too large")
	}
	s.updateGrokUsageSnapshot(ctx, account.ID, xaiQuotaHeaders(response))
	requestInfo := ParseGrokMediaRequest(contentType, body)
	usage := grokMediaUsageFromResponse(endpoint, requestInfo, responseBody)
	result := &OpenAIForwardResult{
		RequestID:        firstNonEmpty(response.Header.Get("x-request-id"), response.Header.Get("xai-request-id")),
		ResponseID:       usage.ResponseID,
		Usage:            usage.Usage,
		Model:            requestInfo.Model,
		BillingModel:     requestInfo.Model,
		UpstreamModel:    requestInfo.Model,
		ResponseHeaders:  response.Header.Clone(),
		Duration:         time.Since(started),
		ImageCount:       usage.ImageCount,
		ImageSize:        usage.ImageSize,
		ImageInputSize:   usage.ImageInputSize,
		ImageOutputSizes: usage.ImageOutputSizes,
	}
	return &CanvasGrokMediaResponse{
		StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: responseBody, Result: result,
	}, nil
}

func xaiQuotaHeaders(response *http.Response) *xai.QuotaSnapshot {
	if response == nil {
		return nil
	}
	return xai.ParseQuotaHeaders(response.Header, response.StatusCode)
}

// ExecuteCanvasAudioSpeech opens a binary OpenAI-compatible speech response.
// The caller owns and must close the returned response body.
func (s *OpenAIGatewayService) ExecuteCanvasAudioSpeech(ctx context.Context, account *Account, body []byte) (*http.Response, error) {
	if s == nil || s.httpUpstream == nil || account == nil {
		return nil, fmt.Errorf("audio speech execution dependencies are unavailable")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("account does not support OpenAI audio speech")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSpace(account.GetOpenAIBaseURL())
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL, err = s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(strings.ToLower(targetURL), "/v1") {
		targetURL += "/audio/speech"
	} else {
		targetURL += "/v1/audio/speech"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request = request.WithContext(WithHTTPUpstreamProfile(request.Context(), HTTPUpstreamProfileOpenAI))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "audio/*, application/octet-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "canvas-audio-"+HashUsageRequestPayload(body))
	if userAgent := account.GetOpenAIUserAgent(); userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	response, err := s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("audio speech upstream returned an empty response")
	}
	return response, nil
}
