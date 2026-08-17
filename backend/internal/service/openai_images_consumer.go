package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/imroc/req/v3"
	"github.com/tidwall/gjson"
)

// consumeOpenAIImagesResponse converts a complete Images API response into
// artifacts. It intentionally does not depend on Gin, so the same parser can
// be used by synchronous HTTP handlers and background image workers.
func (s *OpenAIGatewayService) consumeOpenAIImagesResponse(
	ctx context.Context,
	resp *http.Response,
	parsed *OpenAIImagesRequest,
	sink ImageResultSink,
) (*OpenAIForwardResult, error) {
	if resp != nil && parsed != nil && parsed.Stream && isEventStreamResponse(resp.Header) {
		return s.consumeOpenAIImagesSSE(ctx, resp, parsed, sink, false)
	}
	return s.consumeOpenAIImagesBody(ctx, resp, parsed, sink, false)
}

// consumeOpenAIImagesResponsesSSE parses the ChatGPT Responses bridge stream
// and emits the same ImageArtifact values as the native Images API parser.
func (s *OpenAIGatewayService) consumeOpenAIImagesResponsesSSE(
	ctx context.Context,
	resp *http.Response,
	parsed *OpenAIImagesRequest,
	sink ImageResultSink,
) (*OpenAIForwardResult, error) {
	return s.consumeOpenAIImagesSSE(ctx, resp, parsed, sink, true)
}

