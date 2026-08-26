package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	maxImageEditorDocumentBytes   = 64 << 10
	maxImageEditorParametersBytes = 32 << 10
	maxImageEditorReferences      = 256
	maxImageEditorCanvasEdge      = 16_384
)

type imageEditorAssetService interface {
	GetAsset(context.Context, int64, string) (*ImageAsset, error)
	UploadAssetStream(context.Context, ImageAssetStreamUpload) (*ImageAsset, error)
}

type ImageEditorService struct {
	repository ImageEditorRepository
	assets     imageEditorAssetService
}

func NewImageEditorService(repository ImageEditorRepository, assets *ImageCanvasProjectService) *ImageEditorService {
	return &ImageEditorService{repository: repository, assets: assets}
}

type ImageEditorDerivedAssetUpload struct {
	UserID              int64
	DocumentPublicID    string
	ParentAssetPublicID string
	FileName            string
	MIMEType            string
	ByteSize            int64
	Reader              io.Reader
}

type imageEditorDocumentV1 struct {
	SchemaVersion      int                 `json:"schema_version"`
	Viewport           imageEditorViewport `json:"viewport"`
	Canvas             imageEditorCanvas   `json:"canvas"`
	SelectedRevisionID string              `json:"selected_revision_id,omitempty"`
}

