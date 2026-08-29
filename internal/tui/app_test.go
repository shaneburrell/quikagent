package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"quikagent/internal/agent"
	"quikagent/internal/config"
	"quikagent/internal/llm"
	"quikagent/internal/session"
	"quikagent/internal/tools"
)

type stubLLM struct{ model string }

func (s *stubLLM) Chat(context.Context, []llm.Message, []llm.Tool, int) (<-chan llm.Event, error) {
	return nil, fmt.Errorf("unused")
}
func (s *stubLLM) Model() string     { return s.model }
func (s *stubLLM) SetModel(m string) { s.model = m }

func testAgent(model string) *agent.Agent {
	return agent.New(&stubLLM{model: model}, nil, agent.Options{Model: model})
}

func TestAppCancelTurnIfBusy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &App{
		busy:       true,
		turnCancel: cancel,
	}

	// Cancel should be called
	a.cancelTurnIfBusy()
	// Verify the context was cancelled by checking if it's done
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("Expected context to be cancelled")
	}
}

func TestClipNoteRuneSafe(t *testing.T) {
	// Test that clipNote handles UTF-8 properly without splitting runes
	testString := "Hello 世界 🌍"
	result := clipNote(testString, 10)

	// Should not be longer than requested length (in runes)
	if len([]rune(result)) > 10 {
		t.Errorf("clipNote should truncate to 10 runes, got %d", len([]rune(result)))
	}

	if !utf8.ValidString(result) {
		t.Fatalf("clipNote produced invalid UTF-8: %q", result)
	}
}

func TestHandleApprovalKeyEscDenies(t *testing.T) {
	ch := make(chan error, 1)
	a := &App{
		model:   NewModel(),
		pending: &approveRequest{name: "write", args: `{"path":"x"}`, reply: ch},
		done:    make(chan struct{}),
	}
	a.handleApprovalKey(Key{Kind: KeyNamed, Name: KeyEsc})
	if a.pending != nil {
		t.Fatal("pending should clear")
	}
	err := <-ch
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("got %v", err)
	}
}

func TestHandleQuestionKeyEscSkips(t *testing.T) {
	ch := make(chan questionReply, 1)
	a := &App{
		model:  NewModel(),
		asking: &questionRequest{q: tools.Question{Prompt: "which?", Options: []string{"a", "b"}}, reply: ch},
	}
	a.handleQuestionKey(Key{Kind: KeyNamed, Name: KeyEsc})
	if a.asking != nil {
		t.Fatal("asking should clear")
	}
	got := <-ch
	if got.err == nil || !strings.Contains(got.err.Error(), "skipped") {
		t.Fatalf("%+v", got)
	}
}

func TestPromptStripPinned(t *testing.T) {
	a := &App{
		pending: &approveRequest{name: "write", args: `{"path":"foo.go"}`},
	}
	rows := a.promptStrip(40)
	if len(rows) == 0 {
		t.Fatal("expected prompt strip")
	}
	var b strings.Builder
	for _, r := range rows {
		for _, s := range r.Segs {
			b.WriteString(s.Text)
		}
	}
	if !strings.Contains(b.String(), "approve write") {
		t.Fatalf("%q", b.String())
	}
}

func TestOverlayListKeepsSelectionVisible(t *testing.T) {
	items := make([]string, 40)
	for i := range items {
		items[i] = fmt.Sprintf("  model-%02d", i)
	}
	items[25] = "> model-25"
	rows := renderOverlayList(overlayListOpts{
		Title: "Models", Filter: "", ShowFilt: true, Items: items, Idx: 25, Hint: "hint",
	}, 40, 8)
	if len(rows) != 8 {
		t.Fatalf("rows=%d", len(rows))
	}
	var joined strings.Builder
	for _, r := range rows {
		for _, s := range r.Segs {
			joined.WriteString(s.Text)
		}
		joined.WriteByte('\n')
	}
	out := joined.String()
	if !strings.Contains(out, "model-25") {
		t.Fatalf("selection not visible:\n%s", out)
	}
	if strings.Contains(out, "model-00") {
		t.Fatalf("should have scrolled past the top:\n%s", out)
	}
}

