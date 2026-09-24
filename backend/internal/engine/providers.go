package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/weisyn/wesapp/models"
	"github.com/weisyn/wesapp/provider"
	"github.com/weisyn/wesgine"
	wesconfig "github.com/weisyn/wesgine/config"
)

// modelForProvider looks up the model name for a given provider name.
func (s *Service) modelForProvider(providerName string) string {
	if s.providers != nil {
		for _, v := range s.providers.List() {
			if strings.EqualFold(v.ProviderConfig.Name, providerName) {
				return v.ProviderConfig.Model
			}
		}
		return ""
	}
	for _, p := range s.cfg.Providers {
		if strings.EqualFold(p.Name, providerName) {
			return p.Model
		}
	}
	return ""
}

// ModelForProvider returns the model name for the given provider (exported).
func (s *Service) ModelForProvider(providerName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelForProvider(providerName)
}

// parseBackendIndex extracts the numeric index from "backend-N" format.
// Returns -1 when the format doesn't match.
func parseBackendIndex(id string) int {
	if !strings.HasPrefix(id, "backend-") {
		return -1
	}
	s := strings.TrimPrefix(id, "backend-")
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return -1
	}
	return n
}

// ProviderWithHealth extends ProviderConfig with last-known health status.
//
// ProbeClass is the closed wesgine domain naming *why* the last connectivity
// probe failed. It was a string field named TestError, which read like prose
// and was rendered as prose by the settings page — a user whose base URL
// pointed at an HTML page saw the literal token `invalid_response`. Only the
// class is persisted in health (TestResult.Message is not), so the copy
// belongs to the consumer, and the consumer can only own it if what arrives
// is a class it can switch on.
type ProviderWithHealth struct {
	ProviderConfig
	Status     string             `json:"status"`
	ProbeClass wesgine.ProbeClass `json:"probeClass,omitempty"`
}

// ListProviders returns all configured providers with masked API keys.
// Cell Mode: reads from CellClient (hypervisor.db per-Cell PrivateProviders).
// Config Mode: reads from s.cfg.Providers (config.yaml in-memory).
func (s *Service) ListProviders() []ProviderConfig {
	if s.providers == nil {
		s.mu.Lock()
		items := make([]ProviderConfig, len(s.cfg.Providers))
		copy(items, s.cfg.Providers)
		s.mu.Unlock()
		for i := range items {
			items[i].APIKey = maskAPIKey(items[i].APIKey)
		}
		return items
	}
	views := s.providers.List()
	items := make([]ProviderConfig, 0, len(views))
	for _, v := range views {
		items = append(items, ProviderConfig{
			Name:          v.ProviderConfig.Name,
			Type:          string(v.ProviderConfig.Type),
			BaseURL:       v.ProviderConfig.BaseURL,
			APIKey:        v.APIKey,
			Model:         v.ProviderConfig.Model,
			IsDefault:     v.IsDefault,
			NoStreamUsage: v.ProviderConfig.NoStreamUsage,
		})
	}
	return items
}

// ListProvidersWithHealth returns providers enriched with health status.
func (s *Service) ListProvidersWithHealth(ctx context.Context) []ProviderWithHealth {
	if s.providers == nil {
		items := s.ListProviders()
		result := make([]ProviderWithHealth, 0, len(items))
		for _, p := range items {
			result = append(result, ProviderWithHealth{ProviderConfig: p, Status: "unknown"})
		}
		return result
	}
	views := s.providers.ListWithHealth(ctx)
	items := make([]ProviderWithHealth, 0, len(views))
	for _, v := range views {
		status := "unknown"
		var probeClass wesgine.ProbeClass
		if v.Health != nil {
			status = v.Health.Status
			probeClass = v.Health.Class
		}
		items = append(items, ProviderWithHealth{
			ProviderConfig: ProviderConfig{
				Name:          v.ProviderConfig.Name,
				Type:          string(v.ProviderConfig.Type),
				BaseURL:       v.ProviderConfig.BaseURL,
				APIKey:        v.APIKey,
				Model:         v.ProviderConfig.Model,
				IsDefault:     v.IsDefault,
				NoStreamUsage: v.ProviderConfig.NoStreamUsage,
			},
			Status:     status,
			ProbeClass: probeClass,
		})
	}
	return items
}

