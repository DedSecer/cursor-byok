package modeladapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnthropicStreamReportsFirstEventLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		time.Sleep(20 * time.Millisecond)
		_, _ = fmt.Fprint(writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-test\",\"usage\":{\"input_tokens\":3}}}\n\n")
		_, _ = fmt.Fprint(writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	var finished ModelEvent
	err := NewAnthropicAdapter().Stream(context.Background(), StreamRequest{
		RequestID:       "request",
		RunID:           "request",
		ModelCallID:     "call",
		ModelID:         "claude-test",
		ProviderModelID: "claude-test",
		BaseURL:         server.URL,
		APIKey:          "test-key",
		Messages:        []Message{{Role: "user", Content: "hello"}},
		MaxTokens:       16,
		Stream:          true,
	}, func(event ModelEvent) error {
		if event.Kind == ModelEventKindTurnFinished {
			finished = event
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream anthropic response: %v", err)
	}
	if finished.Kind != ModelEventKindTurnFinished {
		t.Fatal("turn finished event was not emitted")
	}
	if finished.FirstTokenMS < 15 {
		t.Fatalf("first token latency = %dms, want delayed first event", finished.FirstTokenMS)
	}
}

func TestComputeTTFTMSRejectsMissingOrReversedTimes(t *testing.T) {
	startedAt := time.Unix(10, 0)
	if got := computeTTFTMS(startedAt, time.Time{}); got != 0 {
		t.Fatalf("missing first event latency = %d, want 0", got)
	}
	if got := computeTTFTMS(startedAt, startedAt.Add(-time.Millisecond)); got != 0 {
		t.Fatalf("reversed first event latency = %d, want 0", got)
	}
	if got := computeTTFTMS(startedAt, startedAt.Add(25*time.Millisecond)); got != 25 {
		t.Fatalf("first event latency = %d, want 25", got)
	}
}

func TestAnthropicMessageCacheBreakpointsPreserveAppendOnlyHistory(t *testing.T) {
	for size := 1; size <= 32; size++ {
		t.Run(fmt.Sprintf("size_%02d", size), func(t *testing.T) {
			previous := anthropicMessagesForAppendOnlyTest(size)
			current := anthropicMessagesForAppendOnlyTest(size + 1)

			applyAnthropicMessageCacheBreakpoints(previous)
			applyAnthropicMessageCacheBreakpoints(current)

			want := mustMarshalAnthropicMessagesForTest(t, previous)
			got := mustMarshalAnthropicMessagesForTest(t, current[:len(previous)])
			if got != want {
				t.Fatalf("historical message prefix changed after append\nwant: %s\ngot:  %s", want, got)
			}
		})
	}
}

func anthropicMessagesForAppendOnlyTest(count int) []anthropicMessage {
	messages := make([]anthropicMessage, 0, count)
	for index := 0; index < count; index++ {
		messages = append(messages, anthropicMessage{
			Role: "user",
			Content: []map[string]any{{
				"type": "text",
				"text": fmt.Sprintf("message-%02d", index),
			}},
		})
	}
	return messages
}

func mustMarshalAnthropicMessagesForTest(t *testing.T, messages []anthropicMessage) string {
	t.Helper()
	payload, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal anthropic messages: %v", err)
	}
	return string(payload)
}
