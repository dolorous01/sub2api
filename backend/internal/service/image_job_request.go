package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode"
)

func NormalizeImageJobRequest(
	ctx context.Context,
	store ImageJobObjectStore,
	objectSuffix string,
	parsed *OpenAIImagesRequest,
	scenes []string,
	maxInputs int,
) (ImageJobRequest, []ImageJobInput, string, error) {
	if store == nil {
		return ImageJobRequest{}, nil, "", fmt.Errorf("image job object store is required")
	}
	normalized, inputs, uploads, digest, err := prepareImageJobRequest(objectSuffix, parsed, scenes, maxInputs)
	if err != nil {
		return ImageJobRequest{}, nil, "", err
	}
	if err := storePreparedImageJobUploads(ctx, store, uploads); err != nil {
		return ImageJobRequest{}, nil, "", err
	}
	return normalized, inputs, digest, nil
}

type preparedImageJobUpload struct {
	objectKey   string
	contentType string
	data        []byte
}

func prepareImageJobRequest(
	objectSuffix string,
	parsed *OpenAIImagesRequest,
	scenes []string,
	maxInputs int,
) (ImageJobRequest, []ImageJobInput, []preparedImageJobUpload, string, error) {
	if parsed == nil {
		return ImageJobRequest{}, nil, nil, "", fmt.Errorf("parsed image request is required")
	}
	if err := validateImageJobObjectSuffix(objectSuffix); err != nil {
		return ImageJobRequest{}, nil, nil, "", err
	}
	if maxInputs <= 0 {
		return ImageJobRequest{}, nil, nil, "", fmt.Errorf("max image job inputs must be greater than 0")
	}
	if len(parsed.Uploads)+len(parsed.InputImageURLs) > maxInputs {
		return ImageJobRequest{}, nil, nil, "", fmt.Errorf("image request has %d inputs, maximum is %d", len(parsed.Uploads)+len(parsed.InputImageURLs), maxInputs)
	}

	normalized := ImageJobRequest{
		Endpoint:          parsed.Endpoint,
		Model:             parsed.Model,
		Prompt:            parsed.Prompt,
		N:                 parsed.N,
		Size:              parsed.Size,
		ResponseFormat:    parsed.ResponseFormat,
		Quality:           parsed.Quality,
		Background:        parsed.Background,
		OutputFormat:      parsed.OutputFormat,
		OutputCompression: cloneIntPointer(parsed.OutputCompression),
		Moderation:        parsed.Moderation,
		InputFidelity:     parsed.InputFidelity,
		Style:             parsed.Style,
		PartialImages:     cloneIntPointer(parsed.PartialImages),
		InputURLs:         append([]string(nil), parsed.InputImageURLs...),
		MaskURL:           parsed.MaskImageURL,
		Scenes:            append([]string(nil), scenes...),
	}

	prefix := "image-jobs/" + strings.Trim(objectSuffix, "/")
	inputs := make([]ImageJobInput, 0, len(parsed.Uploads)+1)
	uploads := make([]preparedImageJobUpload, 0, len(parsed.Uploads)+1)

	for index, upload := range parsed.Uploads {
		ref, input, prepared, err := prepareImageJobUpload(prefix, "image", index, upload)
		if err != nil {
			return ImageJobRequest{}, nil, nil, "", err
		}
		normalized.Inputs = append(normalized.Inputs, ref)
		inputs = append(inputs, input)
		uploads = append(uploads, prepared)
	}
	if parsed.MaskUpload != nil {
		ref, input, prepared, err := prepareImageJobUpload(prefix, "mask", 0, *parsed.MaskUpload)
		if err != nil {
			return ImageJobRequest{}, nil, nil, "", err
		}
		normalized.Mask = &ref
		inputs = append(inputs, input)
		uploads = append(uploads, prepared)
	}

	digest, err := digestImageJobRequest(normalized)
	if err != nil {
		return ImageJobRequest{}, nil, nil, "", err
	}
	return normalized, inputs, uploads, digest, nil
}

func prepareImageJobUpload(
	prefix string,
	kind string,
	index int,
	upload OpenAIImagesUpload,
) (ImageJobInputRef, ImageJobInput, preparedImageJobUpload, error) {
	if len(upload.Data) == 0 {
		return ImageJobInputRef{}, ImageJobInput{}, preparedImageJobUpload{}, fmt.Errorf("%s image input %d is empty", kind, index)
	}
	if len(upload.Data) > openAIImageMaxUploadPartSize {
		return ImageJobInputRef{}, ImageJobInput{}, preparedImageJobUpload{}, fmt.Errorf("%s image input %d exceeds maximum size", kind, index)
	}
	contentType, extension, err := normalizeImageJobMIMEType(upload.ContentType)
	if err != nil {
		return ImageJobInputRef{}, ImageJobInput{}, preparedImageJobUpload{}, fmt.Errorf("%s image input %d: %w", kind, index, err)
	}

	sum := sha256.Sum256(upload.Data)
	digest := hex.EncodeToString(sum[:])
	objectKey := ""
	if kind == "mask" {
		objectKey = fmt.Sprintf("%s/mask/%s.%s", prefix, digest, extension)
	} else {
		objectKey = fmt.Sprintf("%s/inputs/%d-%s.%s", prefix, index, digest, extension)
	}

	fieldName := strings.TrimSpace(upload.FieldName)
	if kind == "mask" {
		fieldName = "mask"
	} else if fieldName == "" {
		fieldName = "image"
	}
	ref := ImageJobInputRef{
		Kind:      kind,
		Index:     index,
		ObjectKey: objectKey,
		MIMEType:  contentType,
		ByteSize:  int64(len(upload.Data)),
		SHA256:    digest,
		FieldName: fieldName,
	}
	input := ImageJobInput{
		Index:     index,
		Kind:      kind,
		ObjectKey: objectKey,
		MIMEType:  contentType,
		ByteSize:  int64(len(upload.Data)),
		SHA256:    digest,
	}
	prepared := preparedImageJobUpload{objectKey: objectKey, contentType: contentType, data: upload.Data}
	return ref, input, prepared, nil
}

