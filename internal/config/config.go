// Package config loads quikagent settings from a config file and environment.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	EnvBaseURL      = "QUIKAGENT_BASE_URL"
	EnvAPIKey       = "QUIKAGENT_API_KEY"
	EnvModel        = "QUIKAGENT_MODEL"
	EnvRouter       = "QUIKAGENT_ROUTER"
	EnvRouterModel  = "QUIKAGENT_ROUTER_MODEL"
	EnvSmallModel   = "QUIKAGENT_SMALL_MODEL"
	EnvPlanModel    = "QUIKAGENT_PLAN_MODEL"
	EnvWebSearchURL = "QUIKAGENT_WEBSEARCH_URL"
	EnvWebSearchKey = "QUIKAGENT_WEBSEARCH_KEY"
	EnvProvider     = "QUIKAGENT_PROVIDER"

	DefaultBaseURL     = "https://api.openai.com/v1"
	DefaultModel       = "gpt-4o"
	DefaultMaxTokens   = 8192
	DefaultRouterModel = "arch-router-1.5b"
	DefaultSmallModel  = "gpt-4o-mini"
)

// MCPServer describes a stdio MCP server.
type MCPServer struct {
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
}

// RouteTarget maps a router route name to a chat model.
type RouteTarget struct {
	Model       string `yaml:"model" json:"model"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Permissions defines the set of rules for tool execution.
type Permissions struct {
	Allow []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty" json:"deny,omitempty"`
}

// RouterConfig controls optional Arch-Router per-turn model selection.
type RouterConfig struct {
	Enabled bool                   `yaml:"enabled" json:"enabled"`
	Model   string                 `yaml:"model,omitempty" json:"model,omitempty"`
	Routes  map[string]RouteTarget `yaml:"routes,omitempty" json:"routes,omitempty"`
}

// fileRouter is the on-disk router block. Enabled is a pointer so an omitted
// key does not disable a router already enabled in a parent config.
type fileRouter struct {
	Enabled *bool                  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Model   string                 `yaml:"model,omitempty" json:"model,omitempty"`
	Routes  map[string]RouteTarget `yaml:"routes,omitempty" json:"routes,omitempty"`
}

// fileConfig is the on-disk ~/.quikagent/config.yaml shape (and legacy JSON).
type fileConfig struct {
	APIKey         string               `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BaseURL        string               `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Model          string               `yaml:"model,omitempty" json:"model,omitempty"`
	MaxTokens      int                  `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	SmallModel     string               `yaml:"small_model,omitempty" json:"small_model,omitempty"`
	PlanModel      string               `yaml:"plan_model,omitempty" json:"plan_model,omitempty"`
	Sidebar        *bool                `yaml:"sidebar,omitempty" json:"sidebar,omitempty"`
	Router         *fileRouter          `yaml:"router,omitempty" json:"router,omitempty"`
	MCPServers     map[string]MCPServer `yaml:"mcpServers,omitempty" json:"mcpServers,omitempty"`
	Permissions    Permissions          `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	WebSearchURL   string               `yaml:"websearch_url,omitempty" json:"websearch_url,omitempty"`
	WebSearchKey   string               `yaml:"websearch_key,omitempty" json:"websearch_key,omitempty"`
	Providers      map[string]Provider  `yaml:"providers,omitempty" json:"providers,omitempty"`
	ActiveProvider string               `yaml:"provider,omitempty" json:"provider,omitempty"`
	LSP            *LSPConfig           `yaml:"lsp,omitempty" json:"lsp,omitempty"`
}

// LSPConfig names a language server for the experimental lsp tool.
type LSPConfig struct {
	Command string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"`
}

// Provider is a named OpenAI-compatible endpoint.
type Provider struct {
	BaseURL string   `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	APIKey  string   `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Model   string   `yaml:"model,omitempty" json:"model,omitempty"`
	Models  []string `yaml:"models,omitempty" json:"models,omitempty"`
}

