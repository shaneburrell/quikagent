package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"quikagent/internal/agent"
	"quikagent/internal/config"
	"quikagent/internal/llm"
	"quikagent/internal/router"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

const spinnerInterval = 80 * time.Millisecond
const promptHistoryMax = 50

type approveRequest struct {
	name  string
	args  string
	reply chan error
}

type questionRequest struct {
	q     tools.Question
	reply chan questionReply
}

type questionReply struct {
	answer string
	err    error
}

// sideData holds the sidebar data to be refreshed asynchronously.
type sideData struct {
	modified []string
	sessions []session.Info
	mcp      []string
	preview  string
}

// App wires the terminal, renderer, transcript, input buffer and agent
// into the interactive event loop.
type App struct {
	term     *Terminal
	renderer *Renderer
	model    *Model
	input    *Input
	agent    *agent.Agent
	client   *llm.Client
	sess     *session.Session
	cfg      *config.Config

	width, height int
	scroll        int
	sidebarScroll int
	busy          bool
	phase         string
	follow        bool
	hasNew        bool
	sidebarOn     bool
	route         string
	usage         agent.Usage
	pendingBase   int
	turnCancel    context.CancelFunc
	evCh          chan agent.Event
	ticker        *time.Ticker
	tickCh        <-chan time.Time

	// Layout cache for mouse hit-testing (updated each render).
	hitMainW    int
	hitSideW    int
	hitHistH    int
	hitInputTop int // 0-based first input row
	hitInputN   int // number of input rows
	hitBodyH    int // transcript+sep+input (excludes status)

	// Overlays: setup | config | palette | models (empty = main UI).
	overlay string
	setup   *setupForm
	configM *configMenu
	palette *paletteState
	models  *modelPicker

	history    []string
	historyIdx int

	// redoStack holds full history snapshots removed by /undo.
	redoStack [][]llm.Message
	// alwaysAllow remembers tools the user approved with "A" this session.
	alwaysAllow map[string]bool

	modified []string
	sessions []session.Info
	mcpNames []string
	preview  string
	todos    []tools.TodoItem

	inputCh   chan []byte
	approveCh chan approveRequest
	pending   *approveRequest
	askCh     chan questionRequest
	asking    *questionRequest
	askInput  string
	done      chan struct{}
	quit      sync.Once

	// Async refresh support
	sideGen    int64
	sideCh     chan sideData
	compactCh  chan bool
	compacting bool

	// Incomplete CSI paste introducer carried across terminal reads.
	keyCarry []byte

	// Set from the agent goroutine when Compact() rewrote history
	// (auto-compact mid-turn); syncSession replaces instead of appending.
	compacted atomic.Bool
}

// NewApp builds an interactive app over a taken-over terminal.
func NewApp(term *Terminal, ag *agent.Agent, client *llm.Client, sess *session.Session, cfg *config.Config) *App {
	w, h := term.Size()
	a := &App{
		term:        term,
		renderer:    NewRenderer(term),
		model:       NewModel(),
		input:       NewInput(),
		agent:       ag,
		client:      client,
		sess:        sess,
		cfg:         cfg,
		width:       w - 1,
		height:      h,
		follow:      true,
		sidebarOn:   cfg.Sidebar,
		historyIdx:  -1,
		inputCh:     make(chan []byte, 16),
		approveCh:   make(chan approveRequest, 1),
		askCh:       make(chan questionRequest, 1),
		alwaysAllow: map[string]bool{},
		done:        make(chan struct{}),
		sideCh:      make(chan sideData, 1),
		compactCh:   make(chan bool, 1),
	}
	ag.SetAllowTool(a.allowTool)
	ag.SetQuestionAsk(a.askQuestion)
	ag.SetOnCompact(func([]llm.Message) { a.compacted.Store(true) })
	agent.BindSessionTrace(ag, sess, "tui")
	a.refreshSideData()
	return a
}

func (a *App) allowTool(ctx context.Context, name, args string) error {
	switch tools.CheckPermission(a.cfg.Permissions.Allow, a.cfg.Permissions.Deny, name, args) {
	case tools.MatchDeny:
		return fmt.Errorf("denied by permissions")
	case tools.MatchAllow:
		return nil
	}

	if !tools.NeedsInteractiveApproval(name, args) {
		return nil
	}
	if a.alwaysAllow[name] {
		return nil
	}
	req := approveRequest{name: name, args: args, reply: make(chan error, 1)}
	select {
	case a.approveCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return fmt.Errorf("app closed")
	}
	select {
	case err := <-req.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return fmt.Errorf("app closed")
	}
}

func (a *App) askQuestion(ctx context.Context, q tools.Question) (string, error) {
	req := questionRequest{q: q, reply: make(chan questionReply, 1)}
	select {
	case a.askCh <- req:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-a.done:
		return "", fmt.Errorf("app closed")
	}
	select {
	case r := <-req.reply:
		return r.answer, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-a.done:
		return "", fmt.Errorf("app closed")
	}
}

// ReplayHistory rebuilds the transcript from a loaded session.
func (a *App) ReplayHistory(msgs []llm.Message) {
	for _, msg := range msgs {
		switch msg.Role {
		case llm.RoleUser:
			a.model.User(msg.Content)
		case llm.RoleAssistant:
			if msg.Reasoning != "" {
				a.model.Thinking(msg.Reasoning)
			}
			if msg.Content != "" {
				a.model.Text(msg.Content)
			}
			for _, tc := range msg.ToolCalls {
				a.model.ToolStart(tc.Name, tc.Arguments)
			}
		case llm.RoleTool:
			a.model.ToolDone(msg.Name, msg.Content)
		}
	}
	// Replay is historical: no tool should keep spinning, and thinking
	// should not stay expanded as if it were still streaming.
	a.model.closeOpenTools()
	a.model.clearWait()
	a.model.collapseFinishedThinking()
}

// Run blocks until the user quits. It restores nothing; the caller
// owns terminal shutdown.
func (a *App) Run() {
	a.layout()
	a.renderer.Resize(a.term.Size())
	go a.readLoop()
	go a.signalLoop()
	a.render()

	for {
		var evCh <-chan agent.Event
		if a.busy && a.evCh != nil {
			evCh = a.evCh
		}
		select {
		case buf := <-a.inputCh:
			if len(a.keyCarry) > 0 {
				joined := make([]byte, 0, len(a.keyCarry)+len(buf))
				joined = append(joined, a.keyCarry...)
				joined = append(joined, buf...)
				buf = joined
				a.keyCarry = nil
			}
			keys, rest := parseRemain(buf)
			if len(rest) > 0 {
				a.keyCarry = append([]byte(nil), rest...)
			}
			a.handleKeys(keys)
			a.render()
		case e, ok := <-evCh:
			if !ok {
				a.syncSession()
				a.endTurn()
				a.model.Error("agent event stream closed")
				a.render()
				continue
			}
			a.handleEvent(e)
			a.render()
		case req := <-a.approveCh:
			a.pending = &req
			a.render()
		case req := <-a.askCh:
			a.asking = &req
			a.askInput = ""
			a.render()
		case data := <-a.sideCh:
			a.modified = data.modified
			a.sessions = data.sessions
			a.mcpNames = data.mcp
			a.preview = data.preview
			a.render()
		case ok := <-a.compactCh:
			a.applyCompactResult(ok)
			a.render()
		case <-a.tickCh:
			a.model.AdvanceSpinner()
			a.render()
		case <-a.term.Resized():
			a.onResize()
		case <-a.done:
			return
		}
	}
}

