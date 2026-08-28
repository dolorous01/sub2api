package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrImageModelPolicyInvalid            = errors.New("image model policy is invalid")
	ErrImageModelPolicyVersionConflict    = errors.New("image model policy version conflict")
	ErrImageModelSelectionInvalid         = errors.New("selected image model is not allowed")
	ErrNoCompatibleImageModel             = errors.New("no compatible image model")
	ErrImageCanvasAPIKeyNotFound          = errors.New("image canvas api key not found")
	ErrImageCanvasProjectNotFound         = errors.New("image canvas project not found")
	ErrImageCanvasProjectVersionConflict  = errors.New("image canvas project version conflict")
	ErrImageCanvasDocumentInvalid         = errors.New("image canvas document is invalid")
	ErrImageAssetInvalid                  = errors.New("image asset is invalid")
	ErrImageAssetNotFound                 = errors.New("image asset not found")
	ErrImageCanvasModerationBlocked       = errors.New("image canvas request blocked by content moderation")
	ErrImageEditorDocumentNotFound        = errors.New("image editor document not found")
	ErrImageEditorDocumentConflict        = errors.New("image editor document conflict")
	ErrImageEditorDocumentVersionConflict = errors.New("image editor document version conflict")
	ErrImageEditorDocumentInvalid         = errors.New("image editor document is invalid")
)

type ImageOperation string

const (
	ImageOperationGeneration ImageOperation = "generation"
	ImageOperationEdit       ImageOperation = "edit"
)

func imageProviderForModel(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(normalized, "grok-imagine") {
		return ImageProviderGrok
	}
	return ImageProviderOpenAI
}

func normalizeImageProvider(provider, model string) string {
	if strings.EqualFold(strings.TrimSpace(provider), ImageProviderGrok) {
		return ImageProviderGrok
	}
	if strings.EqualFold(strings.TrimSpace(provider), ImageProviderOpenAI) {
		return ImageProviderOpenAI
	}
	return imageProviderForModel(model)
}

const (
	ImageProviderOpenAI = "openai"
	ImageProviderGrok   = "grok"

	ImageDimensionModeSize                  = "size"
	ImageDimensionModeAspectRatioResolution = "aspect_ratio_resolution"
)

// ImageCustomSizeConstraints describes models such as gpt-image-2 that accept
// arbitrary dimensions within a bounded resolution envelope.
type ImageCustomSizeConstraints struct {
	MinPixels      int     `json:"min_pixels"`
	MaxPixels      int     `json:"max_pixels"`
	MaxEdge        int     `json:"max_edge"`
	MultipleOf     int     `json:"multiple_of"`
	MaxAspectRatio float64 `json:"max_aspect_ratio"`
}

type ImageModelDefaults struct {
	Size              string `json:"size,omitempty"`
	AspectRatio       string `json:"aspect_ratio,omitempty"`
	Resolution        string `json:"resolution,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	Background        string `json:"background,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
}

// ImageModelPreset is a server-authoritative parameter bundle rendered by the
// canvas. Experimental is surfaced so high-cost 4K options are never mistaken
// for an ordinary model default.
type ImageModelPreset struct {
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	Parameters   ImageModelDefaults `json:"parameters"`
	Experimental bool               `json:"experimental,omitempty"`
}

type ImageModelCapability struct {
	MediaKind         string                      `json:"media_kind,omitempty"`
	Provider          string                      `json:"provider,omitempty"`
	DimensionMode     string                      `json:"dimension_mode,omitempty"`
	Generation        bool                        `json:"generation"`
	Edit              bool                        `json:"edit"`
	MultiImage        bool                        `json:"multi_image"`
	Mask              bool                        `json:"mask"`
	MaxInputImages    int                         `json:"max_input_images"`
	MaxOutputs        int                         `json:"max_outputs"`
	Sizes             []string                    `json:"sizes"`
	AspectRatios      []string                    `json:"aspect_ratios,omitempty"`
	Resolutions       []string                    `json:"resolutions,omitempty"`
	Qualities         []string                    `json:"qualities,omitempty"`
	OutputFormats     []string                    `json:"output_formats,omitempty"`
	Backgrounds       []string                    `json:"backgrounds,omitempty"`
	OutputCompression bool                        `json:"output_compression,omitempty"`
	PartialImages     bool                        `json:"partial_images,omitempty"`
	MaxPartialImages  int                         `json:"max_partial_images,omitempty"`
	CustomSize        *ImageCustomSizeConstraints `json:"custom_size,omitempty"`
	ExperimentalSizes []string                    `json:"experimental_sizes,omitempty"`
	VideoSeconds      []int                       `json:"video_seconds,omitempty"`
	AudioVoices       []string                    `json:"audio_voices,omitempty"`
	AudioFormats      []string                    `json:"audio_formats,omitempty"`
	AudioSpeedMin     float64                     `json:"audio_speed_min,omitempty"`
	AudioSpeedMax     float64                     `json:"audio_speed_max,omitempty"`
	Defaults          ImageModelDefaults          `json:"defaults,omitempty"`
	Presets           []ImageModelPreset          `json:"presets,omitempty"`
}

