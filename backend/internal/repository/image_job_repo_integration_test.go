//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestImageJobRepositoryCreateReservedIdempotency(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	created, replay, err := repo.CreateReserved(context.Background(), fixture.create("hash-a", "idem-a"))
	if err != nil || !created || replay != nil {
		t.Fatalf("first CreateReserved() = %t, %#v, %v", created, replay, err)
	}
	created, replay, err = repo.CreateReserved(context.Background(), fixture.create("hash-a", "idem-a"))
	if err != nil || created || replay == nil {
		t.Fatalf("replay CreateReserved() = %t, %#v, %v", created, replay, err)
	}
	if _, _, err := repo.CreateReserved(context.Background(), fixture.create("hash-b", "idem-a")); !errors.Is(err, service.ErrImageJobIdempotencyConflict) {
		t.Fatalf("conflicting CreateReserved() error = %v, want idempotency conflict", err)
	}
}

func TestImageJobRepositoryClaimNextOnlyOnce(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	created, _, err := repo.CreateReserved(context.Background(), fixture.create("claim-digest", ""))
	if err != nil || !created {
		t.Fatalf("CreateReserved() created = %t, error = %v", created, err)
	}

	claims := make(chan *service.ImageJobClaim, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			claim, err := repo.ClaimNext(context.Background(), fmt.Sprintf("worker-%d", index), fmt.Sprintf("attempt-%d", index))
			claims <- claim
			errs <- err
		}(i)
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ClaimNext() error = %v", err)
		}
	}
	nonNil := 0
	for claim := range claims {
		if claim != nil {
			nonNil++
		}
	}
	if nonNil != 1 {
		t.Fatalf("non-nil claims = %d, want 1", nonNil)
	}
}

func TestImageJobRepositoryCreateReservedRejectsHeldBalanceOvercommit(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	if _, err := integrationDB.ExecContext(context.Background(), "UPDATE users SET balance = 1.00 WHERE id = $1", fixture.userID); err != nil {
		t.Fatalf("set user balance: %v", err)
	}
	first := fixture.create("reserve-a", "")
	first.ReservedUSD = 0.75
	first.ReservationBillingType = service.BillingTypeBalance
	created, _, err := repo.CreateReserved(context.Background(), first)
	if err != nil || !created {
		t.Fatalf("first CreateReserved() created = %t, error = %v", created, err)
	}
	second := fixture.create("reserve-b", "")
	second.ReservedUSD = 0.50
	second.ReservationBillingType = service.BillingTypeBalance
	if _, _, err := repo.CreateReserved(context.Background(), second); !errors.Is(err, service.ErrImageJobReservationInsufficient) {
		t.Fatalf("second CreateReserved() error = %v, want reservation insufficient", err)
	}
}

func TestImageJobRepositoryCreateReservedReplayDoesNotDoubleReserve(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	if _, err := integrationDB.ExecContext(context.Background(), "UPDATE users SET balance = 0.75 WHERE id = $1", fixture.userID); err != nil {
		t.Fatalf("set user balance: %v", err)
	}
	first := fixture.create("same-reserve", "same-idempotency")
	first.ReservedUSD = 0.75
	first.ReservationBillingType = service.BillingTypeBalance
	created, _, err := repo.CreateReserved(context.Background(), first)
	if err != nil || !created {
		t.Fatalf("first CreateReserved() created = %t, error = %v", created, err)
	}
	replay := fixture.create("same-reserve", "same-idempotency")
	replay.ReservedUSD = 0.75
	replay.ReservationBillingType = service.BillingTypeBalance
	created, existing, err := repo.CreateReserved(context.Background(), replay)
	if err != nil || created || existing == nil {
		t.Fatalf("replay CreateReserved() = %t, %#v, %v", created, existing, err)
	}
}

