package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatJSONAutoFallsBackToJSONObject(t *testing.T) {
	var calls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rf, _ := body["response_format"].(map[string]any)
		kind, _ := rf["type"].(string)
		calls = append(calls, kind)
		if kind == "json_schema" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format json_schema unsupported"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": `{"ok":true}`},
				"finish_reason": "stop",
			}},
		})
	}))
	defer ts.Close()

	client := New(Config{
		BaseURL: ts.URL, APIKey: "key", Model: "fake", SchemaMode: "auto",
		Timeout: 5 * time.Second, MaxOutputTokens: 100, Temperature: 0,
	})
	res, err := client.ChatJSON(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "json_object" || res.Content != `{"ok":true}` {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(calls) != 2 || calls[0] != "json_schema" || calls[1] != "json_object" {
		t.Fatalf("unexpected fallback calls: %v", calls)
	}
}

func TestChatJSONRefusal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": "", "refusal": "cannot help"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer ts.Close()
	client := New(Config{BaseURL: ts.URL, Model: "fake", SchemaMode: "prompt", Timeout: 5 * time.Second, MaxOutputTokens: 100})
	if _, err := client.ChatJSON(context.Background(), "system", "user", nil); err == nil {
		t.Fatal("expected refusal error")
	}
}