func TestScrollByAccountsForPromptStrip(t *testing.T) {
	a := &App{
		height:  20,
		width:   40,
		input:   NewInput(),
		model:   NewModel(),
		pending: &approveRequest{name: "write", args: `{"path":"x"}`},
	}
	a.model.SetWidth(40)
	for i := range 40 {
		a.model.User(fmt.Sprintf("msg %d", i))
	}
	a.scrollBy(1000)
	withStrip := a.scroll
	a.pending = nil
	a.scroll = 0
	a.scrollBy(1000)
	withoutStrip := a.scroll
	if withStrip <= withoutStrip {
		t.Fatalf("max scroll with strip (%d) should exceed without (%d)", withStrip, withoutStrip)
	}
}

func TestHasNewOnToolEventsWhenScrolledUp(t *testing.T) {
	a := &App{model: NewModel(), follow: false}
	a.handleEvent(agent.Event{Type: agent.EventToolStart, Name: "bash", Args: `{}`})
	if !a.hasNew {
		t.Fatal("hasNew should be set on tool start when scrolled up")
	}
	a.hasNew = false
	a.handleEvent(agent.Event{Type: agent.EventToolDone, Name: "bash", Output: "ok"})
	if !a.hasNew {
		t.Fatal("hasNew should be set on tool done when scrolled up")
	}
}

func TestPromptStripKeepsFirstWrappedLines(t *testing.T) {
	a := &App{asking: &questionRequest{q: tools.Question{
		Header:  "Choose one",
		Prompt:  "which option?",
		Options: []string{"a", "b", "c", "d", "e", "f"},
	}}}
	rows := a.promptStrip(40)
	var b strings.Builder
	for _, r := range rows {
		for _, s := range r.Segs {
			b.WriteString(s.Text)
		}
		b.WriteByte('\n')
	}
	out := b.String()
	if !strings.Contains(out, "Choose one") || !strings.Contains(out, "which option") {
		t.Fatalf("first wrap rows dropped:\n%s", out)
	}
}

func TestInputBlockKeepsCursorInView(t *testing.T) {
	var b strings.Builder
	for i := range 12 {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "line %02d", i)
	}
	inp := NewInput()
	inp.SetText(b.String())
	for inp.CursorLine() > 0 {
		inp.Up()
	}
	a := &App{input: inp, width: 80}
	rows, cy, _, visible := a.inputBlock()
	if !visible {
		t.Fatal("cursor should stay in view")
	}
	if cy != 0 {
		t.Fatalf("cy=%d want 0", cy)
	}
	if !strings.Contains(rowTexts(rows)[0], "line 00") {
		t.Fatalf("viewport missing cursor line: %v", rowTexts(rows))
	}
}

func TestWheelOnInputIgnoredWhenBusy(t *testing.T) {
	inp := NewInput()
	inp.SetText("a\nb\nc")
	a := &App{
		busy: true, input: inp,
		hitMainW: 40, hitHistH: 10, hitInputTop: 11, hitInputN: 3, hitBodyH: 15,
	}
	row := inp.CursorLine()
	a.handleMouse(Key{Kind: KeyMouse, Btn: MouseWheelUp, Col: 2, Row: 12, Press: true})
	if inp.CursorLine() != row {
		t.Fatalf("wheel moved caret while busy: %d -> %d", row, inp.CursorLine())
	}
}

func TestHistoryUpIgnoredWhenBusy(t *testing.T) {
	a := &App{
		busy: true, input: NewInput(), model: NewModel(),
		history: []string{"prior"}, historyIdx: -1,
	}
	a.handleKeys([]Key{{Kind: KeyNamed, Name: KeyUp}})
	if a.input.Text() != "" {
		t.Fatalf("history recalled while busy: %q", a.input.Text())
	}
}

