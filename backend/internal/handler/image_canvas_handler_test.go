package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBindImageCanvasProjectWriteRequestRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	body := io.MultiReader(
		strings.NewReader(`{"name":"large","document":"`),
		io.LimitReader(repeatedCanvasByte('a'), maxImageCanvasProjectWriteRequestBytes),
		strings.NewReader(`"}`),
	)
	context.Request = httptest.NewRequest(http.MethodPost, "/", body)
	context.Request.Header.Set("Content-Type", "application/json")

	var request imageCanvasProjectWriteRequest
	if err := bindImageCanvasProjectWriteRequest(context, &request); err == nil {
		t.Fatal("bindImageCanvasProjectWriteRequest() succeeded for an oversized body")
	}
}

type repeatedCanvasByte byte

func (value repeatedCanvasByte) Read(target []byte) (int, error) {
	for index := range target {
		target[index] = byte(value)
	}
	return len(target), nil
}
