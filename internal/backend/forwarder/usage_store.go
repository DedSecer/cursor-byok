package forwarder

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	usageFileName          = "usage.json"
	usageJournalSuffix     = ".events.jsonl"
	usageFileSchemaVersion = 5
	usageRecentEventLimit  = 500

	usageEventKindProvider = "provider_call"
	usageEventKindTurn     = "turn_finalized"
	usageTurnStatusDone    = "completed"
)

type UsageFileStore struct {
	path        string
	journalPath string
}

type usageFileDocument struct {
	SchemaVersion int                       `json:"schema_version"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	NextSequence  int64                     `json:"next_sequence"`
	Totals        usageFileTotals           `json:"totals"`
	Daily         []usageFileDaily          `json:"daily"`
	RecentEvents  []usageFileEvent          `json:"recent_events"`
	EventIndex    map[string]usageFileEvent `json:"event_index,omitempty"`
}

type usageFileTotals struct {
	ProviderCalls            int64 `json:"provider_calls"`
	TurnsTotal               int64 `json:"turns_total"`
	ValidTurnsTotal          int64 `json:"valid_turns_total"`
	InvalidTurnsTotal        int64 `json:"invalid_turns_total"`
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadTokens          int64 `json:"cache_read_tokens"`
	CacheWriteTokens         int64 `json:"cache_write_tokens"`
	TotalTokens              int64 `json:"total_tokens"`
	CacheObservedCalls       int64 `json:"cache_observed_calls"`
	CacheObservedInputTokens int64 `json:"cache_observed_input_tokens"`
	CacheObservedReadTokens  int64 `json:"cache_observed_read_tokens"`
	CacheObservedWriteTokens int64 `json:"cache_observed_write_tokens"`
}

type usageFileDaily struct {
	Date                     string `json:"date"`
	ProviderCalls            int64  `json:"provider_calls"`
	TurnsTotal               int64  `json:"turns_total"`
	ValidTurnsTotal          int64  `json:"valid_turns_total"`
	InvalidTurnsTotal        int64  `json:"invalid_turns_total"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheReadTokens          int64  `json:"cache_read_tokens"`
	CacheWriteTokens         int64  `json:"cache_write_tokens"`
	TotalTokens              int64  `json:"total_tokens"`
	CacheObservedCalls       int64  `json:"cache_observed_calls"`
	CacheObservedInputTokens int64  `json:"cache_observed_input_tokens"`
	CacheObservedReadTokens  int64  `json:"cache_observed_read_tokens"`
	CacheObservedWriteTokens int64  `json:"cache_observed_write_tokens"`
}