func TestApplyCompactResultResetsTranscript(t *testing.T) {
	ag := testAgent("qwen")
	ag.LoadHistory([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	})
	a := &App{model: NewModel(), agent: ag}
	a.model.SetWidth(80)
	a.model.User("stale extra")
	a.applyCompactResult(true)
	for _, b := range a.model.blocks {
		if b.kind == blockUser && b.text == "stale extra" {
			t.Fatal("stale transcript survived compact Reset+Replay")
		}
	}
	joined := strings.Join(rowTexts(a.model.Rows()), "\n")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "hi there") {
		t.Fatalf("replay missing compacted history:\n%s", joined)
	}
	if !strings.Contains(joined, "conversation compacted") {
		t.Fatalf("missing compact note:\n%s", joined)
	}
}

func TestApplyModelPickDisablesRouter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ag := testAgent("old")
	ag.SetRouterEnabled(true)
	cfg := &config.Config{Model: "old", Router: config.RouterConfig{Enabled: true}}
	a := &App{agent: ag, cfg: cfg, model: NewModel()}
	a.applyModelPick("pinned-model")
	if cfg.Router.Enabled {
		t.Fatal("pinning should set Router.Enabled=false")
	}
	if cfg.Model != "pinned-model" {
		t.Fatalf("cfg.Model=%s", cfg.Model)
	}
	if ag.RouterEnabled() {
		t.Fatal("agent router should be off")
	}
}

func TestMatchSessionPrefixUnique(t *testing.T) {
	infos := []session.Info{{ID: "aaa-111"}, {ID: "aaa-222"}, {ID: "bbb-333"}}
	if m, amb := matchSessionPrefix("aaa", infos); m != "" || len(amb) != 2 {
		t.Fatalf("ambiguous: matched=%q amb=%v", m, amb)
	}
	if m, amb := matchSessionPrefix("bbb", infos); m != "bbb-333" || amb != nil {
		t.Fatalf("unique: matched=%q amb=%v", m, amb)
	}
	if m, amb := matchSessionPrefix("aaa-111", infos); m != "aaa-111" || amb != nil {
		t.Fatalf("exact: matched=%q amb=%v", m, amb)
	}
	if m, amb := matchSessionPrefix("zzz", infos); m != "" || amb != nil {
		t.Fatalf("missing: matched=%q amb=%v", m, amb)
	}
}

func TestHandleEventStatusThenTextClearsWait(t *testing.T) {
	a := &App{model: NewModel()}
	a.model.SetWidth(80)
	a.handleEvent(agent.Event{Type: agent.EventStatus, Name: "waiting", Text: "waiting"})
	if a.phase != "waiting" {
		t.Fatalf("phase = %q", a.phase)
	}
	var sawWait bool
	for _, b := range a.model.blocks {
		if b.kind == blockWait && b.name == "waiting" {
			sawWait = true
		}
	}
	if !sawWait {
		t.Fatal("expected wait block")
	}
	a.handleEvent(agent.Event{Type: agent.EventText, Text: "hello"})
	if a.phase != "" {
		t.Fatalf("phase after text = %q", a.phase)
	}
	for _, b := range a.model.blocks {
		if b.kind == blockWait {
			t.Fatal("wait block should be cleared after text")
		}
	}
}

func TestEventRouteDoesNotWriteCfgModel(t *testing.T) {
	cfg := &config.Config{Model: "user-model"}
	a := &App{cfg: cfg, model: NewModel()}
	a.handleEvent(agent.Event{Type: agent.EventRoute, Name: "nano", Model: "routed-model"})
	if cfg.Model != "user-model" {
		t.Fatalf("cfg.Model overwritten to %s", cfg.Model)
	}
}

func TestReplayHistoryClosesToolsAndThinking(t *testing.T) {
	a := &App{model: NewModel()}
	a.model.SetWidth(80)
	a.ReplayHistory([]llm.Message{
		{Role: llm.RoleAssistant, Reasoning: "secret plan", ToolCalls: []llm.ToolCall{{Name: "bash", Arguments: `{}`}}},
	})
	for _, b := range a.model.blocks {
		if b.kind == blockTool && !b.done {
			t.Fatal("unpaired ToolStart still spinning")
		}
		if b.kind == blockThinking && !b.collapsed {
			t.Fatal("thinking still expanded after replay")
		}
	}
}