func (s *OpenAIGatewayService) consumeOpenAIImagesBody(
	ctx context.Context,
	resp *http.Response,
	parsed *OpenAIImagesRequest,
	sink ImageResultSink,
	responses bool,
) (*OpenAIForwardResult, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("image upstream response is missing")
	}
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if sink == nil {
		return nil, fmt.Errorf("image result sink is required")
	}
	body, err := readUpstreamResponseBodyLimited(resp.Body, resolveUpstreamResponseReadLimit(s.cfg))
	if err != nil {
		return nil, err
	}

	usage, _ := extractOpenAIUsageFromJSONBytes(body)
	createdAt := imageCreatedAtFromBody(body)
	artifacts, responseCreatedAt, responseUsage, err := imageArtifactsFromPayload(ctx, body, parsed, responses)
	if err != nil {
		return nil, err
	}
	if responseCreatedAt > 0 {
		createdAt = responseCreatedAt
	}
	mergeImageUsage(&usage, responseUsage)
	if len(artifacts) == 0 {
		if upstreamErr := extractOpenAIImagesUpstreamError(body); upstreamErr != nil {
			return nil, upstreamErr
		}
		return nil, fmt.Errorf("upstream did not return image output")
	}
	for _, artifact := range artifacts {
		if err := sink.Final(ctx, artifact); err != nil {
			return nil, err
		}
	}
	result := newImageConsumerForwardResult(resp, parsed, usage, artifacts, createdAt)
	if err := sink.Complete(ctx, ImageExecutionSummary{CreatedAt: createdAt, Usage: usage, ForwardResult: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *OpenAIGatewayService) consumeOpenAIImagesSSE(
	ctx context.Context,
	resp *http.Response,
	parsed *OpenAIImagesRequest,
	sink ImageResultSink,
	responses bool,
) (*OpenAIForwardResult, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("image upstream response is missing")
	}
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if sink == nil {
		return nil, fmt.Errorf("image result sink is required")
	}
	start := time.Now()
	usage := OpenAIUsage{}
	createdAt := int64(0)
	results := make([]ImageArtifact, 0, imageMaxInt(1, parsed.N))
	seen := make(map[string]struct{})
	pendingResponses := make([]ImageArtifact, 0, imageMaxInt(1, parsed.N))
	pendingResponseSeen := make(map[string]struct{})
	pendingResponseCounts := make(map[string]int)
	var firstTokenMs *int
	var terminalErr error
	var terminalSeen bool
	var accumulator openAISSEDataAccumulator

	emitPayload := func(payload []byte) {
		if terminalErr != nil || len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
			return
		}
		if firstTokenMs == nil {
			ms := int(time.Since(start).Milliseconds())
			firstTokenMs = &ms
		}
		mergeImageUsage(&usage, usageFromImagePayload(payload))
		if eventCreatedAt := imageCreatedAtFromBody(payload); eventCreatedAt > 0 {
			createdAt = eventCreatedAt
		}
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if strings.EqualFold(eventType, "error") || strings.HasSuffix(eventType, ".failed") {
			if upstreamErr := openAIImagesUpstreamErrorFromSSEPayload(payload); upstreamErr != nil {
				terminalErr = upstreamErr
			}
			return
		}
		if strings.Contains(strings.ToLower(eventType), "partial_image") {
			data, mimeType := imagePartialBytes(payload, parsed)
			if len(data) > 0 {
				index := int(gjson.GetBytes(payload, "partial_image_index").Int())
				if err := sink.Partial(ctx, index, data, mimeType); err != nil {
					terminalErr = err
				}
			}
		}
		if responses && eventType == "response.output_item.done" {
			item, itemID, ok, err := extractOpenAIImageFromResponsesOutputItemDone(payload)
			if err != nil {
				terminalErr = err
				return
			}
			if ok {
				artifact, err := responseImageArtifact(len(pendingResponses), item, item)
				if err != nil {
					terminalErr = err
					return
				}
				artifact.UpstreamOutputID = itemID
				key := strings.TrimSpace(itemID)
				if key == "" {
					key = imageArtifactIdentity(artifact)
				}
				if _, exists := pendingResponseSeen[key]; !exists {
					pendingResponseSeen[key] = struct{}{}
					pendingResponses = append(pendingResponses, artifact)
					pendingResponseCounts[imageArtifactContentIdentity(artifact)]++
					if err := sink.Final(ctx, artifact); err != nil {
						terminalErr = err
						return
					}
					results = append(results, artifact)
				}
			}
			return
		}
		artifacts, eventCreatedAt, eventUsage, err := imageArtifactsFromPayload(ctx, payload, parsed, responses)
		if err != nil {
			terminalErr = err
			return
		}
		if eventCreatedAt > 0 {
			createdAt = eventCreatedAt
		}
		mergeImageUsage(&usage, eventUsage)
		if responses && eventType == "response.completed" {
			filtered := artifacts[:0]
			for _, artifact := range artifacts {
				key := imageArtifactContentIdentity(artifact)
				if pendingResponseCounts[key] > 0 {
					pendingResponseCounts[key]--
					continue
				}
				filtered = append(filtered, artifact)
			}
			artifacts = filtered
			pendingResponses = nil
			pendingResponseSeen = nil
			pendingResponseCounts = nil
		}
		for _, artifact := range artifacts {
			key := imageArtifactIdentity(artifact)
			if key != "" {
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
			}
			if err := sink.Final(ctx, artifact); err != nil {
				terminalErr = err
				return
			}
			results = append(results, artifact)
		}
		if strings.HasSuffix(eventType, ".completed") || strings.EqualFold(eventType, "response.done") {
			terminalSeen = true
		}
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			accumulator.AddLine(string(line), emitPayload)
		}
		if terminalErr != nil {
			return nil, terminalErr
		}
		if readErr == io.EOF {
			accumulator.Flush(emitPayload)
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if terminalErr != nil {
		return nil, terminalErr
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("upstream did not return image output")
	}
	// A few bridge deployments omit response.completed and terminate after an
	// output_item.done event. Final artifacts are authoritative in that case.
	_ = terminalSeen
	result := newImageConsumerForwardResult(resp, parsed, usage, results, createdAt)
	result.FirstTokenMs = firstTokenMs
	result.Duration = time.Since(start)
	if err := sink.Complete(ctx, ImageExecutionSummary{CreatedAt: createdAt, Usage: usage, ForwardResult: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func imageArtifactsFromPayload(
	ctx context.Context,
	payload []byte,
	parsed *OpenAIImagesRequest,
	responses bool,
) ([]ImageArtifact, int64, OpenAIUsage, error) {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return nil, 0, OpenAIUsage{}, nil
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "type").String())), "partial_image") {
		return nil, 0, OpenAIUsage{}, nil
	}
	if responses {
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		switch eventType {
		case "response.completed":
			items, createdAt, _, firstMeta, err := extractOpenAIImagesFromResponsesCompleted(payload)
			if err != nil {
				return nil, 0, OpenAIUsage{}, err
			}
			artifacts := make([]ImageArtifact, 0, len(items))
			for index, item := range items {
				artifact, err := responseImageArtifact(index, item, firstMeta)
				if err != nil {
					return nil, 0, OpenAIUsage{}, err
				}
				artifacts = append(artifacts, artifact)
			}
			return artifacts, createdAt, usageFromImagePayload(payload), nil
		case "response.output_item.done":
			item, _, ok, err := extractOpenAIImageFromResponsesOutputItemDone(payload)
			if err != nil || !ok {
				return nil, 0, OpenAIUsage{}, err
			}
			artifact, err := responseImageArtifact(0, item, item)
			if err != nil {
				return nil, 0, OpenAIUsage{}, err
			}
			return []ImageArtifact{artifact}, imageCreatedAtFromBody(payload), usageFromImagePayload(payload), nil
		}
	}

	pointers := collectOpenAIImagePointers(payload)
	if len(pointers) == 0 {
		// `url` values returned by some compatible Images providers do not have
		// a filename suffix. Recover them from the public data array.
		for index, item := range gjson.GetBytes(payload, "data").Array() {
			pointer := openAIImagePointerInfo{
				DownloadURL: strings.TrimSpace(item.Get("url").String()),
				B64JSON:     strings.TrimSpace(item.Get("b64_json").String()),
				MimeType:    strings.TrimSpace(item.Get("mime_type").String()),
				Prompt:      strings.TrimSpace(item.Get("revised_prompt").String()),
			}
			if pointer.DownloadURL != "" || pointer.B64JSON != "" {
				pointers = append(pointers, pointer)
			}
			_ = index
		}
	}
	artifacts := make([]ImageArtifact, 0, len(pointers))
	for index, pointer := range pointers {
		data, err := resolveConsumerImageBytes(ctx, pointer)
		if err != nil {
			return nil, 0, OpenAIUsage{}, err
		}
		format := strings.TrimSpace(parsed.OutputFormat)
		if format == "" {
			format = strings.TrimPrefix(strings.TrimSpace(pointer.MimeType), "image/")
		}
		mimeType := strings.TrimSpace(pointer.MimeType)
		if mimeType == "" && format != "" {
			mimeType = openAIImageOutputMIMEType(format)
		}
		if mimeType == "" {
			mimeType = imageArtifactContentType(ImageArtifact{Data: data})
		}
		artifact := ImageArtifact{
			Index:         index,
			Data:          data,
			MIMEType:      mimeType,
			OutputFormat:  format,
			SizeTier:      parsed.SizeTier,
			RevisedPrompt: pointer.Prompt,
		}
		if size := strings.TrimSpace(gjson.GetBytes(payload, fmt.Sprintf("data.%d.size", index)).String()); size != "" {
			artifact.SizeTier = size
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, imageCreatedAtFromBody(payload), usageFromImagePayload(payload), nil
}

func responseImageArtifact(index int, item, fallback openAIResponsesImageResult) (ImageArtifact, error) {
	encoded := normalizeOpenAIImageBase64(item.Result)
	if encoded == "" {
		return ImageArtifact{}, fmt.Errorf("upstream image result %d is not valid base64", index)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ImageArtifact{}, err
	}
	format := strings.TrimSpace(item.OutputFormat)
	if format == "" {
		format = strings.TrimSpace(fallback.OutputFormat)
	}
	mimeType := openAIImageOutputMIMEType(format)
	return ImageArtifact{
		Index:            index,
		Data:             data,
		MIMEType:         mimeType,
		OutputFormat:     format,
		SizeTier:         firstNonEmptyString(item.Size, fallback.Size),
		Background:       firstNonEmptyString(item.Background, fallback.Background),
		Quality:          firstNonEmptyString(item.Quality, fallback.Quality),
		RevisedPrompt:    item.RevisedPrompt,
		UpstreamOutputID: "",
	}, nil
}

func resolveConsumerImageBytes(ctx context.Context, pointer openAIImagePointerInfo) ([]byte, error) {
	if normalized := normalizeOpenAIImageBase64(pointer.B64JSON); normalized != "" {
		return base64.StdEncoding.DecodeString(normalized)
	}
	url := strings.TrimSpace(pointer.DownloadURL)
	if url == "" && strings.TrimSpace(pointer.Pointer) != "" {
		client := req.C()
		return resolveOpenAIImageBytes(ctx, client, nil, "", pointer, openAIImageMaxDownloadBytes)
	}
	if strings.HasPrefix(strings.ToLower(url), "data:") {
		comma := strings.IndexByte(url, ',')
		if comma < 0 {
			return nil, fmt.Errorf("invalid image data URL")
		}
		return base64.StdEncoding.DecodeString(normalizeBase64ForDecode(url[comma+1:]))
	}
	if url == "" {
		return nil, fmt.Errorf("image asset is missing data")
	}
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	response, err := client.Do(reqHTTP)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download image bytes failed: status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openAIImageMaxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > openAIImageMaxDownloadBytes {
		return nil, fmt.Errorf("downloaded image exceeds maximum size")
	}
	return data, nil
}

func normalizeBase64ForDecode(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "=") + strings.Repeat("=", (4-len(raw)%4)%4)
	return raw
}