type imageEditorViewport struct {
	Zoom float64 `json:"zoom"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

type imageEditorCanvas struct {
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Background string `json:"background"`
}

func (s *ImageEditorService) CreateOrGet(
	ctx context.Context,
	userID int64,
	projectPublicID, nodeID, baseAssetPublicID string,
	document json.RawMessage,
	assetReferences []ImageEditorAssetReference,
) (*ImageEditorDocument, bool, error) {
	if s == nil || s.repository == nil {
		return nil, false, fmt.Errorf("image editor repository is required")
	}
	projectPublicID = strings.TrimSpace(projectPublicID)
	nodeID = strings.TrimSpace(nodeID)
	baseAssetPublicID = strings.TrimSpace(baseAssetPublicID)
	if userID <= 0 || !validImageEditorPublicID(projectPublicID) ||
		!validImageEditorPublicID(baseAssetPublicID) || nodeID == "" ||
		utf8.RuneCountInString(nodeID) > 128 {
		return nil, false, fmt.Errorf("%w: invalid project, node, or base asset", ErrImageEditorDocumentInvalid)
	}
	if _, err := ValidateImageEditorDocument(document); err != nil {
		return nil, false, err
	}
	references, err := validateImageEditorAssetReferences(assetReferences)
	if err != nil {
		return nil, false, err
	}
	return s.repository.CreateOrGet(ctx, ImageEditorDocumentCreate{
		UserID: userID, ProjectPublicID: projectPublicID, NodeID: nodeID,
		BaseAssetPublicID: baseAssetPublicID, Document: document, AssetReferences: references,
	})
}

func (s *ImageEditorService) Get(ctx context.Context, userID int64, publicID string) (*ImageEditorDocument, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image editor repository is required")
	}
	publicID = strings.TrimSpace(publicID)
	if userID <= 0 || !validImageEditorPublicID(publicID) {
		return nil, ErrImageEditorDocumentNotFound
	}
	return s.repository.GetOwned(ctx, userID, publicID)
}

func (s *ImageEditorService) Update(ctx context.Context, input ImageEditorDocumentUpdate) (*ImageEditorDocument, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image editor repository is required")
	}
	input.PublicID = strings.TrimSpace(input.PublicID)
	input.CurrentAssetPublicID = strings.TrimSpace(input.CurrentAssetPublicID)
	input.Operation = strings.ToLower(strings.TrimSpace(input.Operation))
	if input.UserID <= 0 || !validImageEditorPublicID(input.PublicID) || input.Version < 1 {
		return nil, fmt.Errorf("%w: invalid document id or version", ErrImageEditorDocumentInvalid)
	}
	if _, err := ValidateImageEditorDocument(input.Document); err != nil {
		return nil, err
	}
	references, err := validateImageEditorAssetReferences(input.AssetReferences)
	if err != nil {
		return nil, err
	}
	input.AssetReferences = references
	hasAsset := input.CurrentAssetPublicID != ""
	hasOperation := input.Operation != ""
	if hasAsset != hasOperation {
		return nil, fmt.Errorf("%w: current asset and operation must be provided together", ErrImageEditorDocumentInvalid)
	}
	if hasAsset {
		if !validImageEditorPublicID(input.CurrentAssetPublicID) || !validImageEditorOperation(input.Operation) {
			return nil, fmt.Errorf("%w: invalid current asset or operation", ErrImageEditorDocumentInvalid)
		}
		parameters, validateErr := validateImageEditorParameters(input.Parameters)
		if validateErr != nil {
			return nil, validateErr
		}
		input.Parameters = parameters
	} else {
		input.Parameters = nil
	}
	return s.repository.Update(ctx, input)
}

func (s *ImageEditorService) UploadDerivedAssetStream(
	ctx context.Context,
	input ImageEditorDerivedAssetUpload,
) (*ImageAsset, error) {
	if s == nil || s.repository == nil || s.assets == nil {
		return nil, fmt.Errorf("image editor asset dependencies are required")
	}
	input.DocumentPublicID = strings.TrimSpace(input.DocumentPublicID)
	input.ParentAssetPublicID = strings.TrimSpace(input.ParentAssetPublicID)
	if input.UserID <= 0 || !validImageEditorPublicID(input.DocumentPublicID) ||
		!validImageEditorPublicID(input.ParentAssetPublicID) {
		return nil, ErrImageEditorDocumentNotFound
	}
	document, err := s.repository.GetOwned(ctx, input.UserID, input.DocumentPublicID)
	if err != nil {
		return nil, err
	}
	parent, err := s.assets.GetAsset(ctx, input.UserID, input.ParentAssetPublicID)
	if err != nil {
		return nil, err
	}
	if parent.MediaKind != "image" || !imageEditorDocumentContainsAsset(document, parent.PublicID) {
		return nil, ErrImageAssetNotFound
	}
	parentIDs, err := json.Marshal([]string{parent.PublicID})
	if err != nil {
		return nil, fmt.Errorf("marshal image editor lineage: %w", err)
	}
	return s.assets.UploadAssetStream(ctx, ImageAssetStreamUpload{
		UserID: input.UserID, ProjectPublicID: document.ProjectPublicID,
		FileName: input.FileName, MIMEType: input.MIMEType, ByteSize: input.ByteSize,
		Reader: input.Reader, SourceType: "derived", ParentAssetIDs: parentIDs,
	})
}

func ValidateImageEditorDocument(raw json.RawMessage) (*imageEditorDocumentV1, error) {
	if len(raw) == 0 || len(raw) > maxImageEditorDocumentBytes || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized JSON", ErrImageEditorDocumentInvalid)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrImageEditorDocumentInvalid)
	}
	if err := validateImageCanvasJSONValue("", value); err != nil {
		return nil, fmt.Errorf("%w: embedded URLs and sensitive fields are not allowed", ErrImageEditorDocumentInvalid)
	}
	var document imageEditorDocumentV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageEditorDocumentInvalid, err)
	}
	if document.SchemaVersion != 1 {
		return nil, fmt.Errorf("%w: unsupported schema version", ErrImageEditorDocumentInvalid)
	}
	if !finiteImageEditorNumber(document.Viewport.Zoom) ||
		!finiteImageEditorNumber(document.Viewport.X) ||
		!finiteImageEditorNumber(document.Viewport.Y) ||
		document.Viewport.Zoom <= 0 || document.Viewport.Zoom > 100 ||
		math.Abs(document.Viewport.X) > 1e9 || math.Abs(document.Viewport.Y) > 1e9 {
		return nil, fmt.Errorf("%w: invalid viewport", ErrImageEditorDocumentInvalid)
	}
	if document.Canvas.Width <= 0 || document.Canvas.Height <= 0 ||
		document.Canvas.Width > maxImageEditorCanvasEdge || document.Canvas.Height > maxImageEditorCanvasEdge ||
		int64(document.Canvas.Width)*int64(document.Canvas.Height) > maxImageCanvasAssetPixels {
		return nil, fmt.Errorf("%w: invalid canvas dimensions", ErrImageEditorDocumentInvalid)
	}
	switch document.Canvas.Background {
	case "transparent", "white", "black":
	default:
		return nil, fmt.Errorf("%w: invalid canvas background", ErrImageEditorDocumentInvalid)
	}
	if document.SelectedRevisionID != "" && !validImageEditorPublicID(document.SelectedRevisionID) {
		return nil, fmt.Errorf("%w: invalid selected revision", ErrImageEditorDocumentInvalid)
	}
	return &document, nil
}

func validateImageEditorAssetReferences(input []ImageEditorAssetReference) ([]ImageEditorAssetReference, error) {
	if input == nil {
		return nil, nil
	}
	if len(input) > maxImageEditorReferences {
		return nil, fmt.Errorf("%w: too many asset references", ErrImageEditorDocumentInvalid)
	}
	result := make([]ImageEditorAssetReference, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, reference := range input {
		reference.AssetPublicID = strings.TrimSpace(reference.AssetPublicID)
		reference.Role = strings.ToLower(strings.TrimSpace(reference.Role))
		reference.ElementID = strings.TrimSpace(reference.ElementID)
		if !validImageEditorPublicID(reference.AssetPublicID) ||
			!validImageEditorAssetRole(reference.Role) ||
			reference.ElementID == "" || utf8.RuneCountInString(reference.ElementID) > 128 {
			return nil, fmt.Errorf("%w: invalid asset reference", ErrImageEditorDocumentInvalid)
		}
		key := reference.AssetPublicID + "\x00" + reference.Role + "\x00" + reference.ElementID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		reference.AssetID = 0
		result = append(result, reference)
	}
	return result, nil
}

func validateImageEditorParameters(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(raw) > maxImageEditorParametersBytes || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid or oversized operation parameters", ErrImageEditorDocumentInvalid)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: invalid operation parameters", ErrImageEditorDocumentInvalid)
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: operation parameters must be an object", ErrImageEditorDocumentInvalid)
	}
	if err := validateImageCanvasJSONValue("", value); err != nil {
		return nil, fmt.Errorf("%w: unsafe operation parameters", ErrImageEditorDocumentInvalid)
	}
	return append(json.RawMessage(nil), raw...), nil
}

func validImageEditorOperation(operation string) bool {
	switch operation {
	case "crop", "mask_edit", "background_replace", "outpaint", "revision_select":
		return true
	default:
		return false
	}
}

func validImageEditorAssetRole(role string) bool {
	switch role {
	case "source", "layer", "mask", "result":
		return true
	default:
		return false
	}
}

func validImageEditorPublicID(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 64 && imageCanvasProjectPublicIDPattern.MatchString(value)
}

func finiteImageEditorNumber(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value)
}

func imageEditorDocumentContainsAsset(document *ImageEditorDocument, publicID string) bool {
	if document == nil {
		return false
	}
	if document.BaseAsset != nil && document.BaseAsset.PublicID == publicID {
		return true
	}
	if document.CurrentAsset != nil && document.CurrentAsset.PublicID == publicID {
		return true
	}
	for _, reference := range document.AssetReferences {
		if reference.AssetPublicID == publicID {
			return true
		}
	}
	for _, revision := range document.Revisions {
		if revision.Asset != nil && revision.Asset.PublicID == publicID {
			return true
		}
	}
	return false
}