func TestImageJobRepositoryCreateReservedRejectsHeldAPIKeyQuotaOvercommit(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	if _, err := integrationDB.ExecContext(context.Background(),
		"UPDATE api_keys SET quota = 1.00, quota_used = 0.20 WHERE id = $1", fixture.apiKeyID); err != nil {
		t.Fatalf("set API key quota: %v", err)
	}
	first := fixture.create("quota-reserve-a", "")
	first.ReservedUSD = 0.50
	first.ReservationBillingType = service.BillingTypeBalance
	created, _, err := repo.CreateReserved(context.Background(), first)
	if err != nil || !created {
		t.Fatalf("first CreateReserved() created = %t, error = %v", created, err)
	}
	second := fixture.create("quota-reserve-b", "")
	second.ReservedUSD = 0.40
	second.ReservationBillingType = service.BillingTypeBalance
	if _, _, err := repo.CreateReserved(context.Background(), second); !errors.Is(err, service.ErrImageJobReservationInsufficient) {
		t.Fatalf("second CreateReserved() error = %v, want reservation insufficient", err)
	}
}

func TestImageJobRepositoryCreateReservedRejectsHeldSubscriptionOvercommit(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	if _, err := integrationDB.ExecContext(context.Background(),
		"UPDATE groups SET daily_limit_usd = 1.00 WHERE id = $1", fixture.groupID); err != nil {
		t.Fatalf("set subscription daily limit: %v", err)
	}
	subscription := mustCreateSubscription(t, integrationEntClient, &service.UserSubscription{
		UserID: fixture.userID, GroupID: fixture.groupID, DailyUsageUSD: 0.10,
	})
	first := fixture.create("subscription-reserve-a", "")
	first.ReservedUSD = 0.60
	first.ReservationBillingType = service.BillingTypeSubscription
	first.ReservationSubscriptionID = &subscription.ID
	created, _, err := repo.CreateReserved(context.Background(), first)
	if err != nil || !created {
		t.Fatalf("first CreateReserved() created = %t, error = %v", created, err)
	}
	second := fixture.create("subscription-reserve-b", "")
	second.ReservedUSD = 0.40
	second.ReservationBillingType = service.BillingTypeSubscription
	second.ReservationSubscriptionID = &subscription.ID
	if _, _, err := repo.CreateReserved(context.Background(), second); !errors.Is(err, service.ErrImageJobReservationInsufficient) {
		t.Fatalf("second CreateReserved() error = %v, want reservation insufficient", err)
	}
}

type imageJobIntegrationFixture struct {
	userID   int64
	apiKeyID int64
	groupID  int64
	sequence int
}

func newImageJobIntegrationRepo(t *testing.T) (service.ImageJobRepository, *imageJobIntegrationFixture) {
	t.Helper()
	stamp := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: fmt.Sprintf("image-job-group-%d", stamp), Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("image-job-%d@example.test", stamp)})
	groupID := group.ID
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: user.ID, GroupID: &groupID, Key: fmt.Sprintf("sk-image-job-%d", stamp)})
	if _, err := integrationDB.ExecContext(context.Background(), "UPDATE users SET balance = 100 WHERE id = $1", user.ID); err != nil {
		t.Fatalf("seed image job user balance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM image_jobs WHERE api_key_id = $1", apiKey.ID)
	})
	return NewImageJobRepository(integrationEntClient, integrationDB), &imageJobIntegrationFixture{
		userID: user.ID, apiKeyID: apiKey.ID, groupID: group.ID,
	}
}

func (f *imageJobIntegrationFixture) create(digest, idempotency string) *service.ImageJobCreate {
	f.sequence++
	var idempotencyHash *string
	if idempotency != "" {
		idempotencyHash = &idempotency
	}
	return &service.ImageJobCreate{
		PublicID:           fmt.Sprintf("imgjob_integration_%d", f.sequence),
		UserID:             f.userID,
		APIKeyID:           f.apiKeyID,
		GroupID:            f.groupID,
		Endpoint:           "/v1/images/generations",
		Operation:          "generation",
		Mode:               "batch",
		RequestedModel:     "gpt-image-2",
		RequestedCount:     4,
		Request:            service.ImageJobRequest{Endpoint: "/v1/images/generations", Model: "gpt-image-2", Prompt: "test", N: 4},
		RequestDigest:      digest,
		IdempotencyKeyHash: idempotencyHash,
		ReservedUSD:        1,
		ExpiresAt:          time.Now().Add(time.Hour),
	}
}
