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
