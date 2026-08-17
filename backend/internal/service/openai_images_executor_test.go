package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImageExecutorFailsOverBeforeAnyFinalImage(t *testing.T) {
	executor, state := newImageExecutorPolicyFixture()
	executor.forward = func(ctx context.Context, input ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
		state.recordForward(account.ID)
		if account.ID == 101 {
			return nil, &UpstreamFailoverError{StatusCode: 500, ResponseBody: []byte(`{"error":{"message":"temporary"}}`)}
		}
		for index := 0; index < 4; index++ {
			require.NoError(t, sink.Final(ctx, ImageArtifact{Index: index, Data: []byte(fmt.Sprintf("image-%d", index)), MIMEType: "image/png"}))
		}
		forward := &OpenAIForwardResult{Model: input.Parsed.Model, ImageCount: 4}
		require.NoError(t, sink.Complete(ctx, ImageExecutionSummary{ForwardResult: forward}))
		return forward, nil
	}

	sink := NewCollectingImageResultSink()
	result, err := executor.Execute(context.Background(), imageExecutionPolicyInput(4), sink)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(202), result.Account.ID)
	require.Equal(t, []int64{101, 202}, state.forwardedAccounts())
	require.Equal(t, 2, state.releaseCount())
	require.Len(t, sink.Results(), 4)
}

func TestOpenAIImageExecutorDoesNotFailOverAfterFinalImage(t *testing.T) {
	executor, state := newImageExecutorPolicyFixture()
	executor.forward = func(ctx context.Context, _ ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
		state.recordForward(account.ID)
		require.NoError(t, sink.Final(ctx, ImageArtifact{Index: 0, Data: []byte("final"), MIMEType: "image/png"}))
		return &OpenAIForwardResult{ImageCount: 1}, errors.New("stream ended after final image")
	}

	sink := NewCollectingImageResultSink()
	result, err := executor.Execute(context.Background(), imageExecutionPolicyInput(4), sink)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, []int64{101}, state.forwardedAccounts())
	require.Equal(t, 1, state.releaseCount())
	require.Len(t, sink.Results(), 1)
}

func TestOpenAIImageExecutorDoesNotFailOverWhenFinalSinkFails(t *testing.T) {
	executor, state := newImageExecutorPolicyFixture()
	executor.forward = func(ctx context.Context, _ ImageExecutionInput, account *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
		state.recordForward(account.ID)
		if err := sink.Final(ctx, ImageArtifact{Index: 0, Data: []byte("final"), MIMEType: "image/png"}); err != nil {
			return nil, err
		}
		return &OpenAIForwardResult{ImageCount: 1}, nil
	}

	_, err := executor.Execute(context.Background(), imageExecutionPolicyInput(4), failingFinalImageSink{})
	require.Error(t, err)
	require.Equal(t, []int64{101}, state.forwardedAccounts())
	require.Equal(t, 1, state.releaseCount())
}

func TestImageExecutionErrorSanitizesAndTruncatesMessage(t *testing.T) {
	raw := "https://example.test/image?access_token=secret&x=" + strings.Repeat("x", 600)
	err := newImageExecutionError(502, "upstream_error", "upstream_error", raw, true, false, errors.New(raw))
	require.LessOrEqual(t, len(err.Message), 500)
	require.NotContains(t, err.Message, "secret")
}

func TestOpenAIImageExecutorSharesImageSlotsWithOtherEndpoints(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageConcurrency = config.ImageConcurrencyConfig{
		Enabled: true, MaxConcurrentRequests: 1,
		OverflowMode: config.ImageConcurrencyOverflowModeReject,
	}
	executor := NewOpenAIImageExecutor(&OpenAIGatewayService{}, nil, cfg)
	release, err := executor.AcquireImageSlot(context.Background())
	require.NoError(t, err)
	require.NotNil(t, release)
	defer release()

	blockedRelease, err := executor.AcquireImageSlot(context.Background())
	require.Error(t, err)
	require.Nil(t, blockedRelease)
	var executionErr *ImageExecutionError
	require.ErrorAs(t, err, &executionErr)
	require.Equal(t, "image_concurrency_limit", executionErr.Code)
}

