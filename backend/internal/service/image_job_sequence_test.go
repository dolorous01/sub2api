package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageSequenceWithoutReferencesUsesAnchorAndPrevious(t *testing.T) {
	executor := &sequenceRecordingExecutor{}
	sequence := newSequenceFixture(executor, nil)
	sequence.job.Request.InputFidelity = "high"
	require.NoError(t, sequence.Execute(context.Background()))
	require.Equal(t, []string{"generation", "edit", "edit", "edit"}, executor.Operations())
	require.Empty(t, executor.Inputs()[0].Parsed.InputFidelity)
	for _, input := range executor.Inputs()[1:] {
		require.Equal(t, "high", input.Parsed.InputFidelity)
	}
	require.Equal(t, []string{"frame-0", "frame-0"}, executor.InputObjectIDs(1))
	require.Equal(t, []string{"frame-0", "frame-1"}, executor.InputObjectIDs(2))
	require.Equal(t, []string{"frame-0", "frame-2"}, executor.InputObjectIDs(3))
	require.Equal(t, "global\n\nScene 1: s1", executor.Inputs()[0].Parsed.Prompt)
}

func TestImageSequenceWithReferencesUsesEveryAnchor(t *testing.T) {
	executor := &sequenceRecordingExecutor{}
	sequence := newSequenceFixture(executor, []string{"ref-a", "ref-b"})
	require.NoError(t, sequence.Execute(context.Background()))
	require.Equal(t, []string{"edit", "edit", "edit", "edit"}, executor.Operations())
	require.Equal(t, []string{"ref-a", "ref-b"}, executor.InputObjectIDs(0))
	require.Equal(t, []string{"ref-a", "ref-b", "frame-0"}, executor.InputObjectIDs(1))
	for _, input := range executor.Inputs() {
		require.Equal(t, 1, input.Parsed.N)
		require.Equal(t, "high", input.Parsed.InputFidelity)
	}
}

func TestImageSequenceFrameSinkDoesNotCollectWhenDurableWriteFails(t *testing.T) {
	collector := NewCollectingImageResultSink()
	sink := &imageSequenceFrameSink{
		frameIndex: 2,
		target:     failingSequenceResultSink{err: errors.New("durable write failed")},
		collector:  collector,
	}

	err := sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("frame"), MIMEType: "image/png"})
	require.EqualError(t, err, "durable write failed")
	require.Empty(t, collector.Results())
}

func newSequenceFixture(executor ImageExecutor, references []string) *ImageSequenceExecutor {
	job := &ImageJob{
		PublicID: "imgjob_sequence", APIKeyID: 20, RequestDigest: "digest", RequestedCount: 4,
		Request: ImageJobRequest{
			Endpoint: openAIImagesEditsEndpoint, Model: "gpt-image-2", Prompt: "global", N: 1,
			OutputFormat: "png", InputURLs: append([]string(nil), references...),
			Scenes: []string{"s1", "s2", "s3", "s4"},
		},
	}
	return &ImageSequenceExecutor{
		job: job, executor: executor, store: &sequenceNoopStore{}, maxInputs: 4,
		input:       ImageExecutionInput{Parsed: &OpenAIImagesRequest{Model: "gpt-image-2"}},
		sinkFactory: func(int) ImageResultSink { return NewCollectingImageResultSink() },
	}
}

type sequenceRecordingExecutor struct {
	mu     sync.Mutex
	inputs []ImageExecutionInput
}

func (e *sequenceRecordingExecutor) Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error) {
	e.mu.Lock()
	frame := len(e.inputs)
	e.inputs = append(e.inputs, input)
	e.mu.Unlock()
	artifact := ImageArtifact{
		Index: 0, Data: []byte(fmt.Sprintf("frame-%d", frame)), MIMEType: "image/png",
		OutputFormat: "png", UpstreamOutputID: fmt.Sprintf("frame-%d", frame),
	}
	if err := sink.Final(ctx, artifact); err != nil {
		return nil, err
	}
	forward := &OpenAIForwardResult{Model: "gpt-image-2", ImageCount: 1}
	if err := sink.Complete(ctx, ImageExecutionSummary{ForwardResult: forward}); err != nil {
		return nil, err
	}
	return &ImageExecutionResult{Forward: forward, Account: &Account{ID: 1}}, nil
}

func (e *sequenceRecordingExecutor) Inputs() []ImageExecutionInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ImageExecutionInput(nil), e.inputs...)
}

func (e *sequenceRecordingExecutor) Operations() []string {
	inputs := e.Inputs()
	operations := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.Parsed.IsEdits() {
			operations = append(operations, "edit")
		} else {
			operations = append(operations, "generation")
		}
	}
	return operations
}

func (e *sequenceRecordingExecutor) InputObjectIDs(frame int) []string {
	inputs := e.Inputs()
	return append([]string(nil), inputs[frame].InputObjectIDs...)
}

type sequenceNoopStore struct{}

type failingSequenceResultSink struct {
	err error
}

func (s failingSequenceResultSink) Partial(context.Context, int, []byte, string) error { return nil }
func (s failingSequenceResultSink) Final(context.Context, ImageArtifact) error         { return s.err }
func (s failingSequenceResultSink) Complete(context.Context, ImageExecutionSummary) error {
	return nil
}

func (*sequenceNoopStore) Put(context.Context, string, []byte, string) error { return nil }
func (*sequenceNoopStore) Get(context.Context, string) (*ImageJobObject, error) {
	return nil, ErrImageJobNotFound
}
func (*sequenceNoopStore) Delete(context.Context, string) error { return nil }
func (*sequenceNoopStore) Health(context.Context) error         { return nil }