type usageFileEvent struct {
	Sequence           int64     `json:"sequence"`
	EventID            string    `json:"event_id"`
	Kind               string    `json:"kind,omitempty"`
	Status             string    `json:"status,omitempty"`
	SourceProviderID   string    `json:"source_provider_id,omitempty"`
	SourceProviderName string    `json:"source_provider_name,omitempty"`
	ProviderType       string    `json:"provider_type,omitempty"`
	ChannelID          string    `json:"channel_id,omitempty"`
	RequestModel       string    `json:"request_model,omitempty"`
	Model              string    `json:"model,omitempty"`
	PricingModel       string    `json:"pricing_model,omitempty"`
	StatusCode         int       `json:"status_code,omitempty"`
	Error              string    `json:"error,omitempty"`
	LatencyMS          int64     `json:"latency_ms,omitempty"`
	FirstTokenMS       int64     `json:"first_token_ms,omitempty"`
	DurationMS         int64     `json:"duration_ms,omitempty"`
	IsStreaming        bool      `json:"is_streaming"`
	At                 time.Time `json:"at"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	CacheReadTokens    int64     `json:"cache_read_tokens"`
	CacheWriteTokens   int64     `json:"cache_write_tokens"`
	TotalTokens        int64     `json:"total_tokens"`
	UsagePresent       bool      `json:"usage_present"`
	UsageStatus        string    `json:"usage_status,omitempty"`
	CacheUsageObserved bool      `json:"cache_usage_observed"`
}

type usageFileDelta struct {
	providerCalls            int64
	turnsTotal               int64
	validTurnsTotal          int64
	invalidTurnsTotal        int64
	inputTokens              int64
	outputTokens             int64
	cacheReadTokens          int64
	cacheWriteTokens         int64
	totalTokens              int64
	cacheObservedCalls       int64
	cacheObservedInputTokens int64
	cacheObservedReadTokens  int64
	cacheObservedWriteTokens int64
}

func NewUsageFileStore(historyRoot string) *UsageFileStore {
	path := filepath.Join(strings.TrimSpace(historyRoot), usageFileName)
	return &UsageFileStore{path: path, journalPath: path + usageJournalSuffix}
}

func (store *UsageFileStore) UpsertEvent(event usageFileEvent) error {
	if store == nil || strings.TrimSpace(store.path) == "" {
		return nil
	}
	event.EventID = strings.TrimSpace(event.EventID)
	if event.EventID == "" {
		return nil
	}
	event.SourceProviderID = strings.TrimSpace(event.SourceProviderID)
	event.SourceProviderName = strings.TrimSpace(event.SourceProviderName)
	event.ProviderType = strings.TrimSpace(event.ProviderType)
	event.ChannelID = strings.TrimSpace(event.ChannelID)
	event.RequestModel = strings.TrimSpace(event.RequestModel)
	event.Model = strings.TrimSpace(event.Model)
	event.PricingModel = strings.TrimSpace(event.PricingModel)
	event.Error = strings.TrimSpace(event.Error)
	event.Kind = normalizeUsageEventKind(event.Kind)
	event.Status = strings.TrimSpace(event.Status)
	event.UsageStatus = normalizeUsageStatus(event.UsageStatus, event.UsagePresent)
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	} else {
		event.At = event.At.UTC()
	}
	event.InputTokens = nonNegativeInt64(event.InputTokens)
	event.OutputTokens = nonNegativeInt64(event.OutputTokens)
	event.CacheReadTokens = nonNegativeInt64(event.CacheReadTokens)
	event.CacheWriteTokens = nonNegativeInt64(event.CacheWriteTokens)
	event.TotalTokens = event.InputTokens + event.OutputTokens + event.CacheReadTokens + event.CacheWriteTokens

	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		return fmt.Errorf("create usage directory: %w", err)
	}
	release, err := acquireConversationLock(store.path + ".lock")
	if err != nil {
		return err
	}
	defer release()

	doc, err := readUsageFileDocument(store.path)
	if err != nil {
		return err
	}
	journalSequence, err := latestUsageJournalSequence(store.journalPath)
	if err != nil {
		return err
	}
	if journalSequence > doc.NextSequence {
		doc.NextSequence = journalSequence
	}
	if doc.EventIndex == nil {
		doc.EventIndex = make(map[string]usageFileEvent)
	}
	oldEvent, found := doc.EventIndex[event.EventID]
	if found {
		applyUsageFileDelta(&doc, oldEvent.At, negateUsageFileDelta(usageFileEventDelta(oldEvent)))
	}
	doc.NextSequence++
	event.Sequence = doc.NextSequence
	applyUsageFileDelta(&doc, event.At, usageFileEventDelta(event))
	doc.RecentEvents = upsertRecentUsageEvent(doc.RecentEvents, event)
	doc.RecentEvents = trimRecentUsageEvents(doc.RecentEvents, usageRecentEventLimit)
	doc.EventIndex = buildUsageEventIndex(doc.RecentEvents)
	doc.SchemaVersion = usageFileSchemaVersion
	doc.UpdatedAt = time.Now().UTC()
	if err := appendUsageJournalEvent(store.journalPath, event); err != nil {
		return err
	}
	return writeJSONFileAtomic(store.path, doc)
}

func (store *UsageFileStore) LookupEvent(needle string) (usageFileEvent, bool, error) {
	if store == nil || strings.TrimSpace(store.path) == "" {
		return usageFileEvent{}, false, nil
	}
	doc, err := readUsageFileDocument(store.path)
	if err != nil {
		return usageFileEvent{}, false, err
	}
	trimmed := strings.TrimSpace(needle)
	if trimmed == "" {
		return usageFileEvent{}, false, nil
	}
	var aggregate usageFileEvent
	found := false
	events := doc.EventIndex
	if len(events) == 0 {
		events = make(map[string]usageFileEvent, len(doc.RecentEvents))
		for _, event := range doc.RecentEvents {
			if eventID := strings.TrimSpace(event.EventID); eventID != "" {
				events[eventID] = event
			}
		}
	}
	for _, event := range events {
		eventID := strings.TrimSpace(event.EventID)
		if eventID != trimmed && !strings.HasPrefix(eventID, trimmed+"::") {
			continue
		}
		if !found {
			aggregate = usageFileEvent{
				EventID:            trimmed,
				At:                 event.At,
				CacheUsageObserved: event.CacheUsageObserved,
			}
			found = true
		} else {
			aggregate.CacheUsageObserved = aggregate.CacheUsageObserved && event.CacheUsageObserved
		}
		if event.At.After(aggregate.At) {
			aggregate.At = event.At
		}
		aggregate.InputTokens += nonNegativeInt64(event.InputTokens)
		aggregate.OutputTokens += nonNegativeInt64(event.OutputTokens)
		aggregate.CacheReadTokens += nonNegativeInt64(event.CacheReadTokens)
		aggregate.CacheWriteTokens += nonNegativeInt64(event.CacheWriteTokens)
		aggregate.TotalTokens += nonNegativeInt64(event.TotalTokens)
		aggregate.UsagePresent = aggregate.UsagePresent || event.UsagePresent
		aggregate.UsageStatus = mergeUsageStatus(aggregate.UsageStatus, event.UsageStatus, event.UsagePresent)
	}
	if found {
		return aggregate, true, nil
	}
	return usageFileEvent{}, false, nil
}

func readUsageFileDocument(path string) (usageFileDocument, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return usageFileDocument{
				SchemaVersion: usageFileSchemaVersion,
				Daily:         make([]usageFileDaily, 0),
				RecentEvents:  make([]usageFileEvent, 0),
				EventIndex:    make(map[string]usageFileEvent),
			}, nil
		}
		return usageFileDocument{}, fmt.Errorf("read usage file: %w", err)
	}
	var doc usageFileDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return usageFileDocument{}, fmt.Errorf("decode usage file: %w", err)
	}
	if doc.SchemaVersion == 0 {
		doc.SchemaVersion = 1
	}
	doc.NextSequence = normalizeUsageSequences(doc.RecentEvents, doc.NextSequence)
	doc.RecentEvents = trimRecentUsageEvents(doc.RecentEvents, usageRecentEventLimit)
	if len(doc.EventIndex) == 0 {
		doc.EventIndex = buildUsageEventIndex(doc.RecentEvents)
	}
	return doc, nil
}

func upsertRecentUsageEvent(items []usageFileEvent, event usageFileEvent) []usageFileEvent {
	event.EventID = strings.TrimSpace(event.EventID)
	if event.EventID == "" {
		return items
	}
	next := make([]usageFileEvent, 0, len(items)+1)
	next = append(next, event)
	for _, item := range items {
		if strings.TrimSpace(item.EventID) == event.EventID {
			continue
		}
		next = append(next, item)
	}
	return next
}

type UsageEvent struct {
	Sequence           int64     `json:"sequence"`
	EventID            string    `json:"eventId"`
	Kind               string    `json:"kind"`
	Status             string    `json:"status"`
	SourceProviderID   string    `json:"sourceProviderId"`
	SourceProviderName string    `json:"sourceProviderName"`
	ProviderType       string    `json:"providerType"`
	ChannelID          string    `json:"channelId"`
	RequestModel       string    `json:"requestModel"`
	Model              string    `json:"model"`
	PricingModel       string    `json:"pricingModel"`
	StatusCode         int       `json:"statusCode"`
	Error              string    `json:"error,omitempty"`
	LatencyMS          int64     `json:"latencyMs"`
	FirstTokenMS       int64     `json:"firstTokenMs"`
	DurationMS         int64     `json:"durationMs"`
	IsStreaming        bool      `json:"isStreaming"`
	At                 time.Time `json:"at"`
	InputTokens        int64     `json:"inputTokens"`
	OutputTokens       int64     `json:"outputTokens"`
	CacheReadTokens    int64     `json:"cacheReadTokens"`
	CacheWriteTokens   int64     `json:"cacheWriteTokens"`
	UsagePresent       bool      `json:"usagePresent"`
	UsageStatus        string    `json:"usageStatus"`
	CacheUsageObserved bool      `json:"cacheUsageObserved"`
}

type UsageEventPage struct {
	Events     []UsageEvent `json:"events"`
	NextCursor int64        `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}