// Config holds runtime settings for the agent.
type Config struct {
	BaseURL        string
	APIKey         string
	Model          string
	MaxTokens      int
	SmallModel     string
	PlanModel      string
	Sidebar        bool // default sidebar on when terminal is wide enough
	Workdir        string
	Router         RouterConfig
	MCPServers     map[string]MCPServer
	Permissions    Permissions
	WebSearchURL   string
	WebSearchKey   string
	Providers      map[string]Provider
	ActiveProvider string
	LSP            LSPConfig
}

const (
	routeDescNano  = "Trivial mechanical shell and git chores: commits, status, short one-off commands, titles"
	routeDescCoder = "Implement or edit code right now: write files, refactor, tests. Not for planning or proposing a design."
	routeDescQwen  = "Plan or design a system, feature, or architecture before coding. Use this when the user wants a proposal, tradeoffs, or a plan — even if they said make, build, or implement."
	routeDescOther = "Off-topic chat, thanks, or the user said they are done. Not for coding or planning requests."
)

// DefaultRoutes returns the built-in Arch-Router route map.
func DefaultRoutes() map[string]RouteTarget {
	return map[string]RouteTarget{
		"nano": {
			Model:       DefaultSmallModel,
			Description: routeDescNano,
		},
		"coder": {
			Model:       DefaultModel,
			Description: routeDescCoder,
		},
		"qwen": {
			Model:       DefaultModel,
			Description: routeDescQwen,
		},
		"other": {
			Model:       DefaultModel,
			Description: routeDescOther,
		},
	}
}

// staleRouteDescriptions are previous built-in texts that Save() froze into
// ~/.quikagent/config.yaml. Matching descriptions are replaced on load;
// custom text is left alone.
var staleRouteDescriptions = map[string][]string{
	"coder": {
		"Write, edit, refactor, or implement code: multi-file changes, tests, agentic coding loops",
	},
	"qwen": {
		"Architecture and design tradeoffs, deep debugging of subtle failures, long analysis, vision and images — NOT routine write/edit/refactor/implement code",
		"Planning, proposing a design, architecture and design tradeoffs, deep debugging of subtle failures, long analysis, vision and images — NOT routine write/edit/refactor/implement code",
	},
	"other": {
		"Intent unclear — keep the current model; do not switch",
		"Intent unclear — keep the coding model; do not switch to Qwen",
	},
}

func refreshBuiltinRouteDescriptions(routes map[string]RouteTarget) {
	if routes == nil {
		return
	}
	defaults := DefaultRoutes()
	for name, def := range defaults {
		cur, ok := routes[name]
		if !ok {
			continue
		}
		if !staleBuiltinRouteDescription(name, cur.Description) {
			continue
		}
		cur.Description = def.Description
		routes[name] = cur
	}
}

func staleBuiltinRouteDescription(name, desc string) bool {
	for _, old := range staleRouteDescriptions[name] {
		if desc == old {
			return true
		}
	}
	return false
}

// Dir returns ~/.quikagent, creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".quikagent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Path returns the config.yaml path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// LegacyPath returns the old config.json path (read once if YAML is missing).
func LegacyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// NeedsSetup reports whether interactive setup is required (no API key).
func NeedsSetup(cfg *Config) bool {
	return cfg == nil || strings.TrimSpace(cfg.APIKey) == ""
}