func maskAPIKey(key string) string {
	return provider.MaskAPIKey(strings.TrimSpace(key))
}

// AvailableModel is the unified picker row (INV-PROVIDER-VIEW-01).
// Alias of models.Option so RPC JSON stays identical after R3.
type AvailableModel = models.Option

// AvailableModelsResult is the engine-level API boundary for the merged
// model list. An org-catalog failure is a structured signal, NOT a sentinel
// model entry — consumers must never see org:__catalog_error__ as a model.
type AvailableModelsResult struct {
	Models          []AvailableModel
	OrgCatalogError string // non-empty when the org catalog fetch failed
}

// ListAvailableModelsResult returns the merged model list with the
// org-catalog error signal. Single data source for both the settings page
// and the model picker (INV-PROVIDER-VIEW-01).
func (s *Service) ListAvailableModelsResult(ctx context.Context) AvailableModelsResult {
	// Product fetches the three lists; models.Merge only stamps source / wire id / status.
	byokViews := s.ListProvidersWithHealth(ctx)
	byok := make([]models.BYOK, 0, len(byokViews))
	for _, p := range byokViews {
		byok = append(byok, models.BYOK{
			Name:        p.Name,
			Label:       p.Name,
			Model:       p.Model,
			APIKey:      p.APIKey,
			ProbeStatus: p.Status,
			ProbeClass:  p.ProbeClass,
			IsDefault:   p.IsDefault,
		})
	}

	var wesIn []models.WES
	if wes, err := s.ListWesProviders(ctx); err == nil {
		wesIn = make([]models.WES, 0, len(wes))
		for _, w := range wes {
			label := w.DisplayName
			if label == "" {
				label = w.Name
			}
			wesIn = append(wesIn, models.WES{
				ID:          w.ID,
				Name:        w.Name,
				Label:       label,
				Model:       w.Model,
				IsDefault:   w.IsDefault,
				InputPrice:  w.InputPrice,
				OutputPrice: w.OutputPrice,
			})
		}
	}

	orgs, err := s.ListOrgModels(ctx)
	if err != nil {
		slog.Warn("[engine] list org models failed", "error", err)
	}
	orgIn := make([]models.Org, 0, len(orgs))
	for _, o := range orgs {
		label := o.OrgName
		if label == "" {
			label = o.DisplayName
		}
		orgIn = append(orgIn, models.Org{
			ID:          o.ID,
			Label:       label,
			Model:       o.Model,
			Blocked:     o.Blocked,
			IsDefault:   o.IsDefault,
			InputPrice:  o.InputPrice,
			OutputPrice: o.OutputPrice,
			OrgExpiry:   o.OrgExpiry,
			OrgStatus:   o.OrgStatus,
			Last4:       o.Last4,
		})
	}

	merged := models.Merge(byok, wesIn, orgIn, err)
	return AvailableModelsResult{Models: merged.Options, OrgCatalogError: merged.OrgCatalogError}
}

// RefreshAvailableModels clears the upstream WES/org model directory cache
// and returns a freshly fetched merged list. The settings-page "refresh"
// button calls this instead of ListAvailableModelsResult to bypass the 60s
// cache TTL.
func (s *Service) RefreshAvailableModels(ctx context.Context) AvailableModelsResult {
	if s.wesCatalog != nil {
		s.wesCatalog.InvalidateModelsCache()
	}
	return s.ListAvailableModelsResult(ctx)
}

// ListAvailableModels returns only the model entries (no catalog error
// signal). Internal consumers (DefaultModel) that only need selectable
// models keep using this; the RPC layer must use ListAvailableModelsResult.
func (s *Service) ListAvailableModels(ctx context.Context) []AvailableModel {
	return s.ListAvailableModelsResult(ctx).Models
}

