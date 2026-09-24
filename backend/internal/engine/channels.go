package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/weisyn/wesgine/channel"
	wesconfig "github.com/weisyn/wesgine/config"
	// Per-platform adapter imports were removed when bootChannelInstance was
	// delegated to wesclaw/channel.MakeBootFn. See AGENTS.md ARCH section
	// "IM channel architecture" for the unified design.
)

// Note: wesclaw uses SSE for real-time channel events. wescode uses polling
// because VS Code webview doesn't support EventSource. The poll interval (2s)
// provides adequate responsiveness for channel state changes.
// See design/audit-alignment.md CH-01.

// ─── Channel snapshot DTO ────────────────────────────────────────────────────

// ChannelSnapshotView uses snake_case JSON to align with the wire format
// exposed by wesclaw/channel.SnapshotView and consumed by @wesui/connections
// (frontend ChannelSnapshot type uses account_id, display_name, etc.).
type ChannelSnapshotView struct {
	Platform      string `json:"platform"`
	AccountID     string `json:"account_id"`
	Connected     bool   `json:"connected"`
	Status        string `json:"status,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	BoundIdentity string `json:"bound_identity,omitempty"`
	CallbackURL   string `json:"callback_url,omitempty"`
	// QRContent carries the SDK-provided QR string for scan-to-login
	// platforms (weixin iLink). wesui reads this from the snapshot so
	// late-loading pages can render the QR code without re-subscribing
	// to the event stream.
	QRContent string `json:"qr_content,omitempty"`
}

func (s *Service) toChannelView(snap channel.ChannelSnapshot) ChannelSnapshotView {
	status := "disconnected"
	switch {
	case snap.QRPending:
		status = "qr_pending"
	case snap.Connected:
		status = "connected"
	case snap.CircuitOpen:
		status = "circuit_open"
	case snap.LastError != nil:
		status = "error"
	}
	lastErr := ""
	if snap.LastError != nil && !snap.Connected {
		lastErr = snap.LastError.Error()
	}
	view := ChannelSnapshotView{
		Platform:      snap.Platform,
		AccountID:     snap.AccountID,
		Connected:     snap.Connected,
		Status:        status,
		LastError:     lastErr,
		DisplayName:   snap.DisplayName,
		BoundIdentity: snap.BoundIdentity,
		QRContent:     snap.QRContent,
	}

	if view.DisplayName == "" {
		if dn := s.channelConfigDisplayName(snap.Platform, snap.AccountID); dn != "" {
			view.DisplayName = dn
		}
	}

	if snap.Platform == "wecom_callback" {
		s.mu.Lock()
		channels := s.cfg.Channels
		s.mu.Unlock()
		for _, ci := range channels {
			if ci.Platform == snap.Platform && ci.AccountID == snap.AccountID {
				var cfg struct {
					CorpID          string `json:"corpId"`
					AgentID         string `json:"agentId"`
					CallbackURLBase string `json:"callbackURLBase"`
				}
				if len(ci.Config) > 0 {
					if err := json.Unmarshal([]byte(ci.Config), &cfg); err != nil {
						slog.Warn("[channels] unmarshal wecom_callback config failed", "platform", snap.Platform, "account", snap.AccountID, "error", err)
					}
				}
				if cfg.CorpID != "" && cfg.AgentID != "" {
					base := cfg.CallbackURLBase
					if base == "" {
						base = "<公网地址>"
					}
					view.CallbackURL = fmt.Sprintf("%s/wecom-callback/%s/%s", base, cfg.CorpID, cfg.AgentID)
				}
				break
			}
		}
	}
	return view
}

// ─── Channel event DTO ───────────────────────────────────────────────────────

type ChannelEventView struct {
	Seq       uint64            `json:"seq"`
	Kind      string            `json:"kind"`
	Platform  string            `json:"platform"`
	AccountID string            `json:"account_id"`
	Error     string            `json:"error,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
	At        time.Time         `json:"at"`
}