func TestApprovalKeyLowerAAlwaysAllows(t *testing.T) {
	ch := make(chan error, 1)
	a := &App{
		model:       NewModel(),
		pending:     &approveRequest{name: "write", args: `{}`, reply: ch},
		alwaysAllow: map[string]bool{},
	}
	a.handleApprovalKey(Key{Kind: KeyRune, Rune: 'a'})
	if a.pending != nil {
		t.Fatal("pending should clear")
	}
	if !a.alwaysAllow["write"] {
		t.Fatal("'a' should always-allow")
	}
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
}

func TestQuestionDigitDoesNotStealCustomAnswer(t *testing.T) {
	ch := make(chan questionReply, 1)
	a := &App{
		model:  NewModel(),
		asking: &questionRequest{q: tools.Question{Prompt: "?", Options: []string{"one", "two"}}, reply: ch},
	}
	a.handleQuestionKey(Key{Kind: KeyRune, Rune: '2'})
	if a.asking == nil {
		t.Fatal("lone digit key must not submit")
	}
	a.handleQuestionKey(Key{Kind: KeyRune, Rune: 'f'})
	a.handleQuestionKey(Key{Kind: KeyRune, Rune: 'a'})
	a.handleQuestionKey(Key{Kind: KeyNamed, Name: KeyEnter})
	got := <-ch
	if got.answer != "2fa" || got.err != nil {
		t.Fatalf("%+v", got)
	}
}

func TestQuestionLoneDigitEnterSelectsOption(t *testing.T) {
	ch := make(chan questionReply, 1)
	a := &App{
		model:  NewModel(),
		asking: &questionRequest{q: tools.Question{Prompt: "?", Options: []string{"one", "two"}}, reply: ch},
	}
	a.handleQuestionKey(Key{Kind: KeyRune, Rune: '2'})
	a.handleQuestionKey(Key{Kind: KeyNamed, Name: KeyEnter})
	got := <-ch
	if got.answer != "two" || got.err != nil {
		t.Fatalf("%+v", got)
	}
}

func TestQuestionEmptyEnterRequiresAnswer(t *testing.T) {
	ch := make(chan questionReply, 1)
	a := &App{
		model:  NewModel(),
		asking: &questionRequest{q: tools.Question{Prompt: "?", Options: []string{"a"}}, reply: ch},
	}
	a.handleQuestionKey(Key{Kind: KeyNamed, Name: KeyEnter})
	if a.asking == nil {
		t.Fatal("empty enter should not finish")
	}
	joined := strings.Join(rowTexts(a.model.Rows()), "\n")
	if !strings.Contains(joined, "required") {
		t.Fatalf("want required note: %q", joined)
	}
	select {
	case <-ch:
		t.Fatal("should not reply yet")
	default:
	}
}

func TestEnsureModelsPickerDoesNotNeedClient(t *testing.T) {
	a := &App{cfg: &config.Config{Model: "qwen"}, model: NewModel()}
	a.ensureModelsPicker()
	if a.models == nil {
		t.Fatal("expected picker from known models")
	}
	p := a.models
	a.ensureModelsPicker()
	if a.models != p {
		t.Fatal("should not rebuild an existing picker")
	}
}

func TestTabSwitchesModeWhenBusy(t *testing.T) {
	ag := testAgent("qwen")
	a := &App{busy: true, agent: ag, cfg: &config.Config{Model: "qwen"}, model: NewModel(), input: NewInput()}
	if ag.Mode() != agent.Build {
		t.Fatal("expected build")
	}
	a.handleKeys([]Key{{Kind: KeyNamed, Name: KeyTab}})
	if ag.Mode() != agent.Plan {
		t.Fatal("tab should switch to plan while busy")
	}
	a.handleKeys([]Key{{Kind: KeyNamed, Name: KeyF2}})
	if ag.Model() != "qwen" {
		t.Fatalf("f2 changed model while busy: %s", ag.Model())
	}
}

func TestPlanBuildSlashCommands(t *testing.T) {
	ag := testAgent("qwen")
	a := &App{busy: true, agent: ag, model: NewModel(), input: NewInput()}
	a.command("/plan")
	if ag.Mode() != agent.Plan {
		t.Fatal("/plan while busy")
	}
	a.command("/build")
	if ag.Mode() != agent.Build {
		t.Fatal("/build while busy")
	}
	a.command("/mode")
	if ag.Mode() != agent.Plan {
		t.Fatal("/mode toggle")
	}
	a.command("/mode build")
	if ag.Mode() != agent.Build {
		t.Fatal("/mode build")
	}
}

