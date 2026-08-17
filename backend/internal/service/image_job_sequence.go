package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

type ImageSequenceExecutor struct {
	job           *ImageJob
	executor      ImageExecutor
	sinkFactory   func(index int) ImageResultSink
	store         ImageJobObjectStore
	input         ImageExecutionInput
	maxInputs     int
	canceled      func(context.Context) (bool, error)
	beforeExecute func(context.Context, int) error
	settleFrame   func(context.Context, int, *ImageExecutionResult) error

	mu         sync.Mutex
	artifacts  []ImageArtifact
	executions []*ImageExecutionResult
}

func (s *ImageSequenceExecutor) Execute(ctx context.Context) error {
	if s == nil || s.job == nil || s.executor == nil || s.store == nil || s.sinkFactory == nil {
		return fmt.Errorf("image sequence dependencies are unavailable")
	}
	if len(s.job.Request.Scenes) < 2 || len(s.job.Request.Scenes) > 4 {
		return fmt.Errorf("image sequence requires between 2 and 4 scenes")
	}
	if s.maxInputs <= 0 {
		s.maxInputs = 4
	}

	for frameIndex, scene := range s.job.Request.Scenes {
		if strings.TrimSpace(scene) == "" {
			return fmt.Errorf("sequence scene %d must not be empty", frameIndex)
		}
		if s.canceled != nil {
			canceled, err := s.canceled(ctx)
			if err != nil {
				return err
			}
			if canceled {
				return &ImageExecutionError{Status: 409, Type: "canceled", Code: "cancel_requested", Message: "Image sequence was canceled", Retryable: false}
			}
		}
		frameRequest, err := s.buildFrameRequest(frameIndex, strings.TrimSpace(scene))
		if err != nil {
			return err
		}
		body, contentType, parsed, err := BuildOpenAIImagesRequest(ctx, s.store, frameRequest)
		if err != nil {
			return err
		}
		if strings.TrimSpace(parsed.InputFidelity) == "" && parsed.IsEdits() {
			parsed.InputFidelity = "high"
			frameRequest.InputFidelity = "high"
		}
		collector := NewCollectingImageResultSink()
		frameSink := &imageSequenceFrameSink{frameIndex: frameIndex, target: s.sinkFactory(frameIndex), collector: collector}
		frameInput := s.input
		frameInput.Body = body
		frameInput.Parsed = parsed
		frameInput.RequestHeaders = cloneHTTPHeader(s.input.RequestHeaders)
		if frameInput.RequestHeaders == nil {
			frameInput.RequestHeaders = make(map[string][]string)
		}
		frameInput.RequestHeaders.Set("Content-Type", contentType)
		frameInput.RequestPayloadHash = fmt.Sprintf("%s:%d", s.job.RequestDigest, frameIndex)
		frameInput.InputObjectIDs = s.frameObjectIDs(frameIndex)
		if s.beforeExecute != nil {
			if err := s.beforeExecute(ctx, frameIndex); err != nil {
				return err
			}
		}
		execution, err := s.executor.Execute(ctx, frameInput, frameSink)
		if err != nil {
			return err
		}
		results := collector.Results()
		if len(results) != 1 {
			return fmt.Errorf("sequence frame %d returned %d final images, want 1", frameIndex, len(results))
		}
		artifact := results[0]
		artifact.Index = frameIndex
		s.mu.Lock()
		s.artifacts = append(s.artifacts, artifact)
		s.executions = append(s.executions, execution)
		s.mu.Unlock()
		if s.settleFrame != nil {
			if err := s.settleFrame(context.WithoutCancel(ctx), frameIndex, execution); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ImageSequenceExecutor) buildFrameRequest(frameIndex int, scene string) (ImageJobRequest, error) {
	request := cloneImageJobRequest(s.job.Request)
	request.Scenes = nil
	request.N = 1
	request.Prompt = strings.TrimSpace(s.job.Request.Prompt) + "\n\nScene " + fmt.Sprintf("%d", frameIndex+1) + ": " + scene
	request.Mask = nil
	request.MaskURL = ""
	hasReferences := len(s.job.Request.Inputs) > 0 || len(s.job.Request.InputURLs) > 0
	if frameIndex == 0 && !hasReferences {
		request.Endpoint = openAIImagesGenerationsEndpoint
		request.Inputs = nil
		request.InputURLs = nil
		// input_fidelity is an edits-only option. Do not carry it into the
		// generation frame when a sequence has no initial reference image.
		request.InputFidelity = ""
		return request, nil
	}
	request.Endpoint = openAIImagesEditsEndpoint
	if frameIndex == 0 {
		if strings.TrimSpace(request.InputFidelity) == "" {
			request.InputFidelity = "high"
		}
		return request, s.validateFrameInputCount(request)
	}

	s.mu.Lock()
	artifacts := cloneImageArtifacts(s.artifacts)
	s.mu.Unlock()
	if len(artifacts) < frameIndex {
		return ImageJobRequest{}, fmt.Errorf("sequence frame %d prerequisites are unavailable", frameIndex)
	}
	previous := artifacts[frameIndex-1]
	if len(request.Inputs) > 0 {
		if !hasReferences {
			request.Inputs = append(request.Inputs, s.artifactInputRef(artifacts[0], 0))
		}
		request.Inputs = append(request.Inputs, s.artifactInputRef(previous, len(request.Inputs)))
	} else if len(request.InputURLs) > 0 {
		request.InputURLs = append(request.InputURLs, imageArtifactDataURL(previous))
	} else {
		request.InputURLs = []string{imageArtifactDataURL(artifacts[0]), imageArtifactDataURL(previous)}
	}
	if strings.TrimSpace(request.InputFidelity) == "" {
		request.InputFidelity = "high"
	}
	return request, s.validateFrameInputCount(request)
}

func (s *ImageSequenceExecutor) validateFrameInputCount(request ImageJobRequest) error {
	count := len(request.Inputs) + len(request.InputURLs)
	if count > s.maxInputs {
		return fmt.Errorf("sequence frame has %d inputs, maximum is %d", count, s.maxInputs)
	}
	return nil
}

func (s *ImageSequenceExecutor) frameObjectIDs(frameIndex int) []string {
	if s == nil || s.job == nil {
		return nil
	}
	ids := make([]string, 0, len(s.job.Request.Inputs)+len(s.job.Request.InputURLs)+2)
	for _, input := range s.job.Request.Inputs {
		ids = append(ids, input.ObjectKey)
	}
	for _, input := range s.job.Request.InputURLs {
		ids = append(ids, input)
	}
	if frameIndex == 0 {
		return ids
	}
	s.mu.Lock()
	artifacts := cloneImageArtifacts(s.artifacts)
	s.mu.Unlock()
	if len(artifacts) == 0 || frameIndex <= 0 || frameIndex-1 >= len(artifacts) {
		return ids
	}
	anchorID := artifacts[0].UpstreamOutputID
	if anchorID == "" {
		anchorID = fmt.Sprintf("frame-%d", artifacts[0].Index)
	}
	previousID := artifacts[frameIndex-1].UpstreamOutputID
	if previousID == "" {
		previousID = fmt.Sprintf("frame-%d", artifacts[frameIndex-1].Index)
	}
	if len(ids) == 0 {
		ids = append(ids, anchorID)
	}
	ids = append(ids, previousID)
	return ids
}

func (s *ImageSequenceExecutor) artifactInputRef(artifact ImageArtifact, index int) ImageJobInputRef {
	mimeType := imageArtifactContentType(artifact)
	_, extension, _ := normalizeImageJobMIMEType(mimeType)
	key := fmt.Sprintf("image-jobs/%d/%s/results/%d.%s", s.job.APIKeyID, s.job.PublicID, artifact.Index, extension)
	sum := sha256.Sum256(artifact.Data)
	return ImageJobInputRef{
		Kind: "image", Index: index, ObjectKey: key, MIMEType: mimeType,
		ByteSize: int64(len(artifact.Data)), SHA256: hex.EncodeToString(sum[:]), FieldName: "image[]",
	}
}

func (s *ImageSequenceExecutor) Artifacts() []ImageArtifact {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneImageArtifacts(s.artifacts)
}

func (s *ImageSequenceExecutor) Executions() []*ImageExecutionResult {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*ImageExecutionResult(nil), s.executions...)
}

func cloneImageJobRequest(request ImageJobRequest) ImageJobRequest {
	cloned := request
	cloned.OutputCompression = cloneIntPointer(request.OutputCompression)
	cloned.PartialImages = cloneIntPointer(request.PartialImages)
	cloned.InputURLs = append([]string(nil), request.InputURLs...)
	cloned.Inputs = append([]ImageJobInputRef(nil), request.Inputs...)
	cloned.Scenes = append([]string(nil), request.Scenes...)
	if request.Mask != nil {
		mask := *request.Mask
		cloned.Mask = &mask
	}
	return cloned
}

func imageArtifactDataURL(artifact ImageArtifact) string {
	return "data:" + imageArtifactContentType(artifact) + ";base64," + base64.StdEncoding.EncodeToString(artifact.Data)
}

type imageSequenceFrameSink struct {
	frameIndex int
	target     ImageResultSink
	collector  *CollectingImageResultSink
}

func (s *imageSequenceFrameSink) Partial(ctx context.Context, _ int, data []byte, mimeType string) error {
	if s.target != nil {
		if err := s.target.Partial(ctx, s.frameIndex, data, mimeType); err != nil {
			return err
		}
	}
	return s.collector.Partial(ctx, s.frameIndex, data, mimeType)
}

func (s *imageSequenceFrameSink) Final(ctx context.Context, artifact ImageArtifact) error {
	artifact.Index = s.frameIndex
	if s.target == nil || s.collector == nil {
		return fmt.Errorf("sequence frame sink is unavailable")
	}
	// Persist first. The collector drives sequence progression, so recording an
	// artifact before the durable sink accepts it could make an unavailable
	// frame look complete to the next scene.
	if err := s.target.Final(ctx, artifact); err != nil {
		return err
	}
	return s.collector.Final(ctx, artifact)
}

func (s *imageSequenceFrameSink) Complete(ctx context.Context, summary ImageExecutionSummary) error {
	return s.collector.Complete(ctx, summary)
}

var _ ImageResultSink = (*imageSequenceFrameSink)(nil)
