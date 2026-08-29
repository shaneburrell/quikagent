package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsSetupWhenNoKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "")
	cfg, err := Load("/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if !NeedsSetup(cfg) {
		t.Fatal("expected NeedsSetup")
	}
}

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "secret")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "")
	cfg, err := Load("/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != DefaultModel || cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("%+v", cfg)
	}
	if !cfg.Sidebar {
		t.Fatal("sidebar should default on")
	}
	if cfg.Router.Enabled {
		t.Fatal("router should be off by default")
	}
	if cfg.Router.Routes["nano"].Model != DefaultSmallModel {
		t.Fatalf("routes = %+v", cfg.Router.Routes)
	}
	if cfg.Router.Routes["coder"].Model != DefaultModel {
		t.Fatalf("coder route = %+v", cfg.Router.Routes["coder"])
	}
}

func TestDefaultRoutesPolicyCopy(t *testing.T) {
	r := DefaultRoutes()
	if !strings.Contains(r["coder"].Description, "Implement or edit code right now") {
		t.Fatalf("coder = %q", r["coder"].Description)
	}
	if !strings.Contains(r["qwen"].Description, "even if they said make, build, or implement") {
		t.Fatalf("qwen = %q", r["qwen"].Description)
	}
	if !strings.Contains(r["other"].Description, "Off-topic chat") {
		t.Fatalf("other = %q", r["other"].Description)
	}
	if strings.Contains(r["other"].Description, "Intent unclear") {
		t.Fatal("other should not be an unclear-intent escape hatch")
	}
	if r["nano"].Description != routeDescNano {
		t.Fatalf("nano = %q", r["nano"].Description)
	}
}

func TestStaleRouteDescriptionsRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "")
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "" +
		"api_key: k\n" +
		"router:\n" +
		"  enabled: true\n" +
		"  routes:\n" +
		"    coder:\n" +
		"      model: my-coder\n" +
		"      description: \"Write, edit, refactor, or implement code: multi-file changes, tests, agentic coding loops\"\n" +
		"    qwen:\n" +
		"      model: my-qwen\n" +
		"      description: \"Architecture and design tradeoffs, deep debugging of subtle failures, long analysis, vision and images — NOT routine write/edit/refactor/implement code\"\n" +
		"    other:\n" +
		"      model: my-other\n" +
		"      description: \"Intent unclear — keep the coding model; do not switch to Qwen\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Router.Routes["coder"].Model != "my-coder" {
		t.Fatalf("coder model overwritten: %+v", cfg.Router.Routes["coder"])
	}
	if cfg.Router.Routes["qwen"].Model != "my-qwen" {
		t.Fatalf("qwen model overwritten: %+v", cfg.Router.Routes["qwen"])
	}
	if cfg.Router.Routes["coder"].Description != routeDescCoder {
		t.Fatalf("coder desc = %q", cfg.Router.Routes["coder"].Description)
	}
	if cfg.Router.Routes["qwen"].Description != routeDescQwen {
		t.Fatalf("qwen desc = %q", cfg.Router.Routes["qwen"].Description)
	}
	if cfg.Router.Routes["other"].Description != routeDescOther {
		t.Fatalf("other desc = %q", cfg.Router.Routes["other"].Description)
	}
}

func TestCustomRouteDescriptionPreserved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "")
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "" +
		"api_key: k\n" +
		"router:\n" +
		"  routes:\n" +
		"    qwen:\n" +
		"      model: custom-qwen\n" +
		"      description: \"Only for vision and screenshots\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Router.Routes["qwen"].Description != "Only for vision and screenshots" {
		t.Fatalf("custom description rewritten: %q", cfg.Router.Routes["qwen"].Description)
	}
	if cfg.Router.Routes["qwen"].Model != "custom-qwen" {
		t.Fatalf("qwen model = %q", cfg.Router.Routes["qwen"].Model)
	}
}

func TestLoadYAMLThenEnvWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvBaseURL, "https://env.example/v1")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "1")
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "api_key: file-key\nbase_url: https://file.example/v1\nmodel: from-file\nmax_tokens: 4096\nsidebar: false\nrouter:\n  enabled: false\n  model: arch-router-1.5b\nmcpServers:\n  demo:\n    command: echo\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if cfg.BaseURL != "https://env.example/v1" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Model != "from-file" {
		t.Fatalf("Model = %q", cfg.Model)
	}
	if cfg.Sidebar {
		t.Fatal("sidebar false in file should stick")
	}
	if !cfg.Router.Enabled {
		t.Fatal("QUIKAGENT_ROUTER=1 should enable router")
	}
	if cfg.MCPServers["demo"].Command != "echo" {
		t.Fatalf("MCP = %+v", cfg.MCPServers)
	}
}

func TestLoadFileKeyWhenNoEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	dir := filepath.Join(home, ".quikagent")
	_ = os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("api_key: stored-secret\nmodel: m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "stored-secret" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if NeedsSetup(cfg) {
		t.Fatal("should not need setup")
	}
}

func TestSaveYAMLRoundTripIncludesKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvModel, "")
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = "saved-model"
	cfg.Router.Enabled = true
	cfg.Sidebar = false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".quikagent", "config.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "api_key:") || !strings.Contains(string(data), "k") {
		t.Fatalf("API key missing from yaml: %s", data)
	}
	t.Setenv(EnvAPIKey, "")
	loaded, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != "saved-model" || !loaded.Router.Enabled || loaded.APIKey != "k" || loaded.Sidebar {
		t.Fatalf("%+v", loaded)
	}
}

func TestSavePermissionsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions = Permissions{
		Allow: []string{"bash(git status*)", "read(*)"},
		Deny:  []string{"bash(rm *)"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Permissions.Allow) != 2 || loaded.Permissions.Allow[0] != "bash(git status*)" || loaded.Permissions.Allow[1] != "read(*)" {
		t.Fatalf("Allow = %v", loaded.Permissions.Allow)
	}
	if len(loaded.Permissions.Deny) != 1 || loaded.Permissions.Deny[0] != "bash(rm *)" {
		t.Fatalf("Deny = %v", loaded.Permissions.Deny)
	}
}

func TestWebSearchConfigRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvWebSearchURL, "")
	t.Setenv(EnvWebSearchKey, "")
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg.WebSearchURL = "http://127.0.0.1:8888/search"
	cfg.WebSearchKey = "sk-search"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WebSearchURL != "http://127.0.0.1:8888/search" || loaded.WebSearchKey != "sk-search" {
		t.Fatalf("websearch = %q %q", loaded.WebSearchURL, loaded.WebSearchKey)
	}

	t.Setenv(EnvWebSearchURL, "http://env.example/search")
	t.Setenv(EnvWebSearchKey, "env-key")
	over, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if over.WebSearchURL != "http://env.example/search" || over.WebSearchKey != "env-key" {
		t.Fatalf("env override = %q %q", over.WebSearchURL, over.WebSearchKey)
	}
}

func TestLoadLegacyJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	dir := filepath.Join(home, ".quikagent")
	_ = os.MkdirAll(dir, 0o700)
	body := `{"base_url":"https://legacy.example/v1","model":"legacy-model","max_tokens":2048}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://legacy.example/v1" || cfg.Model != "legacy-model" {
		t.Fatalf("%+v", cfg)
	}
}

func TestLoadPlanModelYAMLAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "k")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvPlanModel, "")
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "api_key: k\nmodel: coder\nplan_model: qwen-plan\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlanModel != "qwen-plan" {
		t.Fatalf("PlanModel = %q", cfg.PlanModel)
	}
	t.Setenv(EnvPlanModel, "env-plan")
	cfg, err = Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlanModel != "env-plan" {
		t.Fatalf("env should win, PlanModel = %q", cfg.PlanModel)
	}
}

func TestKnownModels(t *testing.T) {
	cfg := &Config{
		Model:      DefaultModel,
		SmallModel: DefaultSmallModel,
		PlanModel:  "plan-model",
		Router:     RouterConfig{Routes: DefaultRoutes()},
	}
	got := KnownModels(cfg, []string{"extra-model", DefaultModel})
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, DefaultModel) || !strings.Contains(joined, DefaultSmallModel) || !strings.Contains(joined, "plan-model") || !strings.Contains(joined, "extra-model") {
		t.Fatalf("%v", got)
	}
	if got[0] != DefaultModel {
		t.Fatalf("want config model first, got %v", got)
	}
}

// TestSaveAtomicAndMode tests that Save() is atomic and enforces 0600 mode.
func TestSaveAtomicAndMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a config file with 0644 mode (simulating existing file)
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")

	// Pre-create with 0644 mode
	if err := os.WriteFile(path, []byte("api_key: old-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load the config and modify it
	cfg, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = "new-key"
	cfg.Model = "new-model"

	// Save should make it 0600 and be atomic
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	// Check mode is 0600 (not 0644)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}

	// Check content is correct
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "api_key: new-key") || !strings.Contains(content, "model: new-model") {
		t.Fatalf("content incorrect: %s", content)
	}
}

func TestProjectConfigOverridesHome(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "")
	if err := os.MkdirAll(filepath.Join(home, ".quikagent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".quikagent", "config.yaml"), []byte("api_key: home-key\nmodel: home-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wd, ".quikagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".quikagent", "config.yaml"), []byte("model: project-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(wd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "home-key" || cfg.Model != "project-model" {
		t.Fatalf("key=%q model=%q", cfg.APIKey, cfg.Model)
	}
}

func TestApplyProvider(t *testing.T) {
	cfg := &Config{
		BaseURL: "https://default.example/v1",
		Model:   "default",
		Providers: map[string]Provider{
			"lab": {BaseURL: "https://lab.example/v1", APIKey: "lab-key", Model: "lab-model"},
		},
	}
	if err := cfg.ApplyProvider("lab"); err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://lab.example/v1" || cfg.APIKey != "lab-key" || cfg.Model != "lab-model" {
		t.Fatalf("%+v", cfg)
	}
	if err := cfg.ApplyProvider("missing"); err == nil {
		t.Fatal("expected unknown provider")
	}
}

func TestApplyProviderClearsOmittedKey(t *testing.T) {
	cfg := &Config{
		APIKey: "old-key",
		Providers: map[string]Provider{
			"plain": {BaseURL: "https://plain.example/v1", Model: "plain-model"},
		},
	}
	if err := cfg.ApplyProvider("plain"); err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "" {
		t.Fatalf("expected empty key, got %q", cfg.APIKey)
	}
	if cfg.BaseURL != "https://plain.example/v1" || cfg.Model != "plain-model" {
		t.Fatalf("%+v", cfg)
	}
}

func TestProjectOverlayMergesPermissionsRouterMCPLSP(t *testing.T) {
	home := t.TempDir()
	wd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvRouterModel, "")
	t.Setenv(EnvProvider, "")
	if err := os.MkdirAll(filepath.Join(home, ".quikagent"), 0o700); err != nil {
		t.Fatal(err)
	}
	homeYAML := "" +
		"api_key: home-key\n" +
		"router:\n" +
		"  enabled: true\n" +
		"permissions:\n" +
		"  allow:\n" +
		"    - bash(git *)\n" +
		"  deny:\n" +
		"    - bash(rm *)\n" +
		"mcpServers:\n" +
		"  home:\n" +
		"    command: home-mcp\n" +
		"lsp:\n" +
		"  command: gopls\n" +
		"  args: [serve]\n"
	if err := os.WriteFile(filepath.Join(home, ".quikagent", "config.yaml"), []byte(homeYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wd, ".quikagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	projYAML := "" +
		"router:\n" +
		"  model: project-router\n" +
		"permissions:\n" +
		"  allow:\n" +
		"    - write(*.go)\n" +
		"mcpServers:\n" +
		"  proj:\n" +
		"    command: proj-mcp\n" +
		"lsp:\n" +
		"  args: [--stdio]\n"
	if err := os.WriteFile(filepath.Join(wd, ".quikagent", "config.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(wd)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Router.Enabled {
		t.Fatal("project router: without enabled must not disable home router")
	}
	if cfg.Router.Model != "project-router" {
		t.Fatalf("router model=%q", cfg.Router.Model)
	}
	if len(cfg.Permissions.Allow) != 2 || cfg.Permissions.Allow[0] != "bash(git *)" || cfg.Permissions.Allow[1] != "write(*.go)" {
		t.Fatalf("allow=%v", cfg.Permissions.Allow)
	}
	if len(cfg.Permissions.Deny) != 1 || cfg.Permissions.Deny[0] != "bash(rm *)" {
		t.Fatalf("deny wiped: %v", cfg.Permissions.Deny)
	}
	if cfg.MCPServers["home"].Command != "home-mcp" || cfg.MCPServers["proj"].Command != "proj-mcp" {
		t.Fatalf("mcp=%v", cfg.MCPServers)
	}
	if cfg.LSP.Command != "gopls" {
		t.Fatalf("lsp command wiped: %+v", cfg.LSP)
	}
	if len(cfg.LSP.Args) != 1 || cfg.LSP.Args[0] != "--stdio" {
		t.Fatalf("lsp args=%v", cfg.LSP.Args)
	}
}

func TestLoadUnknownProviderSurfaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvRouter, "")
	t.Setenv(EnvProvider, "no-such-provider")
	_, err := Load(".")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("want unknown provider error, got %v", err)
	}
}

func TestKnownModelsCoderOrder(t *testing.T) {
	cfg := &Config{
		Model:      "primary",
		SmallModel: "small",
		Router: RouterConfig{Routes: map[string]RouteTarget{
			"nano":  {Model: "nano-m"},
			"coder": {Model: "coder-m"},
			"qwen":  {Model: "qwen-m"},
			"other": {Model: "other-m"},
		}},
	}
	got := KnownModels(cfg, nil)
	want := []string{"primary", "small", "nano-m", "coder-m", "qwen-m", "other-m"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}
