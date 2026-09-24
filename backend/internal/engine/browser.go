package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weisyn/wesapp/browser"
	"github.com/weisyn/wesgine/adapter/mcp"
)

const playwrightExtensionID = "mmlmfjhmonkocbjadbfplnigmagldckm"

func buildConnectURL(relayURL string) string {
	client, _ := json.Marshal(map[string]string{"name": "wesgine"})
	return fmt.Sprintf(
		"chrome-extension://%s/connect.html?mcpRelayUrl=%s&client=%s&protocolVersion=2",
		playwrightExtensionID,
		url.QueryEscape(relayURL),
		url.QueryEscape(string(client)),
	)
}

// BrowserStatus represents the connection status of the browser extension.
type BrowserStatus struct {
	ExtensionConnected bool   `json:"extensionConnected"`
	MCPReady           bool   `json:"mcpReady"`
	TokenInvalid       bool   `json:"tokenInvalid,omitempty"`
	Error              string `json:"error,omitempty"`
}

// BrowserConnectResult represents the result of a trigger-connect call.
type BrowserConnectResult struct {
	Triggered  bool   `json:"triggered"`
	ConnectURL string `json:"connectUrl,omitempty"`
	Error      string `json:"error,omitempty"`
}

// GetBrowserSettings returns the current browser automation settings as wesui View.
func (s *Service) GetBrowserSettings() browser.SettingsView {
	if s.cell == nil {
		return browser.ViewSettings(browser.Settings{})
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return browser.ViewSettings(browser.Settings{})
	}
	snap, err := mcpHandle.Get("playwright")
	if err != nil {
		return browser.ViewSettings(browser.Settings{Enabled: false})
	}
	enabled := snap.Config.Enabled == nil || *snap.Config.Enabled
	token := ""
	if snap.Config.Env != nil {
		token = snap.Config.Env["PLAYWRIGHT_MCP_EXTENSION_TOKEN"]
	}
	mode := ""
	if snap.Config.Env != nil {
		mode = snap.Config.Env["WESCODE_BROWSER_MODE"]
	}
	return browser.ViewSettings(browser.Settings{
		Enabled: enabled,
		Mode:    mode,
		Token:   token,
	})
}

// ProbeBrowserStatus checks if the browser extension is connected.
func (s *Service) ProbeBrowserStatus(ctx context.Context) BrowserStatus {
	if s.cell == nil {
		return BrowserStatus{Error: "engine not initialized"}
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return BrowserStatus{Error: "mcp not initialized"}
	}
	snap, err := mcpHandle.Get("playwright")
	if err != nil || !snap.Connected {
		return BrowserStatus{MCPReady: false, Error: "playwright mcp not ready"}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	result, toolErr := mcpHandle.CallTool(probeCtx, "playwright", "browser_tabs", json.RawMessage(`{"action":"list"}`))
	connected := toolErr == nil && result != nil && !result.IsError
	tokenInvalid := false

	if !connected && result != nil && result.IsError {
		msg := strings.ToLower(result.Content)
		if strings.Contains(msg, "no tab") || strings.Contains(msg, "no tabs") || strings.Contains(msg, "select a tab") {
			connected = true
		}
		if strings.Contains(msg, "invalid token") {
			tokenInvalid = true
		}
	}
	if toolErr != nil && !connected {
		if probeCtx.Err() != nil && snap.Config.Env["PLAYWRIGHT_MCP_EXTENSION_TOKEN"] != "" {
			tokenInvalid = true
		}
	}

	return BrowserStatus{
		ExtensionConnected: connected,
		MCPReady:           true,
		TokenInvalid:       tokenInvalid,
	}
}

// TriggerBrowserConnect returns the chrome-extension connect URL.
func (s *Service) TriggerBrowserConnect(ctx context.Context) BrowserConnectResult {
	if s.cell == nil {
		return BrowserConnectResult{Error: "engine not initialized"}
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return BrowserConnectResult{Error: "mcp not initialized"}
	}
	snap, err := mcpHandle.Get("playwright")
	if err != nil {
		return BrowserConnectResult{Error: "playwright mcp not registered"}
	}

	relayURL := snap.RelayURL
	if relayURL == "" {
		relayURL = s.warmupRelayURL()
	}
	if relayURL == "" {
		return BrowserConnectResult{Error: "relay URL not available — playwright MCP may still be initializing"}
	}

	return BrowserConnectResult{
		Triggered:  true,
		ConnectURL: buildConnectURL(relayURL),
	}
}

func (s *Service) warmupRelayURL() string {
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return ""
	}
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		_, _ = mcpHandle.CallTool(ctx, "playwright", "browser_tabs", json.RawMessage(`{"action":"list"}`))
		cancel()
		snap, err := mcpHandle.Get("playwright")
		if err == nil && snap.RelayURL != "" {
			return snap.RelayURL
		}
		time.Sleep(250 * time.Millisecond)
	}
	return ""
}

