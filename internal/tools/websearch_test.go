package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runWebSearch(t *testing.T, tool Tool, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), raw)
}

func searxHandler(t *testing.T, n int, gotAuth *string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format = %q, want json", r.URL.Query().Get("format"))
		}
		results := make([]map[string]string, 0, n)
		for i := 1; i <= n; i++ {
			results = append(results, map[string]string{
				"title":   fmt.Sprintf("Result %d", i),
				"url":     fmt.Sprintf("https://example.com/%d", i),
				"content": fmt.Sprintf("Snippet %d", i),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}
}

func TestWebSearchResultsWithLimit(t *testing.T) {
	srv := httptest.NewServer(searxHandler(t, 8, nil))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "")
	out, err := runWebSearch(t, tool, map[string]any{"query": "go testing", "limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1. Result 1", "https://example.com/1", "Snippet 1", "3. Result 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("out missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Result 4") {
		t.Errorf("limit not applied:\n%s", out)
	}
}

func TestWebSearchDefaultAndMaxLimit(t *testing.T) {
	srv := httptest.NewServer(searxHandler(t, 20, nil))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "")

	out, err := runWebSearch(t, tool, map[string]any{"query": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5. Result 5") || strings.Contains(out, "Result 6") {
		t.Errorf("default limit not 5:\n%s", out)
	}

	out, err = runWebSearch(t, tool, map[string]any{"query": "x", "limit": 50})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10. Result 10") || strings.Contains(out, "Result 11") {
		t.Errorf("limit not capped at 10:\n%s", out)
	}
}

func TestWebSearchQueryEscaped(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "")
	out, err := runWebSearch(t, tool, map[string]any{"query": "a b&c=d"})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "a b&c=d" {
		t.Errorf("q = %q, want %q", gotQuery, "a b&c=d")
	}
	if !strings.Contains(out, "No results") {
		t.Errorf("out = %q", out)
	}
}

func TestWebSearchNonJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>not json at all</html>")
	}))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "")
	_, err := runWebSearch(t, tool, map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "not recognized JSON") {
		t.Fatalf("err = %v, want unrecognized JSON error", err)
	}
}

func TestWebSearchNullResultsIsError(t *testing.T) {
	for _, body := range []string{`{"results":null}`, `{}`, `{"results":null,"query":"x"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		tool := NewWebSearch(srv.URL, "")
		_, err := runWebSearch(t, tool, map[string]any{"query": "x"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "missing results") {
			t.Fatalf("body %s: err = %v, want missing results", body, err)
		}
	}
}

func TestWebSearchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "")
	_, err := runWebSearch(t, tool, map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want HTTP 500 error", err)
	}
}

func TestWebSearchAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(searxHandler(t, 1, &gotAuth))
	defer srv.Close()

	tool := NewWebSearch(srv.URL, "sekret")
	if _, err := runWebSearch(t, tool, map[string]any{"query": "x"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sekret")
	}

	gotAuth = ""
	tool = NewWebSearch(srv.URL, "")
	if _, err := runWebSearch(t, tool, map[string]any{"query": "x"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty when no key set", gotAuth)
	}
}

func TestWebSearchUnconfigured(t *testing.T) {
	tool := NewWebSearch("", "")
	_, err := runWebSearch(t, tool, map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want not-configured error", err)
	}
}

func TestWebSearchQueryRequired(t *testing.T) {
	tool := NewWebSearch("http://example.invalid", "")
	for _, args := range []any{map[string]any{}, map[string]any{"query": "  "}} {
		_, err := runWebSearch(t, tool, args)
		if err == nil || !strings.Contains(err.Error(), "query is required") {
			t.Fatalf("args %v: err = %v, want query-required error", args, err)
		}
	}
}

func TestWebSearchSpec(t *testing.T) {
	tool := NewWebSearch("http://localhost:8888/search", "")
	spec := tool.Spec()
	if spec.Name != "websearch" {
		t.Errorf("name = %q", spec.Name)
	}
	if !tool.ReadOnly() {
		t.Error("websearch should be read-only")
	}
}