func (store *UsageFileStore) EventsAfter(cursor int64, limit int) (UsageEventPage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	page := UsageEventPage{Events: make([]UsageEvent, 0, limit), NextCursor: cursor}
	if store == nil || strings.TrimSpace(store.journalPath) == "" {
		return page, nil
	}
	release, err := acquireConversationLock(store.path + ".lock")
	if err != nil {
		return page, err
	}
	defer release()
	if err := compactConfirmedUsageJournal(store.journalPath, cursor); err != nil {
		return page, err
	}
	file, err := os.Open(store.journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return page, nil
		}
		return page, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event usageFileEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return page, fmt.Errorf("decode usage journal: %w", err)
		}
		if event.Sequence <= cursor {
			continue
		}
		if len(page.Events) >= limit {
			page.HasMore = true
			break
		}
		page.Events = append(page.Events, exportUsageEvent(event))
		page.NextCursor = event.Sequence
	}
	if err := scanner.Err(); err != nil {
		return page, err
	}
	return page, nil
}

func compactConfirmedUsageJournal(path string, confirmedSequence int64) error {
	if confirmedSequence <= 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open usage journal for compaction: %w", err)
	}
	defer file.Close()

	kept := make([][]byte, 0)
	changed := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var event usageFileEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode usage journal during compaction: %w", err)
		}
		if event.Sequence < confirmedSequence {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan usage journal during compaction: %w", err)
	}
	if !changed {
		return nil
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close usage journal before compaction: %w", err)
	}

	tempFile, tempPath, err := openUniqueArtifactTempFile(path)
	if err != nil {
		return fmt.Errorf("open usage journal temp file: %w", err)
	}
	renamed := false
	defer func() {
		_ = tempFile.Close()
		if !renamed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := tempFile.Chmod(0o600); err != nil {
		return fmt.Errorf("set usage journal temp permissions: %w", err)
	}
	for _, line := range kept {
		if _, err := tempFile.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write compacted usage journal: %w", err)
		}
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync compacted usage journal: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close compacted usage journal: %w", err)
	}
	if err := renameArtifactTempFile(tempPath, path); err != nil {
		return fmt.Errorf("replace usage journal after compaction: %w", err)
	}
	renamed = true
	return syncDirectory(filepath.Dir(path))
}