func clipNote(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}

	// Use rune-based truncation to avoid splitting UTF-8 characters
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// cancelTurnIfBusy cancels the current turn if busy, for safe shutdown.
func (a *App) cancelTurnIfBusy() {
	if a.busy && a.turnCancel != nil {
		a.turnCancel()
	}
}

// Quit ends the loop (safe from any goroutine).
func (a *App) Quit() {
	a.quit.Do(func() {
		a.cancelTurnIfBusy()
		close(a.done)
	})
}

func (a *App) readLoop() {
	buf := make([]byte, 8192)
	for {
		n, err := a.term.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			select {
			case a.inputCh <- cp:
			case <-a.done:
				return
			}
		}
		if err != nil {
			a.Quit()
			return
		}
	}
}

func (a *App) signalLoop() {
	for sig := range a.term.Signal() {
		if sig == syscall.SIGWINCH {
			a.term.NotifyResize()
		}
	}
}

func (a *App) layout() {
	w, h := a.term.Size()
	a.width, a.height = w-1, h
	a.model.SetWidth(a.width)
}

func (a *App) onResize() {
	a.layout()
	a.renderer.Resize(a.term.Size())
	a.render()
}

func (a *App) handleKeys(keys []Key) {
	for _, k := range keys {
		// Overlay owns the keyboard while visible so palette/setup
		// filters cannot silently approve a hidden prompt.
		if a.overlay != "" {
			a.handleOverlayKey(k)
			continue
		}
		if a.pending != nil {
			a.handleApprovalKey(k)
			continue
		}
		if a.asking != nil {
			a.handleQuestionKey(k)
			continue
		}
		if k.Kind == KeyMouse {
			a.handleMouse(k)
			continue
		}
		switch {
		case k.Kind == KeyRune:
			a.historyIdx = -1
			a.input.Insert(k.Rune)
		case k.Kind == KeyPaste:
			a.historyIdx = -1
			a.input.Paste(k.Text)
		case k.Kind == KeyCtrl:
			switch k.Ctrl {
			case 'c':
				if a.busy && a.turnCancel != nil {
					a.turnCancel()
				} else if a.compacting {
					a.model.Note("compacting in progress...")
				}
			case 'b':
				a.toggleSidebar()
			case 'p':
				a.openPalette()
			case 'q':
				a.Quit()
			}
		case k.is(KeyEnter):
			if a.compacting {
				a.model.Note("compacting in progress...")
				continue
			}
			if a.busy {
				a.model.Note("turn in progress — ctrl+c to cancel")
			} else if text := strings.TrimSpace(a.input.Text()); text != "" {
				a.submit(a.input.Text())
				a.input.Clear()
			}
		case k.is(KeyShiftEnter):
			if a.busy {
				a.model.Note("turn in progress — ctrl+c to cancel")
				continue
			}
			a.historyIdx = -1
			a.input.Newline()
		case k.is(KeyBackspace):
			a.historyIdx = -1
			a.input.Backspace()
		case k.is(KeyDelete):
			a.historyIdx = -1
			a.input.Delete()
		case k.is(KeyLeft):
			a.input.Left()
		case k.is(KeyRight):
			a.input.Right()
		case k.is(KeyUp):
			if !a.busy && strings.TrimSpace(a.input.Text()) == "" && len(a.history) > 0 {
				a.historyUp()
			} else {
				a.input.Up()
			}
		case k.is(KeyDown):
			if a.historyIdx >= 0 && !a.busy {
				a.historyDown()
			} else {
				a.input.Down()
			}
		case k.is(KeyHome):
			a.input.Home()
		case k.is(KeyEnd):
			a.input.End()
		case k.is(KeyTab):
			a.toggleMode()
		case k.is(KeyF2):
			if !a.busy {
				a.cycleModel(false)
			}
		case k.is(KeyShiftF2):
			if !a.busy {
				a.cycleModel(true)
			}
		case k.is(KeyShiftUp):
			a.follow = false
			a.scrollBy(3)
		case k.is(KeyShiftDown):
			a.scrollBy(-3)
			if a.scroll == 0 {
				a.follow = true
				a.hasNew = false
			}
		case k.is(KeyPageUp):
			a.follow = false
			n := a.hitHistH - 1
			if n < 3 {
				n = 10
			}
			a.scrollBy(n)
		case k.is(KeyPageDown):
			n := a.hitHistH - 1
			if n < 3 {
				n = 10
			}
			a.scrollBy(-n)
			if a.scroll == 0 {
				a.follow = true
				a.hasNew = false
			}
		case k.is(KeyEsc):
			a.input.Clear()
			a.historyIdx = -1
		}
	}
}

func (a *App) openSetup() {
	a.overlay = "setup"
	a.setup = newSetupForm(a.cfg)
}

func (a *App) openConfig() {
	a.overlay = "config"
	a.configM = &configMenu{}
}

func (a *App) openPalette() {
	a.overlay = "palette"
	a.palette = newPalette()
	for _, c := range loadProjectCommands(a.cfg.Workdir) {
		a.palette.all = append(a.palette.all, paletteCmd{
			ID: "cmd:" + c.Name, Label: "/" + c.Name, Hint: "project command",
		})
	}
}

func (a *App) openModels() {
	var api []string
	if a.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		ids, err := a.client.ListModels(ctx)
		cancel()
		if err == nil {
			api = ids
		}
	}
	models := config.KnownModels(a.cfg, api)
	showAuto := a.cfg != nil && len(a.cfg.Router.Routes) > 0
	current := ""
	auto, pinned := false, false
	if a.agent != nil {
		current = a.agent.Model()
		auto = a.agent.RouterEnabled()
		pinned = a.agent.ModelPinned()
	}
	a.overlay = "models"
	a.models = newModelPicker(models, showAuto, current, auto, pinned)
}

