package nlu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"skarb/internal/service"
)

func TestLLMParserValidatedIntent(t *testing.T) {
	p, svc, cleanup := testParser(t)
	defer cleanup()
	var gotSchema bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer token")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if rf, ok := body["response_format"].(map[string]any); ok && rf["type"] == "json_schema" {
			gotSchema = true
		}
		intent := map[string]any{
			"operation":  "add_transactions",
			"confidence": 0.92,
			"transactions": []map[string]any{{
				"amount": "512", "currency": "UAH", "date": "2026-10-26",
				"category": "Їжа", "description": "обід",
			}},
			"query":    nil,
			"snapshot": nil,
		}
		content, _ := json.Marshal(intent)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
			}},
		})
	}))
	defer ts.Close()

	cfg := service.DefaultLLMConfig()
	cfg.BaseURL = ts.URL
	cfg.Model = "fake-model"
	cfg.SchemaMode = "json_schema"
	raw, _ := json.Marshal(cfg)
	if err := svc.Store.SetSetting(context.Background(), nil, "llm.config", string(raw), nil); err != nil {
		t.Fatal(err)
	}
	svc.Cfg.LLMAPIKey = "test-key"

	intent, err := p.ParseLLM(context.Background(), "сьогодні 512 грн за їжу")
	if err != nil {
		t.Fatal(err)
	}
	if !gotSchema {
		t.Fatal("parser did not request json_schema")
	}
	if intent.Operation != "add_transactions" || len(intent.Transactions) != 1 {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	tx := intent.Transactions[0]
	if tx.Date != "2026-10-26" || tx.Currency != "UAH" || tx.Amount != "512" {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	if len(tx.Category) < 10 {
		t.Fatalf("category was not resolved to a UID: %q", tx.Category)
	}
}

func TestLLMParserRepairsInvalidOutput(t *testing.T) {
	p, svc, cleanup := testParser(t)
	defer cleanup()
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := "this is not json"
		if calls == 2 {
			intent := map[string]any{
				"operation": "add_transactions", "confidence": 0.9,
				"transactions": []map[string]any{{
					"amount": "300", "currency": "UAH", "date": "2026-10-26",
					"category": "Кава", "description": "кава",
				}},
				"query": nil, "snapshot": nil,
			}
			b, _ := json.Marshal(intent)
			content = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": content},
				"finish_reason": "stop",
			}},
		})
	}))
	defer ts.Close()
	cfg := service.DefaultLLMConfig()
	cfg.BaseURL = ts.URL
	cfg.Model = "fake-model"
	cfg.SchemaMode = "json_schema"
	raw, _ := json.Marshal(cfg)
	if err := svc.Store.SetSetting(context.Background(), nil, "llm.config", string(raw), nil); err != nil {
		t.Fatal(err)
	}
	svc.Cfg.LLMAPIKey = "test-key"
	intent, err := p.ParseLLM(context.Background(), "300 грн за каву")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(intent.Transactions) != 1 {
		t.Fatalf("expected one repair call, calls=%d intent=%+v", calls, intent)
	}
}
