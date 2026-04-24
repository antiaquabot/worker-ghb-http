package polling_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroi-homes/worker-ghb-http/internal/polling"
)

type pollEvent struct {
	eventType  string
	externalID string
}

func newTestServer(t *testing.T, responses [][]map[string]any) *httptest.Server {
	t.Helper()
	callCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := callCount
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(responses[idx]); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func TestPoll_FirstAppearanceNoEvent(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var events []pollEvent
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		events = append(events, pollEvent{eventType, externalID})
	})

	client.Poll(context.Background())

	if len(events) != 0 {
		t.Errorf("first appearance: expected 0 events, got %v", events)
	}
}

// TestPoll_OpenDetected verifies REGISTRATION_OPENED fires when a known-closed object reappears.
// The polling API only returns registration_open=true objects, so "first open" is always a
// first appearance (no event). A subsequent open after disappearance is a detectable transition.
func TestPoll_OpenDetected(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
		{},
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var events []pollEvent
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		events = append(events, pollEvent{eventType, externalID})
	})

	client.Poll(context.Background()) // first appearance — no event
	client.Poll(context.Background()) // disappears — REGISTRATION_CLOSED
	client.Poll(context.Background()) // reappears — REGISTRATION_OPENED

	var openEvents []pollEvent
	for _, e := range events {
		if e.eventType == "REGISTRATION_OPENED" {
			openEvents = append(openEvents, e)
		}
	}
	if len(openEvents) != 1 || openEvents[0].externalID != "123" {
		t.Errorf("expected 1 REGISTRATION_OPENED for 123, got %v", events)
	}
}

func TestPoll_CloseDetected(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
		{},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var events []pollEvent
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		events = append(events, pollEvent{eventType, externalID})
	})

	client.Poll(context.Background())
	client.Poll(context.Background())

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %v", events)
	}
	if events[0].eventType != "REGISTRATION_CLOSED" || events[0].externalID != "123" {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

func TestPoll_ReOpenDetected(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
		{},
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Obj", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var events []pollEvent
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		events = append(events, pollEvent{eventType, externalID})
	})

	client.Poll(context.Background()) // first appearance — no event
	client.Poll(context.Background()) // open→closed — REGISTRATION_CLOSED
	client.Poll(context.Background()) // closed→open — REGISTRATION_OPENED

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %v", events)
	}
	if events[0].eventType != "REGISTRATION_CLOSED" {
		t.Errorf("poll 2: expected REGISTRATION_CLOSED, got %s", events[0].eventType)
	}
	if events[1].eventType != "REGISTRATION_OPENED" {
		t.Errorf("poll 3: expected REGISTRATION_OPENED, got %s", events[1].eventType)
	}
}

func TestPoll_OtherDeveloperFiltered(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "other", "external_id": "999", "registration_open": true, "title": "Other", "registration_url": "", "object_url": ""}},
		{{"developer_id": "other", "external_id": "999", "registration_open": true, "title": "Other", "registration_url": "", "object_url": ""}},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var events []pollEvent
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		events = append(events, pollEvent{eventType, externalID})
	})

	client.Poll(context.Background())
	client.Poll(context.Background())

	if len(events) != 0 {
		t.Errorf("other developer objects must not produce events, got %v", events)
	}
}

func TestPoll_OpenEventDataFields(t *testing.T) {
	responses := [][]map[string]any{
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Tower A", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
		{},
		{{"developer_id": "ghb", "external_id": "123", "registration_open": true, "title": "Tower A", "registration_url": "https://reg.ghb.by/?id=123", "object_url": ""}},
	}
	srv := newTestServer(t, responses)
	defer srv.Close()

	var capturedData map[string]any
	client := polling.New(srv.URL, "ghb", 60, func(eventType, externalID string, data map[string]any) {
		if eventType == "REGISTRATION_OPENED" {
			capturedData = data
		}
	})

	client.Poll(context.Background()) // first appearance — no event
	client.Poll(context.Background()) // disappears — REGISTRATION_CLOSED
	client.Poll(context.Background()) // reappears — REGISTRATION_OPENED with data

	if capturedData == nil {
		t.Fatal("expected REGISTRATION_OPENED event data, got nil")
	}
	if capturedData["title"] != "Tower A" {
		t.Errorf("title: want %q, got %q", "Tower A", capturedData["title"])
	}
	if capturedData["registration_url"] != "https://reg.ghb.by/?id=123" {
		t.Errorf("registration_url: want %q, got %q", "https://reg.ghb.by/?id=123", capturedData["registration_url"])
	}
}
