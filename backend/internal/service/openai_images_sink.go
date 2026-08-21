package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ImageArtifact is one final image produced by an image execution. The byte
// payload is kept at this boundary so a sink can choose HTTP, object storage,
// or an in-memory collector without duplicating response parsing.
type ImageArtifact struct {
	Index            int
	Data             []byte
	MIMEType         string
	Width            int
	Height           int
	SizeTier         string
	OutputFormat     string
	Background       string
	Quality          string
	RevisedPrompt    string
	UpstreamOutputID string
}

// ImageExecutionSummary contains execution-level data emitted after all
// final artifacts have been delivered.
type ImageExecutionSummary struct {
	CreatedAt     int64
	Usage         OpenAIUsage
	ForwardResult *OpenAIForwardResult
}

// ImageSinkOptions controls the wire representation used by an HTTP sink.
// EventPrefix defaults to image_generation and is useful for edits/sequences.
type ImageSinkOptions struct {
	Stream         bool
	ResponseFormat string
	OutputFormat   string
	EventPrefix    string
}

// ImageResultSink receives partial and final image output from an executor.
type ImageResultSink interface {
	Partial(ctx context.Context, index int, data []byte, mimeType string) error
	Final(ctx context.Context, image ImageArtifact) error
	Complete(ctx context.Context, summary ImageExecutionSummary) error
}

// CollectingImageResultSink is a concurrency-safe sink for workers, tests and
// sequence orchestration. Partial images are deliberately not included in
// Results because they are previews, not billable final artifacts.
type CollectingImageResultSink struct {
	mu        sync.Mutex
	partials  []ImageArtifact
	results   []ImageArtifact
	summary   ImageExecutionSummary
	completed bool
}

func NewCollectingImageResultSink() *CollectingImageResultSink {
	return &CollectingImageResultSink{}
}

func (s *CollectingImageResultSink) Partial(_ context.Context, index int, data []byte, mimeType string) error {
	if s == nil {
		return fmt.Errorf("image result sink is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image result sink is already complete")
	}
	s.partials = append(s.partials, ImageArtifact{Index: index, Data: append([]byte(nil), data...), MIMEType: mimeType})
	return nil
}

func (s *CollectingImageResultSink) Final(_ context.Context, image ImageArtifact) error {
	if s == nil {
		return fmt.Errorf("image result sink is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image result sink is already complete")
	}
	image.Data = append([]byte(nil), image.Data...)
	s.results = append(s.results, image)
	return nil
}

func (s *CollectingImageResultSink) Complete(_ context.Context, summary ImageExecutionSummary) error {
	if s == nil {
		return fmt.Errorf("image result sink is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image result sink is already complete")
	}
	s.summary = cloneImageExecutionSummary(summary)
	s.completed = true
	return nil
}

func (s *CollectingImageResultSink) Results() []ImageArtifact {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneImageArtifacts(s.results)
}

func (s *CollectingImageResultSink) Partials() []ImageArtifact {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneImageArtifacts(s.partials)
}

