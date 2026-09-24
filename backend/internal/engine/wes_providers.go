package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/weisyn/weisyn/sdk/wes"
	"github.com/weisyn/wesapp/provider"
	"github.com/weisyn/wesgine"
	wesconfig "github.com/weisyn/wesgine/config"
)

// ErrWesUnavailable reports that this build has no WES catalog wired in, so a
// wes: slug cannot be probed at all. It is not a probe verdict — nothing was
// contacted — and callers map it to their transport's "bad request" rather
// than showing the user a connectivity failure.
var ErrWesUnavailable = errors.New("engine: wes providers not configured")

// BootstrapWesProvider attempts to load a WES provider and reconfigure the engine.
// Uses a 10s timeout to prevent blocking engine initialization when the
// weisyn platform is unreachable.
func (s *Service) BootstrapWesProvider(ctx context.Context) {
	if s.wesCatalog == nil || s.cell == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := wes.SyncWESProviders(syncCtx, s.hyp, s.cell, s.wesCatalog, slog.Default()); err != nil {
		slog.Warn("[engine] wes provider bootstrap failed", "error", err)
	}
}

// EnsureWesTokenFresh re-syncs WES providers if the auth token has changed.
// Catalog.wesFn (ValidAccessToken) is the wes: credential and stays empty when
// personal billing is overdue. Identity JWT (tokenFn) still lists org:.
// SyncWESProviders then compares tokens against the CellSpec snapshot and
// hot-updates only when they differ.
// Typical cost: <1ms (SQLite read + in-memory comparison); no-op when token
// is still valid.
func (s *Service) EnsureWesTokenFresh(ctx context.Context) bool {
	if s.wesCatalog == nil || s.cell == nil {
		return false
	}
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := wes.SyncWESProviders(syncCtx, s.hyp, s.cell, s.wesCatalog, slog.Default()); err != nil {
		slog.Warn("[engine] wes token refresh sync failed", "error", err)
		return false
	}
	return s.wesCatalog.BearerToken(syncCtx) != ""
}

// startWesTokenSyncLoop runs a periodic background sync to keep the WES
// provider JWT fresh. The 10-minute interval is well within the 15-minute
// JWT TTL, so the token is refreshed before it expires even if no chat
// requests are made during idle periods.
func (s *Service) startWesTokenSyncLoop(ctx context.Context) {
	if s.wesCatalog == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.EnsureWesTokenFresh(context.Background())
			}
		}
	}()
}

// ListWesProviders returns platform-hosted LLM models from weisyn.
func (s *Service) ListWesProviders(ctx context.Context) ([]WesProviderInfo, error) {
	if s.wesCatalog == nil {
		return nil, nil
	}
	providers, err := s.wesCatalog.List(ctx)
	if err != nil && len(providers) == 0 {
		return nil, err
	}
	out := make([]WesProviderInfo, 0, len(providers))
	for _, p := range providers {
		out = append(out, WesProviderInfo{
			ID:          p.ID,
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Model:       p.Model,
			Type:        p.Type,
			IsDefault:   p.IsDefault,
			InputPrice:  p.InputPrice,
			OutputPrice: p.OutputPrice,
		})
	}
	return out, nil
}

// TestWesProvider sends a test probe to a WES provider via weisyn proxy.
//
// A non-nil error means no probe ran: WES is not wired into this build, or the
// slug does not name a WES provider. Neither is a connectivity verdict, and
// both used to be returned as a failed probe whose "class" was carved out of
// the message text by the RPC layer.
func (s *Service) TestWesProvider(ctx context.Context, name string) (ProbeOutcome, error) {
	if s.wesCatalog == nil {
		return ProbeOutcome{}, ErrWesUnavailable
	}
	// WES capability is an account-level property, not a function of how much
	// JWT lifetime remains on this machine. When the token is unavailable,
	// classify the reason explicitly so the UI never reports a fake network
	// error for a billing/session problem (INV-BILLING-02).
	//
	// Both states below are genuine probe verdicts — the provider exists and
	// we know why it cannot be reached — so they carry a ProbeClass rather
	// than an error. Note billing here is the *probe* answer; the billing
	// snapshot (`active=false`) is an independent signal and owns the banner.
	token, state := s.WESAuthState(ctx)
	if token == "" {
		if state == WESStateBillingOverdue {
			return ProbeOutcome{Class: wesgine.ProbeClassBalanceDepleted}, nil
		}
		return ProbeOutcome{Class: wesgine.ProbeClassLiveKeyUnavailable}, nil
	}
	cfg, found := s.wesCatalog.ProviderConfig(ctx, name)
	if !found {
		return ProbeOutcome{}, fmt.Errorf("%w: wes provider %q", provider.ErrProviderNotFound, name)
	}
	p := ProviderConfig{
		Name:    cfg.Name,
		Type:    string(cfg.Type),
		BaseURL: cfg.BaseURL,
		// Live credential: the probe must authenticate with the CURRENT
		// WES grant token (sliding renewal inside the auth service), never
		// a frozen snapshot. cfg.APIKeyRef is source="live" — its plaintext
		// is fetched per request via CellSpec.ProviderLiveKeyFn, so the
		// token obtained above is the single source of truth for probes.
		APIKey: token,
		Model:  cfg.Model,
	}
	return s.TestProvider(ctx, p), nil
}

// ModelForWesProvider returns the model name for a WES provider slug.
func (s *Service) ModelForWesProvider(ctx context.Context, name string) string {
	if s.wesCatalog == nil {
		return ""
	}
	providers, _ := s.wesCatalog.List(ctx)
	for _, p := range providers {
		if p.Name == name {
			return p.Model
		}
	}
	return ""
}

// wesConfigForRun builds a ProviderConfig for a WES model used during Run.
func (s *Service) wesConfigForRun(ctx context.Context, modelName string) (wesconfig.ProviderConfig, bool) {
	if s.wesCatalog == nil {
		return wesconfig.ProviderConfig{}, false
	}
	return s.wesCatalog.ProviderConfig(ctx, modelName)
}

// ListOrgModels returns org-provided models (company Key routed via the
// weisyn proxy; keys never land on this machine — enterprise-org.md §6.1).
func (s *Service) ListOrgModels(ctx context.Context) ([]wes.OrgModelInfo, error) {
	if s.wesCatalog == nil {
		return nil, nil
	}
	return s.wesCatalog.ListOrgModels(ctx)
}

// orgConfigForRun builds a runtime ProviderConfig for an org model slug
// (org:{orgID}:{providerID}). Same JWT + weisyn baseURL transport as wes:.
func (s *Service) orgConfigForRun(ctx context.Context, slug string) (wesconfig.ProviderConfig, bool) {
	if s.wesCatalog == nil {
		return wesconfig.ProviderConfig{}, false
	}
	return s.wesCatalog.OrgProviderConfig(ctx, slug)
}