// ensureModelsPicker builds the models overlay from cached/known IDs
// without calling ListModels (must not run on the render path).
func (a *App) ensureModelsPicker() {
	if a.models != nil {
		return
	}
	current := ""
	auto, pinned := false, false
	if a.agent != nil {
		current = a.agent.Model()
		auto = a.agent.RouterEnabled()
		pinned = a.agent.ModelPinned()
	}
	showAuto := a.cfg != nil && len(a.cfg.Router.Routes) > 0
	a.models = newModelPicker(config.KnownModels(a.cfg, nil), showAuto, current, auto, pinned)
}

func (a *App) closeOverlay() {
	a.overlay = ""
	a.setup = nil
	a.configM = nil
	a.palette = nil
	a.models = nil
}

func (a *App) applyModelPick(id string) {
	if id == modelPickAuto {
		a.cfg.Router.Enabled = true
		a.syncRouter()
		a.agent.SetModelAuto(a.cfg.Model)
		if a.client != nil {
			a.client.SetModel(a.agent.Model())
		}
		_ = a.cfg.Save()
		a.model.Note("model: auto (Arch-Router)")
		return
	}
	a.agent.SetModel(id)
	a.cfg.Model = id
	a.cfg.Router.Enabled = false
	a.syncRouter()
	if a.client != nil {
		a.client.SetModel(id)
	}
	a.route = ""
	_ = a.cfg.Save()
	a.model.Note("model pinned: " + id)
}

func (a *App) cycleModel(reverse bool) {
	items := make([]string, 0, 8)
	if len(a.cfg.Router.Routes) > 0 {
		items = append(items, modelPickAuto)
	}
	for _, id := range FavoriteModels(a.cfg, a.agent.Model()) {
		dup := false
		for _, x := range items {
			if x == id {
				dup = true
				break
			}
		}
		if !dup {
			items = append(items, id)
		}
	}
	if len(items) == 0 {
		a.openModels()
		return
	}
	cur := a.agent.Model()
	if a.agent.RouterEnabled() {
		cur = modelPickAuto
	}
	idx := 0
	for i, id := range items {
		if id == cur {
			idx = i
			break
		}
	}
	if reverse {
		idx--
		if idx < 0 {
			idx = len(items) - 1
		}
	} else {
		idx = (idx + 1) % len(items)
	}
	a.applyModelPick(items[idx])
}

func (a *App) toggleSidebar() {
	a.sidebarOn = !a.sidebarOn
	a.sidebarScroll = 0
	if a.sidebarOn && SideWidth(a.width+1) == 0 {
		a.model.Note("sidebar needs width ≥ 100")
	} else if a.sidebarOn {
		a.model.Note("sidebar on")
	} else {
		a.model.Note("sidebar off")
	}
}

func (a *App) applyConnection() {
	if a.client != nil {
		a.client.SetAPIKey(a.cfg.APIKey)
		a.client.SetBaseURL(a.cfg.BaseURL)
		a.client.SetModel(a.cfg.Model)
	}
	a.agent.SetModelAuto(a.cfg.Model)
	a.syncRouter()
}

func (a *App) syncRouter() {
	if a.cfg.Router.Enabled && a.client != nil {
		a.agent.SetRouter(router.New(a.client, a.cfg.Router))
		a.agent.SetRouterEnabled(true)
	} else {
		a.agent.SetRouterEnabled(false)
		a.route = ""
	}
}

func (a *App) handleOverlayKey(k Key) {
	switch a.overlay {
	case "setup":
		if a.setup == nil {
			a.closeOverlay()
			return
		}
		res, done := a.setup.handleKey(k)
		if !done {
			return
		}
		if res == SetupSaved {
			a.setup.apply(a.cfg)
			if err := a.cfg.Save(); err != nil {
				a.model.Error("save config: " + err.Error())
			} else {
				a.applyConnection()
				a.model.Note("saved to ~/.quikagent/config.yaml")
			}
		}
		a.closeOverlay()
	case "config":
		if a.configM == nil {
			a.closeOverlay()
			return
		}
		switch a.configM.handleKey(k) {
		case configActClose:
			a.closeOverlay()
		case configActOpenSetup:
			a.openSetup()
		case configActEditModel:
			a.openModels()
		case configActToggleRouter:
			a.cfg.Router.Enabled = !a.cfg.Router.Enabled
			a.syncRouter()
			_ = a.cfg.Save()
			state := "off"
			if a.cfg.Router.Enabled {
				state = "on"
			}
			a.model.Note("router " + state + " (saved)")
		case configActToggleSidebar:
			a.cfg.Sidebar = !a.cfg.Sidebar
			_ = a.cfg.Save()
			a.sidebarOn = a.cfg.Sidebar
			a.sidebarScroll = 0
			state := "off"
			if a.cfg.Sidebar {
				state = "on"
			}
			a.model.Note("sidebar default " + state + " (saved)")
		case configActShowPath:
			path, _ := config.Path()
			a.model.Note("config file: " + path)
		}
	case "palette":
		if a.palette == nil {
			a.closeOverlay()
			return
		}
		switch a.palette.handleKey(k) {
		case paletteActClose:
			a.closeOverlay()
		case paletteActRun:
			cmd, ok := a.palette.selected()
			a.closeOverlay()
			if ok {
				a.runPalette(cmd.ID)
			}
		}
	case "models":
		if a.models == nil {
			a.closeOverlay()
			return
		}
		switch a.models.handleKey(k) {
		case modelPickClose:
			a.closeOverlay()
		case modelPickSelect:
			id, ok := a.models.selected()
			a.closeOverlay()
			if ok {
				a.applyModelPick(id)
			}
		}
	default:
		a.closeOverlay()
	}
}

func (a *App) runPalette(id string) {
	switch id {
	case "setup":
		a.openSetup()
	case "config":
		a.openConfig()
	case "model":
		a.openModels()
	case "router":
		if a.agent.RouterEnabled() {
			a.command("/router off")
		} else {
			a.applyModelPick(modelPickAuto)
		}
	case "sidebar":
		a.toggleSidebar()
	case "sessions":
		a.command("/sessions")
	case "resume":
		a.command("/sessions")
	case "plan":
		a.toggleMode()
	case "compact":
		a.command("/compact")
	case "undo":
		a.command("/undo")
	case "redo":
		a.command("/redo")
	case "refresh":
		a.command("/refresh")
	case "clear":
		a.command("/clear")
	case "help":
		a.command("/help")
	case "init":
		a.command("/init")
	case "quit":
		a.Quit()
	default:
		if name, ok := strings.CutPrefix(id, "cmd:"); ok {
			if prompt, found := lookupProjectCommand(a.cfg.Workdir, name); found {
				a.submit(prompt)
			}
		}
	}
}