func TestEnterWhileBusyDoesNotGrowInput(t *testing.T) {
	a := &App{busy: true, input: NewInput(), model: NewModel(), agent: testAgent("qwen")}
	a.input.SetText("do it")
	a.handleKeys([]Key{{Kind: KeyNamed, Name: KeyEnter}})
	if strings.Contains(a.input.Text(), "\n") {
		t.Fatalf("enter while busy inserted newline: %q", a.input.Text())
	}
	if a.input.Text() != "do it" {
		t.Fatalf("input = %q", a.input.Text())
	}
}

func TestInputBlockKeepsTrailingSpace(t *testing.T) {
	a := &App{input: NewInput(), width: 40}
	a.input.SetText("hello ")
	rows, _, cx, vis := a.inputBlock()
	if !vis {
		t.Fatal("cursor should be visible")
	}
	if !strings.Contains(rowTexts(rows)[0], "hello ") {
		t.Fatalf("missing trailing space: %q", rowTexts(rows)[0])
	}
	if cx != 8 { // "› " (2) + "hello " (6)
		t.Fatalf("cx=%d want 8", cx)
	}
}

func TestPushSideDataLatestWins(t *testing.T) {
	a := &App{sideCh: make(chan sideData, 1)}
	a.pushSideData(sideData{preview: "old"})
	a.pushSideData(sideData{preview: "new"})
	select {
	case d := <-a.sideCh:
		if d.preview != "new" {
			t.Fatalf("got %q want new", d.preview)
		}
	default:
		t.Fatal("expected queued refresh")
	}
	select {
	case <-a.sideCh:
		t.Fatal("channel should hold only the latest")
	default:
	}
}

func TestEndTurnCancelsPendingQuestion(t *testing.T) {
	ch := make(chan questionReply, 1)
	a := &App{
		model:  NewModel(),
		asking: &questionRequest{q: tools.Question{Prompt: "?"}, reply: ch},
	}
	a.endTurn()
	got := <-ch
	if got.err == nil {
		t.Fatal("expected turn-ended error")
	}
	joined := strings.Join(rowTexts(a.model.Rows()), "\n")
	if !strings.Contains(joined, "skipped") && !strings.Contains(joined, "cancel") {
		t.Fatalf("want cancel/skip note: %q", joined)
	}
}

func TestDraftStaysOutOfTranscript(t *testing.T) {
	a := &App{width: 80, height: 24, input: NewInput(), model: NewModel(), follow: true}
	a.input.SetText("hi")
	_, _, histH, inputRows, promptRows, _, _, _ := a.composeLayout()
	if a.model.Len() != 0 {
		t.Fatal("draft must not commit to the transcript")
	}
	if histH+len(promptRows)+1+len(inputRows)+1 > a.height {
		t.Fatalf("frame overflow hist=%d prompt=%d input=%d height=%d", histH, len(promptRows), len(inputRows), a.height)
	}
	joined := strings.Join(rowTexts(inputRows), "\n")
	if !strings.Contains(joined, "hi") || !strings.Contains(joined, "›") {
		t.Fatalf("live input = %q", joined)
	}
	if strings.Contains(joined, "❯") {
		t.Fatalf("live input used history prefix: %q", joined)
	}
}

func TestComposeBandShrinksInputNotHistOnTinyScreen(t *testing.T) {
	a := &App{width: 40, height: 5, input: NewInput(), model: NewModel()}
	a.input.SetText("one\ntwo\nthree\nfour\nfive")
	_, _, histH, inputRows, promptRows, _, _, _ := a.composeLayout()
	if histH < 1 {
		t.Fatal("histH must stay at least 1")
	}
	if histH+len(promptRows)+1+len(inputRows)+1 > a.height {
		t.Fatalf("overflow hist=%d input=%d prompt=%d", histH, len(inputRows), len(promptRows))
	}
}

