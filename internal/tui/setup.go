package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"quikagent/internal/config"
)

const (
	setupFieldAPI = iota
	setupFieldURL
	setupFieldModel
	setupFieldRouter
	setupFieldCount
)

// SetupResult is returned by RunSetup.
type SetupResult int

const (
	SetupSaved SetupResult = iota
	SetupAborted
)

// MaskAPIKey hides most of a key for display (•••• + last 4).
func MaskAPIKey(key string) string {
	r := []rune(strings.TrimSpace(key))
	if len(r) == 0 {
		return "(empty)"
	}
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", 8) + string(r[len(r)-4:])
}

// setupForm holds editable connection prefs.
type setupForm struct {
	apiKey, baseURL, model string
	router                 bool
	field                  int
	editing                bool // when true, typing goes into active text field
	draft                  string
}

func newSetupForm(cfg *config.Config) *setupForm {
	f := &setupForm{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		router:  cfg.Router.Enabled,
	}
	if f.baseURL == "" {
		f.baseURL = config.DefaultBaseURL
	}
	if f.model == "" {
		f.model = config.DefaultModel
	}
	return f
}

func (f *setupForm) beginEdit() {
	switch f.field {
	case setupFieldAPI:
		f.draft = f.apiKey
		f.editing = true
	case setupFieldURL:
		f.draft = f.baseURL
		f.editing = true
	case setupFieldModel:
		f.draft = f.model
		f.editing = true
	case setupFieldRouter:
		f.router = !f.router
	}
}

func (f *setupForm) commitEdit() {
	if !f.editing {
		return
	}
	v := strings.TrimSpace(f.draft)
	switch f.field {
	case setupFieldAPI:
		f.apiKey = v
	case setupFieldURL:
		if v != "" {
			f.baseURL = v
		}
	case setupFieldModel:
		if v != "" {
			f.model = v
		}
	}
	f.editing = false
	f.draft = ""
}

func (f *setupForm) apply(cfg *config.Config) {
	cfg.APIKey = strings.TrimSpace(f.apiKey)
	cfg.BaseURL = strings.TrimSpace(f.baseURL)
	cfg.Model = strings.TrimSpace(f.model)
	cfg.Router.Enabled = f.router
}

func (f *setupForm) canSave() bool {
	return strings.TrimSpace(f.apiKey) != "" && strings.TrimSpace(f.baseURL) != "" && strings.TrimSpace(f.model) != ""
}

// RunSetup is a blocking first-run / reconnect wizard on an owned terminal.
// Returns SetupSaved after writing config.yaml, or SetupAborted on Esc/Ctrl+C/Ctrl+Q.
func RunSetup(term *Terminal, cfg *config.Config) (SetupResult, error) {
	form := newSetupForm(cfg)
	renderer := NewRenderer(term)
	w, h := term.Size()
	renderer.Resize(w, h)

	redraw := func() {
		w, h = term.Size()
		renderer.Resize(w, h)
		renderer.Render(renderSetupFrame(form, w-1, h))
	}
	redraw()

	buf := make([]byte, 4096)
	for {
		n, err := term.Read(buf)
		if err != nil {
			return SetupAborted, err
		}
		if term.ConsumeWinch() {
			redraw()
		}
		keys := Parse(buf[:n])
		for _, k := range keys {
			res, done := form.handleKey(k)
			if done {
				if res == SetupSaved {
					form.apply(cfg)
					if err := cfg.Save(); err != nil {
						return SetupAborted, err
					}
				}
				return res, nil
			}
		}
		redraw()
	}
}

func (f *setupForm) handleKey(k Key) (SetupResult, bool) {
	if k.Kind == KeyMouse {
		return 0, false
	}
	if f.editing {
		switch {
		case k.Kind == KeyRune:
			f.draft += string(k.Rune)
		case k.Kind == KeyPaste:
			f.draft += k.Text
		case k.is(KeyBackspace):
			if f.draft != "" {
				_, size := utf8.DecodeLastRuneInString(f.draft)
				f.draft = f.draft[:len(f.draft)-size]
			}
		case k.is(KeyEnter), k.is(KeyEsc):
			if k.is(KeyEnter) {
				f.commitEdit()
			} else {
				f.editing = false
				f.draft = ""
			}
		case k.Kind == KeyCtrl && k.Ctrl == 'c':
			return SetupAborted, true
		}
		return 0, false
	}

	switch {
	case k.Kind == KeyCtrl:
		switch k.Ctrl {
		case 'c', 'q':
			return SetupAborted, true
		case 's':
			if f.canSave() {
				return SetupSaved, true
			}
		}
	case k.is(KeyEsc):
		return SetupAborted, true
	case k.is(KeyUp), k.is(KeyShiftUp):
		if f.field > 0 {
			f.field--
		}
	case k.is(KeyDown), k.is(KeyTab), k.is(KeyShiftDown):
		if f.field < setupFieldCount-1 {
			f.field++
		}
	case k.is(KeyEnter):
		switch f.field {
		case setupFieldRouter:
			f.router = !f.router
		default:
			f.beginEdit()
		}
	case k.Kind == KeyRune && (k.Rune == ' ' || k.Rune == 'y' || k.Rune == 'n'):
		if f.field == setupFieldRouter {
			if k.Rune == 'n' {
				f.router = false
			} else {
				f.router = true
			}
		} else {
			f.beginEdit()
			if k.Rune != ' ' {
				f.draft += string(k.Rune)
			}
		}
	}
	return 0, false
}

func renderSetupFrame(f *setupForm, width, height int) []Row {
	if width < 20 {
		width = 20
	}
	lines := []string{
		"quikagent setup",
		"",
		"Connect to your OpenAI-compatible LLM endpoint.",
		"Saved to ~/.quikagent/config.yaml (mode 0600).",
		"",
	}
	labels := []string{"API key", "Base URL", "Model", "Router"}
	for i := range setupFieldCount {
		mark := "  "
		if i == f.field {
			mark = "> "
		}
		var val string
		switch i {
		case setupFieldAPI:
			if f.editing && f.field == i {
				val = f.draft + "▌"
			} else {
				val = MaskAPIKey(f.apiKey)
			}
		case setupFieldURL:
			if f.editing && f.field == i {
				val = f.draft + "▌"
			} else {
				val = f.baseURL
			}
		case setupFieldModel:
			if f.editing && f.field == i {
				val = f.draft + "▌"
			} else {
				val = f.model
			}
		case setupFieldRouter:
			if f.router {
				val = "on"
			} else {
				val = "off"
			}
		}
		lines = append(lines, fmt.Sprintf("%s%s: %s", mark, labels[i], val))
	}
	lines = append(lines, "",
		"↑/↓ select · enter edit/toggle · ctrl+s save · esc abort",
	)
	if !f.canSave() && !f.editing {
		lines = append(lines, "(API key required to save)")
	}

	out := make([]Row, height)
	for y := range height {
		text := ""
		attr := styleDim
		if y < len(lines) {
			text = lines[y]
			if y == 0 {
				attr = styleAccent.withBold().withBG(colSubtle)
				out[y] = Row{Segs: []Segment{{Text: padClip(text, width), Attr: attr}}}
				continue
			} else if strings.HasPrefix(text, "> ") {
				attr = styleDefault.withBold()
			} else {
				attr = styleDefault
			}
		}
		out[y] = Row{Segs: []Segment{{Text: padClip(text, width), Attr: attr}}}
	}
	return out
}