func (s *CollectingImageResultSink) Summary() ImageExecutionSummary {
	if s == nil {
		return ImageExecutionSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneImageExecutionSummary(s.summary)
}

func cloneImageExecutionSummary(summary ImageExecutionSummary) ImageExecutionSummary {
	cloned := summary
	if summary.ForwardResult != nil {
		forward := *summary.ForwardResult
		forward.ResponseHeaders = summary.ForwardResult.ResponseHeaders.Clone()
		forward.ImageOutputSizes = append([]string(nil), summary.ForwardResult.ImageOutputSizes...)
		if summary.ForwardResult.ImageSizeBreakdown != nil {
			forward.ImageSizeBreakdown = make(map[string]int, len(summary.ForwardResult.ImageSizeBreakdown))
			for key, value := range summary.ForwardResult.ImageSizeBreakdown {
				forward.ImageSizeBreakdown[key] = value
			}
		}
		cloned.ForwardResult = &forward
	}
	return cloned
}

func cloneImageArtifacts(source []ImageArtifact) []ImageArtifact {
	result := make([]ImageArtifact, len(source))
	for index, item := range source {
		result[index] = item
		result[index].Data = append([]byte(nil), item.Data...)
	}
	return result
}

// HTTPImageResultSink preserves the public OpenAI image response shape for
// synchronous callers. It buffers JSON responses and emits SSE events as
// artifacts arrive.
type HTTPImageResultSink struct {
	mu          sync.Mutex
	w           http.ResponseWriter
	options     ImageSinkOptions
	results     []ImageArtifact
	completed   bool
	writeHeader bool
}

func NewHTTPImageResultSink(w http.ResponseWriter, options ImageSinkOptions) *HTTPImageResultSink {
	if strings.TrimSpace(options.EventPrefix) == "" {
		options.EventPrefix = "image_generation"
	}
	if strings.TrimSpace(options.ResponseFormat) == "" {
		options.ResponseFormat = "b64_json"
	}
	return &HTTPImageResultSink{w: w, options: options}
}

func (s *HTTPImageResultSink) Partial(_ context.Context, index int, data []byte, mimeType string) error {
	if s == nil || s.w == nil {
		return fmt.Errorf("image HTTP sink requires a response writer")
	}
	if !s.options.Stream {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image HTTP sink is already complete")
	}
	payload := map[string]any{
		"type":                s.options.EventPrefix + ".partial_image",
		"b64_json":            base64.StdEncoding.EncodeToString(data),
		"partial_image_index": index,
	}
	if mimeType != "" {
		payload["mime_type"] = mimeType
	}
	if strings.EqualFold(strings.TrimSpace(s.options.ResponseFormat), "url") {
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		payload["url"] = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writeSSELocked(s.options.EventPrefix+".partial_image", body)
}

func (s *HTTPImageResultSink) Final(_ context.Context, image ImageArtifact) error {
	if s == nil || s.w == nil {
		return fmt.Errorf("image HTTP sink requires a response writer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image HTTP sink is already complete")
	}
	image.Data = append([]byte(nil), image.Data...)
	s.results = append(s.results, image)
	if !s.options.Stream {
		return nil
	}
	body, err := s.imagePayload(image)
	if err != nil {
		return err
	}
	var event map[string]any
	if err := json.Unmarshal(body, &event); err != nil {
		return err
	}
	event["type"] = s.options.EventPrefix + ".completed"
	event["created_at"] = time.Now().Unix()
	event["index"] = image.Index
	body, err = json.Marshal(event)
	if err != nil {
		return err
	}
	return s.writeSSELocked(s.options.EventPrefix+".completed", body)
}

func (s *HTTPImageResultSink) Complete(_ context.Context, summary ImageExecutionSummary) error {
	if s == nil || s.w == nil {
		return fmt.Errorf("image HTTP sink requires a response writer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image HTTP sink is already complete")
	}
	s.completed = true
	created := summary.CreatedAt
	if created <= 0 {
		created = time.Now().Unix()
	}
	if s.options.Stream {
		// Final events already carry each artifact. Do not add a synthetic done
		// event: existing OpenAI image clients terminate on completed events.
		return nil
	}
	items := make([]map[string]any, 0, len(s.results))
	for _, image := range s.results {
		payload, err := s.imagePayload(image)
		if err != nil {
			return err
		}
		var item map[string]any
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		items = append(items, item)
	}
	response := map[string]any{"created": created, "data": items}
	if summary.ForwardResult != nil && summary.ForwardResult.Model != "" {
		response["model"] = summary.ForwardResult.Model
	}
	if hasImageUsage(summary.Usage) {
		response["usage"] = summary.Usage
	}
	if len(s.results) > 0 {
		first := s.results[0]
		if first.OutputFormat != "" {
			response["output_format"] = first.OutputFormat
		}
		if first.Background != "" {
			response["background"] = first.Background
		}
		if first.Quality != "" {
			response["quality"] = first.Quality
		}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if !s.writeHeader {
		s.w.Header().Set("Content-Type", "application/json; charset=utf-8")
		s.writeHeader = true
	}
	_, err = s.w.Write(body)
	return err
}

func (s *HTTPImageResultSink) imagePayload(image ImageArtifact) ([]byte, error) {
	format := strings.ToLower(strings.TrimSpace(s.options.ResponseFormat))
	if format == "" {
		format = "b64_json"
	}
	encoded := base64.StdEncoding.EncodeToString(image.Data)
	payload := map[string]any{}
	if format == "url" {
		mimeType := strings.TrimSpace(image.MIMEType)
		if mimeType == "" {
			mimeType = http.DetectContentType(image.Data)
		}
		payload["url"] = "data:" + mimeType + ";base64," + encoded
	} else {
		payload["b64_json"] = encoded
	}
	if image.RevisedPrompt != "" {
		payload["revised_prompt"] = image.RevisedPrompt
	}
	return json.Marshal(payload)
}

func hasImageUsage(usage OpenAIUsage) bool {
	return usage.InputTokens != 0 || usage.ImageInputTokens != 0 || usage.OutputTokens != 0 ||
		usage.CacheCreationInputTokens != 0 || usage.CacheReadInputTokens != 0 || usage.ImageOutputTokens != 0
}

func (s *HTTPImageResultSink) writeSSELocked(event string, payload []byte) error {
	if !s.writeHeader {
		s.w.Header().Set("Content-Type", "text/event-stream")
		s.w.Header().Set("Cache-Control", "no-cache")
		s.w.Header().Set("Connection", "keep-alive")
		s.writeHeader = true
	}
	if event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", payload); err != nil {
		return err
	}
	if flusher, ok := s.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// ImageSinkResultCount is useful to callers that need a stable count without
// exposing sink internals.
func ImageSinkResultCount(sink ImageResultSink) int {
	collector, ok := sink.(*CollectingImageResultSink)
	if !ok || collector == nil {
		return 0
	}
	return len(collector.Results())
}

func imageArtifactContentType(image ImageArtifact) string {
	if strings.TrimSpace(image.MIMEType) != "" {
		return strings.TrimSpace(image.MIMEType)
	}
	if len(image.Data) > 0 {
		return http.DetectContentType(image.Data)
	}
	return "application/octet-stream"
}
