package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wescode/internal/verification"
	"github.com/weisyn/wesgine/adapter/mcp"
	wesconfig "github.com/weisyn/wesgine/config"
	"github.com/weisyn/wesgine/xdg"
	"gopkg.in/yaml.v3"
)

// wescodeDirs is the XDG path resolver for wescode.
var wescodeDirs = xdg.NewAppDirs("wescode", "WESCODE")

const (
	defaultProviderName  = "deepseek"
	defaultProviderType  = string(wesconfig.ProviderOpenAICompat)
	defaultProviderURL   = "https://api.deepseek.com"
	defaultProviderModel = "deepseek-v4-flash"
)

type ProviderConfig struct {
	Name               string `yaml:"name"`
	Type               string `yaml:"type"`
	BaseURL            string `yaml:"base_url"`
	APIKey             string `yaml:"api_key"`
	Model              string `yaml:"model"`
	CompletionModel    string `yaml:"completion_model,omitempty"` // dedicated FIM model; empty = use Model
	IsDefault          bool   `yaml:"is_default,omitempty"`
	NoStreamUsage      bool   `yaml:"no_stream_usage,omitempty"`
	WatchdogFirstByte  string `yaml:"watchdog_first_byte,omitempty"`
	WatchdogInterChunk string `yaml:"watchdog_inter_chunk,omitempty"`
	ContextWindow      int    `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutput          int    `yaml:"max_output,omitempty" json:"max_output,omitempty"`
}

// Economic window bounds for RunSettings.MaxTokenEstimate (CE-15/CE-16).
//
// This is a wallet decision owned by wescode, not a model capability: it says
// how many input tokens we are willing to pay for per turn, independent of how
// many the model could physically hold. A 1M-window model does not get a 1M
// budget — it just stops being the binding constraint.
const (
	// DefaultEconomicWindow is the per-turn input budget when the user has not
	// set one. Matches the engine's own economic default so an unset knob and a
	// knob left at default behave identically.
	DefaultEconomicWindow = 128_000

	// MinEconomicWindow is the floor below which a persisted value is treated
	// as unset. The fixed per-turn overhead measured on the engine is 16.8K–21.5K
	// (system prompt ≈5K + tool schema 11.9K–16.6K; the 13K CompactBuffer is not
	// part of it — the engine subtracts that on the way from wallet to ceiling,
	// and it is a compression hysteresis gap, not an output reserve, INV-CTX-76).
	// Below a ~43.7K wallet the engine's 0.70 compression trigger falls under
	// that overhead, so the budget can never be met however hard compression
	// summarizes: every turn burns LLM calls on summaries that still overshoot.
	// The historical default of 8000 was exactly this trap.
	MinEconomicWindow = 64_000
)

// economicWindow resolves the per-turn input budget sent to the engine as
// AppRunRequest.MaxTokenEstimate (CE-15).
//
// configured is RunSettings.MaxTokenEstimate; model is the model this Run will
// actually use. The physical window is a ceiling only — it answers "does the
// history fit", never "is resending it worth paying for". Deriving the budget
// from ContextWindow (the pre-CE-15 behavior) meant a 1M-window model reported
// ~14% usage on a 148K transcript, so every budget-gated compression layer
// stayed idle and each turn resent the whole history at full price.
func economicWindow(configured int, model string) int {
	budget := configured
	if budget < MinEconomicWindow {
		// Unset, or persisted below the fixed per-turn overhead (CE-16):
		// either way the user has not made a usable wallet decision.
		budget = DefaultEconomicWindow
	}
	if model == "" {
		return budget
	}
	spec, ok := LookupModelSpec(model)
	if !ok || spec.ContextWindow <= 0 {
		// Unknown slug (WES "deepseek" vs catalog "deepseek-v4-flash") or an
		// unconfigured window: no ceiling to apply, the wallet cap stands.
		return budget
	}
	outputReserve := spec.MaxOutput
	if outputReserve <= 0 {
		outputReserve = 4096
	}
	if ceiling := spec.ContextWindow - outputReserve; ceiling > 0 && budget > ceiling {
		return ceiling
	}
	return budget
}

// RunSettings controls agent execution limits and tool access policy.
//
// TaskBudget unit: thousand tokens (k). It is forwarded to the engine as raw
// tokens (×1000) per Run: the engine injects an advisory "converge" note at
// 85%, and the app-side BudgetCheckFn (wired in run.go) hard-stops the Run
// with a wind-down grace turn at 100% — see INV-TERM-01/04.
//
// MaxTokenEstimate unit: raw input tokens. It is the economic window (CE-15) —
// forwarded verbatim to AppRunRequest.MaxTokenEstimate, clamped down only by
// the run model's physical window minus its output reserve.
type RunSettings struct {
	TaskBudget       int      `yaml:"task_budget,omitempty" json:"taskBudget"`
	MaxTokenEstimate int      `yaml:"max_token_estimate,omitempty" json:"maxTokenEstimate"`
	ToolDeny         []string `yaml:"tool_deny,omitempty" json:"toolDeny"`
	ThinkingLevel    string   `yaml:"thinking_level,omitempty" json:"thinkingLevel,omitempty"`
}

// DefaultTaskBudgetK is the per-Run output budget in thousand tokens when the
// user has not set one.
const DefaultTaskBudgetK = 200

// Resolve fills in defaults for the two numeric knobs and returns the settings
// that the Run will actually enforce.
//
// This is the single normalization point on purpose. The settings page and
// run.go used to normalize separately — the page defaulted TaskBudget to 200
// while run.go read the raw zero and skipped wiring BudgetCheckFn entirely.
// The page therefore displayed a budget that no Run enforced, and the divergence
// pointed the permissive way: no error, no log, just an unlimited Run. Every
// reader now goes through here, so the displayed number is the enforced number.
func (rs RunSettings) Resolve() RunSettings {
	if rs.TaskBudget <= 0 {
		rs.TaskBudget = DefaultTaskBudgetK
	}
	// Reuse the economic-window floor rather than restating it: passing an empty
	// model asks only "is this wallet usable", with no per-model ceiling.
	rs.MaxTokenEstimate = economicWindow(rs.MaxTokenEstimate, "")
	return rs
}

// RunSettingsPatch is a field-level update. A nil field means "not provided",
// which is what makes it different from RunSettings: the UI has three separate
// save entry points (tool toggle, thinking level, the save button) and each
// sends only what it owns. Taking a full RunSettings there made every partial
// save zero the fields it did not mention — flipping one tool cleared the
// budget, changing thinking depth cleared the entire tool denylist while the
// checkboxes still showed them denied.
type RunSettingsPatch struct {
	TaskBudget       *int      `json:"taskBudget,omitempty"`
	MaxTokenEstimate *int      `json:"maxTokenEstimate,omitempty"`
	ToolDeny         *[]string `json:"toolDeny,omitempty"`
	ThinkingLevel    *string   `json:"thinkingLevel,omitempty"`
}

// Apply merges the provided fields onto rs and returns the result.
func (rs RunSettings) Apply(p RunSettingsPatch) RunSettings {
	if p.TaskBudget != nil {
		rs.TaskBudget = *p.TaskBudget
	}
	if p.MaxTokenEstimate != nil {
		rs.MaxTokenEstimate = *p.MaxTokenEstimate
	}
	if p.ToolDeny != nil {
		rs.ToolDeny = *p.ToolDeny
	}
	if p.ThinkingLevel != nil {
		rs.ThinkingLevel = *p.ThinkingLevel
	}
	return rs
}

// MCPServerConfig mirrors the wesgine MCP config shape for persistence.
type MCPServerConfig struct {
	Name             string            `yaml:"name" json:"name"`
	Command          string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args             []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env              map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL              string            `yaml:"url,omitempty" json:"url,omitempty"`
	Transport        string            `yaml:"transport,omitempty" json:"transport,omitempty"`
	Headers          map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	ConnectTimeoutMs int               `yaml:"connect_timeout_ms,omitempty" json:"connectTimeoutMs,omitempty"`
	ToolTimeoutMs    int               `yaml:"tool_timeout_ms,omitempty" json:"toolTimeoutMs,omitempty"`
	Enabled          *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ToolsInclude     []string          `yaml:"tools_include,omitempty" json:"toolsInclude,omitempty"`
	ToolsExclude     []string          `yaml:"tools_exclude,omitempty" json:"toolsExclude,omitempty"`
	ContextVars      map[string]string `yaml:"context_vars,omitempty" json:"contextVars,omitempty"`
}

type AppConfig struct {
	Providers []ProviderConfig  `yaml:"providers"`
	Provider  ProviderConfig    `yaml:"provider,omitempty"`
	Run       RunSettings       `yaml:"run,omitempty"`
	MCP       []MCPServerConfig `yaml:"mcp,omitempty"`
	// AppIdentity is the user's *custom* prompt fragment, not the prompt the
	// engine receives. CellSpec.AppIdentity holds the merged result
	// (baseAppIdentity + platformPrompt + this), which is derived: its other
	// two operands are constants of the running binary. So the input lives
	// here and the output is recomputed and pushed on every boot
	// (resolveAppIdentity → reconcile after GetOrCreate). Storing the merged
	// string here instead would freeze the built-in constitution at whatever
	// version first wrote the file, and a wescode upgrade would never reach
	// existing workspaces.
	//
	// Settings that are *not* derived do not belong here at all — they live
	// in CellSpec, which wesgine persists. MemoryWritePolicy used to sit here
	// too and was a pure second copy: GetOrCreate keeps the persisted spec for
	// an existing Cell, so this field only ever reached first boot, and every
	// later edit was written to disk and silently ignored.
	AppIdentity string `yaml:"app_identity,omitempty"`
	// DesktopEnabled uses flat key "desktop_enabled" (wescode flat config model),
	// unlike wesclaw's nested "desktop.enabled". Intentional for wescode's single-level YAML.
	DesktopEnabled bool                        `yaml:"desktop_enabled,omitempty"`
	Channels       []wesconfig.ChannelInstance `yaml:"channels,omitempty"`
	// IMGateway is the canonical home for IM channel bindings and runtime
	// flags. The flat `channel_bindings` field below is read once on load
	// (for backward compatibility with pre-unification config files) and
	// then cleared; new writes only touch IMGateway.Bindings, keeping a
	// single source of truth across wesclaw / wescode / wescraft.
	IMGateway wesconfig.IMGatewayConfig `yaml:"im_gateway,omitempty"`
	// Deprecated: read once on Load and migrated into IMGateway.Bindings;
	// never persisted on Save. Will be removed in a future iteration once
	// every deployed config.yaml has been re-saved at least once.
	ChannelBindings []wesconfig.ChannelBinding `yaml:"channel_bindings,omitempty"`

	// Verification configures the progressive verification pipeline (L0-L3 + L2.5).
	// All fields have sensible defaults via verification.DefaultConfig(); only
	// non-default values need to appear in config.yaml.
	Verification verification.Config `yaml:"verification,omitempty"`

	// BenchMode enables headless (unattended) mode: auto-allows all tool
	// tiers without HITL popups. Set by CLI subcommands (run/bench);
	// not serialized to YAML. Does NOT disable QualityGate or EditPatrol.
	BenchMode bool `yaml:"-"`

	// DisableCKG skips CKG tool registration and context overlay injection.
	// Used by bench A/B testing to measure Token Efficiency (CKG on vs off).
	// Agent degrades to grep+read mode. Not serialized to YAML.
	DisableCKG bool `yaml:"-"`

	// SkillsOverrideDir, when non-empty, replaces the default skills directory.
	// Used by bench A/B testing to swap skill sets. Not serialized to YAML.
	SkillsOverrideDir string `yaml:"-"`

	DeveloperProfile DeveloperProfileConfig `yaml:"developer_profile,omitempty"`
}

// DeveloperProfileConfig controls the developer AI profile feature.
type DeveloperProfileConfig struct {
	// QualitySignalUpload enables uploading quality signals to the weisyn
	// platform after each Run. Requires a valid weisyn login.
	QualitySignalUpload bool `yaml:"quality_signal_upload,omitempty"`
}

func DefaultConfig() AppConfig {
	return AppConfig{
		Providers: nil,
	}
}

// LoadConfigFrom loads configuration from a specific file path.
func LoadConfigFrom(path string) (AppConfig, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("engine: read config %q: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("engine: parse config %q: %w", path, err)
	}
	if len(cfg.Providers) == 0 {
		if strings.TrimSpace(cfg.Provider.BaseURL) != "" || strings.TrimSpace(cfg.Provider.Model) != "" || strings.TrimSpace(cfg.Provider.APIKey) != "" || strings.TrimSpace(cfg.Provider.Type) != "" {
			cfg.Providers = []ProviderConfig{cfg.Provider}
		}
	}
	overrideFromEnv(&cfg)
	cfg.Providers = normalizeProviderList(cfg.Providers)
	cfg.Provider = ProviderConfig{}

	// One-shot migration: lift the deprecated flat `channel_bindings` field
	// into the canonical IMGateway.Bindings store, then clear the flat slice
	// so saveConfig() never writes it back. This is the wescode side of the
	// "bindings single source of truth" rule (im_channel_unification plan).
	if len(cfg.ChannelBindings) > 0 && len(cfg.IMGateway.Bindings) == 0 {
		cfg.IMGateway.Bindings = append([]wesconfig.ChannelBinding(nil), cfg.ChannelBindings...)
	}
	cfg.ChannelBindings = nil

	return cfg, nil
}

func LoadConfig() (AppConfig, error) {
	cfg := DefaultConfig()
	path := defaultConfigPath()
	if v := strings.TrimSpace(os.Getenv("WESCODE_CONFIG")); v != "" {
		path = v
	}

	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("engine: parse config %q: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("engine: read config %q: %w", path, err)
	}

	// Backward compatibility: convert legacy single-provider config.
	if len(cfg.Providers) == 0 {
		if strings.TrimSpace(cfg.Provider.BaseURL) != "" || strings.TrimSpace(cfg.Provider.Model) != "" || strings.TrimSpace(cfg.Provider.APIKey) != "" || strings.TrimSpace(cfg.Provider.Type) != "" {
			cfg.Providers = []ProviderConfig{cfg.Provider}
		}
	}
	overrideFromEnv(&cfg)
	cfg.Providers = normalizeProviderList(cfg.Providers)
	cfg.Provider = ProviderConfig{}
	return cfg, nil
}

func defaultConfigPath() string {
	if v := os.Getenv("WESCODE_CONFIG"); v != "" {
		return v
	}
	return wescodeDirs.ConfigFile()
}

// DefaultConfigPath returns the resolved wescode config file path
// (WESCODE_CONFIG env override, else the XDG/OS-native config dir).
// Exported so boot-time layout migration can resolve the canonical
// config target with the same override semantics as LoadConfig.
func DefaultConfigPath() string {
	return defaultConfigPath()
}

func defaultDataDir() string {
	if v := os.Getenv("WESCODE_DATA_DIR"); v != "" {
		return v
	}
	return wescodeDirs.DataDir()
}

func overrideFromEnv(cfg *AppConfig) {
	if len(cfg.Providers) == 0 {
		return
	}
	target := &cfg.Providers[0]
	envFill := func(envKey string, field *string) {
		v := strings.TrimSpace(os.Getenv(envKey))
		if v == "" || *field != "" {
			return
		}
		slog.Info("[engine] env fill", "key", envKey, "provider", target.Name)
		*field = v
	}
	envFill("WESCODE_PROVIDER_TYPE", &target.Type)
	envFill("WESCODE_PROVIDER_BASE_URL", &target.BaseURL)
	envFill("WESCODE_PROVIDER_API_KEY", &target.APIKey)
	envFill("DEEPSEEK_API_KEY", &target.APIKey)
	envFill("WESCODE_PROVIDER_MODEL", &target.Model)
}

func normalizeProviderList(in []ProviderConfig) []ProviderConfig {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProviderConfig, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for i, p := range in {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			p.Name = fmt.Sprintf("provider-%d", i+1)
		}
		if _, ok := seen[p.Name]; ok {
			p.Name = fmt.Sprintf("%s-%d", p.Name, i+1)
		}
		seen[p.Name] = struct{}{}
		p.Type = strings.TrimSpace(p.Type)
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Model = strings.TrimSpace(p.Model)
		p.APIKey = strings.TrimSpace(p.APIKey)
		if p.Type == "" {
			p.Type = defaultProviderType
		}
		if p.BaseURL == "" {
			p.BaseURL = defaultProviderURL
		}
		if p.Model == "" {
			p.Model = defaultProviderModel
		}
		out = append(out, p)
	}
	defaultIdx := -1
	for i, p := range out {
		if p.IsDefault {
			defaultIdx = i
			break
		}
	}
	if defaultIdx <= 0 {
		defaultIdx = 0
	}
	def := out[defaultIdx]
	out = append(out[:defaultIdx], out[defaultIdx+1:]...)
	out = append([]ProviderConfig{def}, out...)
	for i := range out {
		out[i].IsDefault = i == 0
	}
	return out
}

func toWesProviderConfigs(in []ProviderConfig) []wesconfig.ProviderConfig {
	out := make([]wesconfig.ProviderConfig, 0, len(in))
	for _, p := range in {
		var wdCfg wesconfig.WatchdogCfg
		if p.WatchdogFirstByte != "" {
			if d, err := time.ParseDuration(p.WatchdogFirstByte); err == nil {
				wdCfg.FirstByteTimeout = d
			}
		}
		if p.WatchdogInterChunk != "" {
			if d, err := time.ParseDuration(p.WatchdogInterChunk); err == nil {
				wdCfg.InterChunkTimeout = d
			}
		}
		out = append(out, wesconfig.ProviderConfig{
			Name:             p.Name,
			Type:             wesconfig.ProviderType(p.Type),
			BaseURL:          p.BaseURL,
			APIKeyRef:        wesconfig.SecretRef{Source: "inline", Value: p.APIKey},
			Model:            p.Model,
			Models:           []wesconfig.ModelConfig{{Name: p.Model}},
			NoStreamUsage:    p.NoStreamUsage,
			WatchdogOverride: wdCfg,
		})
	}
	return out
}

// wesMCPServerConfig projects the wescode-persisted MCPServerConfig into
// adapter/mcp.ServerConfig for cell.MCPs().Upsert (v1.0 API).
func wesMCPServerConfig(c MCPServerConfig) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:             c.Name,
		Command:          c.Command,
		Args:             c.Args,
		Env:              c.Env,
		URL:              c.URL,
		Transport:        c.Transport,
		Headers:          c.Headers,
		ConnectTimeoutMs: c.ConnectTimeoutMs,
		ToolTimeoutMs:    c.ToolTimeoutMs,
		Enabled:          c.Enabled,
		ToolsInclude:     c.ToolsInclude,
		ToolsExclude:     c.ToolsExclude,
		ContextVars:      c.ContextVars,
	}
}

func mcpSnapshotToConfig(snap mcp.ServerConfig) MCPServerConfig {
	return MCPServerConfig{
		Name:             snap.Name,
		Command:          snap.Command,
		Args:             snap.Args,
		Env:              snap.Env,
		URL:              snap.URL,
		Transport:        snap.Transport,
		Headers:          snap.Headers,
		ConnectTimeoutMs: snap.ConnectTimeoutMs,
		ToolTimeoutMs:    snap.ToolTimeoutMs,
		Enabled:          snap.Enabled,
		ToolsInclude:     snap.ToolsInclude,
		ToolsExclude:     snap.ToolsExclude,
		ContextVars:      snap.ContextVars,
	}
}

func saveConfig(cfg AppConfig) error {
	path := defaultConfigPath()
	if v := strings.TrimSpace(os.Getenv("WESCODE_CONFIG")); v != "" {
		path = v
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
