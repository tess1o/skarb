package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotConfigured   = errors.New("LLM is not configured")
	ErrUnsupportedMode = errors.New("response format not supported")
	ErrAuth            = errors.New("LLM authentication failed")
	ErrUpstream        = errors.New("LLM upstream error")
)

type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	SchemaMode      string
	Timeout         time.Duration
	MaxOutputTokens int
	Temperature     float64
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.SchemaMode == "" {
		cfg.SchemaMode = "auto"
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = 800
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

func (c *Client) Configured() bool {
	return strings.TrimSpace(c.cfg.BaseURL) != "" && strings.TrimSpace(c.cfg.Model) != ""
}

type Result struct {
	Content string
	Mode    string
}

// modeCache remembers the last working response-format mode per endpoint/model.
var modeCache sync.Map

func cacheKey(cfg Config) string { return cfg.BaseURL + "|" + cfg.Model }

// ChatJSON requests a JSON object from an OpenAI-compatible endpoint.
// In auto mode it tries json_schema, then json_object, then prompt-only JSON.
func (c *Client) ChatJSON(ctx context.Context, system, user string, schema json.RawMessage) (*Result, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	mode := c.cfg.SchemaMode
	if mode == "auto" {
		key := cacheKey(c.cfg)
		if cached, ok := modeCache.Load(key); ok {
			if res, err := c.call(ctx, system, user, schema, cached.(string)); err == nil {
				return res, nil
			} else if !errors.Is(err, ErrUnsupportedMode) {
				return nil, err
			}
			modeCache.Delete(key)
		}
		for _, candidate := range []string{"json_schema", "json_object", "prompt"} {
			if candidate == "json_schema" && len(schema) == 0 {
				continue
			}
			res, err := c.call(ctx, system, user, schema, candidate)
			if err == nil {
				modeCache.Store(key, candidate)
				return res, nil
			}
			if !errors.Is(err, ErrUnsupportedMode) {
				return nil, err
			}
		}
		return nil, ErrUnsupportedMode
	}
	return c.call(ctx, system, user, schema, mode)
}

func (c *Client) call(ctx context.Context, system, user string, schema json.RawMessage, mode string) (*Result, error) {
	payload := map[string]any{
		"model":       c.cfg.Model,
		"messages":    []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		"temperature": c.cfg.Temperature,
		"max_tokens":  c.cfg.MaxOutputTokens,
	}
	switch mode {
	case "json_schema":
		if len(schema) == 0 {
			return nil, ErrUnsupportedMode
		}
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "skarb_intent",
				"strict": true,
				"schema": json.RawMessage(schema),
			},
		}
	case "json_object":
		payload["response_format"] = map[string]any{"type": "json_object"}
		system += "\nReturn only one valid JSON object. Do not use markdown fences."
	case "prompt":
		system += "\nReturn only one valid JSON object. Do not use markdown fences."
		if len(schema) > 0 {
			system += "\nThe JSON must match this schema:\n" + string(schema)
		}
	default:
		return nil, fmt.Errorf("unknown schema mode %q", mode)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Skarb/1.0")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: HTTP %d", ErrAuth, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusUnprocessableEntity {
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrUnsupportedMode, resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrUpstream, resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode LLM response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("LLM error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}
	choice := parsed.Choices[0]
	if choice.Message.Refusal != "" {
		return nil, fmt.Errorf("LLM refused: %s", choice.Message.Refusal)
	}
	if choice.FinishReason == "length" {
		return nil, fmt.Errorf("LLM output was truncated")
	}
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		return nil, fmt.Errorf("LLM returned empty content")
	}
	return &Result{Content: stripFence(content), Mode: mode}, nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.ReplaceAll(s, "\n", " ")
}