// ResolveModelChoice turns a picker id into the (model, providerName) pair a
// scheduled Run needs. Assembling the picker is this product's job — it takes
// catalog state only wescode holds — but the mapping itself is shared, so the
// three other products land on the same pair for the same row.
//
// Reports ok=false for an id that is not in the current picker — a provider the
// user deleted, or a stale form. The caller refuses the write rather than
// substituting something: a task silently pinned to a model nobody chose is the
// failure this whole path exists to prevent.
func (s *Service) ResolveModelChoice(ctx context.Context, pickerID string) (model, providerName string, ok bool) {
	return models.Resolve(s.ListAvailableModels(ctx), pickerID)
}

// DefaultModel returns the model name of the first available provider.
// Used by RunChat when the user hasn't explicitly selected a model.
func (s *Service) DefaultModel(ctx context.Context) (providerID, model string) {
	models := s.ListAvailableModels(ctx)
	for _, m := range models {
		if m.Status == "available" && m.Model != "" {
			return m.ProviderID, m.Model
		}
	}
	return "", ""
}

// AddProvider appends a provider and persists/reloads runtime config.
func (s *Service) AddProvider(ctx context.Context, p ProviderConfig) (ProviderConfig, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return ProviderConfig{}, fmt.Errorf("provider name is required")
	}
	if p.APIKey == "" {
		return ProviderConfig{}, fmt.Errorf("provider api_key is required")
	}

	if s.providers != nil {
		// Cell Mode: persist to Cell PrivateProviders via CellClient.
		// Conflict check uses CellClient, not s.cfg.Providers.
		for _, v := range s.providers.List() {
			if strings.EqualFold(v.ProviderConfig.Name, p.Name) {
				return ProviderConfig{}, fmt.Errorf("provider_conflict:%s:%s", v.ProviderConfig.Name, p.Name)
			}
		}
		cfg := toSingleWesProviderConfig(p)
		if err := s.providers.Add(ctx, cfg); err != nil {
			return ProviderConfig{}, err
		}
		if p.IsDefault {
			if err := s.providers.SetDefault(ctx, p.Name); err != nil {
				slog.Warn("[provider] SetDefault failed after Add", "name", p.Name, "error", err)
			}
		}
		result := p
		result.APIKey = maskAPIKey(result.APIKey)
		views := s.providers.List()
		for i, v := range views {
			if strings.EqualFold(v.ProviderConfig.Name, p.Name) {
				result.IsDefault = i == 0
				break
			}
		}
		return result, nil
	}

	// Config Mode: persist to config.yaml.
	s.mu.Lock()
	for _, item := range s.cfg.Providers {
		if strings.EqualFold(item.Name, p.Name) {
			s.mu.Unlock()
			return ProviderConfig{}, fmt.Errorf("provider_conflict:%s:%s", item.Name, p.Name)
		}
	}
	s.cfg.Providers = append(s.cfg.Providers, p)
	s.mu.Unlock()
	if err := saveConfig(s.cfg); err != nil {
		slog.Warn("[provider] saveConfig failed after Add", "name", p.Name, "error", err)
	}

	result := p
	result.APIKey = maskAPIKey(result.APIKey)
	s.mu.Lock()
	for i, item := range s.cfg.Providers {
		if strings.EqualFold(item.Name, p.Name) {
			result.IsDefault = i == 0
			break
		}
	}
	s.mu.Unlock()
	return result, nil
}