func imagePartialBytes(payload []byte, parsed *OpenAIImagesRequest) ([]byte, string) {
	encoded := firstNonEmptyString(
		gjson.GetBytes(payload, "partial_image_b64").String(),
		gjson.GetBytes(payload, "partial_image").String(),
		gjson.GetBytes(payload, "b64_json").String(),
	)
	encoded = normalizeOpenAIImageBase64(encoded)
	if encoded == "" {
		return nil, ""
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ""
	}
	format := ""
	if parsed != nil {
		format = parsed.OutputFormat
	}
	if value := strings.TrimSpace(gjson.GetBytes(payload, "output_format").String()); value != "" {
		format = value
	}
	return data, openAIImageOutputMIMEType(format)
}

func imageCreatedAtFromBody(body []byte) int64 {
	for _, path := range []string{"created", "created_at", "response.created_at"} {
		if value := gjson.GetBytes(body, path).Int(); value > 0 {
			return value
		}
	}
	return 0
}

func usageFromImagePayload(payload []byte) OpenAIUsage {
	usage, _ := extractOpenAIUsageFromJSONBytes(payload)
	return usage
}

func mergeImageUsage(dst *OpenAIUsage, src OpenAIUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.ImageInputTokens > 0 {
		dst.ImageInputTokens = src.ImageInputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

func newImageConsumerForwardResult(
	resp *http.Response,
	parsed *OpenAIImagesRequest,
	usage OpenAIUsage,
	artifacts []ImageArtifact,
	createdAt int64,
) *OpenAIForwardResult {
	result := &OpenAIForwardResult{Usage: usage, Stream: parsed != nil && parsed.Stream, ImageCount: len(artifacts)}
	if resp != nil {
		result.RequestID = resp.Header.Get("x-request-id")
		result.ResponseHeaders = resp.Header.Clone()
	}
	if parsed != nil {
		result.Model = parsed.Model
		result.ImageSize = parsed.SizeTier
		result.ImageInputSize = parsed.Size
	}
	result.ImageOutputSizes = make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.SizeTier != "" {
			result.ImageOutputSizes = append(result.ImageOutputSizes, artifact.SizeTier)
		}
	}
	if createdAt > 0 {
		result.ResponseID = fmt.Sprintf("image-%d", createdAt)
	}
	return result
}

func imageArtifactIdentity(artifact ImageArtifact) string {
	if len(artifact.Data) == 0 {
		return ""
	}
	sum := sha256.Sum256(artifact.Data)
	return fmt.Sprintf("%d:%s:%s", artifact.Index, strings.TrimSpace(artifact.OutputFormat), hex.EncodeToString(sum[:]))
}

func imageArtifactContentIdentity(artifact ImageArtifact) string {
	if len(artifact.Data) == 0 {
		return ""
	}
	sum := sha256.Sum256(artifact.Data)
	return strings.TrimSpace(artifact.OutputFormat) + ":" + hex.EncodeToString(sum[:])
}

func imageMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