// ─── Event buffer ────────────────────────────────────────────────────────────

type channelEventBuffer struct {
	mu      sync.Mutex
	events  []ChannelEventView
	nextSeq uint64
	cancel  func()
}

func (b *channelEventBuffer) append(ev ChannelEventView) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSeq++
	ev.Seq = b.nextSeq
	b.events = append(b.events, ev)
	if len(b.events) > 200 {
		b.events = b.events[len(b.events)-200:]
	}
}

func (b *channelEventBuffer) pollSince(since uint64) ([]ChannelEventView, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	latest := b.nextSeq
	if len(b.events) == 0 {
		return nil, latest
	}
	out := make([]ChannelEventView, 0, len(b.events))
	for _, ev := range b.events {
		if ev.Seq > since {
			out = append(out, ev)
		}
	}
	return out, latest
}

// ─── Service methods ─────────────────────────────────────────────────────────

func (s *Service) ensureChannelEventBuffer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channelEventBuf != nil {
		return
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return
	}
	buf := &channelEventBuffer{}
	ch, cancel := hdl.Subscribe()
	buf.cancel = cancel
	s.channelEventBuf = buf
	go func() {
		for ev := range ch {
			errStr := ""
			if ev.Error != nil {
				errStr = ev.Error.Error()
			}
			buf.append(ChannelEventView{
				Kind:      string(ev.Kind),
				Platform:  ev.Platform,
				AccountID: ev.AccountID,
				Error:     errStr,
				Data:      ev.Data,
				At:        ev.At,
			})
		}
	}()
}

func (s *Service) ListChannels() []ChannelSnapshotView {
	if s.cell == nil {
		return nil
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return []ChannelSnapshotView{}
	}
	snaps := hdl.SnapshotAll()
	views := make([]ChannelSnapshotView, len(snaps))
	for i, snap := range snaps {
		views[i] = s.toChannelView(snap)
	}
	return views
}

func (s *Service) GetChannel(platform, accountID string) (*ChannelSnapshotView, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return nil, fmt.Errorf("channel gateway not initialized")
	}
	snap, err := hdl.Snapshot(platform, accountID)
	if err != nil {
		return nil, err
	}
	v := s.toChannelView(snap)
	return &v, nil
}

// ChannelConfigView exposes non-secret persisted config for edit forms.
type ChannelConfigView struct {
	Platform   string          `json:"platform"`
	AccountID  string          `json:"account_id"`
	Config     map[string]any  `json:"config"`
	HasSecrets map[string]bool `json:"has_secrets"`
}

func (s *Service) GetChannelConfig(platform, accountID string) (*ChannelConfigView, error) {
	ci := s.findChannelInstance(platform, accountID)
	if ci == nil {
		return nil, fmt.Errorf("channel not found: %s/%s", platform, accountID)
	}
	cfg := map[string]any{}
	if len(ci.Config) > 0 {
		if err := json.Unmarshal([]byte(ci.Config), &cfg); err != nil {
			return nil, fmt.Errorf("channel config: %w", err)
		}
	}
	hasSecrets := map[string]bool{}
	for key, ref := range ci.Secrets {
		hasSecrets[key] = !ref.IsEmpty()
	}
	return &ChannelConfigView{
		Platform:   platform,
		AccountID:  accountID,
		Config:     cfg,
		HasSecrets: hasSecrets,
	}, nil
}

func (s *Service) findChannelInstance(platform, accountID string) *wesconfig.ChannelInstance {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Channels {
		c := &s.cfg.Channels[i]
		if c.Platform == platform && c.AccountID == accountID {
			cp := *c
			return &cp
		}
	}
	return nil
}

