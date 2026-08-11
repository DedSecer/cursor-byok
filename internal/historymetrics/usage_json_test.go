package historymetrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsageSummaryUsesObservableCacheSubset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	body := []byte(`{
		"totals": {
			"provider_calls": 2,
			"input_tokens": 140,
			"output_tokens": 20,
			"cache_read_tokens": 60,
			"cache_write_tokens": 0,
			"total_tokens": 220,
			"cache_observed_calls": 1,
			"cache_observed_input_tokens": 40,
			"cache_observed_read_tokens": 60,
			"cache_observed_write_tokens": 0
		}
	}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write usage fixture: %v", err)
	}

	summary, err := LoadUsageSummary(path)
	if err != nil {
		t.Fatalf("load usage summary: %v", err)
	}
	if summary.CacheHitRate == nil || *summary.CacheHitRate != 0.6 {
		t.Fatalf("expected observable cache hit rate 0.6, got %v", summary.CacheHitRate)
	}
	if !summary.CacheObservationPartial || summary.CacheObservedCalls != 1 {
		t.Fatalf("expected partial cache observation: %+v", summary)
	}
}

func TestLoadUsageSummaryReturnsZeroForObservableZeroTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	body := []byte(`{
		"totals": {
			"provider_calls": 1,
			"output_tokens": 20,
			"total_tokens": 20,
			"cache_observed_calls": 1,
			"cache_observed_input_tokens": 0,
			"cache_observed_read_tokens": 0
		}
	}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write usage fixture: %v", err)
	}

	summary, err := LoadUsageSummary(path)
	if err != nil {
		t.Fatalf("load usage summary: %v", err)
	}
	if summary.CacheHitRate == nil || *summary.CacheHitRate != 0 {
		t.Fatalf("observable zero cache usage must produce 0%%, got %v", summary.CacheHitRate)
	}
}

func TestLoadUsageSummaryReturnsNilWithoutObservableCacheUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	body := []byte(`{
		"totals": {
			"provider_calls": 1,
			"input_tokens": 100,
			"output_tokens": 20,
			"total_tokens": 120
		}
	}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write usage fixture: %v", err)
	}

	summary, err := LoadUsageSummary(path)
	if err != nil {
		t.Fatalf("load usage summary: %v", err)
	}
	if summary.CacheHitRate != nil || summary.CacheObservationPartial {
		t.Fatalf("unobservable cache usage must remain unavailable: %+v", summary)
	}
}
