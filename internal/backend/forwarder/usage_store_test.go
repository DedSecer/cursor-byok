package forwarder

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestUsageJournalCompactsOnlyConfirmedEvents(t *testing.T) {
	store := NewUsageFileStore(t.TempDir())
	for index := 1; index <= 3; index++ {
		event := usageFileEvent{
			EventID:          "event-" + string(rune('0'+index)),
			Status:           usageTurnStatusDone,
			Model:            "model",
			At:               time.Unix(int64(index), 0),
			InputTokens:      int64(index),
			UsagePresent:     true,
			SourceProviderID: "provider",
		}
		if err := store.UpsertEvent(event); err != nil {
			t.Fatalf("upsert event %d: %v", index, err)
		}
	}

	page, err := store.EventsAfter(2, 10)
	if err != nil {
		t.Fatalf("read events after confirmed cursor: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Sequence != 3 {
		t.Fatalf("unexpected page after compaction: %+v", page)
	}

	sequences := readJournalSequences(t, store.journalPath)
	if len(sequences) != 2 || sequences[0] != 2 || sequences[1] != 3 {
		t.Fatalf("compaction must retain cursor boundary and pending events, got %v", sequences)
	}

	if err := store.UpsertEvent(usageFileEvent{
		EventID:          "event-4",
		Status:           usageTurnStatusDone,
		Model:            "model",
		At:               time.Unix(4, 0),
		InputTokens:      4,
		UsagePresent:     true,
		SourceProviderID: "provider",
	}); err != nil {
		t.Fatalf("append after compaction: %v", err)
	}
	sequences = readJournalSequences(t, store.journalPath)
	if got := sequences[len(sequences)-1]; got != 4 {
		t.Fatalf("sequence must remain monotonic after compaction, got %d", got)
	}
}

func TestUsageJournalPreservesUsageStatus(t *testing.T) {
	store := NewUsageFileStore(t.TempDir())
	if err := store.UpsertEvent(usageFileEvent{
		EventID:          "estimated-event",
		Status:           usageTurnStatusDone,
		Model:            "model",
		At:               time.Unix(1, 0),
		InputTokens:      12,
		OutputTokens:     3,
		UsageStatus:      "estimated",
		SourceProviderID: "provider",
	}); err != nil {
		t.Fatalf("upsert estimated event: %v", err)
	}

	page, err := store.EventsAfter(0, 10)
	if err != nil {
		t.Fatalf("read usage events: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("expected one event, got %d", len(page.Events))
	}
	if page.Events[0].UsageStatus != "estimated" || page.Events[0].UsagePresent {
		t.Fatalf("unexpected exported usage status: %+v", page.Events[0])
	}
}

func TestUsageStatusAggregationPreservesLeastCertainSource(t *testing.T) {
	store := NewUsageFileStore(t.TempDir())
	for _, event := range []usageFileEvent{
		{
			EventID:          "request::reported",
			Status:           usageTurnStatusDone,
			At:               time.Unix(1, 0),
			InputTokens:      10,
			UsagePresent:     true,
			UsageStatus:      "reported",
			SourceProviderID: "provider",
		},
		{
			EventID:          "request::estimated",
			Status:           usageTurnStatusDone,
			At:               time.Unix(2, 0),
			OutputTokens:     4,
			UsageStatus:      "estimated",
			SourceProviderID: "provider",
		},
	} {
		if err := store.UpsertEvent(event); err != nil {
			t.Fatalf("upsert %s: %v", event.EventID, err)
		}
	}

	aggregate, found, err := store.LookupEvent("request")
	if err != nil {
		t.Fatalf("lookup aggregate: %v", err)
	}
	if !found || aggregate.UsageStatus != "estimated" {
		t.Fatalf("mixed reported and estimated usage must aggregate as estimated: %+v", aggregate)
	}
}

func TestUsageStoreAggregatesOnlyObservableCacheUsage(t *testing.T) {
	store := NewUsageFileStore(t.TempDir())
	for _, event := range []usageFileEvent{
		{
			EventID:            "reported",
			At:                 time.Unix(1, 0),
			InputTokens:        40,
			CacheReadTokens:    60,
			UsagePresent:       true,
			UsageStatus:        "reported",
			CacheUsageObserved: true,
		},
		{
			EventID:         "estimated",
			At:              time.Unix(2, 0),
			InputTokens:     100,
			UsageStatus:     "estimated",
			UsagePresent:    false,
			CacheReadTokens: 0,
		},
	} {
		if err := store.UpsertEvent(event); err != nil {
			t.Fatalf("upsert %s: %v", event.EventID, err)
		}
	}

	doc, err := readUsageFileDocument(store.path)
	if err != nil {
		t.Fatalf("read usage document: %v", err)
	}
	if doc.Totals.ProviderCalls != 2 || doc.Totals.InputTokens != 140 {
		t.Fatalf("unexpected full totals: %+v", doc.Totals)
	}
	if doc.Totals.CacheObservedCalls != 1 ||
		doc.Totals.CacheObservedInputTokens != 40 ||
		doc.Totals.CacheObservedReadTokens != 60 {
		t.Fatalf("estimated usage must not enter observable cache totals: %+v", doc.Totals)
	}
}

func readJournalSequences(t *testing.T, path string) []int64 {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer file.Close()

	var sequences []int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event usageFileEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode journal event: %v", err)
		}
		sequences = append(sequences, event.Sequence)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan journal: %v", err)
	}
	return sequences
}
