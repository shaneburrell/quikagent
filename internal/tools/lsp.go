package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"quikagent/internal/llm"
)

// LSPConfig names a language server command.
type LSPConfig struct {
	Command string
	Args    []string
}

type lspTool struct {
	workdir string
	cfg     LSPConfig
}

// NewLSP builds an experimental LSP tool. Callers should skip registration
// when Command is empty.
func NewLSP(workdir string, cfg LSPConfig) Tool {
	return &lspTool{workdir: workdir, cfg: cfg}
}

func (t *lspTool) ReadOnly() bool { return true }

func (t *lspTool) Spec() llm.Tool {
	return llm.Tool{
		Name:        "lsp",
		Description: "Query a language server (experimental). ops: hover, definition, references, symbols. Requires lsp.command in config.",
		Parameters:  []byte(`{"type":"object","properties":{"op":{"type":"string","enum":["hover","definition","references","symbols"]},"path":{"type":"string"},"line":{"type":"integer"},"character":{"type":"integer"},"query":{"type":"string"}},"required":["op"]}`),
	}
}

func (t *lspTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if strings.TrimSpace(t.cfg.Command) == "" {
		return "", fmt.Errorf("lsp not configured")
	}
	var a struct {
		Op        string `json:"op"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
		Query     string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", errInvalidArg(err.Error())
	}
	switch a.Op {
	case "hover", "definition", "references", "symbols":
	default:
		return "", errInvalidArg("unknown op")
	}
	c, err := startLSP(ctx, t.workdir, t.cfg)
	if err != nil {
		return "", err
	}
	defer c.close()

	root, _ := resolve(t.workdir, ".")
	if _, err := c.call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathURI(root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover":      map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"definition": map[string]any{},
				"references": map[string]any{},
			},
			"workspace": map[string]any{"symbol": map[string]any{}},
		},
	}); err != nil {
		return "", fmt.Errorf("lsp initialize: %w", err)
	}
	_ = c.notify("initialized", map[string]any{})

	var raw json.RawMessage
	switch a.Op {
	case "symbols":
		raw, err = c.call(ctx, "workspace/symbol", map[string]any{"query": a.Query})
	case "hover", "definition", "references":
		if a.Path == "" {
			return "", errInvalidArg("path is required")
		}
		abs, err := resolve(t.workdir, a.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		_ = c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri": pathURI(abs), "languageId": langID(abs), "version": 1, "text": string(data),
			},
		})
		pos := map[string]any{
			"textDocument": map[string]any{"uri": pathURI(abs)},
			"position":     map[string]any{"line": a.Line, "character": a.Character},
		}
		switch a.Op {
		case "hover":
			raw, err = c.call(ctx, "textDocument/hover", pos)
		case "definition":
			raw, err = c.call(ctx, "textDocument/definition", pos)
		case "references":
			pos["context"] = map[string]any{"includeDeclaration": true}
			raw, err = c.call(ctx, "textDocument/references", pos)
		}
	default:
		return "", errInvalidArg("unknown op")
	}
	if err != nil {
		return "", err
	}
	return truncate(string(raw)), nil
}

func pathURI(abs string) string { return "file://" + abs }

func langID(path string) string {
	switch strings.ToLower(filepathExt(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	default:
		return "plaintext"
	}
}

func filepathExt(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[i:]
}

type lspClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scan    *bufio.Reader
	mu      sync.Mutex
	next    atomic.Int64
	pending map[int64]chan lspResp
	done    chan struct{}
}

type lspRPCError struct {
	Message string `json:"message"`
}

type lspResp struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *lspRPCError    `json:"error"`
}

func startLSP(ctx context.Context, workdir string, cfg LSPConfig) (*lspClient, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = workdir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &lspClient{
		cmd: cmd, stdin: stdin, scan: bufio.NewReader(stdout),
		pending: map[int64]chan lspResp{}, done: make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *lspClient) close() {
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

func (c *lspClient) readLoop() {
	defer func() {
		c.failAllPending("lsp connection closed")
		close(c.done)
	}()
	for {
		line, err := c.scan.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(strings.ToLower(line), "content-length:") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Content-Length:")))
		if err != nil {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				c.failAllPending("malformed Content-Length header")
				return
			}
			n, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				c.failAllPending("malformed Content-Length header")
				return
			}
		}
		for {
			blank, err := c.scan.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(blank) == "" {
				break
			}
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.scan, body); err != nil {
			return
		}
		var resp lspResp
		if err := json.Unmarshal(body, &resp); err != nil {
			c.rejectMalformed(body, err)
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

func (c *lspClient) rejectMalformed(body []byte, err error) {
	var idOnly struct {
		ID *int64 `json:"id"`
	}
	if json.Unmarshal(body, &idOnly) == nil && idOnly.ID != nil {
		c.mu.Lock()
		ch, ok := c.pending[*idOnly.ID]
		if ok {
			delete(c.pending, *idOnly.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- lspResp{ID: *idOnly.ID, Error: &lspRPCError{Message: "malformed JSON: " + err.Error()}}
		}
		return
	}
	c.failAllPending("malformed JSON: " + err.Error())
}

func (c *lspClient) failAllPending(msg string) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int64]chan lspResp{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- lspResp{Error: &lspRPCError{Message: msg}}
	}
}

func (c *lspClient) dropPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *lspClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	ch := make(chan lspResp, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer c.dropPending(id)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.write(msg); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp: %s", resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *lspClient) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *lspClient) write(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}
