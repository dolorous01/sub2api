package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type ImageJobReservation struct {
	AmountUSD      float64
	BillingType    int8
	SubscriptionID *int64
}

type ImageExecutionResult struct {
	Forward        *OpenAIForwardResult
	Account        *Account
	ChannelMapping ChannelMappingResult
	// RoutingLatencyMs covers account selection and slot acquisition.
	RoutingLatencyMs int64
}

type ImageJobSettlementInput struct {
	Job                  *ImageJob
	FrameIndex           int
	Execution            *ImageExecutionResult
	APIKey               *APIKey
	User                 *User
	Subscription         *UserSubscription
	PersistedResultCount int
	InboundEndpoint      string
	UpstreamEndpoint     string
	UserAgent            string
	IPAddress            string
	RequestPayloadHash   string
	APIKeyService        APIKeyQuotaUpdater
	QuotaPlatform        string
	ChannelUsageFields
}

type imageJobBillingGateway interface {
	EstimateImageJobCost(ctx context.Context, apiKey *APIKey, req ImageJobRequest, count int) (*CostBreakdown, bool, error)
	RecordUsageWithCost(ctx context.Context, input *OpenAIRecordUsageInput) (*CostBreakdown, error)
}

type ImageJobReservationRepository interface {
	SettleReservation(ctx context.Context, jobID int64) error
	ReleaseReservation(ctx context.Context, jobID int64) error
}

type ImageJobBilling struct {
	gateway           imageJobBillingGateway
	reservationRepo   ImageJobReservationRepository
	maxReservationUSD float64
}

func NewImageJobBilling(gateway imageJobBillingGateway, maxReservationUSD float64, reservationRepos ...ImageJobReservationRepository) *ImageJobBilling {
	var reservationRepo ImageJobReservationRepository
	if len(reservationRepos) > 0 {
		reservationRepo = reservationRepos[0]
	}
	return &ImageJobBilling{gateway: gateway, reservationRepo: reservationRepo, maxReservationUSD: maxReservationUSD}
}

func (b *ImageJobBilling) Estimate(ctx context.Context, apiKey *APIKey, subscription *UserSubscription, req ImageJobRequest) (ImageJobReservation, error) {
	if b == nil || b.gateway == nil {
		return ImageJobReservation{}, fmt.Errorf("%w: image billing gateway is unavailable", ErrImageJobReservationUnavailable)
	}
	if apiKey == nil {
		return ImageJobReservation{}, fmt.Errorf("%w: API key is required", ErrImageJobReservationUnavailable)
	}
	if b.maxReservationUSD <= 0 {
		return ImageJobReservation{}, fmt.Errorf("%w: maximum reservation must be greater than zero", ErrImageJobReservationUnavailable)
	}
	count := req.N
	if len(req.Scenes) > 0 {
		count = len(req.Scenes)
	}
	if count <= 0 {
		return ImageJobReservation{}, fmt.Errorf("%w: output count must be greater than zero", ErrImageJobReservationUnavailable)
	}

	cost, tokenPricing, err := b.gateway.EstimateImageJobCost(ctx, apiKey, req, count)
	if err != nil {
		return ImageJobReservation{}, fmt.Errorf("%w: %v", ErrImageJobReservationUnavailable, err)
	}
	amount := b.maxReservationUSD
	if !tokenPricing {
		if cost == nil {
			return ImageJobReservation{}, fmt.Errorf("%w: image price is unavailable", ErrImageJobReservationUnavailable)
		}
		amount = cost.ActualCost
	}
	if amount <= 0 || amount > b.maxReservationUSD+1e-9 {
		return ImageJobReservation{}, fmt.Errorf("%w: estimated cost %.8f exceeds the configured limit %.8f", ErrImageJobReservationUnavailable, amount, b.maxReservationUSD)
	}

	reservation := ImageJobReservation{AmountUSD: amount, BillingType: BillingTypeBalance}
	if subscription != nil && apiKey.Group != nil && apiKey.Group.IsSubscriptionType() {
		reservation.BillingType = BillingTypeSubscription
		subscriptionID := subscription.ID
		reservation.SubscriptionID = &subscriptionID
	}
	return reservation, nil
}