func (a *App) handleMouse(k Key) {
	// Wheel events are presses; ignore clicks/drags/releases.
	if !k.Press || (k.Btn != MouseWheelUp && k.Btn != MouseWheelDown) {
		return
	}
	x, y := k.Col-1, k.Row-1
	region := HitTest(x, y, a.hitMainW, a.hitSideW, a.hitHistH, a.hitInputTop, a.hitInputN, a.hitBodyH)
	up := k.Btn == MouseWheelUp
	switch region {
	case HitTranscript:
		if a.pending != nil {
			return
		}
		if up {
			a.follow = false
			a.scrollBy(3)
		} else {
			a.scrollBy(-3)
			if a.scroll == 0 {
				a.follow = true
				a.hasNew = false
			}
		}
	case HitSidebar:
		if up {
			a.sidebarScrollBy(3)
		} else {
			a.sidebarScrollBy(-3)
		}
	case HitInput:
		if a.busy || a.pending != nil || a.asking != nil {
			return
		}
		// Wheel moves the caret in multiline input; prompt history is ↑/↓
		// on an empty field or shift+↑/↓.
		if up {
			a.input.Up()
		} else {
			a.input.Down()
		}
	}
}

// HitRegion identifies which pane a screen cell belongs to.
type HitRegion int

const (
	HitNone HitRegion = iota
	HitTranscript
	HitSidebar
	HitInput
)

// HitTest maps 0-based screen coords to a scrollable pane.
func HitTest(x, y, mainW, sideW, histH, inputTop, inputN, bodyH int) HitRegion {
	if x < 0 || y < 0 {
		return HitNone
	}
	if sideW > 0 && x > mainW && y < bodyH {
		return HitSidebar
	}
	if x >= mainW {
		return HitNone
	}
	if y < histH {
		return HitTranscript
	}
	if y >= inputTop && y < inputTop+inputN {
		return HitInput
	}
	return HitNone
}

func formatQuestionNote(q tools.Question) string {
	var b strings.Builder
	if q.Header != "" {
		fmt.Fprintf(&b, "%s\n", q.Header)
	}
	fmt.Fprintf(&b, "%s\n", q.Prompt)
	for i, opt := range q.Options {
		fmt.Fprintf(&b, "  %d) %s\n", i+1, opt)
	}
	b.WriteString("type a number or custom answer, enter to submit, esc to skip")
	return strings.TrimSpace(b.String())
}

func (a *App) handleQuestionKey(k Key) {
	if a.asking == nil {
		return
	}
	reply := a.asking.reply
	finish := func(ans string, err error) {
		reply <- questionReply{answer: ans, err: err}
		a.asking = nil
		a.askInput = ""
	}
	switch {
	case k.Kind == KeyRune:
		a.askInput += string(k.Rune)
	case k.Kind == KeyPaste:
		a.askInput += k.Text
	case k.is(KeyBackspace):
		if a.askInput != "" {
			r := []rune(a.askInput)
			a.askInput = string(r[:len(r)-1])
		}
	case k.is(KeyEnter):
		in := strings.TrimSpace(a.askInput)
		if in == "" {
			a.model.Note("an answer is required")
			return
		}
		if len(in) == 1 && in[0] >= '1' && in[0] <= '9' {
			idx := int(in[0] - '1')
			if idx < len(a.asking.q.Options) {
				ans := a.asking.q.Options[idx]
				a.model.Note("answered: " + ans)
				finish(ans, nil)
				return
			}
		}
		a.model.Note("answered: " + a.askInput)
		finish(a.askInput, nil)
	case k.is(KeyEsc) || (k.Kind == KeyCtrl && k.Ctrl == 'c'):
		a.model.Note("question skipped")
		finish("", fmt.Errorf("user skipped question"))
	}
}

func (a *App) handleApprovalKey(k Key) {
	if a.pending == nil {
		return
	}
	reply := a.pending.reply
	switch {
	case k.Kind == KeyRune && (k.Rune == 'y' || k.Rune == 'Y'):
		a.model.Note("approved")
		reply <- nil
		a.pending = nil
	case k.Kind == KeyRune && (k.Rune == 'A' || k.Rune == 'a'):
		a.alwaysAllow[a.pending.name] = true
		a.model.Note("approved always for " + a.pending.name)
		reply <- nil
		a.pending = nil
	case k.Kind == KeyRune && (k.Rune == 'n' || k.Rune == 'N'), k.is(KeyEsc):
		a.model.Note("denied")
		reply <- fmt.Errorf("user denied %s", a.pending.name)
		a.pending = nil
	case k.Kind == KeyCtrl && k.Ctrl == 'c':
		a.model.Note("denied")
		reply <- fmt.Errorf("user denied %s", a.pending.name)
		a.pending = nil
		if a.turnCancel != nil {
			a.turnCancel()
		}
	case k.Kind == KeyCtrl && k.Ctrl == 'q':
		a.model.Note("denied")
		reply <- fmt.Errorf("user denied %s", a.pending.name)
		a.pending = nil
		a.Quit()
	}
}

func (a *App) promptStrip(width int) []Row {
	if width < 1 {
		return nil
	}
	var text string
	attr := styleYellow.withBold()
	switch {
	case a.pending != nil:
		sum := argSummary(a.pending.args)
		if sum == "" {
			sum = a.pending.name
		}
		text = "approve " + a.pending.name + " " + sum + "  y/n/A · esc deny"
	case a.asking != nil:
		attr = styleAccent.withBold()
		text = formatQuestionNote(a.asking.q)
		if a.askInput != "" {
			text += "\n> " + a.askInput
		}
	default:
		return nil
	}
	var out []Row
	for _, line := range strings.Split(text, "\n") {
		wrapped := WrapRow(Row{Segs: []Segment{{Text: line, Attr: attr}}}, width)
		out = append(out, wrapped...)
	}
	return out
}

func (a *App) toggleMode() {
	if a.agent.Mode() == agent.Build {
		a.setAgentMode(agent.Plan)
	} else {
		a.setAgentMode(agent.Build)
	}
}

func (a *App) setAgentMode(m agent.Mode) {
	a.agent.SetMode(m)
	if m == agent.Plan {
		a.model.Note("mode: plan (read-only)")
	} else {
		a.model.Note("mode: build")
	}
}

func (a *App) submit(text string) {
	// @mentions are expanded inside Agent.Run; the transcript shows raw text.
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		a.command(text)
		return
	}
	a.pushHistory(text)
	a.redoStack = nil // a new turn invalidates redo
	a.model.User(text)
	a.scroll = 0
	a.follow = true
	a.hasNew = false
	a.busy = true
	a.pendingBase = len(a.sess.Messages())

	ctx, cancel := context.WithCancel(context.Background())
	a.turnCancel = cancel
	a.evCh = make(chan agent.Event)
	a.ticker = time.NewTicker(spinnerInterval)
	a.tickCh = a.ticker.C

	go a.agent.Run(ctx, text, a.evCh)
}

