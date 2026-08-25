package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxImageCanvasDocumentBytes = 8 << 20
	maxImageCanvasNodes         = 2000
	maxImageCanvasEdges         = 4000
	maxImageCanvasAssetBytes    = 64 << 20
	maxAudioCanvasAssetBytes    = 128 << 20
	maxVideoCanvasAssetBytes    = 512 << 20
	maxImageCanvasAssetPixels   = 40_000_000
	maxCanvasMediaEdge          = 16_384
	maxCanvasMediaDurationMS    = 24 * 60 * 60 * 1000
	imageCanvasThumbnailSize    = 512
)

type ImageCanvasDocument struct {
	SchemaVersion int               `json:"schema_version"`
	Nodes         []json.RawMessage `json:"nodes"`
	Edges         []json.RawMessage `json:"edges"`
	Connections   []json.RawMessage `json:"connections"`
	Viewport      json.RawMessage   `json:"viewport,omitempty"`
}

type ImageAssetUpload struct {
	UserID          int64
	ProjectPublicID string
	FileName        string
	MIMEType        string
	Width           int
	Height          int
	DurationMS      int64
	Data            []byte
}

type ImageCanvasProjectService struct {
	repository ImageCanvasRepository
	store      ImageJobObjectStore
}

func NewImageCanvasProjectService(repository ImageCanvasRepository, store ImageJobObjectStore) *ImageCanvasProjectService {
	return &ImageCanvasProjectService{repository: repository, store: store}
}

func (s *ImageCanvasProjectService) List(ctx context.Context, userID int64) ([]ImageCanvasProject, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	projects, err := s.repository.ListProjects(ctx, userID)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		jobs, err := s.repository.ListOpenJobs(ctx, userID, projects[index].ID)
		if err != nil {
			return nil, err
		}
		projects[index].OpenJobs = jobs
	}
	return projects, nil
}

func (s *ImageCanvasProjectService) Create(ctx context.Context, userID int64, publicID, name string, document json.RawMessage) (*ImageCanvasProject, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	name, err := validateImageCanvasProjectName(name)
	if err != nil {
		return nil, err
	}
	refs, err := ValidateImageCanvasDocument(document)
	if err != nil {
		return nil, err
	}
	publicID, err = validateImageCanvasProjectPublicID(publicID)
	if err != nil {
		return nil, err
	}
	return s.repository.CreateProject(ctx, userID, publicID, name, document, refs)
}

func (s *ImageCanvasProjectService) Get(ctx context.Context, userID int64, publicID string) (*ImageCanvasProject, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	project, err := s.repository.GetProject(ctx, userID, strings.TrimSpace(publicID))
	if err != nil {
		return nil, err
	}
	jobs, err := s.repository.ListOpenJobs(ctx, userID, project.ID)
	if err != nil {
		return nil, err
	}
	project.OpenJobs = jobs
	return project, nil
}

func (s *ImageCanvasProjectService) Update(
	ctx context.Context,
	userID int64,
	publicID string,
	version int64,
	name string,
	document json.RawMessage,
) (*ImageCanvasProject, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	if version < 1 {
		return nil, fmt.Errorf("%w: project version must be positive", ErrImageCanvasDocumentInvalid)
	}
	name, err := validateImageCanvasProjectName(name)
	if err != nil {
		return nil, err
	}
	refs, err := ValidateImageCanvasDocument(document)
	if err != nil {
		return nil, err
	}
	return s.repository.UpdateProject(ctx, userID, strings.TrimSpace(publicID), version, name, document, refs)
}

func (s *ImageCanvasProjectService) Delete(ctx context.Context, userID int64, publicID string) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("image canvas project repository is required")
	}
	return s.repository.DeleteProject(ctx, userID, strings.TrimSpace(publicID))
}

