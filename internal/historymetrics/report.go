package historymetrics

type Summary struct {
	ProviderCallsTotal       int      `json:"providerCallsTotal"`
	TurnsTotal               int      `json:"turnsTotal"`
	ValidTurnsTotal          int      `json:"validTurnsTotal"`
	InvalidTurnsTotal        int      `json:"invalidTurnsTotal"`
	RequestTokensTotal       int64    `json:"requestTokensTotal"`
	PromptTokensTotal        int64    `json:"promptTokensTotal"`
	CacheReadTokens          int64    `json:"cacheReadTokens"`
	CacheWriteTokens         int64    `json:"cacheWriteTokens"`
	CacheObservedCalls       int      `json:"cacheObservedCalls"`
	CacheObservedInputTokens int64    `json:"cacheObservedInputTokens"`
	CacheObservedReadTokens  int64    `json:"cacheObservedReadTokens"`
	CacheObservedWriteTokens int64    `json:"cacheObservedWriteTokens"`
	CacheObservationPartial  bool     `json:"cacheObservationPartial"`
	CacheHitRate             *float64 `json:"cacheHitRate"`
}

type Totals struct {
	InputTokens              int64
	OutputTokens             int64
	CacheReadTokens          int64
	CacheWriteTokens         int64
	PromptTokensTotal        int64
	RequestTokensTotal       int64
	CacheObservedCalls       int
	CacheObservedInputTokens int64
	CacheObservedReadTokens  int64
	CacheObservedWriteTokens int64
}

func cacheHitRateFromTotals(totals Totals) *float64 {
	if totals.CacheObservedCalls <= 0 {
		return nil
	}
	inputCacheTokensTotal := totals.CacheObservedReadTokens + totals.CacheObservedInputTokens
	value := 0.0
	if inputCacheTokensTotal > 0 {
		value = float64(totals.CacheObservedReadTokens) / float64(inputCacheTokensTotal)
	}
	return &value
}