// Load builds a Config: defaults → config.yaml (or legacy JSON) → env.
// Env wins. An empty API key is allowed; callers use NeedsSetup.
func Load(workdir string) (*Config, error) {
	cfg := &Config{
		BaseURL:    DefaultBaseURL,
		Model:      DefaultModel,
		MaxTokens:  DefaultMaxTokens,
		SmallModel: DefaultSmallModel,
		Sidebar:    true,
		Workdir:    workdir,
		Router: RouterConfig{
			Enabled: false,
			Model:   DefaultRouterModel,
			Routes:  DefaultRoutes(),
		},
	}
	if err := applyFile(cfg); err != nil {
		return nil, err
	}
	if err := applyProjectFile(cfg); err != nil {
		return nil, err
	}
	if cfg.ActiveProvider != "" {
		if err := cfg.ApplyProvider(cfg.ActiveProvider); err != nil {
			return nil, err
		}
	}
	fileKey := cfg.APIKey
	applyEnv(cfg)
	if v := os.Getenv(EnvProvider); v != "" {
		if err := cfg.ApplyProvider(v); err != nil {
			return nil, err
		}
	}
	if v := os.Getenv(EnvAPIKey); v != "" {
		cfg.APIKey = v
	} else {
		cfg.APIKey = fileKey
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.Router.Model == "" {
		cfg.Router.Model = DefaultRouterModel
	}
	if len(cfg.Router.Routes) == 0 {
		cfg.Router.Routes = DefaultRoutes()
	}
	refreshBuiltinRouteDescriptions(cfg.Router.Routes)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv(EnvBaseURL); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv(EnvModel); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv(EnvSmallModel); v != "" {
		cfg.SmallModel = v
	}
	if v := os.Getenv(EnvPlanModel); v != "" {
		cfg.PlanModel = v
	}
	if v := os.Getenv(EnvRouterModel); v != "" {
		cfg.Router.Model = v
	}
	if v := os.Getenv(EnvRouter); v != "" {
		cfg.Router.Enabled = envTruthy(v)
	}
	if v := os.Getenv(EnvWebSearchURL); v != "" {
		cfg.WebSearchURL = v
	}
	if v := os.Getenv(EnvWebSearchKey); v != "" {
		cfg.WebSearchKey = v
	}
	if v := os.Getenv(EnvProvider); v != "" {
		cfg.ActiveProvider = v
	}
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		n, err := strconv.Atoi(v)
		return err == nil && n != 0
	}
}

func applyFile(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config: %w", err)
		}
		// Fall back to legacy config.json once.
		legacy, lerr := LegacyPath()
		if lerr != nil {
			return lerr
		}
		data, err = os.ReadFile(legacy)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("read legacy config: %w", err)
		}
		return applyFileData(cfg, data, true)
	}
	return applyFileData(cfg, data, false)
}

func applyFileData(cfg *Config, data []byte, legacyJSON bool) error {
	var f fileConfig
	var err error
	if legacyJSON {
		err = json.Unmarshal(data, &f)
	} else {
		err = yaml.Unmarshal(data, &f)
	}
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	mergeFile(cfg, &f)
	return nil
}

func mergeFile(cfg *Config, f *fileConfig) {
	if f.APIKey != "" {
		cfg.APIKey = f.APIKey
	}
	if f.BaseURL != "" {
		cfg.BaseURL = f.BaseURL
	}
	if f.Model != "" {
		cfg.Model = f.Model
	}
	if f.MaxTokens > 0 {
		cfg.MaxTokens = f.MaxTokens
	}
	if f.SmallModel != "" {
		cfg.SmallModel = f.SmallModel
	}
	if f.PlanModel != "" {
		cfg.PlanModel = f.PlanModel
	}
	if f.Sidebar != nil {
		cfg.Sidebar = *f.Sidebar
	}
	if f.MCPServers != nil {
		if cfg.MCPServers == nil {
			cfg.MCPServers = map[string]MCPServer{}
		}
		for name, srv := range f.MCPServers {
			cfg.MCPServers[name] = srv
		}
	}
	if f.Router != nil {
		if f.Router.Enabled != nil {
			cfg.Router.Enabled = *f.Router.Enabled
		}
		if f.Router.Model != "" {
			cfg.Router.Model = f.Router.Model
		}
		if len(f.Router.Routes) > 0 {
			if cfg.Router.Routes == nil {
				cfg.Router.Routes = map[string]RouteTarget{}
			}
			for k, v := range f.Router.Routes {
				cfg.Router.Routes[k] = v
			}
		}
	}
	if f.Permissions.Allow != nil {
		cfg.Permissions.Allow = mergeStringSlice(cfg.Permissions.Allow, f.Permissions.Allow)
	}
	if f.Permissions.Deny != nil {
		cfg.Permissions.Deny = mergeStringSlice(cfg.Permissions.Deny, f.Permissions.Deny)
	}
	if f.WebSearchURL != "" {
		cfg.WebSearchURL = f.WebSearchURL
	}
	if f.WebSearchKey != "" {
		cfg.WebSearchKey = f.WebSearchKey
	}
	if f.Providers != nil {
		if cfg.Providers == nil {
			cfg.Providers = map[string]Provider{}
		}
		for k, v := range f.Providers {
			cfg.Providers[k] = v
		}
	}
	if f.ActiveProvider != "" {
		cfg.ActiveProvider = f.ActiveProvider
	}
	if f.LSP != nil {
		if f.LSP.Command != "" {
			cfg.LSP.Command = f.LSP.Command
		}
		if f.LSP.Args != nil {
			cfg.LSP.Args = append([]string(nil), f.LSP.Args...)
		}
	}
}