func (s *Service) channelConfigDisplayName(platform, accountID string) string {
	ci := s.findChannelInstance(platform, accountID)
	if ci == nil || len(ci.Config) == 0 {
		return ""
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(ci.Config), &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(configMapString(cfg, "displayName"))
}

func validateChannelConfigMap(platform string, cfg map[string]any) error {
	switch platform {
	case "weixin":
		return nil
	}
	required := channelRequiredFields(platform)
	if required == nil {
		return fmt.Errorf("preflight: unknown platform %q", platform)
	}
	for _, field := range required {
		if configMapString(cfg, field) == "" {
			return fmt.Errorf("preflight: required field %q must not be empty", field)
		}
	}
	return nil
}

func channelRequiredFields(platform string) []string {
	switch platform {
	case "feishu":
		return []string{"appId", "appSecret"}
	case "dingtalk":
		return []string{"clientId", "clientSecret"}
	case "wecom":
		return []string{"botId", "secret"}
	case "wecom_callback":
		return []string{"corpId", "agentId", "secret", "token", "encodingAESKey"}
	case "qqbot":
		return []string{"appId", "appSecret"}
	default:
		return nil
	}
}

func configMapString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func isConfigValueEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) == ""
	default:
		return false
	}
}

func (s *Service) mergeChannelConfigMap(platform, accountID string, cfgMap map[string]any, secrets map[string]wesconfig.SecretRef) map[string]any {
	if cfgMap == nil {
		cfgMap = map[string]any{}
	}
	for key, ref := range secrets {
		if ref.IsEmpty() {
			continue
		}
		val, err := ref.Resolve()
		if err != nil || val == "" {
			continue
		}
		cfgMap[key] = val
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ci := range s.cfg.Channels {
		if ci.Platform != platform || ci.AccountID != accountID {
			continue
		}
		if len(ci.Config) > 0 {
			var stored map[string]any
			if err := json.Unmarshal([]byte(ci.Config), &stored); err == nil {
				for key, val := range stored {
					if isConfigValueEmpty(cfgMap[key]) {
						cfgMap[key] = val
					}
				}
			}
		}
		for key, ref := range ci.Secrets {
			if ref.IsEmpty() {
				continue
			}
			if configMapString(cfgMap, key) != "" {
				continue
			}
			val, err := ref.Resolve()
			if err != nil || val == "" {
				continue
			}
			cfgMap[key] = val
		}
		break
	}
	return cfgMap
}

func mergeChannelInstance(existing, incoming wesconfig.ChannelInstance) wesconfig.ChannelInstance {
	out := incoming
	if out.Secrets == nil {
		out.Secrets = map[string]wesconfig.SecretRef{}
	}
	for key, ref := range existing.Secrets {
		if newRef, ok := out.Secrets[key]; !ok || newRef.IsEmpty() {
			out.Secrets[key] = ref
		}
	}
	if out.Token.IsEmpty() {
		out.Token = existing.Token
	}
	if out.Enabled == nil {
		out.Enabled = existing.Enabled
	}

	mergedCfg := map[string]any{}
	if len(existing.Config) > 0 {
		if err := json.Unmarshal([]byte(existing.Config), &mergedCfg); err != nil {
			slog.Warn("[channels] unmarshal existing config failed", "platform", existing.Platform, "error", err)
		}
	}
	if len(incoming.Config) > 0 {
		var patch map[string]any
		if err := json.Unmarshal([]byte(incoming.Config), &patch); err == nil {
			for key, val := range patch {
				if !isConfigValueEmpty(val) {
					mergedCfg[key] = val
				}
			}
		}
	}
	if raw, err := json.Marshal(mergedCfg); err == nil {
		out.Config = wesconfig.ChannelInstanceConfig(raw)
	} else {
		out.Config = existing.Config
	}
	return out
}

type ChannelUpsertParams struct {
	Platform  string                         `json:"platform"`
	AccountID string                         `json:"account_id"`
	Config    json.RawMessage                `json:"config"`
	Secrets   map[string]wesconfig.SecretRef `json:"secrets,omitempty"`
	Token     string                         `json:"token,omitempty"`
	Enabled   *bool                          `json:"enabled,omitempty"`
}

func (s *Service) UpsertChannel(ctx context.Context, p ChannelUpsertParams) (*ChannelSnapshotView, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	if strings.TrimSpace(p.AccountID) == "" {
		return nil, fmt.Errorf("accountId is required")
	}

	ci := wesconfig.ChannelInstance{
		Platform:  p.Platform,
		AccountID: p.AccountID,
		Config:    wesconfig.ChannelInstanceConfig(p.Config),
		Secrets:   p.Secrets,
		// Token arrives as plaintext from the RPC body, so inline is the only
		// storage class it can take. Empty must stay the zero SecretRef, not
		// inline-with-empty-Value: mergeChannelInstance's IsEmpty() check is what
		// preserves an already-stored token when the operator submits a
		// config-only update.
		Token:   wesconfig.InlineSecret(p.Token),
		Enabled: p.Enabled,
	}
	if existing := s.findChannelInstance(p.Platform, p.AccountID); existing != nil {
		ci = mergeChannelInstance(*existing, ci)
	}

	// Persist to config
	s.mu.Lock()
	s.persistChannelConfig(ci, false)
	s.ensureDefaultChannelBindings()
	s.mu.Unlock()

	s.ensureChannelEventBuffer()
	s.syncChannelBindings(context.Background())

	// Auto-connect (use background ctx — RPC ctx is cancelled when the request returns)
	if ci.IsEnabled() {
		if hdl := s.cell.Channels(); hdl != nil {
			if err := hdl.Unregister(context.Background(), p.Platform, p.AccountID); err != nil {
				slog.Warn("[channels] Unregister before re-connect failed", "platform", p.Platform, "account", p.AccountID, "error", err)
			}
		}
		if err := s.bootChannelInstance(context.Background(), ci); err != nil {
			slog.Warn("[channels] auto-connect after upsert failed", "platform", p.Platform, "account", p.AccountID, "error", err)
		}
	}

	// Return snapshot
	if hdl := s.cell.Channels(); hdl != nil {
		if snap, err := hdl.Snapshot(p.Platform, p.AccountID); err == nil {
			v := s.toChannelView(snap)
			return &v, nil
		}
	}
	v := ChannelSnapshotView{Platform: p.Platform, AccountID: p.AccountID, Status: "disconnected"}
	return &v, nil
}

func (s *Service) DeleteChannel(ctx context.Context, platform, accountID string) error {
	if s.cell == nil {
		return nil
	}
	if hdl := s.cell.Channels(); hdl != nil {
		if err := hdl.Unregister(ctx, platform, accountID); err != nil {
			slog.Warn("[channels] Unregister on delete failed", "platform", platform, "account", accountID, "error", err)
		}
	}
	s.mu.Lock()
	s.persistChannelConfig(wesconfig.ChannelInstance{Platform: platform, AccountID: accountID}, true)
	s.removeChannelBindingsLocked(platform, accountID)
	s.mu.Unlock()
	s.syncChannelBindings(context.Background())
	return nil
}

func (s *Service) removeChannelBindingsLocked(platform, accountID string) {
	if len(s.cfg.IMGateway.Bindings) == 0 {
		return
	}
	filtered := make([]wesconfig.ChannelBinding, 0, len(s.cfg.IMGateway.Bindings))
	removed := false
	for _, b := range s.cfg.IMGateway.Bindings {
		if b.Platform == platform && b.AccountID == accountID {
			removed = true
			continue
		}
		filtered = append(filtered, b)
	}
	if removed {
		s.cfg.IMGateway.Bindings = filtered
		if err := saveConfig(s.cfg); err != nil {
			slog.Warn("[channels] saveConfig failed after removing bindings", "platform", platform, "account", accountID, "error", err)
		}
	}
}

func (s *Service) ConnectChannel(ctx context.Context, platform, accountID string) (*ChannelSnapshotView, error) {
	if s.cell == nil {
		return nil, fmt.Errorf("engine not initialized")
	}

	// Find persisted config
	s.mu.Lock()
	var found *wesconfig.ChannelInstance
	for i := range s.cfg.Channels {
		c := &s.cfg.Channels[i]
		if c.Platform == platform && c.AccountID == accountID {
			cp := *c
			found = &cp
			break
		}
	}
	s.mu.Unlock()

	if found == nil {
		return nil, fmt.Errorf("no config for %s/%s", platform, accountID)
	}

	if hdl := s.cell.Channels(); hdl != nil {
		if snap, err := hdl.Snapshot(platform, accountID); err == nil && snap.Connected {
			v := s.toChannelView(snap)
			return &v, nil
		}
		if err := hdl.Unregister(context.Background(), platform, accountID); err != nil {
			slog.Warn("[channels] Unregister before reconnect failed", "platform", platform, "account", accountID, "error", err)
		}
	}

	if err := s.bootChannelInstance(context.Background(), *found); err != nil {
		return nil, err
	}

	s.ensureChannelEventBuffer()

	if hdl := s.cell.Channels(); hdl != nil {
		if snap, err := hdl.Snapshot(platform, accountID); err == nil {
			v := s.toChannelView(snap)
			return &v, nil
		}
	}
	v := ChannelSnapshotView{Platform: platform, AccountID: accountID, Status: "disconnected"}
	return &v, nil
}

func (s *Service) DisconnectChannel(ctx context.Context, platform, accountID string) error {
	if s.cell == nil {
		return nil
	}
	if hdl := s.cell.Channels(); hdl != nil {
		return hdl.Unregister(ctx, platform, accountID)
	}
	return nil
}

func (s *Service) PollChannelEvents(since uint64) ([]ChannelEventView, uint64) {
	s.mu.Lock()
	buf := s.channelEventBuf
	s.mu.Unlock()
	if buf == nil {
		return nil, since
	}
	return buf.pollSince(since)
}

// SubscribeChannelEvents returns a channel that receives real-time channel
// events (same data as PollChannelEvents but push-based). Returns nil if the
// engine has no channel handle. The caller must call the returned cancel func.
func (s *Service) SubscribeChannelEvents() (<-chan channel.ChannelEvent, func()) {
	if s.cell == nil {
		return nil, func() {}
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return nil, func() {}
	}
	return hdl.Subscribe()
}

func (s *Service) GetChannelBindings() []wesconfig.ChannelBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.IMGateway.Bindings == nil {
		return []wesconfig.ChannelBinding{}
	}
	return s.cfg.IMGateway.Bindings
}

