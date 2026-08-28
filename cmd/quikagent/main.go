// Command quikagent is a minimal terminal coding agent.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"

	"quikagent/internal/agent"
	"quikagent/internal/config"
	"quikagent/internal/llm"
	"quikagent/internal/router"
	"quikagent/internal/server"
	"quikagent/internal/session"
	"quikagent/internal/text"
	"quikagent/internal/tools"
	"quikagent/internal/tui"
)

func main() {
	printMode := flag.String("p", "", "print mode: run one turn non-interactively and exit")
	contin := flag.Bool("continue", false, "resume the most recent session")
	sessionID := flag.String("session", "", "resume a specific session by id")
	planMode := flag.Bool("plan", false, "start in plan (read-only) mode")
	showVersion := flag.Bool("version", false, "print version and exit")
	webListen := flag.String("web", "", "serve web UI on this address (default host 127.0.0.1; e.g. 8080 or 127.0.0.1:8080)")
	webListenAll := flag.Bool("web-listen-all", false, "allow --web to bind non-loopback addresses")
	autoYes := flag.Bool("yes", false, "in print mode, auto-approve write/edit/mutating bash (CI)")
	desktop := flag.Bool("desktop", false, "open the web UI in the system browser (loopback)")
	exportID := flag.String("export", "", "print a session as markdown and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "quikagent — terminal coding agent\n\nusage: quikagent [flags] [prompt...]\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version())
		return
	}

	if err := run(*printMode, *contin, *sessionID, *planMode, *webListen, *webListenAll, *autoYes, *desktop, *exportID, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "quikagent:", err)
		os.Exit(1)
	}
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// llmSummarizer implements the agent.Summarizer interface using an LLM client.
type llmSummarizer struct {
	client *llm.Client
	model  string
}

func (s *llmSummarizer) Summarize(ctx context.Context, text string) (string, error) {
	// Use a short prompt for summarization
	prompt := fmt.Sprintf("Summarize this conversation for continuity: user's goals, key decisions, files changed, current state, open tasks. Under 300 words.\n\n%s", text)

	// Create a system message with the summarizer context
	systemMsg := llm.Message{
		Role:    llm.RoleSystem,
		Content: "You are an expert at summarizing conversations for continuity. You focus on goals, decisions, changes, and open tasks.",
	}
	userMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: prompt,
	}

	messages := []llm.Message{systemMsg, userMsg}

	// Use ChatOnce to get a single response
	result, err := s.client.ChatOnce(ctx, s.model, messages, 2048)
	if err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}

	return result, nil
}