func TestSidebarIntentSurvivesNarrowWidth(t *testing.T) {
	a := &App{width: 119, height: 24, sidebarOn: true, input: NewInput(), model: NewModel()}
	sideW, _ := a.paneWidths()
	if sideW == 0 {
		t.Fatal("expected sidebar at raw 120 cols")
	}
	a.width = 80
	sideW, _ = a.paneWidths()
	if sideW != 0 {
		t.Fatal("expected hidden draw when narrow")
	}
	if !a.sidebarOn {
		t.Fatal("narrow layout must not clear sidebar intent")
	}
	a.width = 119
	sideW, _ = a.paneWidths()
	if sideW == 0 {
		t.Fatal("expected sidebar to return when wide again")
	}
}

func TestToggleSidebarKeepsIntentWhenNarrow(t *testing.T) {
	a := &App{width: 80, height: 24, sidebarOn: false, model: NewModel()}
	a.toggleSidebar()
	if !a.sidebarOn {
		t.Fatal("ctrl+b should set intent even when too narrow")
	}
}

func TestShiftEnterWhileBusyDoesNotGrowInput(t *testing.T) {
	a := &App{busy: true, input: NewInput(), model: NewModel(), agent: testAgent("qwen")}
	a.input.SetText("do it")
	a.handleKeys([]Key{{Kind: KeyNamed, Name: KeyShiftEnter}})
	if strings.Contains(a.input.Text(), "\n") {
		t.Fatalf("shift+enter while busy inserted newline: %q", a.input.Text())
	}
}

func TestAltRuneDoesNotClearInput(t *testing.T) {
	a := &App{input: NewInput()}
	a.input.SetText("hello")
	a.handleKeys(Parse([]byte{0x1b, 'x'}))
	if a.input.Text() != "hellox" {
		t.Fatalf("alt+x cleared or dropped input: %q", a.input.Text())
	}
}

func TestTypingResetsHistoryIndex(t *testing.T) {
	a := &App{input: NewInput(), history: []string{"old"}, historyIdx: 0}
	a.handleKeys([]Key{{Kind: KeyRune, Rune: 'x'}})
	if a.historyIdx != -1 {
		t.Fatalf("historyIdx = %d", a.historyIdx)
	}
	if a.input.Text() != "x" {
		t.Fatalf("input = %q", a.input.Text())
	}
}

func TestOverlayKeysWinOverApproval(t *testing.T) {
	ch := make(chan error, 1)
	a := &App{
		model:   NewModel(),
		overlay: "palette",
		palette: newPalette(),
		pending: &approveRequest{name: "write", args: `{"path":"x"}`, reply: ch},
		done:    make(chan struct{}),
	}
	a.handleKeys([]Key{{Kind: KeyRune, Rune: 'y'}})
	if a.pending == nil {
		t.Fatal("overlay typing must not approve")
	}
	select {
	case err := <-ch:
		t.Fatalf("unexpected approval result: %v", err)
	default:
	}
}

func TestSyncSessionOnStatusAndToolDone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess, err := session.Create()
	if err != nil {
		t.Fatal(err)
	}
	ag := testAgent("fake")
	ag.LoadHistory([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	a := &App{model: NewModel(), agent: ag, sess: sess}
	a.handleEvent(agent.Event{Type: agent.EventStatus, Name: "waiting"})
	if n := len(sess.Messages()); n != 1 {
		t.Fatalf("after status messages = %d", n)
	}
	ag.LoadHistory([]llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "list"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "list", Content: "ok"},
	})
	a.handleEvent(agent.Event{Type: agent.EventToolDone, Name: "list", Output: "ok", ToolCallID: "c1"})
	if n := len(sess.Messages()); n != 3 {
		t.Fatalf("after tool done messages = %d", n)
	}
}

func TestShortIDRuneSafe(t *testing.T) {
	id := strings.Repeat("世", 20)
	got := shortID(id)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid utf8: %q", got)
	}
	if len([]rune(got)) != 13 { // 8 + ellipsis + 4
		t.Fatalf("runes=%d %q", len([]rune(got)), got)
	}
}