const defaultIMAgentID = "builtin-coder"

// ensureDefaultChannelBindings creates platform+account bindings when channels exist
// but the user has not configured any (wesclaw UI usually sets these explicitly).
// Caller must hold s.mu.
func (s *Service) ensureDefaultChannelBindings() {
	hasBinding := func(platform, accountID string) bool {
		for _, b := range s.cfg.IMGateway.Bindings {
			if b.Platform == platform && b.AccountID == accountID && b.FixedAgentID != "" {
				return true
			}
		}
		return false
	}

	changed := false
	for _, ci := range s.cfg.Channels {
		if !ci.IsEnabled() {
			continue
		}
		if hasBinding(ci.Platform, ci.AccountID) {
			continue
		}
		s.cfg.IMGateway.Bindings = append(s.cfg.IMGateway.Bindings, wesconfig.ChannelBinding{
			Platform:     ci.Platform,
			AccountID:    ci.AccountID,
			FixedAgentID: defaultIMAgentID,
		})
		changed = true
		slog.Info("[channels] auto-created default binding",
			"platform", ci.Platform,
			"account", ci.AccountID,
			"agent", defaultIMAgentID,
		)
	}
	if changed {
		if err := saveConfig(s.cfg); err != nil {
			slog.Warn("[channels] saveConfig failed after auto-created binding", "error", err)
		}
	}
}