func (s *ImageCanvasProjectService) UploadAsset(ctx context.Context, input ImageAssetUpload) (*ImageAsset, error) {
	if s == nil || s.repository == nil || s.store == nil {
		return nil, fmt.Errorf("image canvas asset dependencies are required")
	}
	if input.UserID <= 0 || len(input.Data) == 0 || len(input.Data) > maxVideoCanvasAssetBytes {
		return nil, fmt.Errorf("%w: upload must be between 1 byte and 512 MiB", ErrImageAssetInvalid)
	}
	mediaKind, mimeType, extension, err := detectImageCanvasAssetType(input.Data, input.MIMEType)
	if err != nil {
		return nil, err
	}
	maxBytes := maxImageCanvasAssetBytes
	switch mediaKind {
	case "audio":
		maxBytes = maxAudioCanvasAssetBytes
	case "video":
		maxBytes = maxVideoCanvasAssetBytes
	}
	if len(input.Data) > maxBytes {
		return nil, fmt.Errorf("%w: %s upload exceeds its byte limit", ErrImageAssetInvalid, mediaKind)
	}
	width, height, durationMS := input.Width, input.Height, input.DurationMS
	var imageConfig image.Config
	switch mediaKind {
	case "image":
		config, format, decodeErr := image.DecodeConfig(bytes.NewReader(input.Data))
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 {
			return nil, fmt.Errorf("%w: image cannot be decoded", ErrImageAssetInvalid)
		}
		if normalizeImageCanvasFormat(format) != extension {
			return nil, fmt.Errorf("%w: decoded format does not match file signature", ErrImageAssetInvalid)
		}
		if int64(config.Width)*int64(config.Height) > maxImageCanvasAssetPixels {
			return nil, fmt.Errorf("%w: image exceeds 40 megapixels", ErrImageAssetInvalid)
		}
		imageConfig, width, height, durationMS = config, config.Width, config.Height, 0
	case "video":
		if width <= 0 || height <= 0 || width > maxCanvasMediaEdge || height > maxCanvasMediaEdge {
			return nil, fmt.Errorf("%w: video dimensions are missing or invalid", ErrImageAssetInvalid)
		}
		if int64(width)*int64(height) > maxImageCanvasAssetPixels {
			return nil, fmt.Errorf("%w: video dimensions exceed the pixel limit", ErrImageAssetInvalid)
		}
		if durationMS <= 0 || durationMS > maxCanvasMediaDurationMS {
			return nil, fmt.Errorf("%w: video duration is invalid", ErrImageAssetInvalid)
		}
	default:
		width, height = 0, 0
		if durationMS <= 0 || durationMS > maxCanvasMediaDurationMS {
			return nil, fmt.Errorf("%w: audio duration is invalid", ErrImageAssetInvalid)
		}
	}

	var projectID *int64
	if strings.TrimSpace(input.ProjectPublicID) != "" {
		project, err := s.repository.GetProject(ctx, input.UserID, input.ProjectPublicID)
		if err != nil {
			return nil, err
		}
		projectID = &project.ID
	}
	publicID := newImageCanvasServicePublicID("asset")
	objectKey := fmt.Sprintf("canvas-assets/%d/%s/original.%s", input.UserID, publicID, extension)
	if err := s.store.Put(ctx, objectKey, input.Data, mimeType); err != nil {
		return nil, fmt.Errorf("store image canvas asset: %w", err)
	}

	thumbnailKey := ""
	if mediaKind == "image" {
		thumbnail, thumbnailErr := buildImageCanvasThumbnail(input.Data, imageConfig)
		if thumbnailErr == nil && len(thumbnail) > 0 {
			candidate := fmt.Sprintf("canvas-assets/%d/%s/thumbnail.jpg", input.UserID, publicID)
			if putErr := s.store.Put(ctx, candidate, thumbnail, "image/jpeg"); putErr == nil {
				thumbnailKey = candidate
			}
		}
	}
	digest := sha256.Sum256(input.Data)
	asset, err := s.repository.CreateAsset(ctx, ImageAssetCreate{
		PublicID: publicID, OwnerUserID: input.UserID, ProjectID: projectID,
		SourceType: "upload", MediaKind: mediaKind, FileName: sanitizeCanvasAssetFileName(input.FileName),
		ObjectKey: objectKey, ThumbnailObjectKey: thumbnailKey,
		MIMEType: mimeType, Width: width, Height: height, DurationMS: durationMS,
		ByteSize: int64(len(input.Data)), SHA256: hex.EncodeToString(digest[:]),
		ParentAssetIDs: json.RawMessage(`[]`),
	})
	if err == nil {
		return asset, nil
	}
	cleanupErr := s.store.Delete(context.WithoutCancel(ctx), objectKey)
	if thumbnailKey != "" {
		cleanupErr = errors.Join(cleanupErr, s.store.Delete(context.WithoutCancel(ctx), thumbnailKey))
	}
	if cleanupErr != nil {
		return nil, errors.Join(err, fmt.Errorf("clean up failed image canvas asset: %w", cleanupErr))
	}
	return nil, err
}

