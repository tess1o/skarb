package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/domain"
)

// ErrToolNotAdvertised is returned when a caller asks for a tool that is not
// available to the actor of this session (for example an admin tool requested
// by a member).
var ErrToolNotAdvertised = errors.New("mcp: tool is not available for this actor")

// ToolInfo is a provider-neutral description of an advertised MCP tool.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCallResult is a provider-neutral MCP tool result.
type ToolCallResult struct {
	IsError    bool            `json:"is_error"`
	Text       string          `json:"text,omitempty"`
	Structured json.RawMessage `json:"structured,omitempty"`
}

// ToolSession is an in-process MCP client bound to one actor. It lets the
// Telegram bot use exactly the same tools, validation, permissions, and output
// shapes as an external MCP client without opening an HTTP port or minting an
// API token.
type ToolSession struct {
	client *mcpsdk.ClientSession
	server *mcpsdk.ServerSession

	tools  []ToolInfo
	byName map[string]ToolInfo

	closeOnce sync.Once
	closeErr  error
}

// NewToolSession creates a per-actor MCP server and connects an in-memory
// client to it. The caller must Close the session when done.
func (s *Server) NewToolSession(ctx context.Context, actor domain.Actor) (*ToolSession, error) {
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := s.serverForActor(actor).Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect in-process MCP server: %w", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "skarb-telegram", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, fmt.Errorf("connect in-process MCP client: %w", err)
	}

	ts := &ToolSession{client: clientSession, server: serverSession}
	tools, err := ts.loadTools(ctx)
	if err != nil {
		_ = ts.Close()
		return nil, err
	}
	ts.tools = tools
	ts.byName = make(map[string]ToolInfo, len(tools))
	for _, tool := range tools {
		ts.byName[tool.Name] = tool
	}
	return ts, nil
}

func (ts *ToolSession) loadTools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for {
		res, err := ts.client.ListTools(ctx, &mcpsdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		for _, tool := range res.Tools {
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("marshal input schema for %s: %w", tool.Name, err)
			}
			out = append(out, ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: schema,
			})
		}
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// ListTools returns the tools advertised to this session's actor.
func (ts *ToolSession) ListTools() []ToolInfo {
	out := make([]ToolInfo, len(ts.tools))
	copy(out, ts.tools)
	return out
}

// CallTool invokes an advertised tool with raw JSON arguments.
func (ts *ToolSession) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolCallResult, error) {
	if _, ok := ts.byName[name]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotAdvertised, name)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	res, err := ts.client.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	out := &ToolCallResult{IsError: res.IsError}
	var texts []string
	for _, c := range res.Content {
		if text, ok := c.(*mcpsdk.TextContent); ok && text.Text != "" {
			texts = append(texts, text.Text)
		}
	}
	out.Text = strings.Join(texts, "\n")
	if res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			out.Structured = raw
		}
	}
	if len(out.Structured) == 0 && out.Text != "" {
		trimmed := strings.TrimSpace(out.Text)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
			out.Structured = json.RawMessage(trimmed)
		}
	}
	return out, nil
}

// Close releases the client and server sessions. It is safe to call more than
// once.
func (ts *ToolSession) Close() error {
	ts.closeOnce.Do(func() {
		var errs []error
		if ts.client != nil {
			if err := ts.client.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if ts.server != nil {
			if err := ts.server.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		ts.closeErr = errors.Join(errs...)
	})
	return ts.closeErr
}