// syncChannelBindings loads persisted bindings into the IM gateway (in-memory).
// wescode stores bindings separately from wesgine.Config.IMGateway; without this,
// restarts would leave the gateway with empty bindings until the UI saves again.
// When called from Initialize, caller must hold s.mu.
func (s *Service) syncChannelBindings(ctx context.Context) {
	if s.cell == nil {
		return
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return
	}
	bindings := s.cfg.IMGateway.Bindings
	if bindings == nil {
		bindings = []wesconfig.ChannelBinding{}
	}
	if err := hdl.PutBindings(ctx, bindings); err != nil {
		slog.Warn("[channels] sync bindings on init failed", "error", err)
	}
}

func (s *Service) UpdateChannelBindings(ctx context.Context, bindings []wesconfig.ChannelBinding) error {
	if hdl := s.cell.Channels(); hdl != nil {
		if err := hdl.PutBindings(ctx, bindings); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg.IMGateway.Bindings = bindings
	cfg := s.cfg
	s.mu.Unlock()
	return saveConfig(cfg)
}

// ─── Preflight ───────────────────────────────────────────────────────────────

type ChannelPreflightResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Service) PreflightChannel(ctx context.Context, platform, accountID string, config json.RawMessage, secrets map[string]wesconfig.SecretRef) ChannelPreflightResult {
	if s.cell == nil {
		return ChannelPreflightResult{OK: false, Error: "engine not initialized"}
	}
	if strings.TrimSpace(platform) == "" {
		return ChannelPreflightResult{OK: false, Error: "preflight: platform must not be empty"}
	}
	if strings.TrimSpace(accountID) == "" {
		return ChannelPreflightResult{OK: false, Error: "preflight: accountId must not be empty"}
	}

	cfgMap := map[string]any{}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfgMap); err != nil {
			return ChannelPreflightResult{OK: false, Error: "invalid config JSON: " + err.Error()}
		}
	}
	cfgMap = s.mergeChannelConfigMap(platform, accountID, cfgMap, secrets)

	if err := validateChannelConfigMap(platform, cfgMap); err != nil {
		return ChannelPreflightResult{OK: false, Error: err.Error()}
	}

	hdl := s.cell.Channels()
	if hdl != nil {
		if err := hdl.PreflightChannel(ctx, platform, accountID, cfgMap); err != nil {
			return ChannelPreflightResult{OK: false, Error: err.Error()}
		}
	}
	return ChannelPreflightResult{OK: true}
}