func (s *ImageCanvasProjectService) GetAsset(ctx context.Context, userID int64, publicID string) (*ImageAsset, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	return s.repository.GetAsset(ctx, userID, strings.TrimSpace(publicID))
}

func (s *ImageCanvasProjectService) GetAssetObject(ctx context.Context, userID int64, publicID string, thumbnail bool) (*ImageJobObject, error) {
	if s == nil || s.repository == nil || s.store == nil {
		return nil, fmt.Errorf("image canvas asset dependencies are required")
	}
	asset, err := s.repository.GetAsset(ctx, userID, strings.TrimSpace(publicID))
	if err != nil {
		return nil, err
	}
	key := asset.ObjectKey
	if thumbnail && asset.ThumbnailObjectKey != "" {
		key = asset.ThumbnailObjectKey
	}
	object, err := s.store.Get(ctx, key)
	if err != nil || object == nil {
		return nil, ErrImageAssetNotFound
	}
	if object.ContentType == "" {
		object.ContentType = asset.MIMEType
	}
	return object, nil
}

func ValidateImageCanvasDocument(raw json.RawMessage) ([]ImageCanvasAssetReference, error) {
	if len(raw) == 0 || len(raw) > maxImageCanvasDocumentBytes || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized JSON", ErrImageCanvasDocumentInvalid)
	}
	var document ImageCanvasDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageCanvasDocumentInvalid, err)
	}
	if document.SchemaVersion != 1 && document.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: unsupported schema version", ErrImageCanvasDocumentInvalid)
	}
	connections := document.Connections
	if document.SchemaVersion == 1 {
		connections = document.Edges
	}
	if document.Nodes == nil || connections == nil || len(document.Nodes) > maxImageCanvasNodes || len(connections) > maxImageCanvasEdges {
		return nil, fmt.Errorf("%w: invalid nodes or connections", ErrImageCanvasDocumentInvalid)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageCanvasDocumentInvalid, err)
	}
	if err := validateImageCanvasJSONValue("", value); err != nil {
		return nil, err
	}
	if len(document.Viewport) > 0 && string(document.Viewport) != "null" {
		var viewport struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			K float64 `json:"k"`
		}
		if err := json.Unmarshal(document.Viewport, &viewport); err != nil ||
			math.IsInf(viewport.X, 0) || math.IsInf(viewport.Y, 0) || math.IsInf(viewport.K, 0) ||
			math.IsNaN(viewport.X) || math.IsNaN(viewport.Y) || math.IsNaN(viewport.K) ||
			math.Abs(viewport.X) > 1e9 || math.Abs(viewport.Y) > 1e9 || viewport.K <= 0 || viewport.K > 100 {
			return nil, fmt.Errorf("%w: invalid viewport", ErrImageCanvasDocumentInvalid)
		}
	}

	refs := make([]ImageCanvasAssetReference, 0)
	seenNodes := make(map[string]struct{}, len(document.Nodes))
	seenRefs := make(map[string]struct{})
	for _, rawNode := range document.Nodes {
		var node map[string]any
		if err := json.Unmarshal(rawNode, &node); err != nil {
			return nil, fmt.Errorf("%w: invalid node", ErrImageCanvasDocumentInvalid)
		}
		nodeID, _ := node["id"].(string)
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || len(nodeID) > 128 {
			return nil, fmt.Errorf("%w: every node requires a valid id", ErrImageCanvasDocumentInvalid)
		}
		if _, duplicate := seenNodes[nodeID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate node id %q", ErrImageCanvasDocumentInvalid, nodeID)
		}
		seenNodes[nodeID] = struct{}{}
		assetIDs := make([]string, 0)
		collectImageCanvasAssetIDs(node, &assetIDs)
		for _, assetID := range assetIDs {
			assetID = strings.TrimSpace(assetID)
			if assetID == "" || len(assetID) > 64 {
				return nil, fmt.Errorf("%w: invalid asset id", ErrImageCanvasDocumentInvalid)
			}
			key := assetID + "\x00" + nodeID
			if _, duplicate := seenRefs[key]; duplicate {
				continue
			}
			seenRefs[key] = struct{}{}
			refs = append(refs, ImageCanvasAssetReference{AssetPublicID: assetID, NodeID: nodeID})
		}
	}
	seenConnections := make(map[string]struct{}, len(connections))
	for _, rawConnection := range connections {
		var connection map[string]any
		if err := json.Unmarshal(rawConnection, &connection); err != nil {
			return nil, fmt.Errorf("%w: invalid connection", ErrImageCanvasDocumentInvalid)
		}
		connectionID := imageCanvasStringField(connection, "id")
		fromNodeID := imageCanvasStringField(connection, "fromNodeId", "source")
		toNodeID := imageCanvasStringField(connection, "toNodeId", "target")
		if connectionID == "" || len(connectionID) > 128 || fromNodeID == "" || toNodeID == "" {
			return nil, fmt.Errorf("%w: every connection requires valid ids", ErrImageCanvasDocumentInvalid)
		}
		if _, duplicate := seenConnections[connectionID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate connection id %q", ErrImageCanvasDocumentInvalid, connectionID)
		}
		if _, exists := seenNodes[fromNodeID]; !exists {
			return nil, fmt.Errorf("%w: connection source does not exist", ErrImageCanvasDocumentInvalid)
		}
		if _, exists := seenNodes[toNodeID]; !exists {
			return nil, fmt.Errorf("%w: connection target does not exist", ErrImageCanvasDocumentInvalid)
		}
		seenConnections[connectionID] = struct{}{}
	}
	return refs, nil
}

