package tui

import (
	"strings"
	"testing"

	"quikagent/internal/config"
	"quikagent/internal/tools"
)

func TestPaletteIncludesUndoRedo(t *testing.T) {
	found := map[string]bool{}
	for _, c := range defaultPaletteCommands() {
		found[c.ID] = true
	}
	if !found["undo"] || !found["redo"] {
		t.Fatalf("palette missing undo/redo: %+v", found)
	}
	if !found["resume"] || !found["plan"] {
		t.Fatalf("palette missing resume/plan: %+v", found)
	}
}

func TestFormatQuestionNote(t *testing.T) {
	got := formatQuestionNote(tools.Question{Header: "DB", Prompt: "which?", Options: []string{"a", "b"}})
	if !strings.Contains(got, "DB") || !strings.Contains(got, "1) a") || !strings.Contains(got, "2) b") {
		t.Fatalf("%q", got)
	}
}

func TestPaletteFilter(t *testing.T) {
	p := newPalette()
	p.filter = "set"
	items := p.filtered()
	if len(items) == 0 {
		t.Fatal("expected matches for set")
	}
	found := false
	for _, c := range items {
		if c.ID == "setup" || c.ID == "config" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", items)
	}
}

func TestModelPickerFilterAndAuto(t *testing.T) {
	p := newModelPicker([]string{"qwen3.8-27b-q5", "nemotron-nano-q4"}, true, "qwen3.8-27b-q5", false, true)
	if p.items[0] != modelPickAuto {
		t.Fatalf("first = %q", p.items[0])
	}
	p.filter = "nano"
	got := p.filtered()
	if len(got) != 1 || got[0] != "nemotron-nano-q4" {
		t.Fatalf("%v", got)
	}
	p.filter = "auto"
	got = p.filtered()
	if len(got) != 1 || got[0] != modelPickAuto {
		t.Fatalf("%v", got)
	}
}

func TestFavoriteModelsIncludesCurrentAndRoutes(t *testing.T) {
	cfg := &config.Config{
		Model:      config.DefaultModel,
		SmallModel: config.DefaultSmallModel,
		Router:     config.RouterConfig{Routes: config.DefaultRoutes()},
	}
	got := FavoriteModels(cfg, "custom-current")
	if len(got) == 0 || got[0] != modelPickAuto {
		t.Fatalf("want auto first, got %v", got)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "custom-current") {
		t.Fatalf("missing current: %v", got)
	}
	if !strings.Contains(joined, config.DefaultModel) || !strings.Contains(joined, config.DefaultSmallModel) {
		t.Fatalf("missing route targets: %v", got)
	}
	cur := -1
	for i, id := range got {
		if id == "custom-current" {
			cur = i
			break
		}
	}
	if cur != 1 {
		t.Fatalf("current should be second (after auto), got idx %d in %v", cur, got)
	}
}

func TestConfigMenuToggleActions(t *testing.T) {
	m := &configMenu{idx: configItemRouter}
	if act := m.handleKey(Key{Kind: KeyNamed, Name: KeyEnter}); act != configActToggleRouter {
		t.Fatalf("got %v", act)
	}
	m.idx = configItemConnection
	if act := m.handleKey(Key{Kind: KeyNamed, Name: KeyEnter}); act != configActOpenSetup {
		t.Fatalf("got %v", act)
	}
}