// ─── Validate bindings ──────────────────────────────────────────────────────

type ValidateBindingsResult struct {
	OK     bool     `json:"ok"`
	Issues []string `json:"issues,omitempty"`
}

func (s *Service) ValidateChannelBindings(ctx context.Context, bindings []wesconfig.ChannelBinding) ValidateBindingsResult {
	if s.cell == nil {
		return ValidateBindingsResult{OK: false, Issues: []string{"engine not initialized"}}
	}
	hdl := s.cell.Channels()
	if hdl == nil {
		return ValidateBindingsResult{OK: false, Issues: []string{"channel gateway not initialized"}}
	}
	if err := hdl.ValidateBindings(ctx, bindings); err != nil {
		return ValidateBindingsResult{OK: false, Issues: []string{err.Error()}}
	}
	return ValidateBindingsResult{OK: true}
}

// ─── Config persistence ──────────────────────────────────────────────────────

func (s *Service) persistChannelConfig(ci wesconfig.ChannelInstance, del bool) {
	existing := s.cfg.Channels
	if del {
		filtered := make([]wesconfig.ChannelInstance, 0, len(existing))
		for _, c := range existing {
			if c.Platform != ci.Platform || c.AccountID != ci.AccountID {
				filtered = append(filtered, c)
			}
		}
		s.cfg.Channels = filtered
	} else {
		found := false
		for i, c := range existing {
			if c.Platform == ci.Platform && c.AccountID == ci.AccountID {
				s.cfg.Channels[i] = ci
				found = true
				break
			}
		}
		if !found {
			s.cfg.Channels = append(s.cfg.Channels, ci)
		}
	}
	if err := saveConfig(s.cfg); err != nil {
		slog.Warn("[channels] saveConfig failed after persistChannelConfig", "error", err)
	}
}