func storePreparedImageJobUploads(ctx context.Context, store ImageJobObjectStore, uploads []preparedImageJobUpload) error {
	if store == nil {
		return fmt.Errorf("image job object store is required")
	}
	writtenKeys := make([]string, 0, len(uploads))
	cleanup := func(cause error) error {
		cleanupErrors := []error{cause}
		for index := len(writtenKeys) - 1; index >= 0; index-- {
			if err := store.Delete(context.WithoutCancel(ctx), writtenKeys[index]); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("delete image job input %q: %w", writtenKeys[index], err))
			}
		}
		return errors.Join(cleanupErrors...)
	}

	for index, upload := range uploads {
		if err := store.Put(ctx, upload.objectKey, upload.data, upload.contentType); err != nil {
			return cleanup(fmt.Errorf("store image job input %d: %w", index, err))
		}
		writtenKeys = append(writtenKeys, upload.objectKey)
	}
	return nil
}

func validateImageJobObjectSuffix(suffix string) error {
	if suffix == "" {
		return fmt.Errorf("image job object suffix is required")
	}
	if strings.Contains(suffix, "\\") || strings.IndexByte(suffix, 0) >= 0 || path.IsAbs(suffix) {
		return fmt.Errorf("invalid image job object suffix")
	}
	suffix = strings.Trim(suffix, "/")
	for _, segment := range strings.Split(suffix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid image job object suffix")
		}
	}
	return nil
}

func normalizeImageJobMIMEType(value string) (string, string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil || mediaType == "" {
		return "", "", fmt.Errorf("valid image MIME type is required")
	}
	mediaType = strings.ToLower(mediaType)
	if !strings.HasPrefix(mediaType, "image/") {
		return "", "", fmt.Errorf("MIME type %q is not an image", mediaType)
	}

	extension := ""
	switch mediaType {
	case "image/jpeg":
		extension = "jpg"
	case "image/png":
		extension = "png"
	case "image/webp":
		extension = "webp"
	case "image/gif":
		extension = "gif"
	case "image/avif":
		extension = "avif"
	default:
		subtype := strings.TrimPrefix(mediaType, "image/")
		if plus := strings.IndexByte(subtype, '+'); plus >= 0 {
			subtype = subtype[:plus]
		}
		if subtype == "" || strings.IndexFunc(subtype, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) >= 0 {
			return "", "", fmt.Errorf("unsupported image MIME type %q", mediaType)
		}
		extension = subtype
	}
	return mediaType, extension, nil
}

func digestImageJobRequest(req ImageJobRequest) (string, error) {
	type digestInput struct {
		Kind     string `json:"kind"`
		Index    int    `json:"index"`
		MIMEType string `json:"mime_type"`
		ByteSize int64  `json:"byte_size"`
		SHA256   string `json:"sha256"`
	}
	type digestRequest struct {
		Endpoint          string        `json:"endpoint"`
		Model             string        `json:"model"`
		Prompt            string        `json:"prompt"`
		N                 int           `json:"n"`
		Size              string        `json:"size,omitempty"`
		ResponseFormat    string        `json:"response_format,omitempty"`
		Quality           string        `json:"quality,omitempty"`
		Background        string        `json:"background,omitempty"`
		OutputFormat      string        `json:"output_format,omitempty"`
		OutputCompression *int          `json:"output_compression,omitempty"`
		Moderation        string        `json:"moderation,omitempty"`
		InputFidelity     string        `json:"input_fidelity,omitempty"`
		Style             string        `json:"style,omitempty"`
		PartialImages     *int          `json:"partial_images,omitempty"`
		InputURLs         []string      `json:"input_urls,omitempty"`
		MaskURL           string        `json:"mask_url,omitempty"`
		Inputs            []digestInput `json:"inputs,omitempty"`
		Mask              *digestInput  `json:"mask,omitempty"`
		Scenes            []string      `json:"scenes,omitempty"`
	}

	canonical := digestRequest{
		Endpoint:          req.Endpoint,
		Model:             req.Model,
		Prompt:            req.Prompt,
		N:                 req.N,
		Size:              req.Size,
		ResponseFormat:    req.ResponseFormat,
		Quality:           req.Quality,
		Background:        req.Background,
		OutputFormat:      req.OutputFormat,
		OutputCompression: cloneIntPointer(req.OutputCompression),
		Moderation:        req.Moderation,
		InputFidelity:     req.InputFidelity,
		Style:             req.Style,
		PartialImages:     cloneIntPointer(req.PartialImages),
		InputURLs:         append([]string(nil), req.InputURLs...),
		MaskURL:           req.MaskURL,
		Scenes:            append([]string(nil), req.Scenes...),
	}
	for _, input := range req.Inputs {
		canonical.Inputs = append(canonical.Inputs, digestInput{
			Kind: input.Kind, Index: input.Index, MIMEType: input.MIMEType,
			ByteSize: input.ByteSize, SHA256: input.SHA256,
		})
	}
	if req.Mask != nil {
		canonical.Mask = &digestInput{
			Kind: req.Mask.Kind, Index: req.Mask.Index, MIMEType: req.Mask.MIMEType,
			ByteSize: req.Mask.ByteSize, SHA256: req.Mask.SHA256,
		}
	}

	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal image job request digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