func run(print string, cont bool, sessionID string, plan bool, webListen string, webListenAll, autoYes, desktop bool, exportID string, args []string) error {
	prompt := print
	if prompt == "" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		prompt = strings.Join(args, " ")
	}

	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load(workdir)
	if err != nil {
		return err
	}
	if webListen != "" && print != "" {
		return fmt.Errorf("--web cannot be combined with -p")
	}
	if exportID != "" {
		if cont {
			last, err := session.Latest()
			if err != nil {
				return err
			}
			return exportSession(last.ID)
		}
		return exportSession(exportID)
	}
	if desktop && webListen == "" {
		webListen = "127.0.0.1:0"
	}

	interactive := prompt == "" && webListen == ""
	var term *tui.Terminal
	if interactive {
		term, err = tui.NewTerminal()
		if err != nil {
			return err
		}
		defer term.Shutdown()
		if config.NeedsSetup(cfg) {
			res, err := tui.RunSetup(term, cfg)
			if err != nil {
				return err
			}
			if res != tui.SetupSaved {
				return fmt.Errorf("setup cancelled; set QUIKAGENT_API_KEY or run again to configure")
			}
		}
	} else if config.NeedsSetup(cfg) {
		return fmt.Errorf("no API key; set QUIKAGENT_API_KEY or run quikagent interactively to save ~/.quikagent/config.yaml")
	}

	registry := tools.New(workdir)
	defer registry.Close()
	if cfg.WebSearchURL != "" {
		registry.Add(tools.NewWebSearch(cfg.WebSearchURL, cfg.WebSearchKey))
	}
	if cfg.LSP.Command != "" {
		registry.Add(tools.NewLSP(workdir, tools.LSPConfig{Command: cfg.LSP.Command, Args: cfg.LSP.Args}))
	}
	if warnings, err := tools.AttachMCP(registry, cfg.MCPServers); err != nil {
		return err
	} else {
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "quikagent:", w)
		}
	}

	client := llm.New(cfg.BaseURL, cfg.APIKey, cfg.Model)
	ag := agent.New(client, registry, agent.Options{
		Workdir:   workdir,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
	})

	// Set up LLM-based summarizer for compaction
	if cfg.SmallModel != "" {
		ag.SetSummarizer(&llmSummarizer{
			client: client,
			model:  cfg.SmallModel,
		})
	}

	if plan {
		ag.SetMode(agent.Plan)
	}
	if cfg.Router.Enabled {
		ag.SetRouter(router.New(client, cfg.Router))
		ag.SetRouterEnabled(true)
	}

	var sess *session.Session
	resume := false
	switch {
	case sessionID != "":
		loaded, err := session.Load(sessionID)
		if err != nil {
			return err
		}
		sess = loaded
		ag.LoadHistory(sess.Messages())
		resume = true
	case cont:
		last, err := session.Latest()
		if err == nil {
			sess = last
			ag.LoadHistory(sess.Messages())
			resume = true
		} else {
			sess, err = session.Create()
			if err != nil {
				return err
			}
		}
	default:
		var err error
		sess, err = session.Create()
		if err != nil {
			return err
		}
	}
	if sess.SkippedCorrupt > 0 {
		fmt.Fprintf(os.Stderr, "warning: skipped %d corrupt session line(s)\n", sess.SkippedCorrupt)
	}

	if webListen != "" {
		addr, err := normalizeWebAddr(webListen, webListenAll)
		if err != nil {
			return err
		}
		srv := server.New(ag, sess)
		srv.SetPermissions(cfg.Permissions.Allow, cfg.Permissions.Deny)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		actual := ln.Addr().String()
		url := "http://" + displayWebURL(actual)
		fmt.Fprintf(os.Stderr, "quikagent web UI on %s (session %s)\n", url, sess.ID)
		errCh := make(chan error, 1)
		go func() { errCh <- http.Serve(ln, srv.Handler()) }()
		if desktop {
			openBrowser(url)
		}
		return <-errCh
	}

	if prompt != "" {
		return runPrint(ag, sess, prompt, autoYes, cfg.Permissions)
	}

	app := tui.NewApp(term, ag, client, sess, cfg)
	if resume {
		app.ReplayHistory(sess.Messages())
	}
	app.Run()
	return nil
}

func normalizeWebAddr(web string, listenAll bool) (string, error) {
	addr := strings.TrimSpace(web)
	if addr == "" {
		return "", fmt.Errorf("empty --web address")
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	} else if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	host, _, err := splitHostPort(addr)
	if err != nil {
		return "", err
	}
	if !listenAll && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", fmt.Errorf("--web host %q is not loopback; pass --web-listen-all to bind on all interfaces", host)
	}
	return addr, nil
}

func splitHostPort(addr string) (host, port string, err error) {
	// net.SplitHostPort requires brackets for IPv6; handle host:port.
	if strings.HasPrefix(addr, "[") {
		return net.SplitHostPort(addr)
	}
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", fmt.Errorf("address %q: missing port", addr)
	}
	return addr[:i], addr[i+1:], nil
}