// UpdateProvider updates provider by name and keeps API key when input is empty.
func (s *Service) UpdateProvider(ctx context.Context, name string, p ProviderConfig) error {
	if s.providers != nil {
		cfg := toSingleWesProviderConfig(p)
		if err := s.providers.Update(ctx, name, cfg); err != nil {
			return err
		}
		if p.IsDefault {
			if err := s.providers.SetDefault(ctx, p.Name); err != nil {
				slog.Warn("[provider] SetDefault failed after Update", "name", p.Name, "error", err)
			}
		}
		return nil
	}

	s.mu.Lock()
	idx := indexProviderByName(s.cfg.Providers, name)
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("provider %q not found", name)
	}
	if p.APIKey == "" {
		p.APIKey = s.cfg.Providers[idx].APIKey
	}
	s.cfg.Providers[idx] = p
	s.mu.Unlock()
	if err := saveConfig(s.cfg); err != nil {
		slog.Warn("[provider] saveConfig failed after Update", "name", p.Name, "error", err)
	}
	return nil
}

// DeleteProvider removes a provider and keeps at least one entry.
func (s *Service) DeleteProvider(ctx context.Context, name string) error {
	if s.providers != nil {
		return s.providers.Delete(ctx, name)
	}

	s.mu.Lock()
	idx := indexProviderByName(s.cfg.Providers, name)
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("provider %q not found", name)
	}
	s.cfg.Providers = append(s.cfg.Providers[:idx], s.cfg.Providers[idx+1:]...)
	s.mu.Unlock()
	if err := saveConfig(s.cfg); err != nil {
		slog.Warn("[provider] saveConfig failed after Delete", "name", name, "error", err)
	}
	return nil
}

func indexProviderByName(items []ProviderConfig, name string) int {
	for i, item := range items {
		if strings.EqualFold(item.Name, name) {
			return i
		}
	}
	return -1
}

// SetDefaultProvider sets the given provider as default (index 0).
func (s *Service) SetDefaultProvider(ctx context.Context, name string) error {
	if s.providers != nil {
		return s.providers.SetDefault(ctx, name)
	}

	s.mu.Lock()
	idx := indexProviderByName(s.cfg.Providers, name)
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("provider %q not found", name)
	}
	if idx != 0 {
		item := s.cfg.Providers[idx]
		s.cfg.Providers = append(s.cfg.Providers[:idx], s.cfg.Providers[idx+1:]...)
		s.cfg.Providers = append([]ProviderConfig{item}, s.cfg.Providers...)
	}
	s.mu.Unlock()
	if err := saveConfig(s.cfg); err != nil {
		slog.Warn("[provider] saveConfig failed after SetDefault", "name", name, "error", err)
	}
	return nil
}

// toSingleWesProviderConfig converts a wescode ProviderConfig to wesconfig.ProviderConfig.
func toSingleWesProviderConfig(p ProviderConfig) wesconfig.ProviderConfig {
	cfg := wesconfig.ProviderConfig{
		Name:          p.Name,
		Type:          wesconfig.ProviderType(p.Type),
		BaseURL:       p.BaseURL,
		APIKeyRef:     wesconfig.SecretRef{Source: "inline", Value: p.APIKey},
		Model:         p.Model,
		NoStreamUsage: p.NoStreamUsage,
	}
	if p.Model != "" {
		cfg.Models = []wesconfig.ModelConfig{{
			Name:            p.Model,
			ContextWindow:   p.ContextWindow,
			MaxOutputTokens: p.MaxOutput,
		}}
	}
	provider.NormalizeProvider(&cfg)
	for i := range cfg.Models {
		if cfg.Models[i].ContextWindow == 0 {
			cfg.Models[i].ContextWindow = 128_000
		}
	}
	return cfg
}

// WesProviderInfo is the frontend-facing WES provider descriptor.
type WesProviderInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	Model       string  `json:"model"`
	Type        string  `json:"type"`
	IsDefault   bool    `json:"is_default"`
	InputPrice  float64 `json:"input_price"`
	OutputPrice float64 `json:"output_price"`
}