func imageCanvasStringField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result, ok := value[key].(string); ok {
			if result = strings.TrimSpace(result); result != "" {
				return result
			}
		}
	}
	return ""
}

func validateImageCanvasJSONValue(key string, value any) error {
	normalizedKey := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	if isSensitiveImageCanvasKey(normalizedKey) {
		return fmt.Errorf("%w: sensitive field %q is not allowed", ErrImageCanvasDocumentInvalid, key)
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if err := validateImageCanvasJSONValue(childKey, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateImageCanvasJSONValue(key, child); err != nil {
				return err
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "blob:") {
			return fmt.Errorf("%w: embedded or executable URL is not allowed", ErrImageCanvasDocumentInvalid)
		}
		if parsed, err := url.Parse(trimmed); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return fmt.Errorf("%w: external media URL is not allowed", ErrImageCanvasDocumentInvalid)
		}
	}
	return nil
}

func isSensitiveImageCanvasKey(normalizedKey string) bool {
	switch normalizedKey {
	case "apikey", "apikeyvalue", "baseurl", "providerurl", "token", "accesstoken", "refreshtoken",
		"authorization", "password", "secret", "clientsecret", "credential", "credentials",
		"objectkey", "thumbnailobjectkey":
		return true
	}
	return strings.HasSuffix(normalizedKey, "apikey") ||
		strings.HasSuffix(normalizedKey, "apikeyvalue") ||
		strings.HasSuffix(normalizedKey, "baseurl") ||
		strings.HasSuffix(normalizedKey, "providerurl") ||
		strings.HasSuffix(normalizedKey, "token") ||
		strings.HasSuffix(normalizedKey, "accesstoken") ||
		strings.HasSuffix(normalizedKey, "refreshtoken") ||
		strings.HasSuffix(normalizedKey, "password") ||
		strings.HasSuffix(normalizedKey, "secret") ||
		strings.HasSuffix(normalizedKey, "credential") ||
		strings.HasSuffix(normalizedKey, "credentials") ||
		strings.Contains(normalizedKey, "authorization") ||
		strings.HasSuffix(normalizedKey, "objectkey")
}

func collectImageCanvasAssetIDs(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			if normalized == "assetid" || normalized == "assetpublicid" {
				if id, ok := child.(string); ok {
					*result = append(*result, id)
				}
				continue
			}
			if normalized == "storagekey" {
				if storageKey, ok := child.(string); ok && strings.HasPrefix(storageKey, "asset:") {
					*result = append(*result, strings.TrimSpace(strings.TrimPrefix(storageKey, "asset:")))
				}
				continue
			}
			collectImageCanvasAssetIDs(child, result)
		}
	case []any:
		for _, child := range typed {
			collectImageCanvasAssetIDs(child, result)
		}
	}
}

func validateImageCanvasProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 160 {
		return "", fmt.Errorf("%w: project name must be between 1 and 160 characters", ErrImageCanvasDocumentInvalid)
	}
	return name, nil
}

var imageCanvasProjectPublicIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateImageCanvasProjectPublicID(publicID string) (string, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return "", nil
	}
	if len(publicID) < 8 || len(publicID) > 64 || !imageCanvasProjectPublicIDPattern.MatchString(publicID) {
		return "", fmt.Errorf("%w: project id must be 8-64 URL-safe characters", ErrImageCanvasDocumentInvalid)
	}
	return publicID, nil
}

func detectImageCanvasAssetType(data []byte, claimedMIME string) (mediaKind, mimeType, extension string, err error) {
	claimed := normalizeImageCanvasMIMEType(claimedMIME)
	rawClaimed := strings.TrimSpace(claimedMIME)
	if rawClaimed != "" && claimed == "" && !strings.EqualFold(rawClaimed, "application/octet-stream") {
		return "", "", "", fmt.Errorf("%w: declared MIME type is invalid", ErrImageAssetInvalid)
	}
	result := func(kind, detectedMIME, detectedExtension string) (string, string, string, error) {
		if claimed != "" && claimed != detectedMIME {
			return "", "", "", fmt.Errorf("%w: declared MIME type does not match file signature", ErrImageAssetInvalid)
		}
		return kind, detectedMIME, detectedExtension, nil
	}
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return result("image", "image/png", "png")
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return result("image", "image/jpeg", "jpg")
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return result("image", "image/webp", "webp")
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return result("audio", "audio/wav", "wav")
	case len(data) >= 4 && string(data[:4]) == "OggS":
		return result("audio", "audio/ogg", "ogg")
	case len(data) >= 3 && string(data[:3]) == "ID3":
		return result("audio", "audio/mpeg", "mp3")
	case len(data) >= 2 && data[0] == 0xff && (data[1]&0xf6) == 0xf0:
		return result("audio", "audio/aac", "aac")
	case len(data) >= 2 && data[0] == 0xff && (data[1]&0xe0) == 0xe0:
		return result("audio", "audio/mpeg", "mp3")
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		if strings.HasPrefix(claimed, "audio/") || string(data[8:11]) == "M4A" {
			return result("audio", "audio/mp4", "m4a")
		}
		return result("video", "video/mp4", "mp4")
	case len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		if claimed == "audio/webm" {
			return result("audio", "audio/webm", "webm")
		}
		return result("video", "video/webm", "webm")
	default:
		return "", "", "", fmt.Errorf("%w: unsupported image, video, or audio signature", ErrImageAssetInvalid)
	}
}

func normalizeImageCanvasMIMEType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "application/octet-stream" {
		return ""
	}
	if mediaType == "image/jpg" {
		return "image/jpeg"
	}
	switch mediaType {
	case "audio/mp3":
		return "audio/mpeg"
	case "audio/x-wav", "audio/wave":
		return "audio/wav"
	case "video/x-m4v":
		return "video/mp4"
	}
	return mediaType
}

func sanitizeCanvasAssetFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if utf8.RuneCountInString(name) <= 255 {
		return name
	}
	return string([]rune(name)[:255])
}

func normalizeImageCanvasFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg":
		return "jpg"
	case "png", "webp":
		return strings.ToLower(strings.TrimSpace(format))
	default:
		return ""
	}
}

