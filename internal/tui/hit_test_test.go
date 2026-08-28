package tui

import "testing"

func TestHitTest(t *testing.T) {
	// Layout: mainW=88, sideW=30, histH=21, sep at 21, input at 22 (1 row), bodyH=23
	const mainW, sideW, histH = 88, 30, 21
	const inputTop, inputN, bodyH = 22, 1, 23

	cases := []struct {
		name string
		x, y int
		want HitRegion
	}{
		{"transcript", 10, 5, HitTranscript},
		{"transcript edge", mainW - 1, histH - 1, HitTranscript},
		{"sidebar", mainW + 1, 5, HitSidebar},
		{"sidebar over input rows", mainW + 5, inputTop, HitSidebar},
		{"divider ignored", mainW, 5, HitNone},
		{"input", 5, inputTop, HitInput},
		{"separator", 5, histH, HitNone},
		{"status", 5, bodyH, HitNone},
		{"outside", -1, 0, HitNone},
	}
	for _, tc := range cases {
		got := HitTest(tc.x, tc.y, mainW, sideW, histH, inputTop, inputN, bodyH)
		if got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}

	// Without sidebar, x past mainW is none.
	if got := HitTest(mainW+1, 5, mainW, 0, histH, inputTop, inputN, bodyH); got != HitNone {
		t.Fatalf("no sidebar: got %v", got)
	}
}

func TestHitTestMultilineInput(t *testing.T) {
	const mainW, sideW, histH = 88, 30, 19
	const inputTop, inputN, bodyH = 20, 3, 23

	if got := HitTest(5, histH-1, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitTranscript {
		t.Fatalf("row above input: got %v", got)
	}
	for i := 0; i < inputN; i++ {
		if got := HitTest(5, inputTop+i, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitInput {
			t.Fatalf("input row %d: got %v", i, got)
		}
	}
	if got := HitTest(5, inputTop+inputN, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitNone {
		t.Fatalf("row after input: got %v", got)
	}
	if got := HitTest(mainW+5, inputTop+1, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitSidebar {
		t.Fatalf("sidebar over multiline input: got %v", got)
	}
}

func TestHitTestNarrowMain(t *testing.T) {
	// Layout collapses the sidebar when mainW would be < 20.
	const mainW, sideW, histH = 15, 0, 10
	const inputTop, inputN, bodyH = 11, 1, 12

	if got := HitTest(5, 5, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitTranscript {
		t.Fatalf("narrow transcript: got %v", got)
	}
	if got := HitTest(mainW-1, inputTop, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitInput {
		t.Fatalf("narrow input: got %v", got)
	}
	if got := HitTest(mainW, 5, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitNone {
		t.Fatalf("x at collapsed mainW: got %v", got)
	}
	if got := HitTest(mainW+1, 5, mainW, sideW, histH, inputTop, inputN, bodyH); got != HitNone {
		t.Fatalf("past collapsed sidebar: got %v", got)
	}
}