func latestUsageJournalSequence(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()
	var latest int64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event usageFileEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return 0, fmt.Errorf("decode usage journal: %w", err)
		}
		if event.Sequence > latest {
			latest = event.Sequence
		}
	}
	return latest, scanner.Err()
}

func appendUsageJournalEvent(path string, event usageFileEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open usage journal: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("append usage journal: %w", err)
	}
	return file.Sync()
}

func exportUsageEvent(event usageFileEvent) UsageEvent {
	return UsageEvent{
		Sequence: event.Sequence, EventID: event.EventID, Kind: normalizeUsageEventKind(event.Kind),
		Status:           event.Status,
		SourceProviderID: event.SourceProviderID, SourceProviderName: event.SourceProviderName,
		ProviderType: event.ProviderType, ChannelID: event.ChannelID,
		RequestModel: event.RequestModel, Model: event.Model, PricingModel: event.PricingModel,
		StatusCode: event.StatusCode, Error: event.Error, LatencyMS: event.LatencyMS,
		FirstTokenMS: event.FirstTokenMS, DurationMS: event.DurationMS,
		IsStreaming: event.IsStreaming, At: event.At, InputTokens: event.InputTokens,
		OutputTokens: event.OutputTokens, CacheReadTokens: event.CacheReadTokens,
		CacheWriteTokens: event.CacheWriteTokens, UsagePresent: event.UsagePresent,
		UsageStatus:        normalizeUsageStatus(event.UsageStatus, event.UsagePresent),
		CacheUsageObserved: event.CacheUsageObserved,
	}
}