func buildImageCanvasThumbnail(data []byte, config image.Config) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	width, height := config.Width, config.Height
	if width > imageCanvasThumbnailSize || height > imageCanvasThumbnailSize {
		scale := float64(imageCanvasThumbnailSize) / float64(max(width, height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(target, target.Bounds(), source, source.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, target, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func newImageCanvasServicePublicID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

type ImageAssetStreamUpload struct {
	UserID          int64
	ProjectPublicID string
	FileName        string
	MIMEType        string
	Width           int
	Height          int
	DurationMS      int64
	ByteSize        int64
	Reader          io.Reader
	SourceType      string
	ParentAssetIDs  json.RawMessage
}

type ImageAssetObjectStream struct {
	Reader      io.ReadCloser
	ContentType string
	Size        int64
}

// UploadAssetStream validates media from a disk-backed staging file and then
// streams it into object storage. This keeps 512 MiB video uploads bounded by
// disk space instead of process memory.
func (s *ImageCanvasProjectService) UploadAssetStream(ctx context.Context, input ImageAssetStreamUpload) (*ImageAsset, error) {
	if s == nil || s.repository == nil || s.store == nil {
		return nil, fmt.Errorf("image canvas asset dependencies are required")
	}
	if input.UserID <= 0 || input.Reader == nil || input.ByteSize <= 0 || input.ByteSize > maxVideoCanvasAssetBytes {
		return nil, fmt.Errorf("%w: upload must be between 1 byte and 512 MiB", ErrImageAssetInvalid)
	}
	staging, err := os.CreateTemp("", "sub2api-canvas-media-*")
	if err != nil {
		return nil, fmt.Errorf("create canvas media staging file: %w", err)
	}
	stagingPath := staging.Name()
	defer func() {
		_ = staging.Close()
		_ = os.Remove(stagingPath)
	}()
	if err := staging.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("protect canvas media staging file: %w", err)
	}
	hasher := sha256.New()
	reader := &canvasContextReader{ctx: ctx, reader: input.Reader}
	written, copyErr := io.Copy(io.MultiWriter(staging, hasher), io.LimitReader(reader, input.ByteSize+1))
	if copyErr != nil || written != input.ByteSize {
		return nil, errors.Join(fmt.Errorf("%w: upload size does not match request metadata", ErrImageAssetInvalid), copyErr)
	}
	if _, err := staging.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind canvas media staging file: %w", err)
	}
	header := make([]byte, 512)
	headerSize, readErr := io.ReadFull(staging, header)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("%w: read media signature", ErrImageAssetInvalid)
	}
	header = header[:headerSize]
	mediaKind, mimeType, extension, err := detectImageCanvasAssetType(header, input.MIMEType)
	if err != nil {
		return nil, err
	}
	maxBytes := int64(maxImageCanvasAssetBytes)
	switch mediaKind {
	case "audio":
		maxBytes = int64(maxAudioCanvasAssetBytes)
	case "video":
		maxBytes = int64(maxVideoCanvasAssetBytes)
	}
	if input.ByteSize > maxBytes {
		return nil, fmt.Errorf("%w: %s upload exceeds its byte limit", ErrImageAssetInvalid, mediaKind)
	}

	width, height, durationMS := input.Width, input.Height, input.DurationMS
	var thumbnail []byte
	switch mediaKind {
	case "image":
		if _, err := staging.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		config, format, decodeErr := image.DecodeConfig(staging)
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 {
			return nil, fmt.Errorf("%w: image cannot be decoded", ErrImageAssetInvalid)
		}
		if normalizeImageCanvasFormat(format) != extension {
			return nil, fmt.Errorf("%w: decoded format does not match file signature", ErrImageAssetInvalid)
		}
		pixels := int64(config.Width) * int64(config.Height)
		if pixels > maxImageCanvasAssetPixels {
			return nil, fmt.Errorf("%w: image exceeds 40 megapixels", ErrImageAssetInvalid)
		}
		width, height, durationMS = config.Width, config.Height, 0
		if pixels <= 12_000_000 {
			if _, err := staging.Seek(0, io.SeekStart); err == nil {
				if source, _, decodeErr := image.Decode(staging); decodeErr == nil {
					thumbnail, _ = buildImageCanvasThumbnailFromImage(source, config)
				}
			}
		}
	case "video":
		if width <= 0 || height <= 0 || width > maxCanvasMediaEdge || height > maxCanvasMediaEdge ||
			int64(width)*int64(height) > maxImageCanvasAssetPixels {
			return nil, fmt.Errorf("%w: video dimensions are missing or invalid", ErrImageAssetInvalid)
		}
		if durationMS <= 0 || durationMS > maxCanvasMediaDurationMS {
			return nil, fmt.Errorf("%w: video duration is invalid", ErrImageAssetInvalid)
		}
	default:
		width, height = 0, 0
		if durationMS <= 0 || durationMS > maxCanvasMediaDurationMS {
			return nil, fmt.Errorf("%w: audio duration is invalid", ErrImageAssetInvalid)
		}
	}

	var projectID *int64
	if strings.TrimSpace(input.ProjectPublicID) != "" {
		project, err := s.repository.GetProject(ctx, input.UserID, input.ProjectPublicID)
		if err != nil {
			return nil, err
		}
		projectID = &project.ID
	}
	sourceType := strings.ToLower(strings.TrimSpace(input.SourceType))
	if sourceType == "" {
		sourceType = "upload"
	}
	if sourceType != "upload" && sourceType != "generated" && sourceType != "derived" {
		return nil, fmt.Errorf("%w: invalid asset source type", ErrImageAssetInvalid)
	}
	publicID := newImageCanvasServicePublicID("asset")
	objectKey := fmt.Sprintf("canvas-assets/%d/%s/original.%s", input.UserID, publicID, extension)
	if _, err := staging.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := putCanvasAssetObject(ctx, s.store, objectKey, staging, input.ByteSize, mimeType); err != nil {
		return nil, fmt.Errorf("store image canvas asset: %w", err)
	}
	thumbnailKey := ""
	if len(thumbnail) > 0 {
		candidate := fmt.Sprintf("canvas-assets/%d/%s/thumbnail.jpg", input.UserID, publicID)
		if putErr := s.store.Put(ctx, candidate, thumbnail, "image/jpeg"); putErr == nil {
			thumbnailKey = candidate
		}
	}
	parents := input.ParentAssetIDs
	if len(parents) == 0 {
		parents = json.RawMessage("[]")
	}
	asset, err := s.repository.CreateAsset(ctx, ImageAssetCreate{
		PublicID: publicID, OwnerUserID: input.UserID, ProjectID: projectID,
		SourceType: sourceType, MediaKind: mediaKind, FileName: sanitizeCanvasAssetFileName(input.FileName),
		ObjectKey: objectKey, ThumbnailObjectKey: thumbnailKey,
		MIMEType: mimeType, Width: width, Height: height, DurationMS: durationMS,
		ByteSize: input.ByteSize, SHA256: hex.EncodeToString(hasher.Sum(nil)),
		ParentAssetIDs: parents,
	})
	if err == nil {
		return asset, nil
	}
	cleanupErr := s.store.Delete(context.WithoutCancel(ctx), objectKey)
	if thumbnailKey != "" {
		cleanupErr = errors.Join(cleanupErr, s.store.Delete(context.WithoutCancel(ctx), thumbnailKey))
	}
	if cleanupErr != nil {
		return nil, errors.Join(err, fmt.Errorf("clean up failed image canvas asset: %w", cleanupErr))
	}
	return nil, err
}

func (s *ImageCanvasProjectService) OpenAssetObject(ctx context.Context, userID int64, publicID string, thumbnail bool) (*ImageAssetObjectStream, error) {
	if s == nil || s.repository == nil || s.store == nil {
		return nil, fmt.Errorf("image canvas asset dependencies are required")
	}
	asset, err := s.repository.GetAsset(ctx, userID, strings.TrimSpace(publicID))
	if err != nil {
		return nil, err
	}
	key := asset.ObjectKey
	expectedSize := asset.ByteSize
	if thumbnail && asset.ThumbnailObjectKey != "" {
		key = asset.ThumbnailObjectKey
		expectedSize = 0
	}
	if streaming, ok := s.store.(ImageJobStreamingObjectStore); ok {
		reader, contentType, size, err := streaming.Open(ctx, key)
		if err != nil {
			return nil, ErrImageAssetNotFound
		}
		if expectedSize > 0 && size != expectedSize {
			_ = reader.Close()
			return nil, ErrImageAssetNotFound
		}
		if strings.TrimSpace(contentType) == "" {
			contentType = asset.MIMEType
		}
		return &ImageAssetObjectStream{Reader: reader, ContentType: contentType, Size: size}, nil
	}
	object, err := s.store.Get(ctx, key)
	if err != nil || object == nil {
		return nil, ErrImageAssetNotFound
	}
	return &ImageAssetObjectStream{
		Reader: io.NopCloser(bytes.NewReader(object.Data)), ContentType: firstNonEmptyString(object.ContentType, asset.MIMEType),
		Size: int64(len(object.Data)),
	}, nil
}

func putCanvasAssetObject(ctx context.Context, store ImageJobObjectStore, key string, reader io.Reader, size int64, contentType string) error {
	if streaming, ok := store.(ImageJobStreamingObjectStore); ok {
		return streaming.PutReader(ctx, key, reader, size, contentType)
	}
	if size > int64(maxImageCanvasAssetBytes) {
		return fmt.Errorf("object store does not support large canvas media")
	}
	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("canvas media object size mismatch")
	}
	return store.Put(ctx, key, data, contentType)
}

func buildImageCanvasThumbnailFromImage(source image.Image, config image.Config) ([]byte, error) {
	width, height := config.Width, config.Height
	if width > imageCanvasThumbnailSize || height > imageCanvasThumbnailSize {
		scale := float64(imageCanvasThumbnailSize) / float64(max(width, height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(target, target.Bounds(), source, source.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, target, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type canvasContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *canvasContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
