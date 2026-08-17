package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/runtime"
)

func main() {
	runtimeDir := config.ResolveRuntimeDir()
	apiBase := runtime.ResolveAPIBase(runtimeDir)
	apiToken, err := runtime.ReadAPIToken(runtimeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "traio-mcp: API token: %v\n", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 15 * time.Second}

	s := server.NewMCPServer("traio", "0.1.0")

	s.AddTool(mcp.NewTool("traio_health",
		mcp.WithDescription("Check Traio backend health"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, err := getJSON(client, apiBase+"/health", "")
		return textResult(body, err)
	})

	s.AddTool(mcp.NewTool("traio_ibkr_gateway_status",
		mcp.WithDescription("Get IBKR Client Portal Gateway status"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, err := getJSON(client, apiBase+"/api/v1/ibkr/gateway/status", apiToken)
		return textResult(body, err)
	})

	s.AddTool(mcp.NewTool("traio_settings_get",
		mcp.WithDescription("Get all Traio settings (API keys included)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, err := getJSON(client, apiBase+"/api/v1/settings", apiToken)
		return textResult(body, err)
	})

	s.AddTool(mcp.NewTool("traio_watchlist_groups",
		mcp.WithDescription("List watchlist groups"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, err := getJSON(client, apiBase+"/api/v1/watchlist/groups", apiToken)
		return textResult(body, err)
	})

	s.AddTool(mcp.NewTool("traio_quote",
		mcp.WithDescription("Get quote for a symbol"),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Ticker symbol, e.g. AAPL")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		symbol, err := req.RequireString("symbol")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, err := getJSON(client, apiBase+"/api/v1/quotes/"+symbol, apiToken)
		return textResult(body, err)
	})

	s.AddTool(mcp.NewTool("traio_positions",
		mcp.WithDescription("List canonical portfolio positions grouped by instrument_id"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		body, err := getJSON(client, apiBase+"/api/v1/portfolio/positions", apiToken)
		return textResult(body, err)
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "traio-mcp: %v\n", err)
		os.Exit(1)
	}
}

func getJSON(client *http.Client, url, token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	var pretty json.RawMessage
	if json.Unmarshal(data, &pretty) == nil {
		out, _ := json.MarshalIndent(pretty, "", "  ")
		return string(out), nil
	}
	return string(data), nil
}

func textResult(body string, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(body), nil
}
