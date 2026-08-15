package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectingImageResultSinkReceivesFourFinalImages(t *testing.T) {
	sink := NewCollectingImageResultSink()
	for i := 0; i < 4; i++ {
		require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: i, Data: []byte{byte(i)}, MIMEType: "image/png"}))
	}
	require.NoError(t, sink.Complete(context.Background(), ImageExecutionSummary{CreatedAt: 123}))
	results := sink.Results()
	require.Len(t, results, 4)
	require.Equal(t, []byte{0}, results[0].Data)
}

func TestCollectingImageResultSinkDoesNotCountPartials(t *testing.T) {
	sink := NewCollectingImageResultSink()
	require.NoError(t, sink.Partial(context.Background(), 0, []byte("preview"), "image/png"))
	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("final"), MIMEType: "image/png"}))
	require.Len(t, sink.Partials(), 1)
	require.Len(t, sink.Results(), 1)
}

func TestHTTPImageResultSinkPreservesOpenAIJSON(t *testing.T) {
	w := httptest.NewRecorder()
	sink := NewHTTPImageResultSink(w, ImageSinkOptions{ResponseFormat: "b64_json"})
	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("png"), MIMEType: "image/png"}))
	require.NoError(t, sink.Complete(context.Background(), ImageExecutionSummary{CreatedAt: 123}))
	require.JSONEq(t, `{"created":123,"data":[{"b64_json":"cG5n"}]}`, w.Body.String())
}

func TestHTTPImageResultSinkEmitsSSEFinalEvent(t *testing.T) {
	w := httptest.NewRecorder()
	sink := NewHTTPImageResultSink(w, ImageSinkOptions{Stream: true, ResponseFormat: "b64_json", EventPrefix: "image_generation"})
	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("png"), MIMEType: "image/png"}))
	require.NoError(t, sink.Complete(context.Background(), ImageExecutionSummary{}))
	require.Contains(t, w.Body.String(), "event: image_generation.completed")
	require.Contains(t, w.Body.String(), `"b64_json":"cG5n"`)
}

func TestHTTPImageResultSinkURLFormatUsesDataURL(t *testing.T) {
	w := httptest.NewRecorder()
	sink := NewHTTPImageResultSink(w, ImageSinkOptions{ResponseFormat: "url"})
	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Data: []byte("png"), MIMEType: "image/png"}))
	require.NoError(t, sink.Complete(context.Background(), ImageExecutionSummary{}))
	var body struct {
		Data []map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("png")), body.Data[0]["url"])
}