func (a *App) pushHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.history = append(a.history, text)
	if len(a.history) > promptHistoryMax {
		a.history = a.history[len(a.history)-promptHistoryMax:]
	}
	a.historyIdx = -1
}

func (a *App) historyUp() {
	if len(a.history) == 0 {
		return
	}
	if a.historyIdx < 0 {
		a.historyIdx = len(a.history) - 1
	} else if a.historyIdx > 0 {
		a.historyIdx--
	}
	a.input.Clear()
	a.input.Paste(a.history[a.historyIdx])
}

func (a *App) historyDown() {
	if a.historyIdx < 0 {
		return
	}
	a.historyIdx++
	a.input.Clear()
	if a.historyIdx >= len(a.history) {
		a.historyIdx = -1
		return
	}
	a.input.Paste(a.history[a.historyIdx])
}

func (a *App) command(text string) {
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "clear":
		if a.busy {
			a.model.Note("wait for the current turn to finish")
			return
		}
		a.model.Reset()
		a.agent.LoadHistory(nil)
		a.usage = agent.Usage{}
		if s, err := session.Create(); err == nil {
			a.sess = s
			agent.BindSessionTrace(a.agent, s, "tui")
			a.refreshSideData()
			a.model.Note("new session started: " + shortID(s.ID))
		}
	case "sessions":
		infos, err := session.List()
		if err != nil {
			a.model.Error(err.Error())
			return
		}
		if len(infos) == 0 {
			a.model.Note("no sessions")
			return
		}
		var b strings.Builder
		b.WriteString("sessions (oldest → newest):\n")
		start := 0
		if len(infos) > 20 {
			start = len(infos) - 20
		}
		for _, info := range infos[start:] {
			mark := " "
			if info.ID == a.sess.ID {
				mark = "*"
			}
			fmt.Fprintf(&b, "%s %s  %s\n", mark, shortID(info.ID), clipNote(info.Preview, 60))
		}
		a.model.Note(strings.TrimSpace(b.String()))
	case "resume":
		if a.busy {
			a.model.Note("wait for the current turn to finish")
			return
		}
		if len(parts) < 2 {
			a.model.Note("usage: /resume <session-id>")
			return
		}
		id := parts[1]
		infos, _ := session.List()
		matched, ambiguous := matchSessionPrefix(id, infos)
		if len(ambiguous) > 0 {
			a.model.Note("ambiguous session prefix " + id + ": " + strings.Join(ambiguous, ", "))
			return
		}
		if matched == "" {
			a.model.Error("session not found: " + id)
			return
		}
		loaded, err := session.Load(matched)
		if err != nil {
			a.model.Error(err.Error())
			return
		}
		a.sess = loaded
		agent.BindSessionTrace(a.agent, loaded, "tui")
		a.agent.LoadHistory(loaded.Messages())
		a.model.Reset()
		a.ReplayHistory(loaded.Messages())
		a.refreshSideData()
		a.model.Note("resumed " + shortID(loaded.ID))
	case "model":
		if len(parts) < 2 {
			mode := "pin"
			if a.agent.RouterEnabled() {
				mode = "auto"
			}
			note := fmt.Sprintf("model: %s (%s)", a.agent.Model(), mode)
			if a.route != "" {
				note += " last route=" + a.route
			}
			a.model.Note(note)
			return
		}
		name := strings.Join(parts[1:], " ")
		if name == "auto" {
			a.applyModelPick(modelPickAuto)
			return
		}
		a.applyModelPick(name)
	case "models":
		a.openModels()
	case "router":
		if len(parts) < 2 {
			state := "off"
			if a.agent.RouterEnabled() {
				state = "on"
			}
			a.model.Note(fmt.Sprintf("router: %s (last=%s model=%s)", state, a.route, a.agent.Model()))
			return
		}
		switch parts[1] {
		case "on":
			a.applyModelPick(modelPickAuto)
		case "off":
			a.cfg.Router.Enabled = false
			a.syncRouter()
			_ = a.cfg.Save()
			a.model.Note("router off")
		default:
			a.model.Note("usage: /router [on|off]")
		}
	case "refresh":
		a.refreshSideData()
		a.model.Note("sidebar refreshed")
	case "compact":
		if a.compacting {
			a.model.Note("compacting in progress...")
			return
		}
		if a.busy {
			a.model.Note("wait for the current turn to finish")
			return
		}
		a.compacting = true
		a.model.Note("compacting...")
		go func() {
			ok := a.agent.Compact()
			select {
			case a.compactCh <- ok:
			case <-a.done:
			}
		}()
	case "init":
		created, err := writeAgentsMD(a.cfg.Workdir)
		if err != nil {
			a.model.Error("init: " + err.Error())
		} else if created {
			a.model.Note("wrote AGENTS.md")
		} else {
			a.model.Note("AGENTS.md already exists")
		}
	case "undo":
		a.undoTurn()
	case "redo":
		a.redoTurn()
	case "setup", "connect":
		a.openSetup()
	case "plan":
		a.setAgentMode(agent.Plan)
	case "build":
		a.setAgentMode(agent.Build)
	case "mode":
		if len(parts) < 2 {
			a.toggleMode()
			return
		}
		switch parts[1] {
		case "plan":
			a.setAgentMode(agent.Plan)
		case "build":
			a.setAgentMode(agent.Build)
		default:
			a.model.Note("usage: /mode [plan|build]")
		}
	case "config":
		a.openConfig()
	case "help":
		a.model.Note("commands: /setup /connect /config /models /model [name|auto] /router [on|off] /plan /build /mode [plan|build] /clear /sessions /resume <id> /compact /undo /redo /init /refresh /help\nkeys: enter send · tab plan/build · f2 cycle model · ctrl+p commands · ctrl+b sidebar · wheel/shift+↑↓/pgup/pgdn scroll · ctrl+c cancel · ctrl+q quit")
	default:
		if prompt, ok := lookupProjectCommand(a.cfg.Workdir, parts[0]); ok {
			a.submit(prompt)
			return
		}
		a.model.Note("unknown command (try /help)")
	}
}

func shortID(id string) string {
	r := []rune(id)
	if len(r) <= 16 {
		return id
	}
	return string(r[:8]) + "…" + string(r[len(r)-4:])
}

