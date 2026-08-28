package text

import "testing"

func TestClipRunes(t *testing.T) {
	if got := ClipRunes("hello", 5); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := ClipRunes("hello", 3); got != "hel…" {
		t.Fatalf("got %q", got)
	}
	if got := ClipRunes("日本語です", 2); got != "日本…" {
		t.Fatalf("got %q", got)
	}
	if got := ClipRunes("x", 0); got != "" {
		t.Fatalf("got %q", got)
	}
}
