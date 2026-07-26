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