// matchSessionPrefix returns a unique session id for id (exact or prefix).
// If several sessions share the prefix, matched is empty and ambiguous
// holds short ids for a note.
func matchSessionPrefix(id string, infos []session.Info) (matched string, ambiguous []string) {
	var matches []string
	for _, info := range infos {
		if info.ID == id {
			return info.ID, nil
		}
		if strings.HasPrefix(info.ID, id) || strings.HasPrefix(shortID(info.ID), id) {
			matches = append(matches, info.ID)
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	if len(matches) > 1 {
		shorts := make([]string, len(matches))
		for i, m := range matches {
			shorts[i] = shortID(m)
		}
		return "", shorts
	}
	return matches[0], nil
}

func (a *App) handleEvent(e agent.Event) {
	switch e.Type {
	case agent.EventRoute:
		a.route = e.Name
		a.model.Note(fmt.Sprintf("route: %s → %s", e.Name, e.Model))
	case agent.EventStatus:
		name := e.Name
		if name == "" {
			name = e.Text
		}
		a.phase = name
		a.model.Status(name)
		a.markStreamUpdate()
	case agent.EventText:
		a.phase = ""
		a.model.Text(e.Text)
		a.markStreamUpdate()
	case agent.EventThinking:
		a.phase = ""
		a.model.Thinking(e.Text)
		a.markStreamUpdate()
	case agent.EventToolStart:
		a.phase = ""
		a.model.ToolStart(e.Name, e.Args)
		a.markStreamUpdate()
	case agent.EventToolDone:
		a.model.ToolDone(e.Name, e.Output)
		a.markStreamUpdate()
	case agent.EventTodos:
		a.todos = e.Todos
	case agent.EventTurnDone:
		if e.Usage != nil {
			a.usage.Prompt = e.Usage.PromptTokens
			a.usage.Completion = e.Usage.CompletionTokens
		}
		a.syncSession()
		a.refreshSideData()
		a.endTurn()
	case agent.EventError:
		if e.Err != nil && e.Err.Error() != "context canceled" {
			a.model.Error(e.Err.Error())
		} else {
			a.model.Note("cancelled")
		}
		a.syncSession()
		a.refreshSideData()
		a.endTurn()
	}
}

func (a *App) refreshSideData() {
	a.kickSideRefresh()
}

// pushSideData enqueues sidebar data with latest-wins semantics so a
// full sideCh does not drop the newest refresh.
func (a *App) pushSideData(data sideData) {
	select {
	case <-a.sideCh:
	default:
	}
	select {
	case a.sideCh <- data:
	default:
	}
}

// kickSideRefresh starts an async refresh of sidebar data.
func (a *App) kickSideRefresh() {
	// Increment generation counter to track staleness
	gen := atomic.AddInt64(&a.sideGen, 1)

	go func() {
		data := a.fetchSideData()
		if gen != atomic.LoadInt64(&a.sideGen) {
			// Stale result - another refresh was kicked off
			return
		}

		a.pushSideData(data)
	}()
}

// fetchSideData performs the actual work of collecting sidebar data.
func (a *App) fetchSideData() sideData {
	modified := FetchGitStatus(a.cfg.Workdir)
	sessions, _ := session.List()
	var mcpNames []string
	if a.cfg != nil {
		for name := range a.cfg.MCPServers {
			mcpNames = append(mcpNames, name)
		}
	}
	mcpNames = MCPNameList(mcpNames)

	preview := ""
	if a.sess != nil && a.sess.Title != "" {
		preview = a.sess.Title
	} else if a.sess != nil {
		for _, m := range a.sess.Messages() {
			if m.Role == llm.RoleUser && m.Content != "" {
				preview = session.TitleFromPrompt(m.Content)
			}
		}
	}

	return sideData{
		modified: modified,
		sessions: sessions,
		mcp:      mcpNames,
		preview:  preview,
	}
}

// undoTurn rewinds the conversation to just before the last user turn and
// pushes the removed tail onto the redo stack.
func (a *App) undoTurn() {
	if a.busy || a.compacting {
		a.model.Note("wait for the current turn to finish")
		return
	}
	msgs := a.agent.Messages()
	cut := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser && !strings.HasPrefix(msgs[i].Content, "[conversation compacted]") {
			cut = i
			break
		}
	}
	if cut < 0 {
		a.model.Note("nothing to undo")
		return
	}
	a.redoStack = append(a.redoStack, msgs)
	a.resetHistoryTo(msgs[:cut])
	a.model.Note(fmt.Sprintf("undid last turn (%d message(s) removed) — /redo to restore", len(msgs)-cut))
}

// redoTurn restores the most recently undone history snapshot.
func (a *App) redoTurn() {
	if a.busy || a.compacting {
		a.model.Note("wait for the current turn to finish")
		return
	}
	if len(a.redoStack) == 0 {
		a.model.Note("nothing to redo")
		return
	}
	msgs := a.redoStack[len(a.redoStack)-1]
	a.redoStack = a.redoStack[:len(a.redoStack)-1]
	a.resetHistoryTo(msgs)
	a.model.Note("redo applied")
}

// resetHistoryTo replaces agent history, session file, and transcript.
func (a *App) resetHistoryTo(msgs []llm.Message) {
	a.agent.LoadHistory(msgs)
	if err := a.sess.Replace(msgs); err != nil {
		a.model.Error("session save: " + err.Error())
	}
	a.pendingBase = len(a.sess.Messages())
	a.model.Reset()
	a.ReplayHistory(msgs)
	a.refreshSideData()
}

// syncSession persists any messages the agent added this turn. When the
// agent compacted history mid-turn, the whole session is rewritten so the
// JSONL matches the compacted conversation.
func (a *App) syncSession() {
	msgs := a.agent.Messages()
	if a.compacted.Swap(false) {
		if err := a.sess.Replace(msgs); err != nil {
			a.model.Error("session save: " + err.Error())
		} else {
			a.pendingBase = len(a.sess.Messages())
			a.sess.EnsureTitle()
		}
		a.model.Reset()
		a.ReplayHistory(msgs)
		return
	}
	if len(msgs) > a.pendingBase {
		for _, m := range msgs[a.pendingBase:] {
			if err := a.sess.Append(m); err != nil {
				a.model.Error("session save: " + err.Error())
				return
			}
		}
	}
	a.pendingBase = len(a.sess.Messages())
	a.sess.EnsureTitle()
}

func (a *App) endTurn() {
	a.busy = false
	a.phase = ""
	a.model.clearWait()
	if a.ticker != nil {
		a.ticker.Stop()
	}
	a.ticker = nil
	a.tickCh = nil
	a.evCh = nil
	a.turnCancel = nil
	if a.pending != nil {
		a.pending.reply <- fmt.Errorf("turn ended")
		a.pending = nil
	}
	if a.asking != nil {
		a.model.Note("question skipped")
		a.asking.reply <- questionReply{err: fmt.Errorf("turn ended")}
		a.asking = nil
		a.askInput = ""
	}
}

func (a *App) markStreamUpdate() {
	if a.follow {
		a.scroll = 0
		a.hasNew = false
		return
	}
	a.hasNew = true
}

// applyCompactResult syncs session + transcript after /compact (or a
// failed compact). Auto-compact mid-turn uses syncSession instead.
func (a *App) applyCompactResult(ok bool) {
	a.compacting = false
	a.compacted.Store(false) // persisted here; syncSession need not replace again
	if !ok {
		a.model.Note("nothing to compact")
		return
	}
	var msgs []llm.Message
	if a.agent != nil {
		msgs = a.agent.Messages()
	}
	if a.sess != nil {
		if err := a.sess.Replace(msgs); err != nil {
			a.model.Error("session save: " + err.Error())
		} else {
			a.pendingBase = len(a.sess.Messages())
		}
	}
	a.model.Reset()
	a.ReplayHistory(msgs)
	a.model.Note("conversation compacted")
}

// paneWidths returns sidebar and main-pane widths. sidebarOn is intent;
// a too-narrow terminal only hides drawing (sideW == 0).
func (a *App) paneWidths() (sideW, mainW int) {
	if a.sidebarOn {
		sideW = SideWidth(a.width + 1)
	}
	mainW = a.width
	if sideW > 0 {
		mainW = a.width - sideW - 1
	}
	if mainW < 20 {
		return 0, a.width
	}
	return sideW, mainW
}

// inputRowCap is how many compose-band rows fit without overflowing
// the screen. Prefer shrinking input over forcing histH to 1.
func (a *App) inputRowCap(promptN int) int {
	avail := a.height - 2 - promptN
	if avail < 2 {
		avail = 2
	}
	cap := maxInputRows
	if cap > avail-1 {
		cap = avail - 1
	}
	if cap < 1 {
		cap = 1
	}
	return cap
}

// composeLayout sizes the transcript and compose band for the current
// terminal. It sets the transcript wrap width to the main pane.
func (a *App) composeLayout() (sideW, mainW, histH int, inputRows, promptRows []Row, cy, cx int, cursorVisible bool) {
	sideW, mainW = a.paneWidths()
	saved := a.width
	a.width = mainW
	if a.model != nil {
		a.model.SetWidth(mainW)
	}
	promptRows = a.promptStrip(mainW)
	cap := a.inputRowCap(len(promptRows))
	inputRows, cy, cx, cursorVisible = a.inputBlockCapped(cap)
	a.width = saved

	avail := a.height - 2 - len(promptRows)
	if avail < 2 {
		avail = 2
	}
	histH = avail - len(inputRows)
	if histH < 1 {
		histH = 1
	}
	return
}

func (a *App) scrollBy(delta int) {
	_, _, histH, _, _, _, _, _ := a.composeLayout()
	rows := a.model.Rows()
	max := len(rows) - histH
	if max < 0 {
		max = 0
	}
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
}

func (a *App) sidebarScrollBy(delta int) {
	sideW := a.hitSideW
	if sideW <= 0 {
		if a.sidebarOn {
			sideW = SideWidth(a.width + 1)
		}
		if sideW <= 0 {
			return
		}
	}
	bodyH := a.hitBodyH
	if bodyH < 1 {
		_, _, histH, inputRows, promptRows, _, _, _ := a.composeLayout()
		bodyH = histH + len(promptRows) + 1 + len(inputRows)
	}
	d := a.sidebarData("")
	lines := SidebarLines(d, sideW)
	max := len(lines) - bodyH
	if max < 0 {
		max = 0
	}
	a.sidebarScroll += delta
	if a.sidebarScroll < 0 {
		a.sidebarScroll = 0
	}
	if a.sidebarScroll > max {
		a.sidebarScroll = max
	}
}

func (a *App) sidebarData(scrollHint string) SidebarData {
	approve := ""
	if a.pending != nil {
		approve = argSummary(a.pending.args)
		if approve == "" {
			approve = a.pending.name
		}
	}
	mode := "pin"
	route := ""
	if a.agent.RouterEnabled() {
		mode = "auto"
		route = a.route
	} else if a.agent.ModelPinned() {
		mode = "pin"
	}
	toolsMode := "build"
	if a.agent.Mode() == agent.Plan {
		toolsMode = "plan"
	}
	var routeMap []string
	for _, name := range []string{"nano", "coder", "qwen", "other"} {
		if t, ok := a.cfg.Router.Routes[name]; ok && t.Model != "" {
			routeMap = append(routeMap, name+" → "+t.Model)
		}
	}
	return SidebarData{
		SessionID: a.sess.ID, Preview: a.preview,
		Model: a.agent.Model(), ModelMode: mode, ToolsMode: toolsMode, Route: route,
		RouteMap: routeMap, Workdir: a.cfg.Workdir,
		PromptTokens: a.usage.Prompt, CompletionTokens: a.usage.Completion,
		MsgCount: len(a.agent.Messages()), MCP: a.mcpNames,
		Modified: a.modified, Sessions: a.sessions,
		Todos:   a.todos,
		Approve: approve, ScrollHint: scrollHint,
	}
}

func (a *App) render() {
	if a.overlay != "" {
		a.renderOverlay()
		return
	}

	sideW, mainW, histH, inputRows, promptRows, cy, cx, cursorVisible := a.composeLayout()
	rows := a.model.Rows()
	max := len(rows) - histH
	if max < 0 {
		max = 0
	}
	if a.follow {
		a.scroll = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
	start := max - a.scroll

	visible := rows[start:]
	if len(visible) > histH {
		visible = visible[:histH]
	}
	for len(visible) < histH {
		visible = append(visible, Row{})
	}

	scrollHint := ""
	if a.scroll > 0 {
		scrollHint = fmt.Sprintf("↑ %d", a.scroll)
	}
	if a.sidebarScroll > 0 {
		if scrollHint != "" {
			scrollHint += " · "
		}
		scrollHint += fmt.Sprintf("side ↑ %d", a.sidebarScroll)
	}

	mainBody := make([]Row, 0, histH+len(promptRows)+len(inputRows)+1)
	mainBody = append(mainBody, visible...)
	mainBody = append(mainBody, promptRows...)
	mainBody = append(mainBody, SeparatorRow(mainW))
	mainBody = append(mainBody, inputRows...)

	a.hitMainW = mainW
	a.hitSideW = sideW
	a.hitHistH = histH
	a.hitInputTop = histH + len(promptRows) + 1
	a.hitInputN = len(inputRows)
	a.hitBodyH = len(mainBody)

	var sideRows []Row
	if sideW > 0 {
		sideData := a.sidebarData(scrollHint)
		lines := SidebarLines(sideData, sideW)
		maxSide := len(lines) - len(mainBody)
		if maxSide < 0 {
			maxSide = 0
		}
		if a.sidebarScroll > maxSide {
			a.sidebarScroll = maxSide
		}
		if a.sidebarScroll < 0 {
			a.sidebarScroll = 0
		}
		sideRows = RenderSidebar(sideData, sideW, len(mainBody), a.sidebarScroll)
	}

	frame := make([]Row, 0, a.height)
	for y := range mainBody {
		if sideW == 0 {
			frame = append(frame, padRow(mainBody[y], mainW))
			continue
		}
		left := padRow(mainBody[y], mainW)
		rule := Row{Segs: []Segment{{Text: "│", Attr: styleDim}}}
		var right Row
		if y < len(sideRows) {
			right = sideRows[y]
		} else {
			right = Row{Segs: []Segment{{Text: strings.Repeat(" ", sideW), Attr: styleDim}}}
		}
		frame = append(frame, joinRows(left, rule, right))
	}

	mode := "build"
	if a.agent.Mode() == agent.Plan {
		mode = "plan"
	}
	route := ""
	if a.agent.RouterEnabled() {
		route = a.route
	}
	frame = append(frame, StatusRowOpts(StatusOpts{
		Mode: mode, Model: a.agent.Model(), Route: route,
		Auto: a.agent.RouterEnabled(), Pin: a.agent.ModelPinned(),
		SessionID: shortID(a.sess.ID),
		Prompt:    a.usage.Prompt, Completion: a.usage.Completion,
		Busy: a.busy, Phase: a.phase, Spinner: a.model.spinner, Width: a.width,
		ScrollHint: scrollHint, HasNew: a.hasNew && !a.follow,
	}))
	a.renderer.Render(frame)

	if cursorVisible && !a.busy && a.pending == nil && a.asking == nil {
		// +1 for separator between transcript and input
		fmt.Fprintf(a.term, "\x1b[%d;%dH\x1b[?25h", CursorScreenRow(histH+len(promptRows)+1, cy), cx+1)
	} else {
		_, _ = a.term.Write([]byte("\x1b[?25l"))
	}
}

func (a *App) renderOverlay() {
	w, h := a.term.Size()
	a.renderer.Resize(w, h)
	width := w - 1
	if width < 1 {
		width = w
	}
	var frame []Row
	switch a.overlay {
	case "setup":
		if a.setup == nil {
			a.setup = newSetupForm(a.cfg)
		}
		frame = renderSetupFrame(a.setup, width, h)
	case "config":
		if a.configM == nil {
			a.configM = &configMenu{}
		}
		frame = renderConfigMenu(a.configM, a.cfg, width, h)
	case "palette":
		if a.palette == nil {
			a.palette = newPalette()
		}
		frame = renderPalette(a.palette, width, h)
	case "models":
		a.ensureModelsPicker()
		if a.models != nil {
			frame = renderModelPicker(a.models, width, h)
		}
	default:
		a.closeOverlay()
		a.render()
		return
	}
	a.renderer.Render(frame)
	_, _ = a.term.Write([]byte("\x1b[?25l"))
}

func padRow(r Row, width int) Row {
	if width < 1 {
		return Row{}
	}
	w := 0
	for _, s := range r.Segs {
		w += displayWidth(s.Text)
	}
	if w > width {
		return truncateRow(r, width)
	}
	if w == width {
		return r
	}
	r.Segs = append(append([]Segment{}, r.Segs...), Segment{Text: strings.Repeat(" ", width-w), Attr: styleDefault})
	return r
}

func truncateRow(r Row, width int) Row {
	var segs []Segment
	used := 0
	for _, s := range r.Segs {
		sw := displayWidth(s.Text)
		if used+sw <= width {
			segs = append(segs, s)
			used += sw
			continue
		}
		remain := width - used
		if remain > 0 {
			segs = append(segs, Segment{Text: trimDisplay(s.Text, remain), Attr: s.Attr})
		}
		break
	}
	return Row{Segs: segs}
}

func joinRows(parts ...Row) Row {
	var segs []Segment
	for _, p := range parts {
		segs = append(segs, p.Segs...)
	}
	return Row{Segs: segs}
}

// inputBlock renders the prompt + input lines and reports where the
// text cursor sits (screen cells), or that it is scrolled out of view.
func (a *App) inputBlock() (rows []Row, cy, cx int, visible bool) {
	return a.inputBlockCapped(maxInputRows)
}

func (a *App) inputBlockCapped(cap int) (rows []Row, cy, cx int, visible bool) {
	if cap < 1 {
		cap = 1
	}
	lines := strings.Split(a.input.Text(), "\n")
	curLine := a.input.CursorLine()
	curCol := a.input.CursorCol()

	var all []Row
	lineStart := make([]int, 0, len(lines))
	for li, line := range lines {
		lineStart = append(lineStart, len(all))
		wr := WrapRowKeepTrailing(Row{Segs: []Segment{{Text: line, Attr: styleDefault}}}, a.width-2)
		for wi, w := range wr {
			prefix := "  "
			if li == 0 && wi == 0 {
				prefix = "› "
			}
			w.Segs = append([]Segment{{Text: prefix, Attr: styleAccent}}, w.Segs...)
			all = append(all, w)
		}
	}

	// Locate the cursor first so the viewport can keep it in view.
	chunkRow, chunkCol := cursorInLine(lines[curLine], curCol, a.width-2)
	absRow := lineStart[curLine] + chunkRow

	offset := 0
	if len(all) > cap {
		offset = len(all) - cap
		if absRow < offset {
			offset = absRow
		}
		if offset < 0 {
			offset = 0
		}
		if offset+cap > len(all) {
			offset = len(all) - cap
		}
		if offset < 0 {
			offset = 0
		}
	}
	rows = all[offset:]

	if absRow < offset || absRow >= offset+len(rows) {
		return rows, 0, 0, false
	}
	return rows, absRow - offset, chunkCol + 2, true
}

// cursorInLine maps a (line, rune-col) position to wrapped-chunk coordinates
// using the same wrap as WrapRow. colOut is a display-column offset (CJK/emoji
// count as 2), not a rune index.
func cursorInLine(line string, col, width int) (row, colOut int) {
	if width < 1 {
		width = 1
	}
	rs := []rune(line)
	if col < 0 {
		col = 0
	}
	if col > len(rs) {
		col = len(rs)
	}
	if col == 0 {
		return 0, 0
	}
	// Walk the prefix the same way input wrap lays out the line so the
	// caret stays on typed trailing spaces.
	prefix := string(rs[:col])
	chunks := WrapRowKeepTrailing(Row{Segs: []Segment{{Text: prefix, Attr: styleDefault}}}, width)
	if len(chunks) == 0 {
		return 0, 0
	}
	last := chunks[len(chunks)-1]
	w := 0
	for _, seg := range last.Segs {
		w += displayWidth(seg.Text)
	}
	return len(chunks) - 1, w
}
