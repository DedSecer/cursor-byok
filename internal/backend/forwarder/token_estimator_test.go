package forwarder

import (
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestUsageEstimateAccumulatorEstimatesMissingProviderUsage(t *testing.T) {
	accumulator := newUsageEstimateAccumulator(ProviderRequest{
		Messages: []modeladapter.Message{{Role: "user", Content: "Explain this implementation"}},
	})
	accumulator.decorate(modeladapter.ModelEvent{
		Kind: modeladapter.ModelEventKindTextDelta,
		Text: "The implementation uses a shared provider gateway.",
	})

	result := accumulator.decorate(modeladapter.ModelEvent{
		Kind: modeladapter.ModelEventKindTurnFinished,
	})

	if result.UsageStatus != modeladapter.UsageStatusEstimated {
		t.Fatalf("expected estimated usage, got %q", result.UsageStatus)
	}
	if result.InputTokens <= 0 || result.OutputTokens <= 0 {
		t.Fatalf("expected positive estimated tokens, got input=%d output=%d", result.InputTokens, result.OutputTokens)
	}
	if result.UsagePresent {
		t.Fatal("estimated usage must not be marked as provider-reported")
	}
}

func TestUsageEstimateAccumulatorPreservesReportedUsage(t *testing.T) {
	accumulator := newUsageEstimateAccumulator(ProviderRequest{
		Messages: []modeladapter.Message{{Role: "user", Content: "hello"}},
	})

	result := accumulator.decorate(modeladapter.ModelEvent{
		Kind:            modeladapter.ModelEventKindTurnFinished,
		InputTokens:     123,
		OutputTokens:    45,
		UsagePresent:    true,
		CacheReadTokens: 67,
	})

	if result.UsageStatus != modeladapter.UsageStatusReported {
		t.Fatalf("expected reported usage, got %q", result.UsageStatus)
	}
	if result.InputTokens != 123 || result.OutputTokens != 45 || result.CacheReadTokens != 67 {
		t.Fatalf("reported usage was modified: %+v", result)
	}
}

func TestUsageEstimateAccumulatorKeepsUnobservableUsageMissing(t *testing.T) {
	accumulator := newUsageEstimateAccumulator(ProviderRequest{})

	result := accumulator.decorate(modeladapter.ModelEvent{
		Kind: modeladapter.ModelEventKindTurnFinished,
	})

	if result.UsageStatus != modeladapter.UsageStatusMissing {
		t.Fatalf("expected missing usage, got %q", result.UsageStatus)
	}
	if result.InputTokens != 0 || result.OutputTokens != 0 {
		t.Fatalf("missing usage must remain zero-valued: %+v", result)
	}
}
