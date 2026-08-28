package tui

import (
	"testing"

	"quikagent/internal/config"
)

func TestMaskAPIKey(t *testing.T) {
	if got := MaskAPIKey(""); got != "(empty)" {
		t.Fatalf("%q", got)
	}
	if got := MaskAPIKey("abcd"); got != "••••" {
		t.Fatalf("%q", got)
	}
	got := MaskAPIKey("sk-abcdefghij")
	if !containsSuffix(got, "ghij") || got == "sk-abcdefghij" {
		t.Fatalf("%q", got)
	}
}

func containsSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

func TestSetupFormToggleRouter(t *testing.T) {
	f := newSetupForm(&config.Config{Router: config.RouterConfig{Enabled: false}})
	f.field = setupFieldRouter
	if _, done := f.handleKey(Key{Kind: KeyNamed, Name: KeyEnter}); done {
		t.Fatal("toggle should not finish setup")
	}
	if !f.router {
		t.Fatal("enter on router should enable")
	}
	if _, done := f.handleKey(Key{Kind: KeyRune, Rune: 'n'}); done {
		t.Fatal("n should not finish setup")
	}
	if f.router {
		t.Fatal("n on router should disable")
	}
}