func normalizeUsageSequences(items []usageFileEvent, nextSequence int64) int64 {
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Sequence <= 0 {
			nextSequence++
			items[index].Sequence = nextSequence
		} else if items[index].Sequence > nextSequence {
			nextSequence = items[index].Sequence
		}
	}
	return nextSequence
}

func trimRecentUsageEvents(items []usageFileEvent, limit int) []usageFileEvent {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func buildUsageEventIndex(items []usageFileEvent) map[string]usageFileEvent {
	index := make(map[string]usageFileEvent, len(items))
	for _, event := range items {
		event.EventID = strings.TrimSpace(event.EventID)
		if event.EventID == "" {
			continue
		}
		event.Kind = normalizeUsageEventKind(event.Kind)
		index[event.EventID] = event
	}
	return index
}

func normalizeUsageEventKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case usageEventKindTurn:
		return usageEventKindTurn
	default:
		return usageEventKindProvider
	}
}

func normalizeUsageStatus(status string, usagePresent bool) string {
	switch strings.TrimSpace(status) {
	case "reported", "estimated", "missing":
		return strings.TrimSpace(status)
	default:
		if usagePresent {
			return "reported"
		}
		return "missing"
	}
}

func mergeUsageStatus(current string, next string, usagePresent bool) string {
	next = normalizeUsageStatus(next, usagePresent)
	if strings.TrimSpace(current) == "" {
		return next
	}
	current = normalizeUsageStatus(current, false)
	if current == "missing" || next == "missing" {
		return "missing"
	}
	if current == "estimated" || next == "estimated" {
		return "estimated"
	}
	return "reported"
}

func usageFileEventDelta(event usageFileEvent) usageFileDelta {
	switch normalizeUsageEventKind(event.Kind) {
	case usageEventKindTurn:
		delta := usageFileDelta{turnsTotal: 1}
		if strings.TrimSpace(event.Status) == usageTurnStatusDone {
			delta.validTurnsTotal = 1
		} else {
			delta.invalidTurnsTotal = 1
		}
		return delta
	default:
		delta := usageFileDelta{
			providerCalls:    1,
			inputTokens:      nonNegativeInt64(event.InputTokens),
			outputTokens:     nonNegativeInt64(event.OutputTokens),
			cacheReadTokens:  nonNegativeInt64(event.CacheReadTokens),
			cacheWriteTokens: nonNegativeInt64(event.CacheWriteTokens),
			totalTokens:      nonNegativeInt64(event.TotalTokens),
		}
		if event.CacheUsageObserved {
			delta.cacheObservedCalls = 1
			delta.cacheObservedInputTokens = nonNegativeInt64(event.InputTokens)
			delta.cacheObservedReadTokens = nonNegativeInt64(event.CacheReadTokens)
			delta.cacheObservedWriteTokens = nonNegativeInt64(event.CacheWriteTokens)
		}
		return delta
	}
}

func negateUsageFileDelta(value usageFileDelta) usageFileDelta {
	return usageFileDelta{
		providerCalls:            -value.providerCalls,
		turnsTotal:               -value.turnsTotal,
		validTurnsTotal:          -value.validTurnsTotal,
		invalidTurnsTotal:        -value.invalidTurnsTotal,
		inputTokens:              -value.inputTokens,
		outputTokens:             -value.outputTokens,
		cacheReadTokens:          -value.cacheReadTokens,
		cacheWriteTokens:         -value.cacheWriteTokens,
		totalTokens:              -value.totalTokens,
		cacheObservedCalls:       -value.cacheObservedCalls,
		cacheObservedInputTokens: -value.cacheObservedInputTokens,
		cacheObservedReadTokens:  -value.cacheObservedReadTokens,
		cacheObservedWriteTokens: -value.cacheObservedWriteTokens,
	}
}

