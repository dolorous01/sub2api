package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestImageJobBillingSettlementUsesStableRequestID(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{applied: make(map[string]int)}
	reservations := &fakeImageJobReservationRepository{}
	billing := NewImageJobBilling(gateway, 1, reservations)
	user := &User{ID: 10}
	apiKey := &APIKey{ID: 20, UserID: user.ID, User: user}
	execution := &ImageExecutionResult{
		Forward: &OpenAIForwardResult{RequestID: "upstream-changing", Model: "gpt-image-2", ImageCount: 1},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}
	input := ImageJobSettlementInput{
		Job:                  &ImageJob{ID: 41, PublicID: "imgjob_x", RequestedModel: "gpt-image-2", RequestDigest: "digest"},
		FrameIndex:           0,
		Execution:            execution,
		APIKey:               apiKey,
		User:                 user,
		PersistedResultCount: 1,
	}
	if _, err := billing.Settle(context.Background(), input); err != nil {
		t.Fatalf("first Settle() error = %v", err)
	}
	if _, err := billing.Settle(context.Background(), input); err != nil {
		t.Fatalf("second Settle() error = %v", err)
	}
	if got := gateway.applied["imgjob_x:0"]; got != 1 {
		t.Fatalf("billing apply count = %d, want 1", got)
	}
	if gateway.billingRequestID != "imgjob_x:0" {
		t.Errorf("billing request ID = %q, want imgjob_x:0", gateway.billingRequestID)
	}
	if reservations.settled[41] != 2 {
		t.Errorf("settled transitions = %d, want 2 idempotent attempts", reservations.settled[41])
	}
	if execution.Forward.RequestID != "upstream-changing" {
		t.Errorf("Settle() mutated upstream result request ID = %q", execution.Forward.RequestID)
	}
}

