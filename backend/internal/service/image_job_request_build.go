package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

func ParseOpenAIImagesRequestBody(endpoint, contentType string, body []byte) (*OpenAIImagesRequest, error) {
	return parseOpenAIImagesRequestBody(endpoint, contentType, body, true)
}

func parseOpenAIImagesRequestBody(endpoint, contentType string, body []byte, validateModel bool) (*OpenAIImagesRequest, error) {
	endpoint = normalizeOpenAIImagesEndpointPath(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("unsupported images endpoint")
	}

	contentType = strings.TrimSpace(contentType)
	req := &OpenAIImagesRequest{
		Endpoint:    endpoint,
		ContentType: contentType,
		N:           1,
		Body:        body,
	}
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		req.bodyHash = hex.EncodeToString(sum[:8])
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		req.Multipart = true
		if err := parseOpenAIImagesMultipartRequest(body, contentType, req); err != nil {
			return nil, err
		}
	} else {
		if len(body) == 0 {
			return nil, fmt.Errorf("request body is empty")
		}
		if !json.Valid(body) {
			return nil, fmt.Errorf("failed to parse request body")
		}
		if err := parseOpenAIImagesJSONRequest(body, req); err != nil {
			return nil, err
		}
	}

	applyOpenAIImagesDefaults(req)
	if validateModel {
		if err := validateOpenAIImagesModel(req.Model); err != nil {
			return nil, err
		}
	}
	req.Provider = imageProviderForModel(req.Model)
	req.SizeTier = normalizeOpenAIImageSizeTier(req.Size)
	req.RequiredCapability = classifyOpenAIImagesCapability(req)
	return req, nil
}

func BuildOpenAIImagesRequest(
	ctx context.Context,
	store ImageJobObjectStore,
	req ImageJobRequest,
) ([]byte, string, *OpenAIImagesRequest, error) {
	if store == nil {
		return nil, "", nil, fmt.Errorf("image job object store is required")
	}
	if req.N <= 0 {
		return nil, "", nil, fmt.Errorf("image job request n must be greater than 0")
	}
	req.Provider = normalizeImageProvider(req.Provider, req.Model)
	endpoint := normalizeOpenAIImagesEndpointPath(req.Endpoint)
	if endpoint == "" {
		return nil, "", nil, fmt.Errorf("unsupported images endpoint")
	}
	if req.Provider == ImageProviderOpenAI && endpoint == openAIImagesEditsEndpoint && strings.TrimSpace(req.InputFidelity) == "" {
		req.InputFidelity = "high"
	}
	if endpoint != openAIImagesEditsEndpoint && (len(req.Inputs) > 0 || req.Mask != nil || len(req.InputURLs) > 0 || req.MaskURL != "") {
		return nil, "", nil, fmt.Errorf("image inputs require the edits endpoint")
	}

	if len(req.Inputs) > 0 || req.Mask != nil {
		if len(req.InputURLs) > 0 || strings.TrimSpace(req.MaskURL) != "" {
			return nil, "", nil, fmt.Errorf("cannot combine stored uploads with URL image inputs")
		}
		body, contentType, err := buildImageJobMultipartRequest(ctx, store, req)
		if err != nil {
			return nil, "", nil, err
		}
		parsed, err := parseOpenAIImagesRequestBody(endpoint, contentType, body, false)
		if err != nil {
			return nil, "", nil, fmt.Errorf("parse rebuilt image request: %w", err)
		}
		applyImageJobProviderFields(parsed, req)
		return body, contentType, parsed, nil
	}

	body, err := buildImageJobJSONRequest(req)
	if err != nil {
		return nil, "", nil, err
	}
	const contentType = "application/json"
	parsed, err := parseOpenAIImagesRequestBody(endpoint, contentType, body, false)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse rebuilt image request: %w", err)
	}
	applyImageJobProviderFields(parsed, req)
	return body, contentType, parsed, nil
}

func applyImageJobProviderFields(parsed *OpenAIImagesRequest, req ImageJobRequest) {
	if parsed == nil {
		return
	}
	parsed.Provider = normalizeImageProvider(req.Provider, req.Model)
	if parsed.Provider == ImageProviderGrok {
		parsed.Stream = false
		parsed.SizeTier = normalizeOpenAIImageSizeTier(parsed.Resolution)
	}
}