func exportSession(id string) error {
	s, err := session.Load(id)
	if err != nil {
		return err
	}
	if s.Title != "" {
		fmt.Printf("# %s\n\n", s.Title)
	} else {
		fmt.Printf("# session %s\n\n", s.ID)
	}
	for _, m := range s.Messages() {
		switch m.Role {
		case "user":
			fmt.Printf("## User\n\n%s\n\n", m.Content)
		case "assistant":
			if m.Content != "" {
				fmt.Printf("## Assistant\n\n%s\n\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Printf("### Call %s\n\n```\n%s\n```\n\n", tc.Name, tc.Arguments)
			}
		case "tool":
			fmt.Printf("### Tool %s\n\n```\n%s\n```\n\n", m.Name, m.Content)
		}
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func displayWebURL(addr string) string {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return host + ":" + port
}

// runPrint executes one turn headless, streaming output to stdout.
func runPrint(ag *agent.Agent, sess *session.Session, prompt string, autoYes bool, perms config.Permissions) error {
	ag.SetAllowTool(printAllowTool(autoYes, perms))
	compacted := false
	ag.SetOnCompact(func([]llm.Message) { compacted = true })
	base := len(sess.Messages())
	ctx := context.Background()
	ev := make(chan agent.Event)
	go ag.Run(ctx, prompt, ev)

	var turnErr error
	for e := range ev {
		switch e.Type {
		case agent.EventRoute:
			if e.Text != "" {
				fmt.Fprintf(os.Stderr, "route=%s model=%s (router fallback: %s)\n", e.Name, e.Model, e.Text)
			} else {
				fmt.Fprintf(os.Stderr, "route=%s model=%s\n", e.Name, e.Model)
			}
		case agent.EventThinking:
			fmt.Fprint(os.Stderr, e.Text)
		case agent.EventText:
			fmt.Print(e.Text)
		case agent.EventToolStart:
			fmt.Fprintf(os.Stderr, "⏺ %s\n", e.Name)
		case agent.EventToolDone:
			out := text.ClipRunes(e.Output, 400)
			fmt.Fprintf(os.Stderr, "  %s\n", strings.ReplaceAll(out, "\n", " ⏎ "))
		case agent.EventError:
			turnErr = e.Err
		case agent.EventTurnDone:
			fmt.Println()
		}
	}

	if compacted {
		// History was rewritten mid-turn; the append-from-base offsets no
		// longer line up, so persist the whole compacted conversation.
		if err := sess.Replace(ag.Messages()); err != nil {
			if turnErr != nil {
				return fmt.Errorf("%w (also session save: %v)", turnErr, err)
			}
			return fmt.Errorf("session save: %w", err)
		}
		sess.EnsureTitle()
		return turnErr
	}
	for _, m := range ag.Messages()[base:] {
		if err := sess.Append(m); err != nil {
			if turnErr != nil {
				return fmt.Errorf("%w (also session save: %v)", turnErr, err)
			}
			return fmt.Errorf("session save: %w", err)
		}
	}
	sess.EnsureTitle()
	return turnErr
}

func printAllowTool(autoYes bool, perms config.Permissions) agent.AllowFunc {
	// Create one reader to avoid dropping buffered input (e.g., "y\ny\n" typed fast).
	reader := bufio.NewReader(os.Stdin)
	return func(ctx context.Context, name, args string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch tools.CheckPermission(perms.Allow, perms.Deny, name, args) {
		case tools.MatchDeny:
			return fmt.Errorf("denied by permissions")
		case tools.MatchAllow:
			return nil
		}
		if !tools.NeedsInteractiveApproval(name, args) {
			return nil
		}
		if autoYes {
			return nil
		}
		fmt.Fprintf(os.Stderr, "approve %s? [y/N] ", name)
		type readResult struct {
			line string
			err  error
		}
		ch := make(chan readResult, 1)
		go func() {
			line, err := reader.ReadString('\n')
			ch <- readResult{line, err}
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-ch:
			if r.err != nil && r.line == "" {
				return fmt.Errorf("user denied %s", name)
			}
			line := strings.TrimSpace(strings.ToLower(r.line))
			if line == "y" || line == "yes" {
				return nil
			}
			return fmt.Errorf("user denied %s", name)
		}
	}
}