func TestImageJobBillingSequenceSettlementUsesFrameIndex(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	reservations := &fakeImageJobReservationRepository{}
	billing := NewImageJobBilling(gateway, 1, reservations)
	user := &User{ID: 10}

	_, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job: &ImageJob{
			ID: 42, PublicID: "imgjob_sequence", Mode: "sequence",
			RequestedModel: "gpt-image-2", RequestDigest: "digest",
		},
		FrameIndex: 2,
		Execution: &ImageExecutionResult{
			Forward: &OpenAIForwardResult{Model: "gpt-image-2"},
			Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
		APIKey:               &APIKey{ID: 20, UserID: user.ID, User: user},
		User:                 user,
		PersistedResultCount: 1,
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if gateway.billingRequestID != "imgjob_sequence:2" {
		t.Errorf("billing request ID = %q, want imgjob_sequence:2", gateway.billingRequestID)
	}
	if reservations.settled[42] != 0 {
		t.Errorf("sequence frame settled whole-job reservation %d times, want 0", reservations.settled[42])
	}
}

func TestImageJobBillingEstimateUsesConfiguredMaximumForTokenPricing(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{tokenPricing: true}
	billing := NewImageJobBilling(gateway, 1.25)
	apiKey := &APIKey{ID: 20, UserID: 10}

	reservation, err := billing.Estimate(context.Background(), apiKey, nil, ImageJobRequest{
		Model: "gpt-image-2", N: 4, Scenes: []string{"one", "two", "three"},
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if reservation.AmountUSD != 1.25 {
		t.Errorf("reservation amount = %v, want 1.25", reservation.AmountUSD)
	}
	if gateway.estimatedCount != 3 {
		t.Errorf("estimated output count = %d, want 3", gateway.estimatedCount)
	}
	if reservation.BillingType != BillingTypeBalance || reservation.SubscriptionID != nil {
		t.Errorf("reservation billing = %d/%v, want balance/nil", reservation.BillingType, reservation.SubscriptionID)
	}
}

func TestImageJobBillingEstimateUsesExactImageCost(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{
		estimatedCost: &CostBreakdown{TotalCost: 0.6, ActualCost: 0.75, BillingMode: string(BillingModeImage)},
	}
	billing := NewImageJobBilling(gateway, 1)

	reservation, err := billing.Estimate(context.Background(), &APIKey{ID: 20, UserID: 10}, nil, ImageJobRequest{
		Model: "gpt-image-2", N: 3, Size: "1024x1024",
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if reservation.AmountUSD != 0.75 {
		t.Errorf("reservation amount = %v, want 0.75", reservation.AmountUSD)
	}
	if gateway.estimatedCount != 3 {
		t.Errorf("estimated output count = %d, want 3", gateway.estimatedCount)
	}
}

func TestImageJobBillingEstimateUsesOpenAIExactImagePricingPath(t *testing.T) {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	gateway := &OpenAIGatewayService{cfg: cfg, billingService: NewBillingService(cfg, nil)}
	billing := NewImageJobBilling(gateway, 1)
	unitPrice := 0.25

	reservation, err := billing.Estimate(context.Background(), &APIKey{
		ID: 20, UserID: 10, Group: &Group{ImagePrice1K: &unitPrice},
	}, nil, ImageJobRequest{Model: "gpt-image-2", N: 3, Size: "1K"})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if reservation.AmountUSD != 0.75 {
		t.Fatalf("reservation amount = %v, want 0.75", reservation.AmountUSD)
	}
}

func TestImageJobBillingEstimatePersistsSubscriptionMetadata(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{tokenPricing: true}
	billing := NewImageJobBilling(gateway, 1)
	subscription := &UserSubscription{ID: 81}

	reservation, err := billing.Estimate(context.Background(), &APIKey{
		ID: 20, UserID: 10, Group: &Group{SubscriptionType: SubscriptionTypeSubscription},
	}, subscription, ImageJobRequest{Model: "gpt-image-2", N: 1})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if reservation.BillingType != BillingTypeSubscription || reservation.SubscriptionID == nil || *reservation.SubscriptionID != 81 {
		t.Fatalf("reservation billing metadata = %d/%v, want subscription/81", reservation.BillingType, reservation.SubscriptionID)
	}
}

func TestImageJobBillingEstimateRejectsNonPositiveActualCost(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{
		estimatedCost: &CostBreakdown{TotalCost: 0.6, ActualCost: 0, BillingMode: string(BillingModeImage)},
	}
	billing := NewImageJobBilling(gateway, 1)

	_, err := billing.Estimate(context.Background(), &APIKey{ID: 20, UserID: 10}, nil, ImageJobRequest{
		Model: "gpt-image-2", N: 1,
	})
	if !errors.Is(err, ErrImageJobReservationUnavailable) {
		t.Fatalf("Estimate() error = %v, want reservation unavailable", err)
	}
}

func TestImageJobBillingEstimateRejectsImageCostAboveCap(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{
		estimatedCost: &CostBreakdown{TotalCost: 1.1, ActualCost: 1.1, BillingMode: string(BillingModeImage)},
	}
	billing := NewImageJobBilling(gateway, 1)

	_, err := billing.Estimate(context.Background(), &APIKey{ID: 20, UserID: 10}, nil, ImageJobRequest{
		Model: "gpt-image-2", N: 1,
	})
	if !errors.Is(err, ErrImageJobReservationUnavailable) {
		t.Fatalf("Estimate() error = %v, want reservation unavailable", err)
	}
}

func TestImageJobBillingSettlementSkipsBillingWithoutPersistedResult(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	reservations := &fakeImageJobReservationRepository{}
	billing := NewImageJobBilling(gateway, 1, reservations)

	cost, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job:                  &ImageJob{ID: 42, PublicID: "imgjob_empty"},
		PersistedResultCount: 0,
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if cost == nil || cost.ActualCost != 0 {
		t.Fatalf("Settle() cost = %#v, want zero cost", cost)
	}
	if gateway.recordCalls != 0 {
		t.Fatalf("RecordUsageWithCost() calls = %d, want 0", gateway.recordCalls)
	}
	if reservations.released[42] != 1 {
		t.Fatalf("released transitions = %d, want 1", reservations.released[42])
	}
}

func TestImageJobBillingSettlementUsesPersistedSubscription(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	billing := NewImageJobBilling(gateway, 1, &fakeImageJobReservationRepository{})
	user := &User{ID: 10}
	persistedSubscriptionID := int64(91)
	callerSubscription := &UserSubscription{ID: 92}

	_, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job: &ImageJob{
			PublicID: "imgjob_subscription", RequestedModel: "gpt-image-2", RequestDigest: "digest",
			ReservationBillingType: BillingTypeSubscription, ReservationSubscriptionID: &persistedSubscriptionID,
		},
		Execution: &ImageExecutionResult{
			Forward: &OpenAIForwardResult{Model: "gpt-image-2", ImageCount: 1},
			Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
		APIKey:               &APIKey{ID: 20, UserID: user.ID, User: user},
		User:                 user,
		Subscription:         callerSubscription,
		PersistedResultCount: 1,
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if gateway.lastUsageInput == nil || gateway.lastUsageInput.Subscription == nil || gateway.lastUsageInput.Subscription.ID != persistedSubscriptionID {
		t.Fatalf("settlement subscription = %#v, want persisted subscription %d", gateway.lastUsageInput, persistedSubscriptionID)
	}
}

func TestImageJobBillingSettlementBalanceIgnoresCallerSubscription(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	billing := NewImageJobBilling(gateway, 1, &fakeImageJobReservationRepository{})
	user := &User{ID: 10}

	_, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job: &ImageJob{
			PublicID: "imgjob_balance", RequestedModel: "gpt-image-2", RequestDigest: "digest",
			ReservationBillingType: BillingTypeBalance,
		},
		Execution: &ImageExecutionResult{
			Forward: &OpenAIForwardResult{Model: "gpt-image-2", ImageCount: 1},
			Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
		APIKey:               &APIKey{ID: 20, UserID: user.ID, User: user},
		User:                 user,
		Subscription:         &UserSubscription{ID: 92},
		PersistedResultCount: 1,
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if gateway.lastUsageInput == nil || gateway.lastUsageInput.Subscription != nil {
		t.Fatalf("balance settlement subscription = %#v, want nil", gateway.lastUsageInput)
	}
}

func TestImageJobBillingSettlementScalesTokenUsageToPersistedBatchResults(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	billing := NewImageJobBilling(gateway, 1, &fakeImageJobReservationRepository{})
	user := &User{ID: 10}

	_, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job: &ImageJob{
			PublicID: "imgjob_partial_tokens", Mode: "batch", RequestedCount: 4,
			RequestedModel: "gpt-image-2", RequestDigest: "digest", ReservationBillingType: BillingTypeBalance,
		},
		Execution: &ImageExecutionResult{
			Forward: &OpenAIForwardResult{
				Model: "gpt-image-2", ImageCount: 4,
				Usage: OpenAIUsage{
					InputTokens: 400, ImageInputTokens: 200, OutputTokens: 120,
					CacheCreationInputTokens: 80, CacheReadInputTokens: 40, ImageOutputTokens: 1000,
				},
			},
			Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
		APIKey:               &APIKey{ID: 20, UserID: user.ID, User: user},
		User:                 user,
		PersistedResultCount: 2,
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if gateway.lastUsageInput == nil || gateway.lastUsageInput.Result == nil {
		t.Fatal("Settle() did not record usage")
	}
	got := gateway.lastUsageInput.Result
	if got.ImageCount != 2 || got.Usage.InputTokens != 200 || got.Usage.ImageInputTokens != 100 ||
		got.Usage.OutputTokens != 60 || got.Usage.CacheCreationInputTokens != 40 ||
		got.Usage.CacheReadInputTokens != 20 || got.Usage.ImageOutputTokens != 500 {
		t.Fatalf("partial usage = count=%d %#v, want half of four-result usage", got.ImageCount, got.Usage)
	}
}

func TestImageJobBillingRepeatedSettlementUsesUsageBillingDedup(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &deduplicatingImageUsageBillingRepo{seen: make(map[string]string)}
	gateway := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	reservations := &fakeImageJobReservationRepository{}
	billing := NewImageJobBilling(gateway, 1, reservations)
	user := &User{ID: 10}
	input := ImageJobSettlementInput{
		Job: &ImageJob{
			ID: 43, PublicID: "imgjob_real_dedup", Mode: "batch", RequestedCount: 1,
			RequestedModel: "gpt-image-2", RequestDigest: "digest", ReservationBillingType: BillingTypeBalance,
		},
		Execution: &ImageExecutionResult{
			Forward: &OpenAIForwardResult{Model: "gpt-image-2", ImageCount: 1},
			Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		},
		APIKey:               &APIKey{ID: 20, UserID: user.ID, User: user},
		User:                 user,
		PersistedResultCount: 1,
	}
	if _, err := billing.Settle(context.Background(), input); err != nil {
		t.Fatalf("first Settle() error = %v", err)
	}
	if _, err := billing.Settle(context.Background(), input); err != nil {
		t.Fatalf("second Settle() error = %v", err)
	}
	if billingRepo.calls != 2 || billingRepo.applied != 1 || billingRepo.lastRequestID != "imgjob_real_dedup:0" {
		t.Fatalf("billing dedup = calls:%d applied:%d request:%q, want 2/1/imgjob_real_dedup:0", billingRepo.calls, billingRepo.applied, billingRepo.lastRequestID)
	}
}

type fakeImageJobBillingGateway struct {
	tokenPricing     bool
	estimatedCost    *CostBreakdown
	estimatedCount   int
	applied          map[string]int
	recordCalls      int
	billingRequestID string
	lastUsageInput   *OpenAIRecordUsageInput
}

type fakeImageJobReservationRepository struct {
	settled  map[int64]int
	released map[int64]int
	err      error
}

type deduplicatingImageUsageBillingRepo struct {
	seen          map[string]string
	calls         int
	applied       int
	lastRequestID string
}

func (r *deduplicatingImageUsageBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.calls++
	cmd.Normalize()
	r.lastRequestID = cmd.RequestID
	key := fmt.Sprintf("%s:%d", cmd.RequestID, cmd.APIKeyID)
	if fingerprint, ok := r.seen[key]; ok {
		if fingerprint != cmd.RequestFingerprint {
			return nil, ErrUsageBillingRequestConflict
		}
		return &UsageBillingApplyResult{Applied: false}, nil
	}
	r.seen[key] = cmd.RequestFingerprint
	r.applied++
	return &UsageBillingApplyResult{Applied: true}, nil
}

func (f *fakeImageJobReservationRepository) SettleReservation(_ context.Context, jobID int64) error {
	if f.settled == nil {
		f.settled = make(map[int64]int)
	}
	f.settled[jobID]++
	return f.err
}

func (f *fakeImageJobReservationRepository) ReleaseReservation(_ context.Context, jobID int64) error {
	if f.released == nil {
		f.released = make(map[int64]int)
	}
	f.released[jobID]++
	return f.err
}

func (f *fakeImageJobBillingGateway) EstimateImageJobCost(_ context.Context, _ *APIKey, _ ImageJobRequest, count int) (*CostBreakdown, bool, error) {
	f.estimatedCount = count
	if f.estimatedCost == nil {
		f.estimatedCost = &CostBreakdown{TotalCost: 0.5, ActualCost: 0.5, BillingMode: string(BillingModeImage)}
	}
	return f.estimatedCost, f.tokenPricing, nil
}

func (f *fakeImageJobBillingGateway) RecordUsageWithCost(_ context.Context, input *OpenAIRecordUsageInput) (*CostBreakdown, error) {
	f.recordCalls++
	f.billingRequestID = input.BillingRequestID
	f.lastUsageInput = input
	requestID := input.Result.RequestID
	if f.applied == nil {
		f.applied = make(map[string]int)
	}
	if f.applied[requestID] == 0 {
		f.applied[requestID]++
	}
	return &CostBreakdown{TotalCost: 0.25, ActualCost: 0.25, BillingMode: string(BillingModeImage)}, nil
}