func buildImageJobMultipartRequest(ctx context.Context, store ImageJobObjectStore, req ImageJobRequest) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	closeWithError := func(cause error) ([]byte, string, error) {
		_ = writer.Close()
		return nil, "", cause
	}

	if err := writeImageJobMultipartFields(writer, req); err != nil {
		return closeWithError(err)
	}
	for _, ref := range req.Inputs {
		fieldName := strings.TrimSpace(ref.FieldName)
		if fieldName == "" {
			fieldName = "image"
		}
		if fieldName != "image" && fieldName != "image[]" {
			return closeWithError(fmt.Errorf("invalid image input field name %q", fieldName))
		}
		data, contentType, extension, err := loadImageJobInput(ctx, store, ref)
		if err != nil {
			return closeWithError(err)
		}
		fileName := fmt.Sprintf("input-%d.%s", ref.Index, extension)
		if err := writeImageJobMultipartFile(writer, fieldName, fileName, contentType, data); err != nil {
			return closeWithError(err)
		}
	}
	if req.Mask != nil {
		data, contentType, extension, err := loadImageJobInput(ctx, store, *req.Mask)
		if err != nil {
			return closeWithError(err)
		}
		if err := writeImageJobMultipartFile(writer, "mask", "mask."+extension, contentType, data); err != nil {
			return closeWithError(err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize rebuilt image request: %w", err)
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func writeImageJobMultipartFields(writer *multipart.Writer, req ImageJobRequest) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "model", value: req.Model},
		{name: "prompt", value: req.Prompt},
		{name: "n", value: strconv.Itoa(req.N)},
		{name: "size", value: req.Size},
		{name: "aspect_ratio", value: req.AspectRatio},
		{name: "resolution", value: req.Resolution},
		{name: "response_format", value: req.ResponseFormat},
		{name: "quality", value: req.Quality},
		{name: "background", value: req.Background},
		{name: "output_format", value: req.OutputFormat},
		{name: "moderation", value: req.Moderation},
		{name: "input_fidelity", value: req.InputFidelity},
		{name: "style", value: req.Style},
	}
	if normalizeImageProvider(req.Provider, req.Model) == ImageProviderOpenAI {
		fields = append(fields, struct {
			name  string
			value string
		}{name: "stream", value: "true"})
	}
	if req.OutputCompression != nil {
		fields = append(fields, struct {
			name  string
			value string
		}{name: "output_compression", value: strconv.Itoa(*req.OutputCompression)})
	}
	if req.PartialImages != nil {
		fields = append(fields, struct {
			name  string
			value string
		}{name: "partial_images", value: strconv.Itoa(*req.PartialImages)})
	}
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if err := writer.WriteField(field.name, field.value); err != nil {
			return fmt.Errorf("write rebuilt image request field %s: %w", field.name, err)
		}
	}
	return nil
}

func writeImageJobMultipartFile(writer *multipart.Writer, fieldName, fileName, contentType string, data []byte) error {
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": fieldName, "filename": fileName})
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", disposition)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create rebuilt image request file %s: %w", fieldName, err)
	}
	if written, err := part.Write(data); err != nil {
		return fmt.Errorf("write rebuilt image request file %s: %w", fieldName, err)
	} else if written != len(data) {
		return fmt.Errorf("write rebuilt image request file %s: short write", fieldName)
	}
	return nil
}

func loadImageJobInput(ctx context.Context, store ImageJobObjectStore, ref ImageJobInputRef) ([]byte, string, string, error) {
	if strings.TrimSpace(ref.ObjectKey) == "" {
		return nil, "", "", fmt.Errorf("image job input object key is required")
	}
	object, err := store.Get(ctx, ref.ObjectKey)
	if err != nil {
		return nil, "", "", fmt.Errorf("load image job input %q: %w", ref.ObjectKey, err)
	}
	if object == nil {
		return nil, "", "", fmt.Errorf("load image job input %q: empty object", ref.ObjectKey)
	}
	if len(object.Data) == 0 || len(object.Data) > openAIImageMaxUploadPartSize {
		return nil, "", "", fmt.Errorf("image job input %q has invalid size", ref.ObjectKey)
	}
	if ref.ByteSize != int64(len(object.Data)) || object.Size != int64(len(object.Data)) {
		return nil, "", "", fmt.Errorf("image job input %q size does not match metadata", ref.ObjectKey)
	}
	contentType, extension, err := normalizeImageJobMIMEType(ref.MIMEType)
	if err != nil {
		return nil, "", "", fmt.Errorf("image job input %q: %w", ref.ObjectKey, err)
	}
	storedContentType, _, err := normalizeImageJobMIMEType(object.ContentType)
	if err != nil || storedContentType != contentType {
		return nil, "", "", fmt.Errorf("image job input %q MIME type does not match metadata", ref.ObjectKey)
	}
	sum := sha256.Sum256(object.Data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(ref.SHA256)) {
		return nil, "", "", fmt.Errorf("image job input %q digest does not match metadata", ref.ObjectKey)
	}
	return object.Data, contentType, extension, nil
}

func buildImageJobJSONRequest(req ImageJobRequest) ([]byte, error) {
	payload := make(map[string]any)
	payload["model"] = req.Model
	payload["prompt"] = req.Prompt
	payload["n"] = req.N
	if normalizeImageProvider(req.Provider, req.Model) == ImageProviderOpenAI {
		payload["stream"] = true
	}
	addString := func(name, value string) {
		if value != "" {
			payload[name] = value
		}
	}
	addString("size", req.Size)
	addString("aspect_ratio", req.AspectRatio)
	addString("resolution", req.Resolution)
	addString("response_format", req.ResponseFormat)
	addString("quality", req.Quality)
	addString("background", req.Background)
	addString("output_format", req.OutputFormat)
	addString("moderation", req.Moderation)
	addString("input_fidelity", req.InputFidelity)
	addString("style", req.Style)
	if req.OutputCompression != nil {
		payload["output_compression"] = *req.OutputCompression
	}
	if req.PartialImages != nil {
		payload["partial_images"] = *req.PartialImages
	}
	if len(req.InputURLs) > 0 {
		images := make([]map[string]string, 0, len(req.InputURLs))
		for _, imageURL := range req.InputURLs {
			if strings.TrimSpace(imageURL) == "" {
				return nil, fmt.Errorf("image job request contains an empty input URL")
			}
			images = append(images, map[string]string{"image_url": imageURL})
		}
		payload["images"] = images
	}
	if strings.TrimSpace(req.MaskURL) != "" {
		payload["mask"] = map[string]string{"image_url": req.MaskURL}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal rebuilt image request: %w", err)
	}
	return body, nil
}