// SaveBrowserToken updates the Playwright MCP extension token.
// For non-empty tokens: only updates env in memory + persists to config,
// WITHOUT restarting the MCP process (avoids invalidating session tokens).
// For empty tokens (clear): does Upsert to restart without the token.
func (s *Service) SaveBrowserToken(ctx context.Context, token string) error {
	if s.cell == nil {
		return fmt.Errorf("engine not initialized")
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return fmt.Errorf("mcp not initialized")
	}
	snap, err := mcpHandle.Get("playwright")
	if err != nil {
		return fmt.Errorf("playwright mcp not registered")
	}
	cfg := snap.Config
	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	token = strings.TrimSpace(token)
	if strings.Contains(token, "=") {
		parts := strings.SplitN(token, "=", 2)
		token = strings.TrimSpace(parts[1])
	}
	if token == "" {
		delete(cfg.Env, "PLAYWRIGHT_MCP_EXTENSION_TOKEN")
	} else {
		cfg.Env["PLAYWRIGHT_MCP_EXTENSION_TOKEN"] = token
	}
	if uErr := mcpHandle.Upsert(ctx, cfg); uErr != nil {
		return uErr
	}
	return s.saveMCPConfig()
}

// SetBrowserMode switches between "user" and "sandbox" browser modes.
// In sandbox mode, a dedicated browser profile directory is used.
func (s *Service) SetBrowserMode(ctx context.Context, mode string) error {
	if s.cell == nil {
		return fmt.Errorf("engine not initialized")
	}
	if mode != "user" && mode != "sandbox" {
		return fmt.Errorf("invalid mode %q: must be \"user\" or \"sandbox\"", mode)
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return fmt.Errorf("mcp not initialized")
	}
	snap, err := mcpHandle.Get("playwright")
	if err != nil {
		return fmt.Errorf("playwright mcp not registered")
	}
	cfg := snap.Config
	if cfg.Env == nil {
		cfg.Env = make(map[string]string)
	}
	cfg.Env["WESCODE_BROWSER_MODE"] = mode
	if mode == "sandbox" {
		profileDir := filepath.Join(s.dataDir, "browser_profile")
		_ = os.MkdirAll(profileDir, 0o755)
		cfg.Env["PLAYWRIGHT_USER_DATA_DIR"] = profileDir
	} else {
		delete(cfg.Env, "PLAYWRIGHT_USER_DATA_DIR")
	}
	if uErr := mcpHandle.Upsert(ctx, cfg); uErr != nil {
		return uErr
	}
	return s.saveMCPConfig()
}

// ToggleBrowserEnabled enables or disables the Playwright MCP server.
// When enabling and no playwright MCP entry exists, auto-creates one
// (aligned with wesclaw's buildPlaywrightServerConfig).
func (s *Service) ToggleBrowserEnabled(ctx context.Context, enabled bool) error {
	if s.cell == nil {
		return fmt.Errorf("engine not initialized")
	}
	mcpHandle := s.cell.MCPs()
	if mcpHandle == nil {
		return fmt.Errorf("mcp not initialized")
	}

	snap, err := mcpHandle.Get("playwright")
	if err != nil && enabled {
		// Auto-inject playwright MCP entry when enabling for the first time
		cfg := s.buildPlaywrightMCPConfig(enabled)
		if uErr := mcpHandle.Upsert(ctx, cfg); uErr != nil {
			return uErr
		}
		return s.saveMCPConfig()
	}
	if err != nil {
		return fmt.Errorf("playwright mcp not registered")
	}

	cfg := snap.Config
	cfg.Enabled = &enabled
	if err := mcpHandle.Upsert(ctx, cfg); err != nil {
		return err
	}
	return s.saveMCPConfig()
}

// BrowserExtensionZipPath returns the path to the browser extension zip file,
// or empty string if it doesn't exist.
func (s *Service) BrowserExtensionZipPath() string {
	if s.dataDir == "" {
		return ""
	}
	p := filepath.Join(s.dataDir, "browser-extension.zip")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// buildPlaywrightMCPConfig creates the default playwright MCP server config.
func (s *Service) buildPlaywrightMCPConfig(enabled bool) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:    "playwright",
		Command: "npx",
		Args:    []string{"-y", "@playwright/mcp@latest", "--extension"},
		Enabled: &enabled,
	}
}