func TestOpenAIImageExecutorBindsGeneratedPoolSession(t *testing.T) {
	cache := &imageExecutorStickyCache{}
	gateway := &OpenAIGatewayService{cache: cache}
	executor := NewOpenAIImageExecutor(gateway, nil, nil)
	account := &Account{
		ID: 101, Type: AccountTypeAPIKey, Platform: PlatformOpenAI,
		Credentials: map[string]any{"pool_mode": true},
	}
	executor.selectAccount = func(_ context.Context, _ *int64, sessionHash string, _ string, _ map[int64]struct{}, _ OpenAIImagesCapability) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		require.Empty(t, sessionHash)
		return &AccountSelectionResult{Account: account, Acquired: true, ReleaseFunc: func() {}}, OpenAIAccountScheduleDecision{}, nil
	}
	var forwardedSession string
	executor.forward = func(ctx context.Context, input ImageExecutionInput, _ *Account, sink ImageResultSink) (*OpenAIForwardResult, error) {
		forwardedSession = input.SessionHash
		result := &OpenAIForwardResult{Model: input.Parsed.Model}
		require.NoError(t, sink.Complete(ctx, ImageExecutionSummary{ForwardResult: result}))
		return result, nil
	}

	_, err := executor.Execute(context.Background(), imageExecutionPolicyInput(1), NewCollectingImageResultSink())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(forwardedSession, "openai-image-pool-retry-"))
	require.Equal(t, map[string]int64{"openai:" + forwardedSession: account.ID}, cache.bindings())
}

type imageExecutorPolicyState struct {
	mu       sync.Mutex
	forwards []int64
	releases int
}

type imageExecutorStickyCache struct {
	mu       sync.Mutex
	accounts map[string]int64
}

func (c *imageExecutorStickyCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("not found")
}

func (c *imageExecutorStickyCache) SetSessionAccountID(_ context.Context, _ int64, sessionHash string, accountID int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accounts == nil {
		c.accounts = make(map[string]int64)
	}
	c.accounts[sessionHash] = accountID
	return nil
}

func (*imageExecutorStickyCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (*imageExecutorStickyCache) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (c *imageExecutorStickyCache) bindings() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]int64, len(c.accounts))
	for key, accountID := range c.accounts {
		result[key] = accountID
	}
	return result
}

func newImageExecutorPolicyFixture() (*OpenAIImageExecutor, *imageExecutorPolicyState) {
	state := &imageExecutorPolicyState{}
	executor := NewOpenAIImageExecutor(&OpenAIGatewayService{}, nil, nil)
	executor.maxAccountSwitches = 2
	accounts := []*Account{
		{ID: 101, Type: AccountTypeAPIKey, Platform: PlatformOpenAI},
		{ID: 202, Type: AccountTypeAPIKey, Platform: PlatformOpenAI},
	}
	executor.selectAccount = func(_ context.Context, _ *int64, _ string, _ string, excluded map[int64]struct{}, _ OpenAIImagesCapability) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		for _, account := range accounts {
			if _, skip := excluded[account.ID]; skip {
				continue
			}
			return &AccountSelectionResult{
				Account:  account,
				Acquired: true,
				ReleaseFunc: func() {
					state.mu.Lock()
					state.releases++
					state.mu.Unlock()
				},
			}, OpenAIAccountScheduleDecision{}, nil
		}
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	}
	return executor, state
}

func imageExecutionPolicyInput(n int) ImageExecutionInput {
	groupID := int64(1)
	return ImageExecutionInput{
		APIKey: &APIKey{ID: 2, UserID: 3, GroupID: &groupID},
		Parsed: &OpenAIImagesRequest{
			Endpoint:           openAIImagesGenerationsEndpoint,
			Model:              "gpt-image-2",
			N:                  n,
			RequiredCapability: OpenAIImagesCapabilityNative,
		},
	}
}

func (s *imageExecutorPolicyState) recordForward(accountID int64) {
	s.mu.Lock()
	s.forwards = append(s.forwards, accountID)
	s.mu.Unlock()
}

func (s *imageExecutorPolicyState) forwardedAccounts() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.forwards...)
}

func (s *imageExecutorPolicyState) releaseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releases
}

type failingFinalImageSink struct{}

func (failingFinalImageSink) Partial(context.Context, int, []byte, string) error { return nil }
func (failingFinalImageSink) Final(context.Context, ImageArtifact) error {
	return errors.New("result store failed")
}
func (failingFinalImageSink) Complete(context.Context, ImageExecutionSummary) error { return nil }