func mergeStringSlice(base, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, s := range base {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range extra {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func applyProjectFile(cfg *Config) error {
	if cfg.Workdir == "" {
		return nil
	}
	path := filepath.Join(cfg.Workdir, ".quikagent", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read project config: %w", err)
	}
	return applyFileData(cfg, data, false)
}

// ApplyProvider copies a named provider onto BaseURL/APIKey/Model.
func (c *Config) ApplyProvider(name string) error {
	if name == "" {
		return nil
	}
	p, ok := c.Providers[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	c.ActiveProvider = name
	if p.BaseURL != "" {
		c.BaseURL = p.BaseURL
	}
	// Always replace the key so a provider that omits api_key cannot
	// inherit the previous provider's credentials.
	c.APIKey = p.APIKey
	if p.Model != "" {
		c.Model = p.Model
	}
	return nil
}

// Save writes the current config (including api_key) to ~/.quikagent/config.yaml.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	side := c.Sidebar
	f := fileConfig{
		APIKey:         c.APIKey,
		BaseURL:        c.BaseURL,
		Model:          c.Model,
		MaxTokens:      c.MaxTokens,
		SmallModel:     c.SmallModel,
		PlanModel:      c.PlanModel,
		Sidebar:        &side,
		MCPServers:     c.MCPServers,
		Permissions:    c.Permissions,
		WebSearchURL:   c.WebSearchURL,
		WebSearchKey:   c.WebSearchKey,
		Providers:      c.Providers,
		ActiveProvider: c.ActiveProvider,
		LSP:            &c.LSP,
		Router: &fileRouter{
			Enabled: &c.Router.Enabled,
			Model:   c.Router.Model,
			Routes:  c.Router.Routes,
		},
	}
	data, err := yaml.Marshal(&f)
	if err != nil {
		return err
	}

	// Write atomically using temp+rename (like session.save).
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("save config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("save config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	ok = true
	return nil
}

// KnownModels merges API model IDs with config defaults and route targets.
// Order: config model, small_model, plan_model, route models, then remaining API IDs (sorted).
func KnownModels(cfg *Config, apiIDs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	if cfg != nil {
		add(cfg.Model)
		add(cfg.SmallModel)
		add(cfg.PlanModel)
		if cfg.Router.Routes != nil {
			// Stable-ish: nano, qwen, other first if present, then rest.
			for _, name := range []string{"nano", "coder", "qwen", "other"} {
				if t, ok := cfg.Router.Routes[name]; ok {
					add(t.Model)
				}
			}
			for _, t := range cfg.Router.Routes {
				add(t.Model)
			}
		}
	}
	rest := append([]string(nil), apiIDs...)
	sort.Strings(rest)
	for _, id := range rest {
		add(id)
	}
	return out
}
