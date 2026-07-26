package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	serverconfig "cursor/internal/backend/server/config"
)

const modelTestPrompt = "Output the numbers 1 through 120 separated by a single space. No commas, no newlines, no explanation."

type ModelTestResult struct {
	AdapterID        string  `json:"adapterId"`
	Status           string  `json:"status"`
	TokensPerSecond  float64 `json:"tokensPerSecond"`
	FirstTextTokenMS int64   `json:"firstTextTokenMs"`
	TotalDurationMS  int64   `json:"totalDurationMs"`
	OutputTokens     int64   `json:"outputTokens"`
	Error            string  `json:"error,omitempty"`
}

func (host *Host) TestModel(ctx context.Context, adapter serverconfig.ModelAdapterConfig) (ModelTestResult, error) {
	normalized, err := serverconfig.NormalizeModelAdapterConfigs([]serverconfig.ModelAdapterConfig{adapter})
	if err != nil {
		return ModelTestResult{Status: "error", Error: err.Error()}, err
	}
	if len(normalized) != 1 {
		err := errors.New("exactly one model adapter is required")
		return ModelTestResult{Status: "error", Error: err.Error()}, err
	}
	adapter = normalized[0]
	result := ModelTestResult{AdapterID: adapter.ID, Status: "running"}
	startedAt := time.Now()
	var firstTextAt time.Time
	var outputTokens int64
	request := modeladapter.StreamRequest{
		RequestID:                   "cc-switch-model-test-" + adapter.ID,
		RunID:                       "cc-switch-model-test-" + adapter.ID,
		ModelCallID:                 "cc-switch-model-test-" + adapter.ID,
		ModelID:                     adapter.ID,
		Provider:                    adapter.Type,
		BaseURL:                     adapter.BaseURL,
		APIKey:                      adapter.APIKey,
		ProviderModelID:             adapter.ModelID,
		PricingModel:                adapter.PricingModel,
		ResolvedChannelID:           adapter.ID,
		ResolvedChannelName:         adapter.DisplayName,
		ResolvedContextWindowTokens: adapter.ContextWindowTokens,
		ReasoningEffort:             adapter.ReasoningEffort,
		OpenAIEndpoint:              adapter.OpenAIEndpoint,
		OpenAIExtraParamsEnabled:    adapter.OpenAIExtraParamsEnabled,
		OpenAIExtraParamsJSON:       adapter.OpenAIExtraParamsJSON,
		CustomHeadersEnabled:        adapter.CustomHeadersEnabled,
		CustomHeadersJSON:           adapter.CustomHeadersJSON,
		AnthropicExtraParamsEnabled: adapter.AnthropicExtraParamsEnabled,
		AnthropicExtraParamsJSON:    adapter.AnthropicExtraParamsJSON,
		AnthropicMaxTokens:          adapter.AnthropicMaxTokens,
		AnthropicThinkingEffort:     adapter.AnthropicThinkingEffort,
		ThinkingBudgetTokens:        adapter.ThinkingBudgetTokens,
		Messages:                    []modeladapter.Message{{Role: "user", Content: modelTestPrompt}},
		MaxTokens:                   testMaxTokens(adapter),
		Stream:                      true,
		RequestKnobs:                map[string]any{"stream": true},
		ProviderStreamIdleTimeout:   45 * time.Second,
	}
	var provider modeladapter.ModelAdapter
	switch adapter.Type {
	case "openai":
		provider = modeladapter.NewOpenAIAdapter()
	case "anthropic":
		provider = modeladapter.NewAnthropicAdapter()
	default:
		err := fmt.Errorf("unsupported provider %q", adapter.Type)
		return ModelTestResult{AdapterID: adapter.ID, Status: "error", Error: err.Error()}, err
	}
	err = provider.Stream(ctx, request, func(event modeladapter.ModelEvent) error {
		if event.Kind == modeladapter.ModelEventKindTextDelta && strings.TrimSpace(event.Text) != "" && firstTextAt.IsZero() {
			firstTextAt = time.Now()
		}
		if event.OutputTokens > outputTokens {
			outputTokens = event.OutputTokens
		}
		return nil
	})
	finishedAt := time.Now()
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.TotalDurationMS = finishedAt.Sub(startedAt).Milliseconds()
		return result, err
	}
	if firstTextAt.IsZero() {
		err = errors.New("provider returned no text output")
		result.Status = "error"
		result.Error = err.Error()
		result.TotalDurationMS = finishedAt.Sub(startedAt).Milliseconds()
		return result, err
	}
	result.Status = "success"
	result.FirstTextTokenMS = firstTextAt.Sub(startedAt).Milliseconds()
	result.TotalDurationMS = finishedAt.Sub(startedAt).Milliseconds()
	result.OutputTokens = outputTokens
	if outputTokens > 0 && finishedAt.After(firstTextAt) {
		result.TokensPerSecond = float64(outputTokens) / finishedAt.Sub(firstTextAt).Seconds()
	}
	return result, nil
}

func testMaxTokens(adapter serverconfig.ModelAdapterConfig) int {
	if adapter.Type == "anthropic" && adapter.AnthropicMaxTokens > 0 {
		return adapter.AnthropicMaxTokens
	}
	if adapter.MaxCompletionTokens > 0 {
		return adapter.MaxCompletionTokens
	}
	return 4096
}
