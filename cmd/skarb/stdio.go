package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func runMCPStdio(args []string) error {
	fs := flag.NewFlagSet("mcp-stdio", flag.ExitOnError)
	url := fs.String("url", os.Getenv("SKARB_MCP_URL"), "Skarb MCP HTTP endpoint, e.g. https://skarb.example.com/mcp")
	_ = fs.Parse(args)
	if strings.TrimSpace(*url) == "" {
		return fmt.Errorf("--url or SKARB_MCP_URL is required")
	}
	token := strings.TrimSpace(os.Getenv("SKARB_MCP_TOKEN"))
	if token == "" {
		return fmt.Errorf("SKARB_MCP_TOKEN is required")
	}

	ctx := context.Background()
	httpClient := &http.Client{Transport: &bearerTransport{token: token}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:        "skarb-bridge",
		Title:       "Skarb stdio bridge",
		Description: "Forwards MCP calls to a Skarb HTTP endpoint",
		Version:     "1.0.0",
	}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             *url,
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return fmt.Errorf("connect to Skarb MCP: %w", err)
	}
	defer session.Close()

	var tools []*mcpsdk.Tool
	params := &mcpsdk.ListToolsParams{}
	for {
		res, err := session.ListTools(ctx, params)
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		params.Cursor = res.NextCursor
	}

	local := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:        "skarb",
		Title:       "Skarb",
		Description: "Family money tracker via Skarb HTTP MCP",
		Version:     "1.0.0",
	}, nil)
	for _, upstream := range tools {
		tool := upstream
		local.AddTool(tool, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			var args any
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, fmt.Errorf("invalid tool arguments: %w", err)
				}
			}
			return session.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool.Name, Arguments: args})
		})
	}
	if err := local.Run(ctx, &mcpsdk.StdioTransport{}); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("stdio bridge: %w", err)
	}
	return nil
}

type bearerTransport struct {
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}
