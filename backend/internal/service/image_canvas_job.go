package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type ImageCanvasJobParameters struct {
	Size              string `json:"size,omitempty"`
	AspectRatio       string `json:"aspect_ratio,omitempty"`
	Resolution        string `json:"resolution,omitempty"`
	N                 int    `json:"n,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	Background        string `json:"background,omitempty"`
	Style             string `json:"style,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	InputFidelity     string `json:"input_fidelity,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	PartialImages     *int   `json:"partial_images,omitempty"`
}

type ImageCanvasJobCreate struct {
	UserID          int64                    `json:"-"`
	APIKeyID        int64                    `json:"api_key_id"`
	ProjectPublicID string                   `json:"project_id"`
	ClientNodeID    string                   `json:"client_node_id"`
	Operation       ImageOperation           `json:"operation"`
	SelectedModel   string                   `json:"selected_model"`
	Prompt          string                   `json:"prompt"`
	InputAssetIDs   []string                 `json:"input_asset_ids"`
	MaskAssetID     string                   `json:"mask_asset_id,omitempty"`
	Parameters      ImageCanvasJobParameters `json:"parameters"`
	IdempotencyKey  string                   `json:"-"`
}

type imageCanvasActiveSubscriptionProvider interface {
	GetActiveSubscription(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
}

type ImageCanvasJobService struct {
	projects      ImageCanvasRepository
	policies      *ImageModelPolicyService
	catalog       ImageModelCatalog
	apiKeys       ImageJobAPIKeyProvider
	subscriptions imageCanvasActiveSubscriptionProvider
	jobs          *ImageJobService
	store         ImageJobObjectStore
	moderation    *ContentModerationService
}

func NewImageCanvasJobService(
	projects ImageCanvasRepository,
	policies *ImageModelPolicyService,
	catalog ImageModelCatalog,
	apiKeys *APIKeyService,
	subscriptions *SubscriptionService,
	jobs *ImageJobService,
	store ImageJobObjectStore,
	moderation *ContentModerationService,
) *ImageCanvasJobService {
	return &ImageCanvasJobService{
		projects: projects, policies: policies, catalog: catalog, apiKeys: apiKeys,
		subscriptions: subscriptions, jobs: jobs, store: store, moderation: moderation,
	}
}

func (s *ImageCanvasJobService) Create(ctx context.Context, input ImageCanvasJobCreate) (*ImageJob, bool, error) {
	if s == nil || s.projects == nil || s.policies == nil || s.catalog == nil || s.apiKeys == nil || s.jobs == nil || s.store == nil {
		return nil, false, fmt.Errorf("%w: image canvas job dependencies are unavailable", ErrImageJobUnavailable)
	}
	input.ProjectPublicID = strings.TrimSpace(input.ProjectPublicID)
	input.ClientNodeID = strings.TrimSpace(input.ClientNodeID)
	input.SelectedModel = strings.TrimSpace(input.SelectedModel)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.UserID <= 0 || input.APIKeyID <= 0 || input.ProjectPublicID == "" || input.ClientNodeID == "" || len(input.ClientNodeID) > 128 {
		return nil, false, fmt.Errorf("%w: project, client node, user, and API key are required", ErrImageJobInvalidRequest)
	}
	if input.Prompt == "" || len(input.Prompt) > 32<<10 {
		return nil, false, fmt.Errorf("%w: prompt must be between 1 and 32768 bytes", ErrImageJobInvalidRequest)
	}
	if len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 255 {
		return nil, false, fmt.Errorf("%w: Idempotency-Key must be between 1 and 255 bytes", ErrImageJobInvalidRequest)
	}
	if input.Operation != ImageOperationGeneration && input.Operation != ImageOperationEdit {
		return nil, false, fmt.Errorf("%w: operation must be generation or edit", ErrImageJobInvalidRequest)
	}
	project, err := s.projects.GetProject(ctx, input.UserID, input.ProjectPublicID)
	if err != nil {
		return nil, false, err
	}
	apiKey, err := s.apiKeys.GetByID(ctx, input.APIKeyID)
	if err != nil || apiKey == nil || apiKey.UserID != input.UserID || apiKey.GroupID == nil || apiKey.Group == nil || !apiKey.IsActive() {
		return nil, false, ErrImageCanvasAPIKeyNotFound
	}
	if err := s.apiKeys.CheckAPIKeyQuotaAndExpiry(apiKey); err != nil {
		return nil, false, ErrImageCanvasAPIKeyNotFound
	}
	allowed, err := s.catalog.ForAPIKey(ctx, input.UserID, input.APIKeyID)
	if err != nil {
		return nil, false, err
	}
	policy, err := s.policies.Get(ctx)
	if err != nil {
		return nil, false, err
	}
	attemptPlan, err := BuildImageAttemptPlan(*policy, input.SelectedModel, input.Operation, allowed)
	if err != nil {
		return nil, false, err
	}
	capability := allowed[input.SelectedModel]
	input.Parameters = applyImageCanvasParameterDefaults(input.Parameters, capability.Defaults)
	if err := validateImageCanvasJobCapability(input, capability); err != nil {
		return nil, false, err
	}
	selectedProvider := normalizeImageProvider(capability.Provider, input.SelectedModel)
	compatiblePlan := make([]string, 0, len(attemptPlan))
	for _, candidate := range attemptPlan {
		candidateCapability, ok := allowed[candidate]
		if !ok || normalizeImageProvider(candidateCapability.Provider, candidate) != selectedProvider {
			continue
		}
		candidateInput := input
		candidateInput.SelectedModel = candidate
		if validateImageCanvasJobCapability(candidateInput, candidateCapability) == nil {
			compatiblePlan = append(compatiblePlan, candidate)
		}
	}
	if len(compatiblePlan) == 0 {
		return nil, false, ErrNoCompatibleImageModel
	}
	attemptPlan = compatiblePlan

	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint, Provider: selectedProvider, Model: input.SelectedModel, ExplicitModel: true,
		Prompt: input.Prompt, N: input.Parameters.N, Size: strings.TrimSpace(input.Parameters.Size),
		AspectRatio: strings.TrimSpace(input.Parameters.AspectRatio), Resolution: strings.ToLower(strings.TrimSpace(input.Parameters.Resolution)),
		ExplicitSize: strings.TrimSpace(input.Parameters.Size) != "", Quality: strings.TrimSpace(input.Parameters.Quality),
		OutputFormat: strings.TrimSpace(input.Parameters.OutputFormat), Background: strings.TrimSpace(input.Parameters.Background),
		Style: strings.TrimSpace(input.Parameters.Style), Moderation: strings.TrimSpace(input.Parameters.Moderation), PartialImages: input.Parameters.PartialImages,
		InputFidelity: strings.TrimSpace(input.Parameters.InputFidelity), OutputCompression: input.Parameters.OutputCompression,
		RequiredCapability: OpenAIImagesCapabilityNative,
	}
	if parsed.N <= 0 {
		parsed.N = 1
	}
	if selectedProvider == ImageProviderGrok {
		parsed.ResponseFormat = "b64_json"
	}
	if input.Operation == ImageOperationEdit {
		parsed.Endpoint = openAIImagesEditsEndpoint
		parsed.Multipart = true
		for _, assetID := range input.InputAssetIDs {
			upload, err := s.loadAssetUpload(ctx, input.UserID, assetID, "image")
			if err != nil {
				return nil, false, err
			}
			parsed.Uploads = append(parsed.Uploads, upload)
		}
		if strings.TrimSpace(input.MaskAssetID) != "" {
			mask, err := s.loadAssetUpload(ctx, input.UserID, input.MaskAssetID, "mask")
			if err != nil {
				return nil, false, err
			}
			parsed.MaskUpload = &mask
			parsed.HasMask = true
		}
	}
	if selectedProvider == ImageProviderGrok {
		parsed.SizeTier = normalizeOpenAIImageSizeTier(parsed.Resolution)
	} else {
		parsed.SizeTier = normalizeOpenAIImageSizeTier(parsed.Size)
	}

	if s.moderation != nil {
		body, _ := json.Marshal(map[string]any{"prompt": input.Prompt, "images": parsed.moderationImages()})
		decision, checkErr := s.moderation.Check(ctx, ContentModerationCheckInput{
			UserID: input.UserID, APIKeyID: apiKey.ID, APIKeyName: apiKey.Name,
			GroupID: apiKey.GroupID, GroupName: apiKey.Group.Name, Endpoint: parsed.Endpoint,
			Provider: selectedProvider, Model: input.SelectedModel, Protocol: ContentModerationProtocolOpenAIImages, Body: body,
		})
		if checkErr != nil {
			return nil, false, checkErr
		}
		if decision != nil && decision.Blocked {
			return nil, false, fmt.Errorf("%w: %s", ErrImageCanvasModerationBlocked, strings.TrimSpace(decision.Message))
		}
	}

	var subscription *UserSubscription
	if apiKey.Group.IsSubscriptionType() {
		if s.subscriptions == nil {
			return nil, false, ErrImageJobReservationUnavailable
		}
		subscription, err = s.subscriptions.GetActiveSubscription(ctx, input.UserID, *apiKey.GroupID)
		if err != nil {
			return nil, false, err
		}
	}
	projectID := project.ID
	return s.jobs.Create(ctx, CreateImageJobInput{
		APIKey: apiKey, Subscription: subscription, Parsed: parsed, Mode: "batch",
		IdempotencyKey: input.IdempotencyKey, CandidateModels: attemptPlan,
		Canvas: &ImageCanvasJobMetadata{
			ProjectID: &projectID, ClientNodeID: input.ClientNodeID, SelectedModel: input.SelectedModel,
			PolicyVersion: policy.Version, AttemptPlan: attemptPlan,
		},
	})
}

