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
	APIKeyService        APIKeyQuotaUpdater
	QuotaPlatform        string
	ChannelUsageFields
}

type imageJobBillingGateway interface {
	EstimateImageJobCost(ctx context.Context, apiKey *APIKey, req ImageJobRequest, count int) (*CostBreakdown, bool, error)
	RecordUsageWithCost(ctx context.Context, input *OpenAIRecordUsageInput) (*CostBreakdown, error)
}

type ImageJobBilling struct {
	gateway           imageJobBillingGateway
	maxReservationUSD float64
}

func NewImageJobBilling(gateway imageJobBillingGateway, maxReservationUSD float64) *ImageJobBilling {
	return &ImageJobBilling{gateway: gateway, maxReservationUSD: maxReservationUSD}
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

func (b *ImageJobBilling) Settle(ctx context.Context, input ImageJobSettlementInput) (*CostBreakdown, error) {
	if b == nil || b.gateway == nil {
		return nil, fmt.Errorf("image billing gateway is unavailable")
	}
	if input.Job == nil || strings.TrimSpace(input.Job.PublicID) == "" {
		return nil, fmt.Errorf("image job is required for settlement")
	}
	if input.PersistedResultCount <= 0 {
		return &CostBreakdown{}, nil
	}
	if input.Execution == nil || input.Execution.Forward == nil || input.Execution.Account == nil {
		return nil, fmt.Errorf("image execution result is required for settlement")
	}
	if input.APIKey == nil {
		return nil, fmt.Errorf("API key is required for image settlement")
	}
	user := input.User
	if user == nil {
		user = input.APIKey.User
	}
	if user == nil {
		return nil, fmt.Errorf("user is required for image settlement")
	}

	settlementIndex := input.FrameIndex
	if input.Job.Mode != "sequence" {
		settlementIndex = 0
	}
	requestID := fmt.Sprintf("%s:%d", input.Job.PublicID, settlementIndex)
	forward := *input.Execution.Forward
	forward.RequestID = requestID
	forward.ImageCount = input.PersistedResultCount
	if strings.TrimSpace(forward.Model) == "" {
		forward.Model = input.Job.RequestedModel
	}
	usageInput := &OpenAIRecordUsageInput{
		Result:             &forward,
		APIKey:             input.APIKey,
		User:               user,
		Account:            input.Execution.Account,
		Subscription:       input.Subscription,
		InboundEndpoint:    firstNonEmptyString(input.InboundEndpoint, input.Job.Endpoint),
		UpstreamEndpoint:   input.UpstreamEndpoint,
		UserAgent:          input.UserAgent,
		IPAddress:          input.IPAddress,
		BillingRequestID:   requestID,
		RequestPayloadHash: fmt.Sprintf("%s:%d", input.Job.RequestDigest, settlementIndex),
		APIKeyService:      input.APIKeyService,
		QuotaPlatform:      input.QuotaPlatform,
		ChannelUsageFields: input.ChannelUsageFields,
	}
	if usageInput.OriginalModel == "" {
		usageInput.OriginalModel = input.Job.RequestedModel
	}
	if usageInput.ChannelMappedModel == "" {
		usageInput.ChannelMappedModel = input.Job.MappedModel
	}
	return b.gateway.RecordUsageWithCost(ctx, usageInput)
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
		ImageSize:    req.Size,
	}
	ApplyOpenAIImageBillingResolution(result)
	return s.calculateOpenAIImageCost(ctx, model, apiKey, result, imageMultiplier), false, nil
}