func (b *ImageJobBilling) EstimateCandidates(
	ctx context.Context,
	apiKey *APIKey,
	subscription *UserSubscription,
	request ImageJobRequest,
	models []string,
) (ImageJobReservation, error) {
	if len(models) == 0 {
		return ImageJobReservation{}, fmt.Errorf("%w: at least one candidate model is required", ErrImageJobReservationUnavailable)
	}
	var highest ImageJobReservation
	for _, model := range models {
		candidate := request
		candidate.Model = strings.TrimSpace(model)
		if candidate.Model == "" {
			return ImageJobReservation{}, fmt.Errorf("%w: candidate model must not be empty", ErrImageJobReservationUnavailable)
		}
		reservation, err := b.Estimate(ctx, apiKey, subscription, candidate)
		if err != nil {
			return ImageJobReservation{}, err
		}
		if highest.AmountUSD == 0 || reservation.AmountUSD > highest.AmountUSD {
			highest = reservation
		}
	}
	if highest.AmountUSD <= 0 {
		return ImageJobReservation{}, ErrImageJobReservationUnavailable
	}
	return highest, nil
}

func (b *ImageJobBilling) Settle(ctx context.Context, input ImageJobSettlementInput) (*CostBreakdown, error) {
	if b == nil || b.gateway == nil {
		return nil, fmt.Errorf("image billing gateway is unavailable")
	}
	if input.Job == nil || strings.TrimSpace(input.Job.PublicID) == "" {
		return nil, fmt.Errorf("image job is required for settlement")
	}
	if b.reservationRepo == nil {
		return nil, fmt.Errorf("image job reservation repository is unavailable")
	}
	if input.PersistedResultCount <= 0 {
		if input.Job.Mode == "sequence" {
			return &CostBreakdown{}, nil
		}
		if err := b.reservationRepo.ReleaseReservation(ctx, input.Job.ID); err != nil {
			return nil, fmt.Errorf("release image job reservation: %w", err)
		}
		return &CostBreakdown{}, nil
	}
	if input.Execution == nil || input.Execution.Forward == nil || input.Execution.Account == nil {
		return nil, fmt.Errorf("image execution result is required for settlement")
	}
	if input.APIKey == nil {
		return nil, fmt.Errorf("API key is required for image settlement")
	}
	if input.Job.APIKeyID > 0 && input.APIKey.ID != input.Job.APIKeyID {
		return nil, fmt.Errorf("API key does not match image job reservation")
	}
	user := input.User
	if user == nil {
		user = input.APIKey.User
	}
	if user == nil {
		return nil, fmt.Errorf("user is required for image settlement")
	}
	if input.Job.UserID > 0 && user.ID != input.Job.UserID {
		return nil, fmt.Errorf("user does not match image job reservation")
	}
	billingType := input.Job.ReservationBillingType
	var subscription *UserSubscription
	switch billingType {
	case BillingTypeBalance:
	case BillingTypeSubscription:
		if input.Job.ReservationSubscriptionID == nil || *input.Job.ReservationSubscriptionID <= 0 {
			return nil, fmt.Errorf("persisted image job subscription is required for settlement")
		}
		subscription = &UserSubscription{ID: *input.Job.ReservationSubscriptionID}
	default:
		return nil, fmt.Errorf("unsupported persisted image job billing type %d", billingType)
	}

	settlementIndex := input.FrameIndex
	if input.Job.Mode != "sequence" {
		settlementIndex = 0
	}
	requestID := fmt.Sprintf("%s:%d", input.Job.PublicID, settlementIndex)
	forward := *input.Execution.Forward
	resultCount := forward.ImageCount
	if resultCount <= 0 {
		resultCount = input.Job.RequestedCount
	}
	if input.Job.Mode != "sequence" && resultCount > input.PersistedResultCount {
		forward.Usage = scaleImageJobUsage(forward.Usage, input.PersistedResultCount, resultCount)
	}
	forward.RequestID = requestID
	forward.ImageCount = input.PersistedResultCount
	if strings.TrimSpace(forward.Model) == "" {
		forward.Model = input.Job.RequestedModel
	}
	usageInput := &OpenAIRecordUsageInput{
		Result:                &forward,
		APIKey:                input.APIKey,
		User:                  user,
		Account:               input.Execution.Account,
		Subscription:          subscription,
		InboundEndpoint:       firstNonEmptyString(input.InboundEndpoint, input.Job.Endpoint),
		UpstreamEndpoint:      input.UpstreamEndpoint,
		UserAgent:             input.UserAgent,
		IPAddress:             input.IPAddress,
		BillingRequestID:      requestID,
		BillingType:           &billingType,
		BillingSubscriptionID: input.Job.ReservationSubscriptionID,
		RequestPayloadHash:    fmt.Sprintf("%s:%d", firstNonEmptyString(input.RequestPayloadHash, input.Job.RequestDigest), settlementIndex),
		APIKeyService:         input.APIKeyService,
		QuotaPlatform:         input.QuotaPlatform,
		ChannelUsageFields:    input.ChannelUsageFields,
	}
	if usageInput.OriginalModel == "" {
		usageInput.OriginalModel = input.Job.RequestedModel
	}
	if usageInput.ChannelMappedModel == "" {
		usageInput.ChannelMappedModel = input.Job.MappedModel
	}
	cost, err := b.gateway.RecordUsageWithCost(ctx, usageInput)
	if err != nil {
		return nil, err
	}
	if input.Job.Mode != "sequence" {
		if err := b.reservationRepo.SettleReservation(ctx, input.Job.ID); err != nil {
			return nil, fmt.Errorf("settle image job reservation: %w", err)
		}
	}
	return cost, nil
}

