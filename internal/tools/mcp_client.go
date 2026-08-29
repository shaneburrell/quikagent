package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"quikagent/internal/config"
	"quikagent/internal/llm"
)

type mcpClient struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scan    *bufio.Scanner
	mu      sync.Mutex
	next    atomic.Int64
	pending map[int64]chan mcpResponse
	done    chan struct{}
}

type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

func attachOneMCP(r *Registry, name string, srv config.MCPServer) error {
	cmd := exec.CommandContext(context.Background(), srv.Command, srv.Args...)
	cmd.Env = scrubMCPEnv(srv.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp %s stdin: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp %s stdout: %w", name, err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp %s start: %w", name, err)
	}

	c := &mcpClient{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		scan:    bufio.NewScanner(stdout),
		pending: map[int64]chan mcpResponse{},
		done:    make(chan struct{}),
	}
	c.scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	go c.readLoop()

	initCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.call(initCtx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "quikagent", "version": "0.1"},
	}); err != nil {
		c.close()
		return fmt.Errorf("mcp %s initialize: %w", name, err)
	}
	_ = c.notify("notifications/initialized", map[string]any{})

	listCtx, cancelList := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelList()
	result, err := c.call(listCtx, "tools/list", map[string]any{})
	if err != nil {
		c.close()
		return fmt.Errorf("mcp %s tools/list: %w", name, err)
	}
	var listed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		c.close()
		return fmt.Errorf("mcp %s decode tools: %w", name, err)
	}
	for _, t := range listed.Tools {
		params := t.InputSchema
		if len(params) == 0 {
			params = []byte(`{"type":"object","properties":{}}`)
		}
		toolName := "mcp_" + name + "_" + t.Name
		r.Add(&mcpTool{
			client:     c,
			remoteName: t.Name,
			spec: llm.Tool{
				Name:        toolName,
				Description: fmt.Sprintf("[mcp:%s] %s", name, t.Description),
				Parameters:  params,
			},
		})
	}
	r.AddCloser(c.close)
	return nil
}

func (c *mcpClient) readLoop() {
	defer func() {
		c.failAllPending("mcp connection closed")
		close(c.done)
	}()
	for c.scan.Scan() {
		line := c.scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp mcpResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			c.rejectMalformed(line, err)
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

func (c *mcpClient) rejectMalformed(line []byte, err error) {
	var idOnly struct {
		ID *int64 `json:"id"`
	}
	if json.Unmarshal(line, &idOnly) == nil && idOnly.ID != nil {
		c.mu.Lock()
		ch, ok := c.pending[*idOnly.ID]
		if ok {
			delete(c.pending, *idOnly.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- mcpResponse{ID: *idOnly.ID, Error: &mcpRPCError{Message: "malformed JSON: " + err.Error()}}
		}
		return
	}
	c.failAllPending("malformed JSON: " + err.Error())
}

func (c *mcpClient) failAllPending(msg string) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int64]chan mcpResponse{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- mcpResponse{Error: &mcpRPCError{Message: msg}}
	}
}

func (c *mcpClient) dropPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	ch := make(chan mcpResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer c.dropPending(id)

	req := mcpRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	_, err = c.stdin.Write(append(line, '\n'))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s", resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *mcpClient) notify(method string, params any) error {
	req := mcpRequest{JSONRPC: "2.0", Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(line, '\n'))
	return err
}

func (c *mcpClient) close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	if c.done != nil {
		<-c.done
	}
}

type mcpTool struct {
	client     *mcpClient
	remoteName string
	spec       llm.Tool
}

func (t *mcpTool) Spec() llm.Tool { return t.spec }
func (t *mcpTool) ReadOnly() bool { return false }
func (t *mcpTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return "", errInvalidArg(err.Error())
		}
	} else {
		params = map[string]any{}
	}
	result, err := t.client.call(ctx, "tools/call", map[string]any{
		"name":      t.remoteName,
		"arguments": params,
	})
	if err != nil {
		return "", err
	}
	var wrap struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &wrap); err != nil {
		return truncate(string(result)), nil
	}
	var b string
	for _, c := range wrap.Content {
		if c.Type == "text" || c.Text != "" {
			if b != "" {
				b += "\n"
			}
			b += c.Text
		}
	}
	if b == "" {
		b = string(result)
	}
	if wrap.IsError {
		return "Error: " + truncate(b), nil
	}
	return truncate(b), nil
}

func scrubMCPEnv(extra map[string]string) []string {
	allow := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"LANG": true, "LC_ALL": true, "TERM": true, "TMPDIR": true,
	}
	var out []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		uk := strings.ToUpper(k)
		if strings.HasPrefix(uk, "QUIKAGENT_") || strings.Contains(uk, "API_KEY") ||
			strings.Contains(uk, "SECRET") || strings.Contains(uk, "TOKEN") || strings.Contains(uk, "PASSWORD") {
			continue
		}
		if allow[k] || strings.HasPrefix(k, "LC_") {
			out = append(out, e)
		}
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