// ─── Boot adapter ────────────────────────────────────────────────────────────

// bootChannelInstance delegates to the wesclaw/channel shared package's
// per-platform boot logic. The previous 120-line switch lived here and is
// the canonical example of the duplication that the unification plan
// (im_channel_unification_across_3_apps) removed.
//
// wecom_callback is intentionally rejected here because the shared package
// defers inbound webhook handling to a later iteration; the wescode UI
// surfaces a "暂未支持" hint on that platform card.
func (s *Service) bootChannelInstance(ctx context.Context, ci wesconfig.ChannelInstance) error {
	if s.channelBoot == nil {
		return fmt.Errorf("channels: shared boot function not configured")
	}
	return s.channelBoot(ctx, ci)
}

// ─── Channel Schema ──────────────────────────────────────────────────────────

// ChannelField describes a single form field for channel configuration UI.
type ChannelField struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Label       string   `json:"label"`
	SecretKey   string   `json:"secret_key,omitempty"`
	Default     string   `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

// ChannelSchema describes the configuration form for a platform.
type ChannelSchema struct {
	Platform string         `json:"platform"`
	Label    string         `json:"label"`
	Fields   []ChannelField `json:"fields"`
	Guide    string         `json:"guide,omitempty"`
	Notes    []string       `json:"notes,omitempty"`
}

// channelSchemas is the static schema map for all supported platforms.
// Mirrors wesclaw's platformSchema() (internal/gateway/handler/channels.go).
var channelSchemas = map[string]ChannelSchema{
	"feishu": {
		Platform: "feishu",
		Label:    "飞书 / Lark",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "连接名称", Placeholder: "飞书机器人"},
			{Name: "appId", Type: "string", Required: true, Label: "App ID"},
			{Name: "appSecret", Type: "secret", Required: true, Label: "App Secret", SecretKey: "appSecret"},
			{Name: "domain", Type: "enum", Required: false, Default: "feishu", Options: []string{"feishu", "lark"}, Label: "域"},
			{Name: "connectionMode", Type: "enum", Required: false, Default: "websocket", Options: []string{"websocket", "webhook"}, Label: "接入模式"},
			{Name: "encryptKey", Type: "secret", Required: false, Label: "Encrypt Key（可选）", SecretKey: "encryptKey"},
			{Name: "verificationToken", Type: "secret", Required: false, Label: "Verification Token（可选）", SecretKey: "verificationToken"},
		},
		Guide: "1. 登录飞书开放平台 → 创建自建应用 → 记录 App ID / App Secret\n2. 应用能力 → 机器人 → 启用\n3. 事件订阅 → 添加事件 → im.message.receive_v1",
	},
	"dingtalk": {
		Platform: "dingtalk",
		Label:    "钉钉",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "连接名称", Placeholder: "钉钉机器人"},
			{Name: "clientId", Type: "string", Required: true, Label: "Client ID"},
			{Name: "clientSecret", Type: "secret", Required: true, Label: "Client Secret", SecretKey: "clientSecret"},
			{Name: "robotCode", Type: "string", Required: false, Label: "RobotCode（多机器人时填写）"},
		},
		Guide: "1. 登录钉钉开放平台 → 应用管理 → 创建应用\n2. 应用能力 → 机器人 → 启用\n3. 记录 AppKey（clientId）和 AppSecret（clientSecret）",
	},
	"wecom": {
		Platform: "wecom",
		Label:    "企业微信 AI Bot",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "连接名称", Placeholder: "企微 AI Bot"},
			{Name: "botId", Type: "string", Required: true, Label: "Bot ID"},
			{Name: "secret", Type: "secret", Required: true, Label: "Bot Secret", SecretKey: "secret"},
		},
		Guide: "1. 登录企业微信管理后台 → 应用管理 → 自建 → AI 助理\n2. 记录机器人 ID（botId）和 Secret",
	},
	"weixin": {
		Platform: "weixin",
		Label:    "微信 iLink",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "显示名称（可选）", Placeholder: "例如：我的微信机器人"},
		},
		Guide: "1. 登录 https://ilinkai.weixin.qq.com 确认账号\n2. 点击「添加渠道」→ 填写显示名称\n3. 点击「连接」→ 使用微信 App 扫描二维码\n4. 凭证自动保存，下次启动免扫码",
		Notes: []string{"首次连接必须扫码，无法用 Token 直接登录", "不支持消息流式编辑"},
	},
	"wecom_callback": {
		Platform: "wecom_callback",
		Label:    "企业微信自建应用",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "连接名称", Placeholder: "企微自建应用"},
			{Name: "corpId", Type: "string", Required: true, Label: "企业ID (CorpID)"},
			{Name: "agentId", Type: "string", Required: true, Label: "应用 AgentID"},
			{Name: "secret", Type: "secret", Required: true, Label: "应用 Secret", SecretKey: "secret"},
			{Name: "token", Type: "secret", Required: true, Label: "消息校验 Token", SecretKey: "token"},
			{Name: "encodingAESKey", Type: "secret", Required: true, Label: "消息加解密 Key", SecretKey: "encodingAESKey"},
			{Name: "callbackURLBase", Type: "string", Required: false, Label: "公网服务地址", Placeholder: "例如：https://my.server.com"},
		},
		Guide: "1. 企业微信管理后台 → 应用管理 → 自建 → 创建应用\n2. 应用详情 → 接收消息 → 设置API接收\n3. 回调URL格式：<公网地址>/wecom-callback/<CorpID>/<AgentID>",
		Notes: []string{"⚠️ 必须有公网可访问地址（或 ngrok/frp 内网穿透）", "保存后复制回调 URL 粘贴到「接收消息」URL 字段完成验证"},
	},
	"qqbot": {
		Platform: "qqbot",
		Label:    "QQ Bot（过渡期 ⚠️）",
		Fields: []ChannelField{
			{Name: "displayName", Type: "string", Required: false, Label: "连接名称", Placeholder: "QQ 机器人"},
			{Name: "appId", Type: "string", Required: true, Label: "AppID"},
			{Name: "appSecret", Type: "secret", Required: true, Label: "AppSecret", SecretKey: "appSecret"},
		},
		Guide: "1. 登录 QQ 开放平台 https://q.qq.com → 应用管理 → 创建机器人\n2. 记录 AppID 和 AppSecret\n3. 开启「事件订阅」→ 公域消息 AT_MESSAGE_CREATE",
		Notes: []string{"⚠️ WebSocket 模式处于过渡期，官方计划迁移至 Webhook 回调", "仅支持频道 @机器人 消息和私信"},
	},
}

// GetChannelSchema returns the form schema for a given platform.
func (s *Service) GetChannelSchema(platform string) (*ChannelSchema, error) {
	schema, ok := channelSchemas[platform]
	if !ok {
		return nil, fmt.Errorf("no schema registered for platform %q", platform)
	}
	return &schema, nil
}

// ListChannelSchemas returns schemas for all supported platforms.
func (s *Service) ListChannelSchemas() []ChannelSchema {
	result := make([]ChannelSchema, 0, len(channelSchemas))
	for _, schema := range channelSchemas {
		result = append(result, schema)
	}
	return result
}