func (s *ImageCanvasJobService) GetOwned(ctx context.Context, userID int64, publicID string) (*ImageJob, error) {
	if s == nil || s.jobs == nil || s.apiKeys == nil {
		return nil, ErrImageJobUnavailable
	}
	job, err := s.jobs.GetAdmin(ctx, strings.TrimSpace(publicID))
	if err != nil || job == nil || job.UserID != userID || job.ProjectID == nil {
		return nil, ErrImageJobNotFound
	}
	return job, nil
}

func (s *ImageCanvasJobService) CancelOwned(ctx context.Context, userID int64, publicID string) (*ImageJob, error) {
	job, err := s.GetOwned(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return s.jobs.CancelOwned(ctx, job.PublicID, job.APIKeyID)
}

func (s *ImageCanvasJobService) GetResult(ctx context.Context, userID int64, publicID string, index int) (*ImageJobObject, error) {
	job, err := s.GetOwned(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return s.jobs.GetOwnedResult(ctx, job.PublicID, job.APIKeyID, index)
}

func (s *ImageCanvasJobService) loadAssetUpload(ctx context.Context, userID int64, publicID, fieldName string) (OpenAIImagesUpload, error) {
	asset, err := s.projects.GetAsset(ctx, userID, strings.TrimSpace(publicID))
	if err != nil {
		return OpenAIImagesUpload{}, err
	}
	object, err := s.store.Get(ctx, asset.ObjectKey)
	if err != nil || object == nil || int64(len(object.Data)) != asset.ByteSize {
		return OpenAIImagesUpload{}, ErrImageAssetNotFound
	}
	return OpenAIImagesUpload{
		FieldName: fieldName, FileName: asset.PublicID, ContentType: asset.MIMEType,
		Data: object.Data, Width: asset.Width, Height: asset.Height,
	}, nil
}

func validateImageCanvasJobCapability(input ImageCanvasJobCreate, capability ImageModelCapability) error {
	n := input.Parameters.N
	if n <= 0 {
		n = 1
	}
	if capability.MaxOutputs > 0 && n > capability.MaxOutputs {
		return fmt.Errorf("%w: requested output count exceeds model capability", ErrImageJobInvalidRequest)
	}
	size := strings.TrimSpace(input.Parameters.Size)
	aspectRatio := strings.TrimSpace(input.Parameters.AspectRatio)
	resolution := strings.ToLower(strings.TrimSpace(input.Parameters.Resolution))
	switch capability.DimensionMode {
	case ImageDimensionModeSize:
		if aspectRatio != "" || resolution != "" {
			return fmt.Errorf("%w: selected model accepts size instead of aspect_ratio/resolution", ErrImageJobInvalidRequest)
		}
	case ImageDimensionModeAspectRatioResolution:
		if size != "" {
			return fmt.Errorf("%w: selected model accepts aspect_ratio/resolution instead of size", ErrImageJobInvalidRequest)
		}
	}
	if size != "" && !containsImageCanvasString(capability.Sizes, size) {
		if capability.CustomSize == nil || !validImageCanvasCustomSize(size, *capability.CustomSize) {
			return fmt.Errorf("%w: requested size is not supported by the selected model", ErrImageJobInvalidRequest)
		}
	}
	if aspectRatio != "" && !containsImageCanvasString(capability.AspectRatios, aspectRatio) {
		return fmt.Errorf("%w: requested aspect ratio is not supported by the selected model", ErrImageJobInvalidRequest)
	}
	if resolution != "" && !containsImageCanvasString(capability.Resolutions, resolution) {
		return fmt.Errorf("%w: requested resolution is not supported by the selected model", ErrImageJobInvalidRequest)
	}
	quality := strings.TrimSpace(input.Parameters.Quality)
	if quality != "" && !containsImageCanvasString(capability.Qualities, quality) {
		return fmt.Errorf("%w: requested quality is not supported by the selected model", ErrImageJobInvalidRequest)
	}
	outputFormat := strings.ToLower(strings.TrimSpace(input.Parameters.OutputFormat))
	if outputFormat != "" && !containsImageCanvasString(capability.OutputFormats, outputFormat) {
		return fmt.Errorf("%w: requested output format is not supported by the selected model", ErrImageJobInvalidRequest)
	}
	background := strings.TrimSpace(input.Parameters.Background)
	if background != "" && !containsImageCanvasString(capability.Backgrounds, background) {
		return fmt.Errorf("%w: requested background is not supported by the selected model", ErrImageJobInvalidRequest)
	}
	if background == "transparent" && outputFormat == "jpeg" {
		return fmt.Errorf("%w: transparent backgrounds require png or webp", ErrImageJobInvalidRequest)
	}
	if input.Parameters.OutputCompression != nil {
		compression := *input.Parameters.OutputCompression
		if !capability.OutputCompression || compression < 0 || compression > 100 || (outputFormat != "jpeg" && outputFormat != "webp") {
			return fmt.Errorf("%w: output compression requires a supported jpeg or webp output", ErrImageJobInvalidRequest)
		}
	}
	if input.Parameters.PartialImages != nil {
		partial := *input.Parameters.PartialImages
		if !capability.PartialImages || partial < 0 || partial > capability.MaxPartialImages {
			return fmt.Errorf("%w: requested partial image count is not supported by the selected model", ErrImageJobInvalidRequest)
		}
	}
	if input.Operation == ImageOperationGeneration {
		if len(input.InputAssetIDs) > 0 || strings.TrimSpace(input.MaskAssetID) != "" {
			return fmt.Errorf("%w: generation does not accept input assets", ErrImageJobInvalidRequest)
		}
		return nil
	}
	if len(input.InputAssetIDs) == 0 {
		return fmt.Errorf("%w: edit requires at least one input asset", ErrImageJobInvalidRequest)
	}
	if len(input.InputAssetIDs) > 1 && !capability.MultiImage {
		return fmt.Errorf("%w: selected model does not support multiple input images", ErrImageJobInvalidRequest)
	}
	if capability.MaxInputImages > 0 && len(input.InputAssetIDs) > capability.MaxInputImages {
		return fmt.Errorf("%w: input image count exceeds model capability", ErrImageJobInvalidRequest)
	}
	if strings.TrimSpace(input.MaskAssetID) != "" && !capability.Mask {
		return fmt.Errorf("%w: selected model does not support masks", ErrImageJobInvalidRequest)
	}
	return nil
}

func ValidateImageModelDefaultsForCapability(parameters ImageModelDefaults, capability ImageModelCapability) error {
	return validateImageCanvasJobCapability(ImageCanvasJobCreate{
		Operation: ImageOperationGeneration,
		Parameters: ImageCanvasJobParameters{
			Size: parameters.Size, AspectRatio: parameters.AspectRatio,
			Resolution: parameters.Resolution, N: 1, Quality: parameters.Quality,
			OutputFormat: parameters.OutputFormat, Background: parameters.Background,
			OutputCompression: parameters.OutputCompression,
		},
	}, capability)
}

func applyImageCanvasParameterDefaults(parameters ImageCanvasJobParameters, defaults ImageModelDefaults) ImageCanvasJobParameters {
	if parameters.N <= 0 {
		parameters.N = 1
	}
	if strings.TrimSpace(parameters.Size) == "" {
		parameters.Size = defaults.Size
	}
	if strings.TrimSpace(parameters.AspectRatio) == "" {
		parameters.AspectRatio = defaults.AspectRatio
	}
	if strings.TrimSpace(parameters.Resolution) == "" {
		parameters.Resolution = defaults.Resolution
	}
	if strings.TrimSpace(parameters.Quality) == "" {
		parameters.Quality = defaults.Quality
	}
	if strings.TrimSpace(parameters.OutputFormat) == "" {
		parameters.OutputFormat = defaults.OutputFormat
	}
	if strings.TrimSpace(parameters.Background) == "" {
		parameters.Background = defaults.Background
	}
	if parameters.OutputCompression == nil && defaults.OutputCompression != nil {
		value := *defaults.OutputCompression
		parameters.OutputCompression = &value
	}
	return parameters
}

func validImageCanvasCustomSize(size string, constraints ImageCustomSizeConstraints) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return false
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return false
	}
	if constraints.MaxEdge > 0 && (width > constraints.MaxEdge || height > constraints.MaxEdge) {
		return false
	}
	if constraints.MultipleOf > 0 && (width%constraints.MultipleOf != 0 || height%constraints.MultipleOf != 0) {
		return false
	}
	pixels := width * height
	if constraints.MinPixels > 0 && pixels < constraints.MinPixels {
		return false
	}
	if constraints.MaxPixels > 0 && pixels > constraints.MaxPixels {
		return false
	}
	shortEdge, longEdge := width, height
	if shortEdge > longEdge {
		shortEdge, longEdge = longEdge, shortEdge
	}
	return constraints.MaxAspectRatio <= 0 || float64(longEdge)/float64(shortEdge) <= constraints.MaxAspectRatio
}

func containsImageCanvasString(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}
