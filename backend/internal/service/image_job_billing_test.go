package service

import (
	"context"
	"errors"
	"testing"
)

func TestImageJobBillingSettlementUsesStableRequestID(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{applied: make(map[string]int)}
	billing := NewImageJobBilling(gateway, 1)
	user := &User{ID: 10}
	apiKey := &APIKey{ID: 20, UserID: user.ID, User: user}
	execution := &ImageExecutionResult{
		Forward: &OpenAIForwardResult{RequestID: "upstream-changing", Model: "gpt-image-2", ImageCount: 1},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}
	input := ImageJobSettlementInput{
		Job:                  &ImageJob{PublicID: "imgjob_x", RequestedModel: "gpt-image-2", RequestDigest: "digest"},
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
	if execution.Forward.RequestID != "upstream-changing" {
		t.Errorf("Settle() mutated upstream result request ID = %q", execution.Forward.RequestID)
	}
}

func TestImageJobBillingSequenceSettlementUsesFrameIndex(t *testing.T) {
	gateway := &fakeImageJobBillingGateway{}
	billing := NewImageJobBilling(gateway, 1)
	user := &User{ID: 10}

	_, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job: &ImageJob{
			PublicID: "imgjob_sequence", Mode: "sequence",
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
	billing := NewImageJobBilling(gateway, 1)

	cost, err := billing.Settle(context.Background(), ImageJobSettlementInput{
		Job:                  &ImageJob{PublicID: "imgjob_empty"},
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
}

type fakeImageJobBillingGateway struct {
	tokenPricing     bool
	estimatedCost    *CostBreakdown
	estimatedCount   int
	applied          map[string]int
	recordCalls      int
	billingRequestID string
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
	requestID := input.Result.RequestID
	if f.applied == nil {
		f.applied = make(map[string]int)
	}
	if f.applied[requestID] == 0 {
		f.applied[requestID]++
	}
	return &CostBreakdown{TotalCost: 0.25, ActualCost: 0.25, BillingMode: string(BillingModeImage)}, nil
}