func scaleImageJobUsage(usage OpenAIUsage, numerator, denominator int) OpenAIUsage {
	if numerator <= 0 || denominator <= 0 || numerator >= denominator {
		return usage
	}
	scale := func(value int) int {
		return int(int64(value) * int64(numerator) / int64(denominator))
	}
	usage.InputTokens = scale(usage.InputTokens)
	usage.ImageInputTokens = scale(usage.ImageInputTokens)
	usage.OutputTokens = scale(usage.OutputTokens)
	usage.CacheCreationInputTokens = scale(usage.CacheCreationInputTokens)
	usage.CacheReadInputTokens = scale(usage.CacheReadInputTokens)
	usage.ImageOutputTokens = scale(usage.ImageOutputTokens)
	return usage
}

func (s *OpenAIGatewayService) EstimateImageJobCost(ctx context.Context, apiKey *APIKey, req ImageJobRequest, count int) (*CostBreakdown, bool, error) {
	if s == nil || s.billingService == nil {
		return nil, false, fmt.Errorf("image billing service is unavailable")
	}
	if apiKey == nil {
		return nil, false, fmt.Errorf("API key is required")
	}
	if count <= 0 {
		return nil, false, fmt.Errorf("image count must be greater than zero")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, false, fmt.Errorf("image model is required")
	}
	if resolved := s.resolveOpenAIChannelPricing(ctx, model, apiKey); resolved != nil && resolved.Mode == BillingModeToken {
		return nil, true, nil
	}

	multiplier := 1.0
	if s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if apiKey.GroupID != nil && apiKey.Group != nil && apiKey.User != nil {
		resolver := s.userGroupRateResolver
		if resolver == nil {
			resolver = newUserGroupRateResolver(nil, nil, resolveUserGroupRateCacheTTL(s.cfg), nil, "service.image_job_billing")
		}
		multiplier = resolver.Resolve(ctx, apiKey.User.ID, *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	_, imageMultiplier := computePeakAwareMultipliers(apiKey, multiplier, timezone.Now())
	result := &OpenAIForwardResult{
		Model:        model,
		BillingModel: model,
		ImageCount:   count,
		ImageSize:    firstNonEmptyString(req.Size, req.Resolution),
	}
	ApplyOpenAIImageBillingResolution(result)
	return s.calculateOpenAIImageCost(ctx, model, apiKey, result, imageMultiplier), false, nil
}