// ProbeOutcome is the verdict of one connectivity probe.
//
// Class is the machine-readable reason and is the only thing a consumer may
// branch on; Message is a diagnostic detail (upstream body excerpt, transport
// error) that may be empty and is never a substitute for the class. This used
// to be `(ok bool, latencyMs int64, errMsg string)`, which forced every caller
// to re-derive the class by substring-matching the message — the settings page
// classified "403 Forbidden" as auth_failed and any Chinese-language upstream
// error as unreachable. The class is decided once, at the probe.
type ProbeOutcome struct {
	OK        bool
	LatencyMs int64
	Class     wesgine.ProbeClass // zero (ProbeClassOK) iff OK
	Message   string             // diagnostic detail; may be empty
}

// TestProvider tests a provider's connectivity. Cell Mode delegates to
// CellClient (persists health state). Config Mode / Booting uses a direct
// HTTP probe — provider verification is a stateless operation that has no
// essential dependency on Cell.
func (s *Service) TestProvider(ctx context.Context, p ProviderConfig) ProbeOutcome {
	if s.providers != nil {
		cfg := toSingleWesProviderConfig(p)
		start := time.Now()
		result := s.providers.Test(ctx, cfg)
		return ProbeOutcome{
			OK:        result.OK,
			LatencyMs: time.Since(start).Milliseconds(),
			Class:     result.Class,
			Message:   result.Message,
		}
	}
	return testProviderDirect(ctx, p)
}

// testProviderDirect sends a minimal chat completion request to verify
// connectivity without any Cell dependency. Mirrors the logic of
// wesgine ProviderHandle.Test (handle_providers.go:120-189).
func testProviderDirect(ctx context.Context, p ProviderConfig) ProbeOutcome {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	endpoint := baseURL + "/v1/chat/completions"

	body, _ := json.Marshal(map[string]any{
		"model":      p.Model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})

	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(testCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		// A BaseURL that will not compose into a request is a settings
		// problem, not a network one — the user must edit the field, and
		// telling them the provider is "unreachable" sends them to check
		// their network instead.
		return ProbeOutcome{Class: wesgine.ProbeClassInvalidURL, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		if p.Type == "anthropic" {
			req.Header.Set("x-api-key", p.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		return ProbeOutcome{LatencyMs: latencyMs, Class: wesgine.ProbeClassUnreachable, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ProbeOutcome{OK: true, LatencyMs: latencyMs}
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	class, msg := wesgine.ClassifyProbeHTTP(resp.StatusCode, respBody)
	if class == wesgine.ProbeClassOK {
		// ClassifyProbeHTTP only returns OK for a 2xx, which this branch
		// already excluded; keep the guard so a future classifier change
		// cannot report a failed probe as a success.
		class = wesgine.ProbeClassUnreachable
	}
	return ProbeOutcome{LatencyMs: latencyMs, Class: class, Message: msg}
}

// TestProviderByName tests an already saved provider by its name.
// Cell Mode: delegates to CellClient.TestByNameAndPersist (key resolved internally).
// Config Mode: reads from s.cfg.Providers.
//
// A non-nil error means no probe ran (the name is not a configured provider);
// callers map it to their transport's "bad request", never to a probe verdict.
func (s *Service) TestProviderByName(ctx context.Context, name string) (ProbeOutcome, error) {
	if s.providers != nil {
		start := time.Now()
		result, err := s.providers.TestByNameAndPersist(ctx, name)
		if err != nil {
			return ProbeOutcome{}, err
		}
		return ProbeOutcome{
			OK:        result.OK,
			LatencyMs: time.Since(start).Milliseconds(),
			Class:     result.Class,
			Message:   result.Message,
		}, nil
	}

	s.mu.Lock()
	items := append([]ProviderConfig(nil), s.cfg.Providers...)
	s.mu.Unlock()
	idx := indexProviderByName(items, name)
	if idx < 0 {
		return ProbeOutcome{}, fmt.Errorf("%w: %q", provider.ErrProviderNotFound, name)
	}
	return s.TestProvider(ctx, items[idx]), nil
}

// probeAllProviders tests all BYOK providers and persists health status.
// Called once at boot to populate initial state.
func (s *Service) probeAllProviders() {
	if s.providers == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.providers.ProbeAll(ctx)
}