type ImageModelPolicyItem struct {
	Model      string               `json:"model"`
	Enabled    bool                 `json:"enabled"`
	Position   int                  `json:"position"`
	Capability ImageModelCapability `json:"capability,omitempty"`
}

// ImageModelCoverage is the currently schedulable footprint of a model. The
// counts intentionally describe references (an account can belong to more
// than one group) rather than exposing any credential material.
type ImageModelCoverage struct {
	AccountCount int `json:"account_count"`
	GroupCount   int `json:"group_count"`
	APIKeyCount  int `json:"api_key_count"`
}

// ImageModelCatalogEntry is the administrator-facing model inventory row.
// Capability remains the server-authoritative parameter contract used by the
// user canvas; the other fields explain why a model can (or cannot) be used.
type ImageModelCatalogEntry struct {
	Model                string               `json:"model"`
	Provider             string               `json:"provider,omitempty"`
	MediaKind            string               `json:"media_kind,omitempty"`
	Capability           ImageModelCapability `json:"capability,omitempty"`
	Coverage             ImageModelCoverage   `json:"coverage"`
	Schedulable          bool                 `json:"schedulable"`
	SchedulabilityReason string               `json:"schedulability_reason,omitempty"`
}

const (
	ImageModelSchedulabilityReasonNoAccount     = "no_schedulable_account"
	ImageModelSchedulabilityReasonNoGroup       = "no_eligible_group"
	ImageModelSchedulabilityReasonGroupFiltered = "group_model_filter"
	ImageModelSchedulabilityReasonUnknownModel  = "unknown_model"
)

// ImageModelCatalogAdmin is optional so user-facing catalog implementations
// and test doubles do not need to expose administrator-only coverage data.
type ImageModelCatalogAdmin interface {
	ListAdmin(ctx context.Context) ([]ImageModelCatalogEntry, error)
	CheckSchedulability(ctx context.Context, model string) (ImageModelCatalogEntry, error)
}

type ImageModelPolicy struct {
	Version int64                  `json:"version"`
	Enabled bool                   `json:"enabled"`
	Items   []ImageModelPolicyItem `json:"models"`
}