func applyUsageFileDelta(doc *usageFileDocument, at time.Time, delta usageFileDelta) {
	if doc == nil {
		return
	}
	doc.Totals.ProviderCalls = clampNonNegativeInt64(doc.Totals.ProviderCalls + delta.providerCalls)
	doc.Totals.TurnsTotal = clampNonNegativeInt64(doc.Totals.TurnsTotal + delta.turnsTotal)
	doc.Totals.ValidTurnsTotal = clampNonNegativeInt64(doc.Totals.ValidTurnsTotal + delta.validTurnsTotal)
	doc.Totals.InvalidTurnsTotal = clampNonNegativeInt64(doc.Totals.InvalidTurnsTotal + delta.invalidTurnsTotal)
	doc.Totals.InputTokens = clampNonNegativeInt64(doc.Totals.InputTokens + delta.inputTokens)
	doc.Totals.OutputTokens = clampNonNegativeInt64(doc.Totals.OutputTokens + delta.outputTokens)
	doc.Totals.CacheReadTokens = clampNonNegativeInt64(doc.Totals.CacheReadTokens + delta.cacheReadTokens)
	doc.Totals.CacheWriteTokens = clampNonNegativeInt64(doc.Totals.CacheWriteTokens + delta.cacheWriteTokens)
	doc.Totals.TotalTokens = clampNonNegativeInt64(doc.Totals.TotalTokens + delta.totalTokens)
	doc.Totals.CacheObservedCalls = clampNonNegativeInt64(doc.Totals.CacheObservedCalls + delta.cacheObservedCalls)
	doc.Totals.CacheObservedInputTokens = clampNonNegativeInt64(doc.Totals.CacheObservedInputTokens + delta.cacheObservedInputTokens)
	doc.Totals.CacheObservedReadTokens = clampNonNegativeInt64(doc.Totals.CacheObservedReadTokens + delta.cacheObservedReadTokens)
	doc.Totals.CacheObservedWriteTokens = clampNonNegativeInt64(doc.Totals.CacheObservedWriteTokens + delta.cacheObservedWriteTokens)

	date := at.UTC().Format("2006-01-02")
	for index := range doc.Daily {
		if doc.Daily[index].Date != date {
			continue
		}
		applyUsageDailyDelta(&doc.Daily[index], delta)
		return
	}
	item := usageFileDaily{Date: date}
	applyUsageDailyDelta(&item, delta)
	doc.Daily = append(doc.Daily, item)
}

func applyUsageDailyDelta(item *usageFileDaily, delta usageFileDelta) {
	if item == nil {
		return
	}
	item.ProviderCalls = clampNonNegativeInt64(item.ProviderCalls + delta.providerCalls)
	item.TurnsTotal = clampNonNegativeInt64(item.TurnsTotal + delta.turnsTotal)
	item.ValidTurnsTotal = clampNonNegativeInt64(item.ValidTurnsTotal + delta.validTurnsTotal)
	item.InvalidTurnsTotal = clampNonNegativeInt64(item.InvalidTurnsTotal + delta.invalidTurnsTotal)
	item.InputTokens = clampNonNegativeInt64(item.InputTokens + delta.inputTokens)
	item.OutputTokens = clampNonNegativeInt64(item.OutputTokens + delta.outputTokens)
	item.CacheReadTokens = clampNonNegativeInt64(item.CacheReadTokens + delta.cacheReadTokens)
	item.CacheWriteTokens = clampNonNegativeInt64(item.CacheWriteTokens + delta.cacheWriteTokens)
	item.TotalTokens = clampNonNegativeInt64(item.TotalTokens + delta.totalTokens)
	item.CacheObservedCalls = clampNonNegativeInt64(item.CacheObservedCalls + delta.cacheObservedCalls)
	item.CacheObservedInputTokens = clampNonNegativeInt64(item.CacheObservedInputTokens + delta.cacheObservedInputTokens)
	item.CacheObservedReadTokens = clampNonNegativeInt64(item.CacheObservedReadTokens + delta.cacheObservedReadTokens)
	item.CacheObservedWriteTokens = clampNonNegativeInt64(item.CacheObservedWriteTokens + delta.cacheObservedWriteTokens)
}

func clampNonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
