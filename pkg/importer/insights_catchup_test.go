package importer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thingzio/devpulse/pkg/data"
)

// TestRunCatchUpInsights_NilLLMConfig — when no LLM is configured the
// catch-up must be a hard no-op. Passes a nil store to prove no DB calls
// happen before the llmCfg gate.
func TestRunCatchUpInsights_NilLLMConfig(t *testing.T) {
	t.Parallel()
	runCatchUpInsights(context.Background(), nil, nil, "pro", "org", "repo")
	// No panic, no assertion needed beyond "did not deref the nil store".
}

// TestRunCatchUpInsights_FreePlan — Free tier has AILevel=0, so the gate
// must return before the store is touched. Nil store proves it.
func TestRunCatchUpInsights_FreePlan(t *testing.T) {
	t.Parallel()
	llmCfg := &data.LLMConfig{}
	runCatchUpInsights(context.Background(), nil, llmCfg, "free", "org", "repo")
}

// TestRunCatchUpInsights_FreshInsights — Pro plan with insights generated
// today must not trigger a regeneration (the age gate inside
// checkInsightStaleness returns false). Verifies the catch-up delegates
// correctly to generateRepoInsights' staleness gates.
func TestRunCatchUpInsights_FreshInsights(t *testing.T) {
	t.Parallel()
	mock := &backfillMockStore{
		insightsGeneratedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		insightsSavedCount:  100,
		insightsSummary:     &data.InsightsSummary{Events: 105}, // 5% delta, irrelevant — age gate blocks first
	}
	llmCfg := &data.LLMConfig{}

	runCatchUpInsights(context.Background(), mock, llmCfg, "pro", "org", "repo")

	assert.False(t, mock.saveInsightsCalled, "fresh insights must not be regenerated")
}

// TestRunCatchUpInsights_SmallDelta — insights are old enough but the
// event-count delta is below threshold. checkInsightStaleness blocks
// regeneration; SaveRepoInsights must not be called.
func TestRunCatchUpInsights_SmallDelta(t *testing.T) {
	t.Parallel()
	mock := &backfillMockStore{
		insightsGeneratedAt: time.Now().Add(-15 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05Z"),
		insightsSavedCount:  100,
		insightsSummary:     &data.InsightsSummary{Events: 105}, // 5% delta — below 10% threshold
	}
	llmCfg := &data.LLMConfig{}

	runCatchUpInsights(context.Background(), mock, llmCfg, "pro", "org", "repo")

	assert.False(t, mock.saveInsightsCalled, "small delta must not trigger regen")
}
