package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Client is a minimal MCP JSON-RPC client over Streamable HTTP.
// It does NOT implement the full MCP spec — only initialize + tools/call,
// which is all this daemon needs.
type Client struct {
	endpoint    string
	http        *http.Client
	tokenSource TokenSource
	sessionID   string
	idCounter   atomic.Int64
}

// TokenSource provides a fresh access token (refreshes on demand).
type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

func New(endpoint string, ts TokenSource) *Client {
	return &Client{
		endpoint:    endpoint,
		http:        &http.Client{Timeout: 30 * time.Second},
		tokenSource: ts,
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// Raw exposes a raw JSON-RPC call (e.g. "tools/list"). Returns the unwrapped
// result payload — useful for probing the server's tool catalog.
func (c *Client) Raw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.call(ctx, method, params)
}

// Initialize performs the MCP initialize handshake. Required once per session.
func (c *Client) Initialize(ctx context.Context) error {
	resp, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "indmoney-watch", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	_ = resp
	// Send initialized notification (per MCP spec).
	return c.notify(ctx, "notifications/initialized", nil)
}

// CallTool calls a tool by name with the given arguments and unmarshals the
// "structured" result into out. INDmoney returns a tool result whose `content`
// has a single text item containing JSON; we unwrap that.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any, out any) error {
	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return err
	}

	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return fmt.Errorf("decode tool result: %w", err)
	}
	if wrapper.IsError {
		return fmt.Errorf("tool %s returned error: %s", name, firstText(wrapper.Content))
	}
	if len(wrapper.Content) == 0 {
		return fmt.Errorf("tool %s returned empty content", name)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(wrapper.Content[0].Text), out)
}

func firstText(items []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Text
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.idCounter.Add(1)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	respBody, err := c.do(ctx, body)
	if err != nil {
		return nil, err
	}
	var resp rpcResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		// Streamable HTTP can return SSE; try to parse the first data: line.
		if line := extractSSEData(respBody); line != nil {
			if err2 := json.Unmarshal(line, &resp); err2 == nil {
				goto decoded
			}
		}
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, truncate(string(respBody), 200))
	}
decoded:
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, body)
	return err
}

func (c *Client) do(ctx context.Context, body []byte) ([]byte, error) {
	tok, err := c.tokenSource.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	return respBody, nil
}

// ErrUnauthorized is returned when the access token is invalid/expired.
var ErrUnauthorized = fmt.Errorf("unauthorized")

func extractSSEData(b []byte) []byte {
	const prefix = "data: "
	for {
		nl := bytes.IndexByte(b, '\n')
		if nl < 0 {
			break
		}
		line := bytes.TrimRight(b[:nl], "\r")
		b = b[nl+1:]
		if bytes.HasPrefix(line, []byte(prefix)) {
			return line[len(prefix):]
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
