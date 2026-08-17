package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConsumeOpenAIImagesResponseEmitsEveryJSONArtifact(t *testing.T) {
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", N: 4, ResponseFormat: "b64_json"}
	sink := NewCollectingImageResultSink()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"req-four"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1710000010,"data":[{"b64_json":"MA=="},{"b64_json":"MQ=="},{"b64_json":"Mg=="},{"b64_json":"Mw=="}]}`)),
	}
	result, err := service.consumeOpenAIImagesResponse(context.Background(), resp, parsed, sink)
	require.NoError(t, err)
	require.Equal(t, 4, result.ImageCount)
	require.Len(t, sink.Results(), 4)
	require.Equal(t, []byte("3"), sink.Results()[3].Data)
	require.Equal(t, "req-four", result.RequestID)
}

func TestConsumeOpenAIImagesResponseEmitsPartialAndFinalSSEArtifacts(t *testing.T) {
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", N: 1, Stream: true, OutputFormat: "png"}
	sink := NewCollectingImageResultSink()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cHJldmlldw==\",\"partial_image_index\":0}\n\n" +
				"data: {\"type\":\"image_generation.completed\",\"created\":1710000011,\"b64_json\":\"ZmluYWw=\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}\n\n" + "data: [DONE]\n\n",
		)),
	}
	result, err := service.consumeOpenAIImagesResponse(context.Background(), resp, parsed, sink)
	require.NoError(t, err)
	require.Equal(t, 1, result.ImageCount)
	require.Len(t, sink.Partials(), 1)
	require.Equal(t, []byte("preview"), sink.Partials()[0].Data)
	require.Equal(t, []byte("final"), sink.Results()[0].Data)
	require.Equal(t, 2, result.Usage.InputTokens)
}

func TestConsumeOpenAIImagesResponsesSSEDecodesCompletedResults(t *testing.T) {
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", N: 2, Stream: true}
	sink := NewCollectingImageResultSink()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000012,\"usage\":{\"input_tokens\":4,\"output_tokens\":5},\"output\":[{\"type\":\"image_generation_call\",\"result\":\"YQ==\",\"output_format\":\"png\"},{\"type\":\"image_generation_call\",\"result\":\"Yg==\",\"output_format\":\"png\"}]}}\n\n" +
				"data: [DONE]\n\n",
		)),
	}
	result, err := service.consumeOpenAIImagesResponsesSSE(context.Background(), resp, parsed, sink)
	require.NoError(t, err)
	require.Equal(t, 2, result.ImageCount)
	require.Equal(t, []byte("a"), sink.Results()[0].Data)
	require.Equal(t, []byte("b"), sink.Results()[1].Data)
	require.Equal(t, int64(1710000012), sink.Summary().CreatedAt)
}

func TestConsumeOpenAIImagesResponsesSSEDoesNotDuplicateOutputItemFallback(t *testing.T) {
	service := &OpenAIGatewayService{cfg: &config.Config{}}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", N: 2, Stream: true}
	sink := NewCollectingImageResultSink()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"img-a\",\"type\":\"image_generation_call\",\"result\":\"YQ==\",\"output_format\":\"png\"}}\n\n" +
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"img-b\",\"type\":\"image_generation_call\",\"result\":\"YQ==\",\"output_format\":\"png\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"YQ==\",\"output_format\":\"png\"},{\"type\":\"image_generation_call\",\"result\":\"YQ==\",\"output_format\":\"png\"}]}}\n\n",
		)),
	}

	result, err := service.consumeOpenAIImagesResponsesSSE(context.Background(), resp, parsed, sink)
	require.NoError(t, err)
	require.Equal(t, 2, result.ImageCount)
	require.Len(t, sink.Results(), 2)
	require.Equal(t, []byte("a"), sink.Results()[0].Data)
	require.Equal(t, []byte("a"), sink.Results()[1].Data)
}