type ImageModelPolicyAudit struct {
	ID             int64           `json:"id"`
	OperatorUserID int64           `json:"operator_user_id"`
	OldVersion     int64           `json:"old_version"`
	NewVersion     int64           `json:"new_version"`
	BeforeValue    json.RawMessage `json:"before_value"`
	AfterValue     json.RawMessage `json:"after_value"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ImageCanvasAPIKey is the intentionally redacted API key shape exposed to
// the canvas. It must never grow a key, prefix, hash, or credential field.
type ImageCanvasAPIKey struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	GroupID           int64  `json:"group_id"`
	GroupName         string `json:"group_name"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type ImageCanvasProject struct {
	ID               int64             `json:"-"`
	PublicID         string            `json:"id"`
	UserID           int64             `json:"-"`
	Name             string            `json:"name"`
	Document         json.RawMessage   `json:"document"`
	Version          int64             `json:"version"`
	ThumbnailAssetID *int64            `json:"-"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	OpenJobs         []ImageJob        `json:"open_jobs,omitempty"`
	MediaTasks       []CanvasMediaTask `json:"media_tasks,omitempty"`
}

type ImageCanvasAssetReference struct {
	AssetPublicID string `json:"asset_id"`
	NodeID        string `json:"node_id"`
}

type ImageAsset struct {
	ID                 int64           `json:"-"`
	PublicID           string          `json:"id"`
	OwnerUserID        int64           `json:"-"`
	ProjectID          *int64          `json:"-"`
	SourceType         string          `json:"source_type"`
	MediaKind          string          `json:"media_kind"`
	FileName           string          `json:"file_name,omitempty"`
	ObjectKey          string          `json:"-"`
	ThumbnailObjectKey string          `json:"-"`
	MIMEType           string          `json:"mime_type"`
	Width              int             `json:"width"`
	Height             int             `json:"height"`
	DurationMS         int64           `json:"duration_ms,omitempty"`
	ByteSize           int64           `json:"byte_size"`
	SHA256             string          `json:"sha256"`
	OriginJobID        *int64          `json:"-"`
	ParentAssetIDs     json.RawMessage `json:"parent_asset_ids,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ImageAssetCreate struct {
	PublicID           string
	OwnerUserID        int64
	ProjectID          *int64
	SourceType         string
	MediaKind          string
	FileName           string
	ObjectKey          string
	ThumbnailObjectKey string
	MIMEType           string
	Width              int
	Height             int
	DurationMS         int64
	ByteSize           int64
	SHA256             string
	OriginJobID        *int64
	ParentAssetIDs     json.RawMessage
}

type ImageEditorAssetReference struct {
	AssetID       int64  `json:"-"`
	AssetPublicID string `json:"asset_id"`
	Role          string `json:"role"`
	ElementID     string `json:"element_id"`
}

type ImageEditorRevision struct {
	ID         int64           `json:"-"`
	PublicID   string          `json:"id"`
	DocumentID int64           `json:"-"`
	Version    int64           `json:"version"`
	AssetID    int64           `json:"-"`
	Asset      *ImageAsset     `json:"asset"`
	Operation  string          `json:"operation"`
	Parameters json.RawMessage `json:"parameters"`
	CreatedAt  time.Time       `json:"created_at"`
}

type ImageEditorDocument struct {
	ID              int64                       `json:"-"`
	PublicID        string                      `json:"id"`
	UserID          int64                       `json:"-"`
	ProjectID       int64                       `json:"-"`
	ProjectPublicID string                      `json:"project_id"`
	NodeID          string                      `json:"node_id"`
	BaseAssetID     int64                       `json:"-"`
	BaseAsset       *ImageAsset                 `json:"base_asset"`
	CurrentAssetID  int64                       `json:"-"`
	CurrentAsset    *ImageAsset                 `json:"current_asset"`
	Document        json.RawMessage             `json:"document"`
	Version         int64                       `json:"version"`
	AssetReferences []ImageEditorAssetReference `json:"asset_references"`
	Revisions       []ImageEditorRevision       `json:"revisions"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
}

type ImageEditorDocumentCreate struct {
	UserID            int64
	ProjectPublicID   string
	NodeID            string
	BaseAssetPublicID string
	Document          json.RawMessage
	AssetReferences   []ImageEditorAssetReference
}

type ImageEditorDocumentUpdate struct {
	UserID               int64
	PublicID             string
	Version              int64
	Document             json.RawMessage
	CurrentAssetPublicID string
	Operation            string
	Parameters           json.RawMessage
	AssetReferences      []ImageEditorAssetReference
}

type ImageEditorRepository interface {
	CreateOrGet(ctx context.Context, input ImageEditorDocumentCreate) (*ImageEditorDocument, bool, error)
	GetOwned(ctx context.Context, userID int64, publicID string) (*ImageEditorDocument, error)
	Update(ctx context.Context, input ImageEditorDocumentUpdate) (*ImageEditorDocument, error)
}

type ImageCanvasRepository interface {
	ListProjects(ctx context.Context, userID int64) ([]ImageCanvasProject, error)
	CreateProject(ctx context.Context, userID int64, publicID, name string, document json.RawMessage, assetRefs []ImageCanvasAssetReference) (*ImageCanvasProject, error)
	GetProject(ctx context.Context, userID int64, publicID string) (*ImageCanvasProject, error)
	UpdateProject(ctx context.Context, userID int64, publicID string, version int64, name string, document json.RawMessage, assetRefs []ImageCanvasAssetReference) (*ImageCanvasProject, error)
	DeleteProject(ctx context.Context, userID int64, publicID string) error
	CreateAsset(ctx context.Context, input ImageAssetCreate) (*ImageAsset, error)
	GetAsset(ctx context.Context, userID int64, publicID string) (*ImageAsset, error)
	ListOpenJobs(ctx context.Context, userID int64, projectID int64) ([]ImageJob, error)
}
