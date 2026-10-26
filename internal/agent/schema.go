package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"skarb/internal/mcp"
)

// decisionSchema builds the strict JSON schema for a router decision. The
// arguments are carried as a JSON string because OpenAI-compatible strict
// json_schema mode does not accept free-form objects with arbitrary keys.
func decisionSchema(tools []mcp.ToolInfo) json.RawMessage {
	names := make([]string, 0, len(tools)+1)
	names = append(names, "noop")
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"tool", "arguments_json", "confidence"},
		"properties": map[string]any{
			"tool":           map[string]any{"type": "string", "enum": names},
			"arguments_json": map[string]any{"type": "string"},
			"confidence":     map[string]any{"type": "number"},
		},
	}
	raw, _ := json.Marshal(schema)
	return raw
}

// compactCatalog renders tool signatures for the prompt without output schemas
// or host-managed arguments. The full schemas are still used for validation.
func compactCatalog(tools []mcp.ToolInfo) string {
	sorted := make([]mcp.ToolInfo, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, tool := range sorted {
		b.WriteString("- ")
		b.WriteString(tool.Name)
		if desc := firstLine(tool.Description); desc != "" {
			b.WriteString(" — ")
			b.WriteString(truncate(desc, 180))
		}
		if sig := signature(tool.InputSchema); sig != "" {
			b.WriteString("; args: ")
			b.WriteString(sig)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

var hiddenArgs = map[string]bool{
	"cursor":          true,
	"idempotency_key": true,
	"original_text":   true,
}

func signature(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return ""
	}
	return objectSignature(schema, 0)
}

func objectSignature(schema map[string]any, depth int) string {
	if depth > 3 {
		return "object"
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return typeName(schema)
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, v := range list {
			if name, ok := v.(string); ok {
				required[name] = true
			}
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		if !hiddenArgs[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		typ := typeName(prop)
		if !required[name] {
			typ += "?"
		}
		parts = append(parts, name+":"+typ)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func typeName(schema map[string]any) string {
	if schema == nil {
		return "any"
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		values := make([]string, 0, len(enum))
		for _, v := range enum {
			values = append(values, fmt.Sprint(v))
		}
		return strings.Join(values, "|")
	}
	rawType, _ := schema["type"]
	switch t := rawType.(type) {
	case string:
		switch t {
		case "object":
			return objectSignature(schema, 1)
		case "array":
			item, _ := schema["items"].(map[string]any)
			return "array<" + typeName(item) + ">"
		default:
			return t
		}
	case []any:
		// Nullable union: use the first non-null type.
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return typeName(map[string]any{"type": s})
			}
		}
	}
	return "any"
}
